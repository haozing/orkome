package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/haozing/ploykit/platform/cronx"
	"orkome/internal/automation/domain"
	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
)

const ChildDoneDefaultInstruction = "The sub work items of this work item have finished: the trigger facts list each stage's convergence and the next stage to start." +
	"If later stages are waiting, confirm their dependencies are met, then move their sub work items out of todo to start them; " +
	"If closed stages contain cancelled sub-items, first decide whether that work still needs completing." +
	"After all sub-items finish, summarize their outputs on this work item and advance its status."

type WakeupService struct {
	repo  Repo
	enq   TaskEnqueuer
	facts FactsReader
	now   func() time.Time
	uow   UoW

	memberNotifier MemberNotifier

	burstMax int
}

func NewWakeupService(repo Repo, enq TaskEnqueuer, facts FactsReader) *WakeupService {
	return &WakeupService{repo: repo, enq: enq, facts: facts, now: time.Now}
}

func (s *WakeupService) SetUow(uow UoW) { s.uow = uow }

func (s *WakeupService) SetMemberNotifier(n MemberNotifier) { s.memberNotifier = n }

type MemberNotifier func(ctx context.Context, workspaceID, userID, title, body, dedupKey string) error

func (s *WakeupService) SetBurstLimit(n int) { s.burstMax = n }

func (s *WakeupService) Create(ctx context.Context, w Wakeup) (*Wakeup, error) {
	if strings.TrimSpace(w.CreatedBy) == "" {
		// created_by 缺失会让 fire 阶段补零 UUID 撞 originator FK：任务插不进去、
		// next_fire_at 永不推进、due 扫描每秒重试同一失败。必须在创建入口拦下。
		return nil, apierr.NewValidation("created_by required (authenticated principal missing)")
	}
	if err := domain.ValidateInstruction(w.Instruction); err != nil {
		return nil, apierr.NewValidation(err.Error())
	}
	switch w.Kind {
	case domain.WakeupTime:
		if w.CronExpression != "" {
			next, err := cronx.Next(w.CronExpression, w.Timezone, s.now())
			if err != nil {
				return nil, apierr.NewValidation(err.Error())
			}
			w.NextFireAt = &next
		} else {
			next := domain.IntervalNext(s.now(), w.IntervalSeconds)
			w.NextFireAt = &next
		}
	case domain.WakeupEvent:

		if len(w.EventTypes) == 0 {
			return nil, apierr.NewValidation("event_types required for kind=event")
		}
		w.NextFireAt = nil
	case domain.WakeupCondition:
		if w.Condition == nil {
			return nil, apierr.NewValidation("condition predicate required")
		}
	default:
		return nil, apierr.NewValidation("kind must be time|event|condition (system rules are platform-managed)")
	}
	w.Enabled = true
	out, err := s.repo.CreateWakeup(ctx, w)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *WakeupService) List(ctx context.Context, workItemID string) ([]Wakeup, error) {
	return s.repo.ListWakeupsByWorkItem(ctx, workItemID)
}

func (s *WakeupService) Get(ctx context.Context, ws, workItemID, id string) (*Wakeup, error) {
	w, ok, err := s.repo.GetWakeup(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if !ok || w.WorkItemID != workItemID {
		return nil, apierr.New(apierr.NotFound, "wakeup not found")
	}
	return &w, nil
}

func (s *WakeupService) SetEnabled(ctx context.Context, ws, id string, en bool) error {
	return s.repo.SetWakeupEnabled(ctx, ws, id, en)
}

func (s *WakeupService) Delete(ctx context.Context, ws, workItemID, id string) error {
	w, err := s.Get(ctx, ws, workItemID, id)
	if err != nil {
		return err
	}
	if w.SystemRule != "" {
		return apierr.New(apierr.Validation, "system rule cannot be deleted; disable it instead")
	}
	return s.repo.DeleteWakeup(ctx, ws, id)
}

func (s *WakeupService) EditInstruction(ctx context.Context, ws, id, instruction string) error {
	if err := domain.ValidateInstruction(instruction); err != nil {
		return apierr.NewValidation(err.Error())
	}
	ok, err := s.repo.SetWakeupInstruction(ctx, ws, id, instruction)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.New(apierr.NotFound, "wakeup not found")
	}
	return nil
}

func (s *WakeupService) Update(ctx context.Context, ws, workItemID, id string, patch Wakeup, expectedRev int64) (*Wakeup, error) {
	cur, err := s.Get(ctx, ws, workItemID, id)
	if err != nil {
		return nil, err
	}
	if cur.SystemRule != "" {
		return nil, apierr.New(apierr.Validation, "system rule updates go through system-wakeups endpoints")
	}
	next := *cur
	if patch.Instruction != "" {
		next.Instruction = patch.Instruction
	}
	if err := domain.ValidateInstruction(next.Instruction); err != nil {
		return nil, apierr.NewValidation(err.Error())
	}
	if patch.IntervalSeconds != 0 {
		next.IntervalSeconds = patch.IntervalSeconds
	}
	if patch.CronExpression != "" {
		next.CronExpression = patch.CronExpression
	}
	if patch.Timezone != "" {
		next.Timezone = patch.Timezone
	}
	if patch.MaxFires != 0 {
		next.MaxFires = patch.MaxFires
	}
	if patch.EventTypes != nil {
		next.EventTypes = patch.EventTypes
	}
	next.FilterAgentID = pickStr(patch.FilterAgentID, next.FilterAgentID)
	next.FilterTaskID = pickStr(patch.FilterTaskID, next.FilterTaskID)
	next.FilterActorType = pickStr(patch.FilterActorType, next.FilterActorType)
	next.FilterActorID = pickStr(patch.FilterActorID, next.FilterActorID)
	if patch.Condition != nil {
		next.Condition = patch.Condition
	}

	if next.Kind == domain.WakeupTime {
		var nv time.Time
		if next.CronExpression != "" {
			nv, err = cronx.Next(next.CronExpression, next.Timezone, s.now())
			if err != nil {
				return nil, apierr.NewValidation(err.Error())
			}
		} else {
			nv = domain.IntervalNext(s.now(), next.IntervalSeconds)
		}
		next.NextFireAt = &nv
	}
	if next.Kind == domain.WakeupEvent && len(next.EventTypes) == 0 {
		return nil, apierr.NewValidation("event_types required for kind=event")
	}
	out, ok, err := s.repo.UpdateWakeup(ctx, next, expectedRev)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.Conflict, "revision conflict")
	}
	return &out, nil
}

func (s *WakeupService) Trigger(ctx context.Context, ws, workItemID, id string) error {
	w, err := s.Get(ctx, ws, workItemID, id)
	if err != nil {
		return err
	}
	if !s.fireOne(ctx, *w, s.now(), fireOpts{evidence: "manual trigger"}) {
		return apierr.New(apierr.Validation, "wakeup could not fire (disabled/paused/quota or no target agent)")
	}
	return nil
}

func (s *WakeupService) CheckIn(ctx context.Context, ws, workItemID, id, taskID, note string) error {
	if err := domain.ValidateCheckinNote(note); err != nil {
		return apierr.NewValidation(err.Error())
	}
	w, err := s.Get(ctx, ws, workItemID, id)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"note": note, "task_id": taskID})
	return s.repo.InsertReceipt(ctx, Receipt{
		WakeupID: w.ID, Revision: w.Revision, EventKey: "checkin:" + taskID,
		WorkspaceID: ws, CapturedAt: s.now(), Payload: payload,
	})
}

func (s *WakeupService) ListWorkspace(ctx context.Context, ws string) ([]Wakeup, error) {
	return s.repo.ListWorkspaceWakeups(ctx, ws)
}

func (s *WakeupService) ListPaused(ctx context.Context, ws string) ([]Wakeup, error) {
	return s.repo.ListPausedWakeups(ctx, ws)
}

func (s *WakeupService) Summaries(ctx context.Context, ws string) ([]WakeupSummary, error) {
	return s.repo.ListWakeupSummaries(ctx, ws)
}

func (s *WakeupService) ListSystem(ctx context.Context, ws string) ([]Wakeup, error) {
	return s.repo.ListSystemWakeups(ctx, ws)
}

func (s *WakeupService) GetSystemDefault(ctx context.Context, ws string) (*SystemWakeupDefault, error) {
	d, ok, err := s.repo.GetSystemWakeupDefault(ctx, ws)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &SystemWakeupDefault{Enabled: true}, nil
	}
	return &d, nil
}

func (s *WakeupService) SetSystemDefault(ctx context.Context, ws string, d SystemWakeupDefault) error {
	if n := len([]rune(d.Instruction)); n > domain.InstructionMax {
		return apierr.NewValidation(fmt.Sprintf("instruction must be <= %d runes, got %d", domain.InstructionMax, n))
	}
	return s.repo.SetSystemWakeupDefault(ctx, ws, d)
}

func (s *WakeupService) EnsureChildDoneRule(ctx context.Context, ws, workItemID string) (*Wakeup, error) {
	rules, err := s.repo.ListSystemWakeupsByWorkItem(ctx, ws, workItemID)
	if err != nil {
		return nil, err
	}
	if len(rules) > 0 {
		return &rules[0], nil
	}
	def, ok, err := s.repo.GetSystemWakeupDefault(ctx, ws)
	if err != nil {
		return nil, err
	}
	if !ok {
		def = SystemWakeupDefault{Enabled: true}
	}
	instr := strings.TrimSpace(def.Instruction)
	if instr == "" {
		instr = ChildDoneDefaultInstruction
	}

	var base *string
	children, err := s.facts.ChildStageFacts(ctx, ws, workItemID)
	if err != nil {
		return nil, err
	}
	if _, fp := domain.ChildrenDone(children); fp != "" {
		base = &fp
	}
	out, err := s.repo.EnsureSystemWakeup(ctx, Wakeup{
		WorkspaceID: ws, WorkItemID: workItemID, Kind: domain.WakeupSystem,
		Instruction: instr, SystemRule: domain.SystemRuleChildrenDone,
		Condition:      map[string]any{"type": domain.SystemRuleChildrenDone, "each_stage": true},
		ConditionState: base, Enabled: def.Enabled,
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *WakeupService) ListSystemByWorkItem(ctx context.Context, ws, workItemID string) ([]Wakeup, error) {
	return s.repo.ListSystemWakeupsByWorkItem(ctx, ws, workItemID)
}

func (s *WakeupService) UpdateSystem(ctx context.Context, ws, id string, instruction *string, enabled *bool) (*Wakeup, error) {
	if instruction != nil {
		if err := domain.ValidateInstruction(*instruction); err != nil {
			return nil, apierr.NewValidation(err.Error())
		}
	}
	out, ok, err := s.repo.UpdateSystemWakeup(ctx, ws, id, instruction, enabled)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "system wakeup not found")
	}
	return &out, nil
}

func (s *WakeupService) NotifyChildrenChanged(ctx context.Context, ws, workItemID string) error {

	ids := []string{workItemID}
	if pr, ok := s.facts.(interface {
		ParentOf(ctx context.Context, workspaceID, workItemID string) (string, error)
	}); ok {
		if parent, err := pr.ParentOf(ctx, ws, workItemID); err == nil && parent != "" && parent != workItemID {
			ids = append(ids, parent)
		}
	}
	for _, id := range ids {
		rules, err := s.repo.ListSystemWakeupsByWorkItem(ctx, ws, id)
		if err != nil {
			return err
		}
		for _, w := range rules {
			if w.SystemRule != domain.SystemRuleChildrenDone {
				continue
			}
			s.evaluateCondition(ctx, w, s.now(), false)
		}
	}
	return nil
}

func (s *WakeupService) NotifyBatchDone(ctx context.Context, ws, workItemID, batchID string) error {
	conds, err := s.repo.ListConditionWakeups(ctx)
	if err != nil {
		return err
	}
	for _, w := range conds {
		if w.WorkspaceID != ws || w.WorkItemID != workItemID {
			continue
		}
		if batchField(w.Condition) {
			s.evaluateCondition(ctx, w, s.now(), true)
		}
	}
	return nil
}

func batchField(cond map[string]any) bool {
	f := domain.FieldPredicate{}
	b, _ := json.Marshal(cond)
	_ = json.Unmarshal(b, &f)
	return f.Field == "batch"
}

// WakeAgentForBatch 批次终态唤醒创建者 agent（batch v2.0 设计 §2.2）：绕过
// 条件规则（无 wakeup 行可锁、无规则状态可推进，故不经 fireOneSteps），
// 直接按 agent 建收割任务。建任务机制复用 TaskEnqueuer——wakeup 同款
// MergeIntoPending 去重合并 + trigger 上下文注入。agentID 为空（面板/
// 第三方建的批次无创建者 agent）→ 跳过，交由现有 condition 扫描（
// NotifyBatchDone），不视为错误。
func (s *WakeupService) WakeAgentForBatch(ctx context.Context, ws, workItemID, agentID, taskID, evidence string) error {
	if agentID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, domain.DispatchBudget)
	defer cancel()
	if taskID != "" {
		evidence = "Originator task: " + taskID + "\n" + evidence
	}
	_, _, err := s.enq.EnqueueForWakeup(ctx, ws, workItemID, agentID, "", domain.CapEvidence(evidence))
	return err
}

func (s *WakeupService) RecordEvent(ctx context.Context, e events.Envelope) {
	if e.WorkspaceID == "" {
		return
	}
	cands, err := s.repo.ListEventWakeups(ctx, e.WorkspaceID, e.Event)
	if err != nil {
		return
	}
	if len(cands) == 0 {
		return
	}
	b, _ := json.Marshal(e.Payload)
	payload := map[string]any{}
	_ = json.Unmarshal(b, &payload)
	facts := domain.ExtractEventFacts(e.Event, payload)
	key := e.EventID
	if key == "" {
		key = e.Event + ":" + e.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	for _, w := range cands {
		f := domain.EventFilter{EventTypes: w.EventTypes, AgentID: w.FilterAgentID,
			TaskID: w.FilterTaskID, ActorType: w.FilterActorType, ActorID: w.FilterActorID}
		if !f.Matches(facts) {
			continue
		}

		_ = s.repo.InsertReceipt(ctx, Receipt{WakeupID: w.ID, Revision: w.Revision,
			EventKey: key, WorkspaceID: e.WorkspaceID, CapturedAt: s.now(), Payload: b})
	}
}

func (s *WakeupService) TickOnce(ctx context.Context) (int, error) {
	now := s.now()
	if paused, err := s.repo.ListBurstPausedWakeups(ctx, now, 100); err == nil {
		for _, w := range paused {
			_, _ = s.repo.ResumeWakeupBurst(ctx, w.ID, w.Revision)
		}
	}
	due, err := s.repo.ListDueWakeups(ctx, now, 100)
	if err != nil {
		return 0, err
	}
	fired := 0
	for _, d := range due {
		switch d.Source {
		case ScanDue:
			if s.fireOne(ctx, d.Wakeup, now, fireOpts{}) {
				fired++
			}
		case ScanExpire:

			_, _ = s.repo.MarkWakeupExpired(ctx, d.Wakeup.ID, d.Wakeup.Revision)
		case ScanCondition:
			if s.evaluateCondition(ctx, d.Wakeup, now, false) {
				fired++
			}
		case ScanReceipt:
			if s.dispatchReceipt(ctx, d, now) {
				fired++
			}
		}
	}
	return fired, nil
}

func (s *WakeupService) dispatchReceipt(ctx context.Context, d DueWakeup, now time.Time) bool {
	if d.ReceiptRev != d.Wakeup.Revision {
		_ = s.repo.MarkReceiptProcessed(ctx, d.ReceiptID)
		return false
	}
	if s.burstGate(ctx, d.Wakeup, now) {

		_ = s.repo.MarkReceiptProcessed(ctx, d.ReceiptID)
		return false
	}
	if s.fireOne(ctx, d.Wakeup, now, fireOpts{revGate: d.ReceiptRev, evidence: "Event: " + d.EventKey}) {
		_ = s.repo.MarkReceiptProcessed(ctx, d.ReceiptID)
		return true
	}

	if d.CapturedAt != nil && now.Sub(*d.CapturedAt) > domain.ReceiptRetryWindow {
		_ = s.repo.MarkReceiptProcessed(ctx, d.ReceiptID)
	}
	return false
}

func (s *WakeupService) burstGate(ctx context.Context, w Wakeup, now time.Time) bool {
	if w.Kind != domain.WakeupEvent {
		return false
	}
	max := s.burstMax
	if max <= 0 {
		max = domain.BurstMaxFires
	}
	fires, err := s.repo.ListRecentWakeupFires(ctx, w.ID, now.Add(-domain.BurstWindow), 1000)
	if err != nil {
		return false
	}
	if !domain.BurstExceeded(fires, now, domain.BurstWindow, max) {
		return false
	}
	_, _ = s.repo.PauseWakeupBurst(ctx, w.ID, w.Revision, now.Add(domain.BurstQuietPeriod))
	return true
}

func (s *WakeupService) evaluateCondition(ctx context.Context, w Wakeup, now time.Time, batchDone bool) bool {
	if w.Kind == domain.WakeupSystem || condType(w.Condition) == domain.SystemRuleChildrenDone {
		return s.evaluateChildrenDone(ctx, w, now)
	}
	f := domain.FieldPredicate{}
	b, _ := json.Marshal(w.Condition)
	_ = json.Unmarshal(b, &f)
	var fp string
	if f.Field == "batch" {
		if !batchDone {
			return false
		}
		fp = domain.CondFingerprint(f.Eval(domain.EvalFacts{BatchDone: true}))
	} else {
		st, pr, err := s.facts.WorkItemFacts(ctx, w.WorkspaceID, w.WorkItemID)
		if err != nil {
			return false
		}
		fp = domain.CondFingerprint(f.Eval(domain.EvalFacts{Status: st, Priority: pr}))
	}
	return s.advanceCondition(ctx, w, now, fp)
}

func condType(cond map[string]any) string {
	if cond == nil {
		return ""
	}
	if v, ok := cond["type"].(string); ok {
		return v
	}
	return ""
}

func (s *WakeupService) evaluateChildrenDone(ctx context.Context, w Wakeup, now time.Time) bool {
	children, err := s.facts.ChildStageFacts(ctx, w.WorkspaceID, w.WorkItemID)
	if err != nil {
		return false
	}
	_, fp := domain.ChildrenDone(children)
	return s.advanceCondition(ctx, w, now, fp)
}

func (s *WakeupService) advanceCondition(ctx context.Context, w Wakeup, now time.Time, fp string) bool {
	if domain.EdgeTriggered(w.ConditionState, fp) {
		return s.fireOne(ctx, w, now, fireOpts{condState: &fp, evidence: "Condition met: " + fp})
	}

	_, _ = s.repo.FireWakeup(ctx, w.ID, w.Revision, nil, &fp, "")
	return false
}

type fireOpts struct {
	condState *string
	revGate   int64
	evidence  string
}

func (s *WakeupService) fireOne(ctx context.Context, w Wakeup, now time.Time, opts fireOpts) bool {
	ctx, cancel := context.WithTimeout(ctx, domain.DispatchBudget)
	defer cancel()
	if s.uow != nil {
		return s.fireOneTx(ctx, w, now, opts)
	}
	fired, _ := s.fireOneSteps(ctx, w, now, opts)
	return fired
}

func (s *WakeupService) fireOneTx(ctx context.Context, w Wakeup, now time.Time, opts fireOpts) bool {
	var fired, looped bool
	err := s.uow(ctx, func(ctx context.Context) error {

		if err := s.repo.SetDispatchLockTimeout(ctx); err != nil {
			return err
		}
		fired, looped = s.fireOneSteps(ctx, w, now, opts)
		if !fired && !looped {
			return errWakeupNotFired
		}

		return nil
	})
	return err == nil && fired
}

var errWakeupNotFired = errors.New("wakeup not fired")

func (s *WakeupService) fireOneSteps(ctx context.Context, w Wakeup, now time.Time, opts fireOpts) (bool, bool) {

	if err := s.repo.LockTaskOwner(ctx, w.WorkItemID); err != nil {
		return false, false
	}
	locked, ok, err := s.repo.LockWakeup(ctx, w.ID)
	if err != nil || !ok {
		return false, false
	}
	if !locked.Enabled || locked.PausedReason != "" ||
		(locked.MaxFires > 0 && locked.FireCount >= locked.MaxFires) {
		return false, false
	}

	if opts.revGate > 0 && locked.Revision != opts.revGate {
		return false, false
	}
	agentID := locked.AgentID
	if agentID == "" && locked.SystemRule != "" {

		agentID, err = s.facts.WakeupTargetAgent(ctx, locked.WorkspaceID, locked.WorkItemID)
		if err != nil {
			return false, false
		}
		if agentID == "" {

			if s.memberNotifier == nil {
				return false, false
			}
			if mr, ok := s.facts.(interface {
				WakeupMemberTarget(ctx context.Context, workspaceID, workItemID string) (string, error)
			}); ok {
				uid, merr := mr.WakeupMemberTarget(ctx, locked.WorkspaceID, locked.WorkItemID)
				if merr != nil || uid == "" {
					return false, false
				}
				body := "Work item wakeup: " + locked.Instruction
				if derr := s.memberNotifier(ctx, locked.WorkspaceID, uid,
					"work item wakeup notice", body,
					"wakeup_member:"+locked.ID+":"+itoa(locked.Revision)); derr != nil {
					return false, false
				}

				var next *time.Time
				if locked.Kind == domain.WakeupTime {
					var nv time.Time
					if locked.CronExpression != "" {
						nv, _ = cronx.Next(locked.CronExpression, locked.Timezone, now)
					} else {
						nv = domain.IntervalNext(now, locked.IntervalSeconds)
					}
					next = &nv
				}
				if updated, err := s.repo.FireWakeup(ctx, locked.ID, locked.Revision, next, opts.condState, ""); err != nil || !updated {
					return false, false
				}
				return true, false
			}
			return false, false
		}
	}

	chain, _ := s.repo.LatestWakeupChain(ctx, locked.WorkspaceID, locked.WorkItemID, agentID)
	if domain.ChainContains(chain, locked.ID) {

		_, _ = s.repo.PauseWakeupLoop(ctx, locked.ID, locked.Revision)
		return false, true
	}
	evidence := domain.CapEvidence("Wakeup: " + locked.Instruction +
		"\nPrevious task: " + nullIfEmpty(locked.LastTaskID) + "\n" + opts.evidence)
	taskID, merged, err := s.enqueueForWakeup(ctx, locked, agentID, domain.ExtendChain(chain, locked.ID), evidence)
	if err != nil {
		return false, false
	}
	var next *time.Time
	if locked.Kind == domain.WakeupTime {
		var nv time.Time
		if locked.CronExpression != "" {
			nv, _ = cronx.Next(locked.CronExpression, locked.Timezone, now)
		} else {
			nv = domain.IntervalNext(now, locked.IntervalSeconds)
		}
		next = &nv
	}
	updated, err := s.repo.FireWakeup(ctx, locked.ID, locked.Revision, next, opts.condState, taskID)
	if err != nil || !updated {
		return false, false
	}
	_ = merged
	return true, false
}

func (s *WakeupService) enqueueForWakeup(ctx context.Context, locked Wakeup, agentID string, chain []string, evidence string) (string, bool, error) {
	if cx, ok := s.enq.(WakeupChainEnqueuer); ok {
		return cx.EnqueueForWakeupWithChain(ctx, locked.WorkspaceID, locked.WorkItemID,
			agentID, locked.CreatedBy, locked.ID, chain, evidence)
	}
	if wx, ok := s.enq.(WakeupIDEnqueuer); ok {
		return wx.EnqueueForWakeupWithID(ctx, locked.WorkspaceID, locked.WorkItemID,
			agentID, locked.CreatedBy, locked.ID, evidence)
	}
	return s.enq.EnqueueForWakeup(ctx, locked.WorkspaceID, locked.WorkItemID,
		agentID, locked.CreatedBy, evidence)
}

func pickStr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func nullIfEmpty(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
