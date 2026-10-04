package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"orkome/internal/automation/domain"
	"orkome/internal/contract/events"
)

var testNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

type quotaRow struct {
	id                  string
	reserved, committed int
	status              string
}

type fakeDelivery struct {
	id, triggerID, ws, key string
	payload                []byte
	status                 string
	err                    string
	attempts               int
	nextAttempt            *time.Time
}

type fakeReceipt struct {
	id         string
	wakeupID   string
	revision   int64
	eventKey   string
	ws         string
	capturedAt time.Time
	processed  bool
	payload    []byte
}

type fakeRepo struct {
	dueOverride func(now time.Time) ([]Trigger, error)

	Repo

	autopilots map[string]Autopilot
	triggers   map[string]Trigger
	tokenIdx   map[string]string
	wakeups    map[string]Wakeup
	runs       []Run
	quota      map[string]quotaRow
	quotaByID  map[string]string
	deliveries []fakeDelivery
	receipts   []fakeReceipt

	systemDefaults map[string]SystemWakeupDefault
	subscribers    map[string][]SubscriberRef

	ruleVersions  []RuleVersion
	collaborators map[string][]Collaborator
	taskChains    map[string][]string
	chainReadErr  error
	nowFn         func() time.Time

	nextSeq int

	lockWakeupErr  error
	setTokenDup    int
	dispatchCtxs   []context.Context
	callOrder      []string
	pauseFuseCalls int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		autopilots:    map[string]Autopilot{},
		triggers:      map[string]Trigger{},
		tokenIdx:      map[string]string{},
		wakeups:       map[string]Wakeup{},
		quota:         map[string]quotaRow{},
		quotaByID:     map[string]string{},
		collaborators: map[string][]Collaborator{},
	}
}

func (f *fakeRepo) seq(prefix string) string {
	f.nextSeq++
	return fmt.Sprintf("%s-%d", prefix, f.nextSeq)
}

func (f *fakeRepo) now() time.Time {
	if f.nowFn != nil {
		return f.nowFn()
	}
	return testNow
}

type repoSnapshot struct {
	autopilots map[string]Autopilot
	triggers   map[string]Trigger
	tokenIdx   map[string]string
	wakeups    map[string]Wakeup
	runs       []Run
	quota      map[string]quotaRow
	quotaByID  map[string]string
	receipts   []fakeReceipt
}

func cpRuns(in []Run) []Run { return append([]Run(nil), in...) }

func (f *fakeRepo) snapshot() repoSnapshot {
	return repoSnapshot{autopilots: copyAP(f.autopilots), triggers: copyTrig(f.triggers),
		tokenIdx: copyStr(f.tokenIdx), wakeups: copyWk(f.wakeups), runs: cpRuns(f.runs),
		quota: copyQuota(f.quota), quotaByID: copyStr(f.quotaByID), receipts: append([]fakeReceipt(nil), f.receipts...)}
}

func (f *fakeRepo) restore(s repoSnapshot) {
	f.autopilots, f.triggers, f.tokenIdx, f.wakeups = s.autopilots, s.triggers, s.tokenIdx, s.wakeups
	f.runs, f.quota, f.quotaByID, f.receipts = s.runs, s.quota, s.quotaByID, s.receipts
}

func copyAP(m map[string]Autopilot) map[string]Autopilot {
	o := make(map[string]Autopilot, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

func copyTrig(m map[string]Trigger) map[string]Trigger {
	o := make(map[string]Trigger, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

func copyWk(m map[string]Wakeup) map[string]Wakeup {
	o := make(map[string]Wakeup, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

func copyStr(m map[string]string) map[string]string {
	o := make(map[string]string, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

func copyQuota(m map[string]quotaRow) map[string]quotaRow {
	o := make(map[string]quotaRow, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

func rollbackUow(f *fakeRepo) UoW {
	return func(ctx context.Context, fn func(ctx context.Context) error) error {
		snap := f.snapshot()
		if err := fn(ctx); err != nil {
			f.restore(snap)
			return err
		}
		return nil
	}
}

func (f *fakeRepo) CreateWakeup(_ context.Context, w Wakeup) (Wakeup, error) {
	w.ID = f.seq("wk")
	w.Revision = 1
	f.wakeups[w.ID] = w
	return w, nil
}

func (f *fakeRepo) GetWakeup(_ context.Context, ws, id string) (Wakeup, bool, error) {
	w, ok := f.wakeups[id]
	if !ok || w.WorkspaceID != ws {
		return Wakeup{}, false, nil
	}
	return w, true, nil
}

func (f *fakeRepo) ListWakeupsByWorkItem(_ context.Context, wi string) ([]Wakeup, error) {
	var out []Wakeup
	for _, w := range f.wakeups {
		if w.WorkItemID == wi {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListWorkspaceWakeups(_ context.Context, ws string) ([]Wakeup, error) {
	var out []Wakeup
	for _, w := range f.wakeups {
		if w.WorkspaceID == ws {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListPausedWakeups(_ context.Context, ws string) ([]Wakeup, error) {
	var out []Wakeup
	for _, w := range f.wakeups {
		if w.WorkspaceID == ws && w.PausedReason != "" {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListWakeupSummaries(_ context.Context, ws string) ([]WakeupSummary, error) {
	agg := map[string]*WakeupSummary{}
	for _, w := range f.wakeups {
		if w.WorkspaceID != ws {
			continue
		}
		s, ok := agg[w.WorkItemID]
		if !ok {
			s = &WakeupSummary{WorkItemID: w.WorkItemID}
			agg[w.WorkItemID] = s
		}
		s.Total++
		if w.Enabled {
			s.Enabled++
		}
		if w.PausedReason != "" {
			s.Paused++
		}
		if w.NextFireAt != nil && w.Enabled && (s.NextFireAt == nil || w.NextFireAt.Before(*s.NextFireAt)) {
			s.NextFireAt = w.NextFireAt
		}
		if w.LastTaskID != "" {
			s.LastTaskID = w.LastTaskID
		}
	}
	var out []WakeupSummary
	for _, s := range agg {
		out = append(out, *s)
	}
	return out, nil
}

func (f *fakeRepo) SetWakeupEnabled(_ context.Context, _, id string, en bool) error {
	if w, ok := f.wakeups[id]; ok {
		w.Enabled = en
		w.Revision++
		f.wakeups[id] = w
	}
	return nil
}

func (f *fakeRepo) DeleteWakeup(_ context.Context, _, id string) error {
	delete(f.wakeups, id)
	return nil
}

func (f *fakeRepo) UpdateWakeup(_ context.Context, w Wakeup, expectedRev int64) (Wakeup, bool, error) {
	cur, ok := f.wakeups[w.ID]
	if !ok || cur.Revision != expectedRev {
		return cur, false, nil
	}
	w.Revision = cur.Revision + 1
	f.wakeups[w.ID] = w
	return w, true, nil
}

func (f *fakeRepo) SetWakeupInstruction(_ context.Context, _, id, instr string) (bool, error) {
	w, ok := f.wakeups[id]
	if !ok {
		return false, nil
	}
	w.Instruction = instr
	w.Revision++
	f.wakeups[id] = w
	return true, nil
}

func (f *fakeRepo) ListDueWakeups(_ context.Context, now time.Time, limit int) ([]DueWakeup, error) {
	var out []DueWakeup
	for _, w := range f.wakeups {
		if !w.Enabled || w.PausedReason != "" {
			continue
		}
		switch {
		case w.ExpiresAt != nil && !w.ExpiresAt.After(now):
			out = append(out, DueWakeup{Wakeup: w, Source: ScanExpire})
		case w.Kind == domain.WakeupTime && w.NextFireAt != nil && !w.NextFireAt.After(now):
			out = append(out, DueWakeup{Wakeup: w, Source: ScanDue})
		case w.Kind == domain.WakeupCondition || w.Kind == domain.WakeupSystem:
			out = append(out, DueWakeup{Wakeup: w, Source: ScanCondition})
		}
	}
	for _, rc := range f.receipts {
		if rc.processed {
			continue
		}
		w, ok := f.wakeups[rc.wakeupID]
		if !ok || !w.Enabled || w.PausedReason != "" {
			continue
		}
		out = append(out, DueWakeup{Wakeup: w, Source: ScanReceipt, ReceiptID: rc.id,
			ReceiptRev: rc.revision, EventKey: rc.eventKey, CapturedAt: &rc.capturedAt})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeRepo) ListConditionWakeups(_ context.Context) ([]Wakeup, error) {
	var out []Wakeup
	for _, w := range f.wakeups {
		if w.Enabled && w.Kind == domain.WakeupCondition {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListEventWakeups(_ context.Context, ws, eventType string) ([]Wakeup, error) {
	var out []Wakeup
	for _, w := range f.wakeups {
		if !w.Enabled || w.Kind != domain.WakeupEvent || w.WorkspaceID != ws {
			continue
		}
		for _, et := range w.EventTypes {
			if et == eventType {
				out = append(out, w)
				break
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) InsertReceipt(_ context.Context, rc Receipt) error {
	for _, ex := range f.receipts {
		if ex.wakeupID == rc.WakeupID && ex.revision == rc.Revision && ex.eventKey == rc.EventKey {
			return nil
		}
	}
	id := rc.ID
	if id == "" {
		id = f.seq("rc")
	}
	f.receipts = append(f.receipts, fakeReceipt{id: id, wakeupID: rc.WakeupID, revision: rc.Revision,
		eventKey: rc.EventKey, ws: rc.WorkspaceID, capturedAt: rc.CapturedAt, payload: rc.Payload})
	return nil
}

func (f *fakeRepo) MarkReceiptProcessed(_ context.Context, id string) error {
	for i := range f.receipts {
		if f.receipts[i].id == id {
			f.receipts[i].processed = true
		}
	}
	return nil
}

func (f *fakeRepo) MarkWakeupExpired(_ context.Context, id string, expectedRev int64) (bool, error) {
	w, ok := f.wakeups[id]
	if !ok || w.Revision != expectedRev || !w.Enabled {
		return false, nil
	}
	w.Enabled = false
	w.PausedReason = "expired"
	w.Revision++
	f.wakeups[id] = w
	return true, nil
}

func (f *fakeRepo) ListRecentWakeupFires(_ context.Context, wakeupID string, since time.Time, limit int) ([]time.Time, error) {
	var out []time.Time
	for _, rc := range f.receipts {
		if rc.wakeupID == wakeupID && !rc.capturedAt.Before(since) {
			out = append(out, rc.capturedAt)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) PauseWakeupBurst(_ context.Context, id string, expectedRev int64, resumeAt time.Time) (bool, error) {
	w, ok := f.wakeups[id]
	if !ok || w.Revision != expectedRev || !w.Enabled || w.PausedReason != "" {
		return false, nil
	}
	w.PausedReason = domain.PausedReasonBurst
	w.NextFireAt = &resumeAt
	w.Revision++
	f.wakeups[id] = w
	return true, nil
}

func (f *fakeRepo) ListBurstPausedWakeups(_ context.Context, now time.Time, limit int) ([]Wakeup, error) {
	var out []Wakeup
	for _, w := range f.wakeups {
		if w.PausedReason == domain.PausedReasonBurst && w.Enabled &&
			w.NextFireAt != nil && !w.NextFireAt.After(now) {
			out = append(out, w)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) ResumeWakeupBurst(_ context.Context, id string, expectedRev int64) (bool, error) {
	w, ok := f.wakeups[id]
	if !ok || w.Revision != expectedRev || w.PausedReason != domain.PausedReasonBurst {
		return false, nil
	}
	w.PausedReason = ""
	w.NextFireAt = nil
	w.Revision++
	f.wakeups[id] = w
	return true, nil
}

func (f *fakeRepo) LockTaskOwner(_ context.Context, workItemID string) error {
	f.callOrder = append(f.callOrder, "lockOwner:"+workItemID)
	return nil
}

func (f *fakeRepo) LockWakeup(_ context.Context, id string) (Wakeup, bool, error) {
	if f.lockWakeupErr != nil {
		return Wakeup{}, false, f.lockWakeupErr
	}
	w, ok := f.wakeups[id]
	f.callOrder = append(f.callOrder, "lockWakeup")
	return w, ok, nil
}

func (f *fakeRepo) FireWakeup(_ context.Context, id string, rev int64, next *time.Time, cond *string, taskID string) (bool, error) {
	w, ok := f.wakeups[id]
	if !ok || w.Revision != rev {
		return false, nil
	}
	w.FireCount++
	w.Revision++
	if next != nil {
		w.NextFireAt = next
	}
	if cond != nil {
		w.ConditionState = cond
	}
	if taskID != "" {
		w.LastTaskID = taskID
	}
	f.wakeups[id] = w
	return true, nil
}

func (f *fakeRepo) ListSystemWakeups(_ context.Context, ws string) ([]Wakeup, error) {
	var out []Wakeup
	for _, w := range f.wakeups {
		if w.WorkspaceID == ws && w.SystemRule != "" {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListSystemWakeupsByWorkItem(_ context.Context, ws, wi string) ([]Wakeup, error) {
	var out []Wakeup
	for _, w := range f.wakeups {
		if w.WorkspaceID == ws && w.WorkItemID == wi && w.SystemRule != "" {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeRepo) EnsureSystemWakeup(_ context.Context, w Wakeup) (Wakeup, error) {
	for _, ex := range f.wakeups {
		if ex.WorkItemID == w.WorkItemID && ex.SystemRule == domain.SystemRuleChildrenDone {
			return ex, nil
		}
	}
	w.ID = f.seq("syswk")
	w.Revision = 1
	f.wakeups[w.ID] = w
	return w, nil
}

func (f *fakeRepo) UpdateSystemWakeup(_ context.Context, ws, id string, instruction *string, enabled *bool) (Wakeup, bool, error) {
	w, ok := f.wakeups[id]
	if !ok || w.WorkspaceID != ws || w.SystemRule == "" {
		return w, false, nil
	}
	if instruction != nil {
		w.Instruction = *instruction
	}
	if enabled != nil {
		w.Enabled = *enabled
	}
	w.Revision++
	f.wakeups[id] = w
	return w, true, nil
}

func (f *fakeRepo) GetSystemWakeupDefault(_ context.Context, ws string) (SystemWakeupDefault, bool, error) {
	d, ok := f.systemDefaults[ws]
	return d, ok, nil
}

func (f *fakeRepo) SetSystemWakeupDefault(_ context.Context, ws string, d SystemWakeupDefault) error {
	if f.systemDefaults == nil {
		f.systemDefaults = map[string]SystemWakeupDefault{}
	}
	f.systemDefaults[ws] = d
	return nil
}

func (f *fakeRepo) CreateAutopilot(_ context.Context, a Autopilot) (Autopilot, error) {
	a.ID = f.seq("ap")
	f.autopilots[a.ID] = a
	return a, nil
}

func (f *fakeRepo) GetAutopilot(_ context.Context, _, id string) (Autopilot, bool, error) {
	a, ok := f.autopilots[id]
	return a, ok, nil
}

func (f *fakeRepo) ListAutopilots(_ context.Context, _ string, _ int) ([]Autopilot, error) {
	var out []Autopilot
	for _, a := range f.autopilots {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeRepo) CreateTrigger(_ context.Context, t Trigger) (Trigger, error) {
	t.ID = f.seq("trig")
	f.triggers[t.ID] = t
	f.tokenIdx[t.WebhookToken] = t.ID
	return t, nil
}

func (f *fakeRepo) GetTriggerByToken(_ context.Context, token string) (Trigger, bool, error) {
	id, ok := f.tokenIdx[token]
	if !ok {
		return Trigger{}, false, nil
	}
	t := f.triggers[id]
	t.SetWorkspace(f.autopilots[t.AutopilotID].WorkspaceID)
	return t, true, nil
}

func (f *fakeRepo) GetTriggerByID(_ context.Context, id string) (Trigger, bool, error) {
	t, ok := f.triggers[id]
	if ok {
		t.SetWorkspace(f.autopilots[t.AutopilotID].WorkspaceID)
	}
	return t, ok, nil
}

func (f *fakeRepo) ListDueScheduleTriggers(_ context.Context, now time.Time) ([]Trigger, error) {
	if f.dueOverride != nil {
		return f.dueOverride(now)
	}
	var out []Trigger
	for _, t := range f.triggers {
		if t.Enabled && t.NextRunAt != nil && !t.NextRunAt.After(now) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeRepo) ClaimDueTrigger(_ context.Context, id string, from, to *time.Time) (bool, error) {
	t, ok := f.triggers[id]
	if !ok {
		return false, nil
	}
	same := (t.NextRunAt == nil && from == nil) ||
		(t.NextRunAt != nil && from != nil && t.NextRunAt.Equal(*from))
	if !same {
		return false, nil
	}
	t.NextRunAt = to
	f.triggers[id] = t
	return true, nil
}

func (f *fakeRepo) UpdateTrigger(_ context.Context, ws, apID, trigID string, cron, tz *string, nextRun *time.Time, enabled *bool) (bool, error) {
	t, ok := f.triggers[trigID]
	if !ok || t.AutopilotID != apID || f.autopilots[apID].WorkspaceID != ws {
		return false, nil
	}
	if cron != nil {
		t.CronExpression = *cron
	}
	if tz != nil {
		t.Timezone = *tz
	}
	if nextRun != nil {
		t.NextRunAt = nextRun
	}
	if enabled != nil {
		t.Enabled = *enabled
	}
	f.triggers[trigID] = t
	return true, nil
}

func (f *fakeRepo) DeleteTrigger(_ context.Context, ws, apID, trigID string) (bool, error) {
	t, ok := f.triggers[trigID]
	if !ok || t.AutopilotID != apID || f.autopilots[apID].WorkspaceID != ws {
		return false, nil
	}
	delete(f.tokenIdx, t.WebhookToken)
	delete(f.triggers, trigID)
	return true, nil
}

func (f *fakeRepo) SetTriggerSigningSecret(_ context.Context, ws, apID, trigID, secret string) (bool, error) {
	t, ok := f.triggers[trigID]
	if !ok || t.AutopilotID != apID || f.autopilots[apID].WorkspaceID != ws {
		return false, nil
	}
	t.SigningSecret = secret
	f.triggers[trigID] = t
	return true, nil
}

func (f *fakeRepo) SetTriggerWebhookToken(_ context.Context, ws, apID, trigID, token string) (bool, error) {
	if f.setTokenDup > 0 {
		f.setTokenDup--
		return false, fmt.Errorf("rotate: %w", domain.ErrWebhookTokenCollision)
	}
	t, ok := f.triggers[trigID]
	if !ok || t.AutopilotID != apID || f.autopilots[apID].WorkspaceID != ws {
		return false, nil
	}
	delete(f.tokenIdx, t.WebhookToken)
	t.WebhookToken = token
	f.triggers[trigID] = t
	f.tokenIdx[token] = trigID
	return true, nil
}

func (f *fakeRepo) InsertRun(_ context.Context, r Run) (Run, error) {
	r.ID = f.seq("run")
	f.runs = append(f.runs, r)
	return r, nil
}

func (f *fakeRepo) SetRunTask(_ context.Context, id, taskID, wi string) error {
	for i := range f.runs {
		if f.runs[i].ID == id {
			f.runs[i].TaskID = taskID
			f.runs[i].WorkItemID = wi
			now := testNow
			f.runs[i].TriggeredAt = &now
		}
	}
	return nil
}

func (f *fakeRepo) SetRunStatus(_ context.Context, id, status, _ string) error {
	for i := range f.runs {
		if f.runs[i].ID == id {
			f.runs[i].Status = status
		}
	}
	return nil
}

func (f *fakeRepo) ListRunsByAutopilot(_ context.Context, ws, apID string, limit int) ([]Run, error) {
	var out []Run
	for i := len(f.runs) - 1; i >= 0 && len(out) < limit; i-- {
		r := f.runs[i]
		if r.AutopilotID != apID {
			continue
		}
		if f.autopilots[r.AutopilotID].WorkspaceID != ws {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeRepo) GetRun(_ context.Context, ws, runID string) (Run, bool, error) {
	for _, r := range f.runs {
		if r.ID == runID && f.autopilots[r.AutopilotID].WorkspaceID == ws {
			return r, true, nil
		}
	}
	return Run{}, false, nil
}

func (f *fakeRepo) SetRunStatusByTask(_ context.Context, taskID, status string) error {
	for i := range f.runs {
		r := &f.runs[i]
		if r.TaskID != taskID || r.Status != domain.RunRunning {
			continue
		}
		r.Status = status
		if key, ok := f.quotaByID[r.QuotaReservationID]; ok {
			q := f.quota[key]
			if q.reserved > 0 {
				q.reserved--
				q.committed++
				if q.reserved == 0 {
					q.status = "committed"
				}
				f.quota[key] = q
			}
		}
	}
	return nil
}

func (f *fakeRepo) ReserveQuota(_ context.Context, apID string, wsStart, _ time.Time, limit int) (string, error) {
	key := apID + "|" + wsStart.Format(time.RFC3339)
	q, ok := f.quota[key]
	if !ok {
		q = quotaRow{id: f.seq("qr"), reserved: 1, status: "reserved"}
		f.quota[key] = q
		f.quotaByID[q.id] = key
		return q.id, nil
	}
	if q.reserved+q.committed >= limit {
		return "", fmt.Errorf("quota window: %w", domain.ErrQuotaExceeded)
	}
	q.reserved++
	f.quota[key] = q
	return q.id, nil
}

func (f *fakeRepo) SetDispatchLockTimeout(ctx context.Context) error {
	f.dispatchCtxs = append(f.dispatchCtxs, ctx)
	return nil
}

func (f *fakeRepo) InsertDelivery(_ context.Context, _, trigID, key string, payload []byte) (string, bool, error) {
	for _, d := range f.deliveries {
		if d.key == key {
			return "", true, nil
		}
	}
	id := f.seq("dlv")
	f.deliveries = append(f.deliveries, fakeDelivery{id: id, triggerID: trigID, key: key, payload: payload, status: "queued"})
	return id, false, nil
}

func (f *fakeRepo) ClaimDelivery(_ context.Context) (string, string, string, []byte, int, bool, error) {
	now := f.now()
	for i := range f.deliveries {
		d := &f.deliveries[i]
		if d.status != "queued" {
			continue
		}

		if d.nextAttempt != nil && d.nextAttempt.After(now) {
			continue
		}
		d.status = "processing"
		d.attempts++
		return d.id, d.triggerID, "", d.payload, d.attempts, true, nil
	}
	return "", "", "", nil, 0, false, nil
}

func (f *fakeRepo) FinishDelivery(_ context.Context, id, status, errMsg string) error {
	for i := range f.deliveries {
		if f.deliveries[i].id == id {
			f.deliveries[i].status = status
			f.deliveries[i].err = errMsg
			f.deliveries[i].nextAttempt = nil
		}
	}
	return nil
}

func (f *fakeRepo) FailDelivery(_ context.Context, id string, deadLetter bool, nextAttemptAt *time.Time, errMsg string) error {
	for i := range f.deliveries {
		if f.deliveries[i].id == id {
			if deadLetter {
				f.deliveries[i].status = "failed"
			} else {
				f.deliveries[i].status = "queued"
			}
			f.deliveries[i].err = errMsg
			f.deliveries[i].nextAttempt = nextAttemptAt
		}
	}
	return nil
}

func (f *fakeRepo) GetDelivery(_ context.Context, ws, id string) (Delivery, bool, error) {
	for _, d := range f.deliveries {
		if d.id == id {
			return Delivery{ID: d.id, WorkspaceID: ws, TriggerID: d.triggerID, Status: d.status,
				Attempts: d.attempts, Error: d.err, CreatedAt: testNow, NextAttemptAt: d.nextAttempt}, true, nil
		}
	}
	return Delivery{}, false, nil
}

func (f *fakeRepo) ListDeliveries(_ context.Context, ws string, limit int) ([]Delivery, error) {
	var out []Delivery
	for i := len(f.deliveries) - 1; i >= 0 && len(out) < limit; i-- {
		d := f.deliveries[i]
		out = append(out, Delivery{ID: d.id, WorkspaceID: ws, TriggerID: d.triggerID, Status: d.status,
			Attempts: d.attempts, Error: d.err, CreatedAt: testNow, NextAttemptAt: d.nextAttempt})
	}
	return out, nil
}

func (f *fakeRepo) ReplayDelivery(_ context.Context, ws, id string) (bool, error) {
	for i := range f.deliveries {
		if f.deliveries[i].id == id {
			f.deliveries[i].status = "queued"
			f.deliveries[i].err = ""
			f.deliveries[i].attempts = 0
			f.deliveries[i].nextAttempt = nil
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) PauseAutopilotsByFailureRate(_ context.Context, window time.Duration, minRuns int, failRate float64, pausedReason string) ([]AutopilotFailureStat, error) {
	f.pauseFuseCalls++
	_ = minRuns
	_ = failRate
	now := f.now()
	runsByAP := map[string]*AutopilotFailureStat{}
	for _, r := range f.runs {
		if r.PlannedAt.Before(now.Add(-window)) {
			continue
		}
		st, ok := runsByAP[r.AutopilotID]
		if !ok {
			ap := f.autopilots[r.AutopilotID]
			st = &AutopilotFailureStat{AutopilotID: r.AutopilotID, WorkspaceID: ap.WorkspaceID,
				Name: ap.Name, CreatedBy: ap.CreatedBy}
			runsByAP[r.AutopilotID] = st
		}
		st.Runs++
		if r.Status == domain.RunFailed {
			st.Failed++
		}
	}
	var out []AutopilotFailureStat
	for _, st := range runsByAP {
		if !domain.FailureFuseTripped(st.Runs, st.Failed) {
			continue
		}
		ap, ok := f.autopilots[st.AutopilotID]
		if !ok || !ap.Enabled {
			continue
		}
		ap.Enabled = false
		ap.PausedReason = pausedReason
		f.autopilots[st.AutopilotID] = ap
		out = append(out, *st)
	}
	return out, nil
}

func (f *fakeRepo) LatestWakeupChain(_ context.Context, ws, wi, agentID string) ([]string, error) {
	if f.chainReadErr != nil {
		return nil, f.chainReadErr
	}
	return f.taskChains[ws+"|"+wi+"|"+agentID], nil
}

func (f *fakeRepo) PauseWakeupLoop(_ context.Context, id string, expectedRev int64) (bool, error) {
	w, ok := f.wakeups[id]
	if !ok || w.Revision != expectedRev || !w.Enabled || w.PausedReason != "" {
		return false, nil
	}
	w.PausedReason = domain.PausedReasonLoop
	w.Revision++
	f.wakeups[id] = w
	return true, nil
}

func (f *fakeRepo) ListTemplateSubscribers(_ context.Context, autopilotID string) ([]SubscriberRef, error) {
	return f.subscribers[autopilotID], nil
}

type fakeEnqueuer struct {
	err    error
	calls  int
	agents []string
	chains [][]string
}

func (e *fakeEnqueuer) EnqueueForWakeup(_ context.Context, _, _, agentID, _, _ string) (string, bool, error) {
	e.calls++
	e.agents = append(e.agents, agentID)
	if e.err != nil {
		return "", false, e.err
	}
	return fmt.Sprintf("task-%d", e.calls), false, nil
}

func (e *fakeEnqueuer) EnqueueForWakeupWithChain(_ context.Context, _, _, agentID, _, _ string, chain []string, _ string) (string, bool, error) {
	e.calls++
	e.agents = append(e.agents, agentID)
	cp := append([]string(nil), chain...)
	e.chains = append(e.chains, cp)
	if e.err != nil {
		return "", false, e.err
	}
	return fmt.Sprintf("task-%d", e.calls), false, nil
}

type fakeCreator struct{ err error }

func (c *fakeCreator) CreateForAutopilot(_ context.Context, _, _, _, _, _, _ string) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	return "wi-1", nil
}

type subscriberCreator struct {
	fakeCreator
	got []SubscriberRef
}

func (c *subscriberCreator) CreateForAutopilotWithSubscribers(_ context.Context, _, _, _, _, _, _ string, subs []SubscriberRef) (string, error) {
	c.got = subs
	return "wi-1", nil
}

type fakeFacts struct {
	children    []domain.ChildStageFact
	status      string
	priority    int
	targetAgent string
	err         error
}

func (f fakeFacts) WorkItemFacts(context.Context, string, string) (string, int, error) {
	return f.status, f.priority, f.err
}

func (f fakeFacts) ChildStageFacts(_ context.Context, _, _ string) ([]domain.ChildStageFact, error) {
	return f.children, f.err
}

func (f fakeFacts) WakeupTargetAgent(context.Context, string, string) (string, error) {
	return f.targetAgent, f.err
}

func sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + string(body)))
	return hex.EncodeToString(mac.Sum(nil))
}

func newTestAutopilotService(repo *fakeRepo, enq *fakeEnqueuer) *AutopilotService {
	s := NewAutopilotService(repo, &fakeCreator{}, enq)
	s.now = func() time.Time { return testNow }
	return s
}

func newTestWakeupService(repo *fakeRepo, enq *fakeEnqueuer, facts FactsReader) *WakeupService {
	s := NewWakeupService(repo, enq, facts)
	s.now = func() time.Time { return testNow }
	return s
}

func seedAutopilotWithTrigger(t *testing.T, repo *fakeRepo, dailyLimit int) (Autopilot, Trigger) {
	t.Helper()
	ap := Autopilot{WorkspaceID: "ws-1", Name: "ap", ExecMode: "task_only", DailyLimit: dailyLimit, Enabled: true}
	ap, err := repo.CreateAutopilot(context.Background(), ap)
	require.NoError(t, err)
	sec, err := domain.MintSigningSecret()
	require.NoError(t, err)
	trig := Trigger{AutopilotID: ap.ID, Kind: domain.TriggerWebhook, Enabled: true, WebhookToken: "tok-old", SigningSecret: sec}
	trig, err = repo.CreateTrigger(context.Background(), trig)
	require.NoError(t, err)
	return ap, trig
}

func seedWakeup(t *testing.T, svc *WakeupService, repo *fakeRepo, kind, ws, wi, agent string, mutators ...func(*Wakeup)) Wakeup {
	t.Helper()
	w, err := svc.Create(context.Background(), Wakeup{
		WorkspaceID: ws, WorkItemID: wi, AgentID: agent, Kind: kind, Instruction: "check it",
		IntervalSeconds: 3600, EventTypes: []string{"comment.created"},
		Condition: map[string]any{"field": "status", "op": "eq", "value": "done"},
	})
	require.NoError(t, err)
	stored := repo.wakeups[w.ID]
	for _, m := range mutators {
		m(&stored)
	}
	repo.wakeups[w.ID] = stored
	return stored
}

func TestIngest_EmptySigningSecret_FailClosed(t *testing.T) {
	repo := newFakeRepo()
	ap, trig := seedAutopilotWithTrigger(t, repo, 10)
	trig.SigningSecret = ""
	repo.triggers[trig.ID] = trig
	svc := NewWebhookService(repo, newTestAutopilotService(repo, &fakeEnqueuer{}))
	svc.now = func() time.Time { return testNow }

	ts := fmt.Sprintf("%d", testNow.Unix())
	body := []byte(`{"a":1}`)

	code, err := svc.Ingest(context.Background(), "tok-old", sign("", ts, body), ts, body)
	require.Error(t, err)
	require.Equal(t, 403, code)
	_ = ap
}

func TestIngest_MintedSecret_VerifiesAndRejects(t *testing.T) {
	repo := newFakeRepo()
	_, trig := seedAutopilotWithTrigger(t, repo, 10)
	svc := NewWebhookService(repo, newTestAutopilotService(repo, &fakeEnqueuer{}))
	svc.now = func() time.Time { return testNow }

	body := []byte(`{"a":1}`)
	ts := fmt.Sprintf("%d", testNow.Unix())

	code, err := svc.Ingest(context.Background(), "tok-old", sign(trig.SigningSecret, ts, body), ts, body)
	require.NoError(t, err)
	require.Equal(t, 202, code)

	code, err = svc.Ingest(context.Background(), "tok-old", "deadbeef", ts, body)
	require.Error(t, err)
	require.Equal(t, 403, code)

	stale := fmt.Sprintf("%d", testNow.Add(-10*time.Minute).Unix())
	code, err = svc.Ingest(context.Background(), "tok-old", sign(trig.SigningSecret, stale, body), stale, body)
	require.Error(t, err)
	require.Equal(t, 403, code)
}

func TestCreateAutopilot_MintsSigningSecret(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestAutopilotService(repo, &fakeEnqueuer{})
	_, trig, err := svc.Create(context.Background(), Autopilot{WorkspaceID: "ws-1", Name: "ap"}, "", "UTC")
	require.NoError(t, err)
	require.NotEmpty(t, trig.SigningSecret)
	require.Len(t, trig.SigningSecret, 64)
	stored, ok := repo.triggers[trig.ID]
	require.True(t, ok)
	require.Equal(t, trig.SigningSecret, stored.SigningSecret)
}

func TestSetSigningSecret_ValidationAndFingerprint(t *testing.T) {
	repo := newFakeRepo()
	ap, trig := seedAutopilotWithTrigger(t, repo, 10)
	svc := NewWebhookService(repo, newTestAutopilotService(repo, &fakeEnqueuer{}))

	_, err := svc.SetSigningSecret(context.Background(), "ws-1", ap.ID, trig.ID, "short")
	require.Error(t, err)
	require.Contains(t, err.Error(), "E_VALIDATION")

	secret := strings.Repeat("k", 40)
	cred, err := svc.SetSigningSecret(context.Background(), "ws-1", ap.ID, trig.ID, secret)
	require.NoError(t, err)
	require.Equal(t, trig.ID, cred.TriggerID)
	require.True(t, cred.HasSigningSecret)
	require.Equal(t, domain.SecretFingerprint(secret), cred.SecretFingerprint)
	require.Len(t, cred.SecretFingerprint, 8)
	require.NotContains(t, cred.SecretFingerprint, secret, "fingerprint never carries plaintext")

	ts := fmt.Sprintf("%d", testNow.Unix())
	body := []byte(`{"b":2}`)
	svc.now = func() time.Time { return testNow }
	code, err := svc.Ingest(context.Background(), "tok-old", sign(secret, ts, body), ts, body)
	require.NoError(t, err)
	require.Equal(t, 202, code)
	code, err = svc.Ingest(context.Background(), "tok-old", sign(trig.SigningSecret, ts, body), ts, body)
	require.Error(t, err)
	require.Equal(t, 403, code)

	_, err = svc.SetSigningSecret(context.Background(), "ws-other", ap.ID, trig.ID, secret)
	require.Error(t, err)
	require.Contains(t, err.Error(), "E_NOT_FOUND")
}

func TestRotateWebhookToken_ReplacesOldToken(t *testing.T) {
	repo := newFakeRepo()
	ap, trig := seedAutopilotWithTrigger(t, repo, 10)
	svc := NewWebhookService(repo, newTestAutopilotService(repo, &fakeEnqueuer{}))
	svc.now = func() time.Time { return testNow }

	repo.setTokenDup = 1
	cred, err := svc.RotateWebhookToken(context.Background(), "ws-1", ap.ID, trig.ID)
	require.NoError(t, err)
	require.NotEmpty(t, cred.WebhookToken)
	require.NotEqual(t, "tok-old", cred.WebhookToken)

	ts := fmt.Sprintf("%d", testNow.Unix())
	body := []byte(`{"c":3}`)
	code, err := svc.Ingest(context.Background(), "tok-old", sign(trig.SigningSecret, ts, body), ts, body)
	require.Error(t, err)
	require.Equal(t, 404, code, "old tokens must be invalidated immediately")

	stored, _, _ := repo.GetTriggerByToken(context.Background(), cred.WebhookToken)
	code, err = svc.Ingest(context.Background(), cred.WebhookToken, sign(stored.SigningSecret, ts, body), ts, body)
	require.NoError(t, err)
	require.Equal(t, 202, code, "the new token must be usable")

	_, err = svc.RotateWebhookToken(context.Background(), "ws-1", ap.ID, "trig-nope")
	require.Error(t, err)
	require.Contains(t, err.Error(), "E_NOT_FOUND")
}

func TestStartRun_QuotaReserveCommitCycle(t *testing.T) {
	repo := newFakeRepo()
	ap, trig := seedAutopilotWithTrigger(t, repo, 2)
	enq := &fakeEnqueuer{}
	svc := newTestAutopilotService(repo, enq)
	svc.SetUow(rollbackUow(repo))
	wh := NewWebhookService(repo, svc)
	wh.now = func() time.Time { return testNow }

	for i := 0; i < 2; i++ {
		run, err := svc.StartRun(context.Background(), ap.ID, "", "manual")
		require.NoError(t, err)
		require.Equal(t, domain.RunRunning, run.Status)
		require.NotEmpty(t, run.QuotaReservationID)
	}
	ws, _ := domain.QuotaWindow(testNow)
	key := ap.ID + "|" + ws.Format(time.RFC3339)
	require.Equal(t, 2, repo.quota[key].reserved)

	run3, err := svc.StartRun(context.Background(), ap.ID, "", "manual")
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrQuotaExceeded)
	require.Equal(t, domain.RunSkipped, run3.Status)
	require.Equal(t, domain.ReasonQuotaExceeded, run3.ReasonCode)
	require.Empty(t, run3.QuotaReservationID, "skipped runs do not consume reservations")
	require.Equal(t, 2, repo.quota[key].reserved, "over-quota attempts must not pollute the count")

	svc.BackfillFromTask(context.Background(), "task-1", "completed")
	require.Equal(t, 1, repo.quota[key].reserved)
	require.Equal(t, 1, repo.quota[key].committed)

	_, err = svc.StartRun(context.Background(), ap.ID, "", "manual")
	require.ErrorIs(t, err, domain.ErrQuotaExceeded)

	svc.BackfillFromTask(context.Background(), "task-1", "completed")
	require.Equal(t, 1, repo.quota[key].committed)

	repo.deliveries = append(repo.deliveries, fakeDelivery{id: "dlv-9", triggerID: trig.ID, status: "queued"})
	ok, err := wh.ConsumeOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "skipped", repo.deliveries[0].status, "over-quota deliveries must be skipped, not failed")
}

func TestStartRun_EnqueueFailure_RollsBackAllWrites(t *testing.T) {
	repo := newFakeRepo()
	ap, _ := seedAutopilotWithTrigger(t, repo, 5)
	enq := &fakeEnqueuer{err: errors.New("enqueue down")}
	svc := newTestAutopilotService(repo, enq)
	svc.SetUow(rollbackUow(repo))

	_, err := svc.StartRun(context.Background(), ap.ID, "", "manual")
	require.Error(t, err)
	require.NotErrorIs(t, err, domain.ErrQuotaExceeded)

	require.Empty(t, repo.runs, "enqueue failure must leave no run rows")
	ws, _ := domain.QuotaWindow(testNow)
	key := ap.ID + "|" + ws.Format(time.RFC3339)
	_, hasQuota := repo.quota[key]
	require.False(t, hasQuota, "rollback must revoke the quota reservation too")

	ap.ExecMode = "create_work_item"
	repo.autopilots[ap.ID] = ap
	_, err = svc.StartRun(context.Background(), ap.ID, "", "manual")
	require.Error(t, err)
	require.Empty(t, repo.runs)
}

func TestFireOneTx_DispatchBudgetAndLockTimeoutInTx(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})

	var txCtx context.Context
	uow := func(ctx context.Context, fn func(ctx context.Context) error) error {
		txCtx = ctx
		return fn(ctx)
	}
	svc.SetUow(uow)

	w := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-1", "ag-1")

	before := time.Now()
	require.True(t, svc.fireOne(context.Background(), w, testNow, fireOpts{}))

	dl, ok := txCtx.Deadline()
	require.True(t, ok, "dispatch transaction ctx must carry the budget deadline")
	require.LessOrEqual(t, dl.Sub(before), domain.DispatchBudget+50*time.Millisecond)

	require.Len(t, repo.dispatchCtxs, 1, "SET LOCAL lock_timeout must run once with the dispatch transaction")
	require.Equal(t, txCtx, repo.dispatchCtxs[0], "lock_timeout must apply to the transaction ctx (SET LOCAL transaction scope)")
}

func TestTickOnce_LockTimeoutAbortsQuietly(t *testing.T) {
	repo := newFakeRepo()
	repo.lockWakeupErr = fmt.Errorf("fetch: %w", domain.ErrLockTimeout)
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetUow(rollbackUow(repo))

	w := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-1", "ag-1")
	stored := repo.wakeups[w.ID]
	stored.NextFireAt = &testNow
	repo.wakeups[w.ID] = stored

	fired, err := svc.TickOnce(context.Background())
	require.NoError(t, err, "lock timeout is a retryable sentinel, must not kill the worker")
	require.Equal(t, 0, fired)
	require.Equal(t, 0, enq.calls, "lock wait failure must not enqueue")
}

func TestFireOne_LockOrderOwnerFenceBeforeWakeupLock(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetUow(rollbackUow(repo))

	w := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-owner", "ag-1")
	require.True(t, svc.fireOne(context.Background(), w, testNow, fireOpts{}))

	require.NotEmpty(t, repo.callOrder, "dispatch must reach the lock face")
	require.Equal(t, "lockOwner:wi-owner", repo.callOrder[0], "lock order steps 1-2 = owner fence lock + lock task")
	require.Equal(t, "lockWakeup", repo.callOrder[1], "lock order step 3 = lock the wakeup row")
	require.Len(t, repo.callOrder, 2, "dispatch path must not have pre-calls outside the lock order")
}

func TestRecordEvent_MatchFilterAndIdempotent(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestWakeupService(repo, &fakeEnqueuer{}, fakeFacts{})

	hit := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(w *Wakeup) { w.EventTypes = []string{"comment.created"} })
	filtered := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(w *Wakeup) { w.EventTypes = []string{"comment.created"}; w.FilterAgentID = "ag-other" })
	other := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(w *Wakeup) { w.EventTypes = []string{"task.completed"} })

	svc.RecordEvent(context.Background(), events.Envelope{
		EventID: "evt-1", Event: "comment.created", WorkspaceID: "ws-1",
		Payload: events.CommentPayload{ID: "c-1", AuthorType: "agent", AuthorID: "ag-1", Content: "hi"},
	})

	svc.RecordEvent(context.Background(), events.Envelope{
		EventID: "evt-1", Event: "comment.created", WorkspaceID: "ws-1",
		Payload: events.CommentPayload{ID: "c-1", AuthorType: "agent", AuthorID: "ag-1"},
	})

	var hitRows, filteredRows, otherRows int
	for _, rc := range repo.receipts {
		switch rc.wakeupID {
		case hit.ID:
			hitRows++
			require.Equal(t, "evt-1", rc.eventKey)
			require.Equal(t, hit.Revision, rc.revision, "receipts carry the rule revision at capture time")
		case filtered.ID:
			filteredRows++
		case other.ID:
			otherRows++
		}
	}
	require.Equal(t, 1, hitRows, "hit rules yield exactly one receipt (idempotent key dedup)")
	require.Equal(t, 0, filteredRows, "rules whose filter predicate misses produce no receipts")
	require.Equal(t, 0, otherRows, "rules not subscribing to the event produce no receipts")
}

func TestTickOnce_ReceiptRevisionGate(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetUow(rollbackUow(repo))

	stale := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(w *Wakeup) { w.EventTypes = []string{"comment.created"} })
	fresh := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-2",
		func(w *Wakeup) { w.EventTypes = []string{"comment.created"} })

	edited := repo.wakeups[stale.ID]
	edited.Revision = 2
	repo.wakeups[stale.ID] = edited

	require.NoError(t, repo.InsertReceipt(context.Background(), Receipt{
		WakeupID: stale.ID, Revision: 1, EventKey: "evt-stale", WorkspaceID: "ws-1", CapturedAt: testNow}))
	require.NoError(t, repo.InsertReceipt(context.Background(), Receipt{
		WakeupID: fresh.ID, Revision: 1, EventKey: "evt-fresh", WorkspaceID: "ws-1", CapturedAt: testNow}))

	fired, err := svc.TickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, fired)
	require.Equal(t, 1, enq.calls, "only revision-consistent receipts dispatch")

	for _, rc := range repo.receipts {
		switch rc.eventKey {
		case "evt-stale":
			require.True(t, rc.processed, "old receipts with mismatched revision must be dropped (no dispatch, no queue)")
		case "evt-fresh":
			require.True(t, rc.processed, "dispatched receipt marks processed")
		}
	}
}

func TestTickOnce_ReceiptRetryWindowDropsStale(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{err: errors.New("enqueue down")}
	svc := newTestWakeupService(repo, enq, fakeFacts{})

	w := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(w *Wakeup) { w.EventTypes = []string{"comment.created"} })
	old := testNow.Add(-domain.ReceiptRetryWindow - time.Minute)
	require.NoError(t, repo.InsertReceipt(context.Background(), Receipt{
		WakeupID: w.ID, Revision: 1, EventKey: "evt-old", WorkspaceID: "ws-1", CapturedAt: old}))
	require.NoError(t, repo.InsertReceipt(context.Background(), Receipt{
		WakeupID: w.ID, Revision: 1, EventKey: "evt-young", WorkspaceID: "ws-1", CapturedAt: testNow}))

	fired, err := svc.TickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, fired)
	for _, rc := range repo.receipts {
		if rc.eventKey == "evt-old" {
			require.True(t, rc.processed, "poison receipts outside the window must be dropped")
		} else {
			require.False(t, rc.processed, "failed receipts inside the window kept for retry (at least once)")
		}
	}
}

func TestEnsureChildDoneRule_BaselineAndIdempotent(t *testing.T) {
	repo := newFakeRepo()
	facts := fakeFacts{children: []domain.ChildStageFact{
		{Stage: "1", Terminal: true}, {Stage: "2", Terminal: false},
	}}
	svc := newTestWakeupService(repo, &fakeEnqueuer{}, facts)

	r1, err := svc.EnsureChildDoneRule(context.Background(), "ws-1", "parent-1")
	require.NoError(t, err)
	require.Equal(t, domain.SystemRuleChildrenDone, r1.SystemRule)
	require.Equal(t, domain.WakeupSystem, r1.Kind)
	require.NotNil(t, r1.ConditionState, "fingerprint baselined when the rule is created")
	require.Equal(t, "stage:1", *r1.ConditionState)
	require.Empty(t, r1.AgentID, "system rules have no fixed agent")
	require.True(t, r1.Enabled, "unconfigured default = enabled (only explicit false disables)")

	r2, err := svc.EnsureChildDoneRule(context.Background(), "ws-1", "parent-1")
	require.NoError(t, err)
	require.Equal(t, r1.ID, r2.ID)
}

func TestNotifyChildrenChanged_EdgeAndTargetResolution(t *testing.T) {
	repo := newFakeRepo()
	facts := fakeFacts{targetAgent: "ag-parent"}
	svc := newTestWakeupService(repo, &fakeEnqueuer{}, facts)
	svc.SetUow(rollbackUow(repo))

	rule, err := svc.EnsureChildDoneRule(context.Background(), "ws-1", "parent-1")
	require.NoError(t, err)
	enq := &fakeEnqueuer{}
	svc.enq = enq

	facts.children = []domain.ChildStageFact{{Stage: "1", Terminal: false}, {Stage: "2", Terminal: false}}
	svc.facts = facts
	require.NoError(t, svc.NotifyChildrenChanged(context.Background(), "ws-1", "parent-1"))
	require.Equal(t, 0, enq.calls, "unfinished sub-items do not trigger")

	facts.children = []domain.ChildStageFact{{Stage: "1", Terminal: true}, {Stage: "2", Terminal: false}}
	svc.facts = facts
	require.NoError(t, svc.NotifyChildrenChanged(context.Background(), "ws-1", "parent-1"))
	require.Equal(t, 1, enq.calls)
	require.Equal(t, []string{"ag-parent"}, enq.agents, "target resolved via the port per parent assignment")
	require.Equal(t, "stage:1", *repo.wakeups[rule.ID].ConditionState)

	require.NoError(t, svc.NotifyChildrenChanged(context.Background(), "ws-1", "parent-1"))
	require.Equal(t, 1, enq.calls)

	facts.children = []domain.ChildStageFact{{Stage: "1", Terminal: true}, {Stage: "2", Terminal: true}}
	svc.facts = facts
	require.NoError(t, svc.NotifyChildrenChanged(context.Background(), "ws-1", "parent-1"))
	require.Equal(t, 2, enq.calls)
	require.Equal(t, "all", *repo.wakeups[rule.ID].ConditionState)
}

func TestSystemWakeup_NoTargetAgentSkips(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestWakeupService(repo, &fakeEnqueuer{}, fakeFacts{targetAgent: "ag-parent"})
	rule, err := svc.EnsureChildDoneRule(context.Background(), "ws-1", "parent-1")
	require.NoError(t, err)

	enq := &fakeEnqueuer{}
	svc.enq = enq
	svc.facts = fakeFacts{children: []domain.ChildStageFact{
		{Stage: "1", Terminal: true}, {Stage: "2", Terminal: false}}}
	require.NoError(t, svc.NotifyChildrenChanged(context.Background(), "ws-1", "parent-1"))
	require.Equal(t, 0, enq.calls, "non-target agents do not enqueue")
	require.Nil(t, repo.wakeups[rule.ID].ConditionState, "fingerprint does not advance (no dispatch-success edge observed)")
}

func TestSystemDefault_AndUpdateSystemRule(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestWakeupService(repo, &fakeEnqueuer{}, fakeFacts{})

	def, err := svc.GetSystemDefault(context.Background(), "ws-1")
	require.NoError(t, err)
	require.True(t, def.Enabled, "unconfigured defaults on")

	require.NoError(t, svc.SetSystemDefault(context.Background(), "ws-1",
		SystemWakeupDefault{Enabled: false, Instruction: "close directly after summary"}))
	def, err = svc.GetSystemDefault(context.Background(), "ws-1")
	require.NoError(t, err)
	require.False(t, def.Enabled)
	require.Equal(t, "close directly after summary", def.Instruction)

	repo2 := newFakeRepo()
	repo2.systemDefaults = map[string]SystemWakeupDefault{"ws-1": {Enabled: true, Instruction: "workspace default instruction"}}
	svc2 := newTestWakeupService(repo2, &fakeEnqueuer{}, fakeFacts{})
	rule, err := svc2.EnsureChildDoneRule(context.Background(), "ws-1", "p-1")
	require.NoError(t, err)
	require.Equal(t, "workspace default instruction", rule.Instruction)

	newInstr := "single custom item"
	updated, err := svc2.UpdateSystem(context.Background(), "ws-1", rule.ID, &newInstr, nil)
	require.NoError(t, err)
	require.Equal(t, newInstr, updated.Instruction)
}

func TestTickOnce_FourPathRouting(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{status: "done"})
	svc.SetUow(rollbackUow(repo))

	due := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-1", "ag-1",
		func(w *Wakeup) { nv := testNow.Add(-time.Minute); w.NextFireAt = &nv })
	expiring := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-2", "ag-1",
		func(w *Wakeup) {
			nv := testNow.Add(-time.Minute)
			w.ExpiresAt = &nv
			w.NextFireAt = &nv
		})
	cond := seedWakeup(t, svc, repo, domain.WakeupCondition, "ws-1", "wi-3", "ag-1",
		func(w *Wakeup) { w.Condition = map[string]any{"field": "status", "op": "eq", "value": "done"} })
	ev := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-4", "ag-1",
		func(w *Wakeup) { w.EventTypes = []string{"comment.created"} })
	require.NoError(t, repo.InsertReceipt(context.Background(), Receipt{
		WakeupID: ev.ID, Revision: ev.Revision, EventKey: "evt-1", WorkspaceID: "ws-1", CapturedAt: testNow}))

	fired, err := svc.TickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, fired, "due+condition+receipt three-way dispatch; expire does not dispatch")
	require.Equal(t, 3, enq.calls)

	require.False(t, repo.wakeups[expiring.ID].Enabled, "expired must be invalidated and disabled")
	require.Equal(t, "expired", repo.wakeups[expiring.ID].PausedReason)
	require.Equal(t, "met", *repo.wakeups[cond.ID].ConditionState, "condition edge triggers and advances the fingerprint")
	require.Equal(t, 1, repo.wakeups[due.ID].FireCount)
	require.Equal(t, 1, repo.wakeups[ev.ID].FireCount, "event type dispatches only via receipt (once)")
	for _, rc := range repo.receipts {
		require.True(t, rc.processed)
	}
}

func TestTickOnce_ConditionNegativeEdgeNoFire(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{status: "done"})
	svc.SetUow(rollbackUow(repo))

	cond := seedWakeup(t, svc, repo, domain.WakeupCondition, "ws-1", "wi-1", "ag-1")
	met := "met"
	stored := repo.wakeups[cond.ID]
	stored.ConditionState = &met
	repo.wakeups[cond.ID] = stored

	svc.facts = fakeFacts{status: "todo"}
	fired, err := svc.TickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, fired)
	require.NotNil(t, repo.wakeups[cond.ID].ConditionState)
	require.Equal(t, "", *repo.wakeups[cond.ID].ConditionState)

	svc.facts = fakeFacts{status: "done"}
	fired, err = svc.TickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, fired)
}

func TestCheckIn_NoteLimitAndIdempotent(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestWakeupService(repo, &fakeEnqueuer{}, fakeFacts{})
	w := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-1", "ag-1")

	require.NoError(t, svc.CheckIn(context.Background(), "ws-1", "wi-1", w.ID, "task-1", "checked, all clear"))
	require.NoError(t, svc.CheckIn(context.Background(), "ws-1", "wi-1", w.ID, "task-1", "checked, all clear (replay)"))
	require.Len(t, repo.receipts, 1, "same-task check-in idempotent")
	require.Equal(t, "checkin:task-1", repo.receipts[0].eventKey)

	err := svc.CheckIn(context.Background(), "ws-1", "wi-1", w.ID, "task-1", strings.Repeat("x", 501))
	require.Error(t, err, "over 500 runes rejected")

	err = svc.CheckIn(context.Background(), "ws-2", "wi-1", w.ID, "task-1", "x")
	require.Error(t, err)
}

func TestWakeup_UpdateRevisionConflict(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestWakeupService(repo, &fakeEnqueuer{}, fakeFacts{})
	w := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-1", "ag-1")

	out, err := svc.Update(context.Background(), "ws-1", "wi-1", w.ID,
		Wakeup{Instruction: "new instruction", IntervalSeconds: 7200}, 1)
	require.NoError(t, err)
	require.Equal(t, "new instruction", out.Instruction)
	require.Equal(t, int64(2), out.Revision)
	require.NotNil(t, out.NextFireAt)
	require.True(t, out.NextFireAt.After(testNow), "interval changes recompute next_fire")

	_, err = svc.Update(context.Background(), "ws-1", "wi-1", w.ID,
		Wakeup{Instruction: "again"}, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "E_REVISION_CONFLICT")
}

func TestWakeup_EditInstructionDeleteTrigger(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetUow(rollbackUow(repo))
	w := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-1", "ag-1")

	require.NoError(t, svc.EditInstruction(context.Background(), "ws-1", w.ID, "updated instruction"))
	require.Equal(t, "updated instruction", repo.wakeups[w.ID].Instruction)
	require.Equal(t, int64(2), repo.wakeups[w.ID].Revision, "changing the instruction advances revision")

	err := svc.EditInstruction(context.Background(), "ws-1", w.ID, strings.Repeat("x", 4001))
	require.Error(t, err, "over 4000 runes rejected")

	require.NoError(t, svc.Trigger(context.Background(), "ws-1", "wi-1", w.ID))
	require.Equal(t, 1, enq.calls)

	require.NoError(t, svc.Delete(context.Background(), "ws-1", "wi-1", w.ID))
	_, ok := repo.wakeups[w.ID]
	require.False(t, ok)

	svc.facts = fakeFacts{targetAgent: "ag-p"}
	sysRule, err := svc.EnsureChildDoneRule(context.Background(), "ws-1", "wi-1")
	require.NoError(t, err)
	err = svc.Delete(context.Background(), "ws-1", "wi-1", sysRule.ID)
	require.Error(t, err, "system rules owned by platform, undeletable")
}

func TestWakeup_CreateValidation(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestWakeupService(repo, &fakeEnqueuer{}, fakeFacts{})

	_, err := svc.Create(context.Background(), Wakeup{
		WorkspaceID: "ws-1", WorkItemID: "wi-1", AgentID: "ag-1",
		Kind: domain.WakeupEvent, Instruction: "x"})
	require.Error(t, err, "event type must carry event_types")

	_, err = svc.Create(context.Background(), Wakeup{
		WorkspaceID: "ws-1", WorkItemID: "wi-1", AgentID: "ag-1",
		Kind: "weird", Instruction: "x"})
	require.Error(t, err, "unknown kind rejected (system only via the platform entry)")

	out, err := svc.Create(context.Background(), Wakeup{
		WorkspaceID: "ws-1", WorkItemID: "wi-1", AgentID: "ag-1",
		Kind: domain.WakeupEvent, Instruction: "x", EventTypes: []string{"comment.created"}})
	require.NoError(t, err)
	require.Nil(t, out.NextFireAt, "event type has no next_fire (receipt-driven)")
}

func TestAutopilotTriggerCRUD(t *testing.T) {
	repo := newFakeRepo()
	ap, _ := seedAutopilotWithTrigger(t, repo, 5)
	svc := newTestAutopilotService(repo, &fakeEnqueuer{})

	_, err := svc.CreateTrigger(context.Background(), "ws-1", ap.ID, domain.TriggerSchedule, "", "UTC", true)
	require.Error(t, err)

	wh, err := svc.CreateTrigger(context.Background(), "ws-1", ap.ID, domain.TriggerWebhook, "", "UTC", true)
	require.NoError(t, err)
	require.NotEmpty(t, wh.WebhookToken)
	require.NotEmpty(t, wh.SigningSecret)

	sch, err := svc.CreateTrigger(context.Background(), "ws-1", ap.ID, domain.TriggerSchedule, "0 9 * * *", "Asia/Shanghai", true)
	require.NoError(t, err)
	require.NotNil(t, sch.NextRunAt)
	require.Equal(t, "2026-10-03T01:00:00Z", sch.NextRunAt.UTC().Format(time.RFC3339))

	nv := "0 18 * * *"
	out, err := svc.UpdateTrigger(context.Background(), "ws-1", ap.ID, sch.ID, &nv, nil, nil)
	require.NoError(t, err)
	require.Equal(t, nv, out.CronExpression)
	require.Equal(t, "2026-10-03T10:00:00Z", out.NextRunAt.UTC().Format(time.RFC3339))

	require.NoError(t, svc.DeleteTrigger(context.Background(), "ws-1", ap.ID, wh.ID))
	_, ok := repo.triggers[wh.ID]
	require.False(t, ok)
	err = svc.DeleteTrigger(context.Background(), "ws-2", ap.ID, sch.ID)
	require.Error(t, err, "workspace mismatch = invisible (E_NOT_FOUND)")
}

func TestAutopilotCronPreview(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestAutopilotService(repo, &fakeEnqueuer{})

	runs, err := svc.CronPreview(context.Background(), "0 9 * * *", "Asia/Shanghai", 3)
	require.NoError(t, err)
	require.Len(t, runs, 3)
	require.Equal(t, "2026-10-03T01:00:00Z", runs[0].UTC().Format(time.RFC3339))
	require.Equal(t, "2026-10-04T01:00:00Z", runs[1].UTC().Format(time.RFC3339))

	_, err = svc.CronPreview(context.Background(), "not a cron", "UTC", 3)
	require.Error(t, err)

	runs, err = svc.CronPreview(context.Background(), "* * * * *", "", 99)
	require.NoError(t, err)
	require.Len(t, runs, 10, "count cap 10")
}

func TestAutopilotRunsRead(t *testing.T) {
	repo := newFakeRepo()
	ap, _ := seedAutopilotWithTrigger(t, repo, 5)
	svc := newTestAutopilotService(repo, &fakeEnqueuer{})
	run, err := svc.StartRun(context.Background(), ap.ID, "", "manual")
	require.NoError(t, err)

	runs, err := svc.ListRuns(context.Background(), "ws-1", ap.ID, 0)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	got, err := svc.GetRun(context.Background(), "ws-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, run.ID, got.ID)

	_, err = svc.GetRun(context.Background(), "ws-2", run.ID)
	require.Error(t, err, "workspace mismatch invisible")
}

func TestWebhookReplayDelivery(t *testing.T) {
	repo := newFakeRepo()
	svc := NewWebhookService(repo, newTestAutopilotService(repo, &fakeEnqueuer{}))
	repo.deliveries = append(repo.deliveries,
		fakeDelivery{id: "dlv-1", triggerID: "t-1", status: "failed", err: "boom"})

	out, err := svc.ReplayDelivery(context.Background(), "ws-1", "dlv-1")
	require.NoError(t, err)
	require.Equal(t, "queued", out.Status)
	require.Empty(t, out.Error)

	_, err = svc.ReplayDelivery(context.Background(), "ws-1", "dlv-nope")
	require.Error(t, err)
}

func TestStartRun_PassesTemplateSubscribers(t *testing.T) {
	repo := newFakeRepo()
	repo.subscribers = map[string][]SubscriberRef{"ap-1": {{UserID: "u-1"}, {UserID: "u-2"}}}

	ap := Autopilot{WorkspaceID: "ws-1", Name: "ap", ExecMode: "create_work_item",
		DailyLimit: 5, Enabled: true, CreatedBy: "u-0"}
	ap.ID = "ap-1"
	repo.autopilots[ap.ID] = ap

	sc := &subscriberCreator{}
	svc := NewAutopilotService(repo, sc, &fakeEnqueuer{})
	svc.now = func() time.Time { return testNow }
	svc.SetUow(rollbackUow(repo))

	_, err := svc.StartRun(context.Background(), "ap-1", "", "manual")
	require.NoError(t, err)
	require.Equal(t, []SubscriberRef{{UserID: "u-1"}, {UserID: "u-2"}}, sc.got,
		"subscriber lists must be passed with creation (internal archive 05 §9 same-transaction hookup)")

	repo.subscribers["ap-1"] = nil
	sc2 := &subscriberCreator{}
	svc.creator = sc2
	_, err = svc.StartRun(context.Background(), "ap-1", "", "manual")
	require.NoError(t, err)
	require.Equal(t, []SubscriberRef{}, sc2.got)

	svc.creator = &fakeCreator{}
	_, err = svc.StartRun(context.Background(), "ap-1", "", "manual")
	require.NoError(t, err)
}

func TestTickOnce_BurstPauseAndResume(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetBurstLimit(3)

	w := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(w *Wakeup) { w.EventTypes = []string{"comment.created"} })
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.InsertReceipt(context.Background(), Receipt{
			WakeupID: w.ID, Revision: 1, EventKey: fmt.Sprintf("evt-%d", i),
			WorkspaceID: "ws-1", CapturedAt: testNow.Add(-time.Duration(i) * time.Second)}))
	}

	fired, err := svc.TickOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, fired)
	require.Zero(t, enq.calls, "fused round zero dispatches")
	stored := repo.wakeups[w.ID]
	require.Equal(t, domain.PausedReasonBurst, stored.PausedReason, "paused_reason=burst")
	require.NotNil(t, stored.NextFireAt, "next_fire_at carries the silent recovery point")
	require.True(t, stored.NextFireAt.Equal(testNow.Add(domain.BurstQuietPeriod)))
	require.True(t, stored.Enabled, "burst pause keeps enabled (distinct from terminal expired)")
	for _, rc := range repo.receipts {
		require.True(t, rc.processed, "fused-round receipts dropped (silent period = receipt retry window, backlog drains naturally)")
	}

	fired, err = svc.TickOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, fired)
	require.Equal(t, domain.PausedReasonBurst, repo.wakeups[w.ID].PausedReason)

	svc.now = func() time.Time { return testNow.Add(domain.BurstQuietPeriod + time.Second) }
	_, err = svc.TickOnce(context.Background())
	require.NoError(t, err)
	resumed := repo.wakeups[w.ID]
	require.Empty(t, resumed.PausedReason, "auto-recovered after the silent period")
	require.Nil(t, resumed.NextFireAt, "recovery point cleared")
	require.True(t, resumed.Enabled)
}

func TestTickOnce_BurstNotTrippedUnderLimit(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetBurstLimit(3)

	w := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(w *Wakeup) { w.EventTypes = []string{"comment.created"} })
	for i := 0; i < 2; i++ {
		require.NoError(t, repo.InsertReceipt(context.Background(), Receipt{
			WakeupID: w.ID, Revision: 1, EventKey: fmt.Sprintf("evt-%d", i),
			WorkspaceID: "ws-1", CapturedAt: testNow.Add(-time.Duration(i) * time.Second)}))
	}

	fired, err := svc.TickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, fired, "first receipt dispatched; subsequent same-revision receipts dropped by the wakeup consistency gate (§2.3 ④)")
	require.Equal(t, 1, enq.calls)
	require.Empty(t, repo.wakeups[w.ID].PausedReason, "no fuse trip")
}

func TestTickOnce_BurstWindowSlides(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetBurstLimit(3)

	w := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(w *Wakeup) { w.EventTypes = []string{"comment.created"} })

	require.NoError(t, repo.InsertReceipt(context.Background(), Receipt{
		WakeupID: w.ID, Revision: 1, EventKey: "evt-ancient",
		WorkspaceID: "ws-1", CapturedAt: testNow.Add(-2 * domain.BurstWindow)}))
	for i := 0; i < 2; i++ {
		require.NoError(t, repo.InsertReceipt(context.Background(), Receipt{
			WakeupID: w.ID, Revision: 1, EventKey: fmt.Sprintf("evt-%d", i),
			WorkspaceID: "ws-1", CapturedAt: testNow.Add(-time.Duration(i) * time.Second)}))
	}

	fired, err := svc.TickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, fired, "one dispatch (revision gate), in-window density 2<3, no fuse trip")
	require.Empty(t, repo.wakeups[w.ID].PausedReason, "old receipts outside the window do not consume density quota")
}

func TestBurstGate_SkipsNonEventKinds(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetBurstLimit(1)

	for _, kind := range []string{domain.WakeupTime, domain.WakeupCondition} {
		w := seedWakeup(t, svc, repo, kind, "ws-1", "wi-1", "ag-1")
		for i := 0; i < 5; i++ {
			require.NoError(t, repo.InsertReceipt(context.Background(), Receipt{
				WakeupID: w.ID, Revision: 1, EventKey: fmt.Sprintf("evt-%s-%d", kind, i),
				WorkspaceID: "ws-1", CapturedAt: testNow.Add(-time.Duration(i) * time.Second)}))
		}
		require.False(t, svc.burstGate(context.Background(), repo.wakeups[w.ID], testNow),
			"kind=%s must not fuse", kind)
		require.Empty(t, repo.wakeups[w.ID].PausedReason)
	}
}
