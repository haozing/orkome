package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orkome/internal/automation/domain"
	"orkome/internal/contract/apierr"
	"orkome/internal/platform/cronx"
)

type AutopilotService struct {
	repo    Repo
	creator WorkItemCreator
	enq     TaskEnqueuer
	now     func() time.Time
	uow     UoW

	notifier FailureNotifier

	monitorMu      sync.Mutex
	monitorLastRun time.Time
	monitorEvery   time.Duration
}

func NewAutopilotService(repo Repo, creator WorkItemCreator, enq TaskEnqueuer) *AutopilotService {
	return &AutopilotService{repo: repo, creator: creator, enq: enq, now: time.Now}
}

func (s *AutopilotService) SetFailureNotifier(n FailureNotifier) { s.notifier = n }

func (s *AutopilotService) SetUow(uow UoW) { s.uow = uow }

func (s *AutopilotService) Create(ctx context.Context, a Autopilot, cronExpr, tz string) (*Autopilot, *Trigger, error) {
	if strings.TrimSpace(a.Name) == "" {
		return nil, nil, apierr.NewValidation("name required")
	}
	if a.ExecMode == "" {
		a.ExecMode = "create_work_item"
	}
	if a.DailyLimit <= 0 {
		a.DailyLimit = 10
	}
	a.Enabled = true

	var out Autopilot
	steps := func(ctx context.Context) error {
		created, err := s.repo.CreateAutopilot(ctx, a)
		if err != nil {
			return err
		}
		out = created
		_, err = s.repo.InsertRuleVersion(ctx, RuleVersion{
			WorkspaceID: created.WorkspaceID, AutopilotID: created.ID,
			PublishedByType: "member", PublishedByID: created.CreatedBy,
			ConfigSummary: ruleVersionSummary(created),
		})
		return err
	}
	var err error
	if s.uow != nil {
		err = s.uow(ctx, steps)
	} else {
		err = steps(ctx)
	}
	if err != nil {
		return nil, nil, err
	}
	if cronExpr == "" {

		t := Trigger{AutopilotID: out.ID, Kind: domain.TriggerSchedule, Enabled: true}
		tok, err := domain.MintWebhookToken()
		if err != nil {
			return nil, nil, err
		}
		t.WebhookToken = tok
		sec, err := domain.MintSigningSecret()
		if err != nil {
			return nil, nil, err
		}
		t.SigningSecret = sec
		tout, err := s.repo.CreateTrigger(ctx, t)
		if err != nil {
			return nil, nil, err
		}
		return &out, &tout, nil
	}
	t, err := s.CreateTrigger(ctx, out.WorkspaceID, out.ID, domain.TriggerSchedule, cronExpr, tz, true)
	if err != nil {
		return nil, nil, err
	}
	return &out, t, nil
}

func (s *AutopilotService) List(ctx context.Context, ws string, limit int) ([]Autopilot, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	out, err := s.repo.ListAutopilots(ctx, ws, limit)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Autopilot{}
	}
	return out, nil
}

func (s *AutopilotService) Get(ctx context.Context, ws, id string) (*Autopilot, error) {
	a, ok, err := s.repo.GetAutopilot(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "autopilot not found")
	}
	return &a, nil
}

const (
	ExecModeCreateWorkItem = "create_work_item"
	ExecModeTaskOnly       = "task_only"
)

func (s *AutopilotService) Update(ctx context.Context, ws, id string,
	name, execMode *string, template map[string]any, targetAgentID *string, enabled *bool) (*Autopilot, error) {
	if name != nil && strings.TrimSpace(*name) == "" {
		return nil, apierr.NewValidation("name must not be blank")
	}
	if execMode != nil && *execMode != ExecModeCreateWorkItem && *execMode != ExecModeTaskOnly {
		return nil, apierr.NewValidation("exec_mode must be create_work_item|task_only")
	}
	a, ok, err := s.repo.UpdateAutopilot(ctx, ws, id, name, execMode, template, targetAgentID, enabled)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "autopilot not found")
	}
	return &a, nil
}

func (s *AutopilotService) Delete(ctx context.Context, ws, id string) error {
	out, err := s.repo.DeleteAutopilot(ctx, ws, id)
	if err != nil {
		return err
	}
	switch {
	case !out.Found:
		return apierr.New(apierr.NotFound, "autopilot not found")
	case out.BlockedTriggers:
		return apierr.New(apierr.Conflict, "autopilot still has enabled triggers; disable or delete them first")
	case out.BlockedRuns:
		return apierr.New(apierr.Conflict, "autopilot has pending/running runs")
	}
	return nil
}

func (s *AutopilotService) CreateTrigger(ctx context.Context, ws, autopilotID, kind, cronExpr, tz string, enabled bool) (*Trigger, error) {
	if _, ok, err := s.repo.GetAutopilot(ctx, ws, autopilotID); err != nil {
		return nil, err
	} else if !ok {
		return nil, apierr.New(apierr.NotFound, "autopilot not found")
	}
	t := Trigger{AutopilotID: autopilotID, Kind: kind, CronExpression: cronExpr, Timezone: tz, Enabled: enabled}
	switch kind {
	case domain.TriggerSchedule:
		if cronExpr == "" {
			return nil, apierr.NewValidation("cron_expression required for kind=schedule")
		}
		next, err := cronx.Next(cronExpr, tz, s.now())
		if err != nil {
			return nil, apierr.NewValidation(err.Error())
		}
		t.NextRunAt = &next
	case domain.TriggerWebhook:
		tok, err := domain.MintWebhookToken()
		if err != nil {
			return nil, err
		}
		t.WebhookToken = tok
		sec, err := domain.MintSigningSecret()
		if err != nil {
			return nil, err
		}
		t.SigningSecret = sec
	case domain.TriggerManual:

	default:
		return nil, apierr.NewValidation(fmt.Sprintf("kind must be %s|%s|%s",
			domain.TriggerSchedule, domain.TriggerWebhook, domain.TriggerManual))
	}
	out, err := s.repo.CreateTrigger(ctx, t)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *AutopilotService) UpdateTrigger(ctx context.Context, ws, autopilotID, triggerID string, cron, tz *string, enabled *bool) (*Trigger, error) {
	t, ok, err := s.repo.GetTriggerByID(ctx, triggerID)
	if err != nil {
		return nil, err
	}
	if !ok || t.AutopilotID != autopilotID || t.Workspace() != ws {
		return nil, apierr.New(apierr.NotFound, "trigger not found")
	}
	nextCron := t.CronExpression
	if cron != nil {
		nextCron = strings.TrimSpace(*cron)
	}
	nextTZ := t.Timezone
	if tz != nil {
		nextTZ = *tz
	}
	var nextRun *time.Time
	if t.Kind == domain.TriggerSchedule && nextCron != "" {
		nv, err := cronx.Next(nextCron, nextTZ, s.now())
		if err != nil {
			return nil, apierr.NewValidation(err.Error())
		}
		nextRun = &nv
	}
	updated, err := s.repo.UpdateTrigger(ctx, ws, autopilotID, triggerID, cron, tz, nextRun, enabled)
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, apierr.New(apierr.NotFound, "trigger not found")
	}
	out, _, err := s.repo.GetTriggerByID(ctx, triggerID)
	return &out, err
}

func (s *AutopilotService) DeleteTrigger(ctx context.Context, ws, autopilotID, triggerID string) error {
	ok, err := s.repo.DeleteTrigger(ctx, ws, autopilotID, triggerID)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.New(apierr.NotFound, "trigger not found")
	}
	return nil
}

func (s *AutopilotService) CronPreview(ctx context.Context, expr, tz string, count int) ([]time.Time, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, apierr.NewValidation("cron_expression required")
	}
	if count <= 0 {
		count = 5
	}
	if count > 10 {
		count = 10
	}
	out := make([]time.Time, 0, count)
	cur := s.now()
	for i := 0; i < count; i++ {
		nxt, err := cronx.Next(expr, tz, cur)
		if err != nil {
			return nil, apierr.NewValidation(err.Error())
		}
		out = append(out, nxt)
		cur = nxt
	}
	return out, nil
}

func (s *AutopilotService) CronTickOnce(ctx context.Context) (int, error) {
	now := s.now()
	due, err := s.repo.ListDueScheduleTriggers(ctx, now)
	if err != nil {
		return 0, err
	}
	ran := 0
	for _, t := range due {
		late := t.NextRunAt != nil && domain.PlanLate(*t.NextRunAt, now)

		next := s.nextPlan(t)
		won, err := s.repo.ClaimDueTrigger(ctx, t.ID, t.NextRunAt, next)
		if err != nil || !won {
			continue
		}
		if late {
			continue
		}
		if _, err := s.StartRun(ctx, t.AutopilotID, t.ID, "schedule"); err == nil {
			ran++
		}
	}
	s.maybeMonitorFailures(ctx)
	return ran, nil
}

func (s *AutopilotService) maybeMonitorFailures(ctx context.Context) {
	every := s.monitorEvery
	if every <= 0 {
		every = time.Hour
	}
	s.monitorMu.Lock()
	now := s.now()
	if !s.monitorLastRun.IsZero() && now.Sub(s.monitorLastRun) < every {
		s.monitorMu.Unlock()
		return
	}
	s.monitorLastRun = now
	s.monitorMu.Unlock()
	_, _ = s.MonitorAutopilotFailures(ctx)
}

func (s *AutopilotService) MonitorAutopilotFailures(ctx context.Context) (int, error) {
	hits, err := s.repo.PauseAutopilotsByFailureRate(ctx, domain.AutopilotFailureWindow,
		domain.AutopilotFailureMinRuns, domain.AutopilotFailureRate, domain.AutopilotPausedFailureRate)
	if err != nil {
		return 0, err
	}
	for _, h := range hits {
		s.notifyFailure(ctx, h)
	}
	return len(hits), nil
}

func (s *AutopilotService) notifyFailure(ctx context.Context, h AutopilotFailureStat) {
	if s.notifier == nil {
		return
	}
	stat := fmt.Sprintf("7-day window: %d runs / %d failures (%.0f%%)",
		h.Runs, h.Failed, 100*float64(h.Failed)/float64(h.Runs))
	s.notifier(ctx, h.WorkspaceID, h.CreatedBy, h.Name, stat)
}

func (s *AutopilotService) nextPlan(t Trigger) *time.Time {
	if t.CronExpression == "" {
		return nil
	}
	if next, err := cronx.Next(t.CronExpression, t.Timezone, s.now()); err == nil {
		return &next
	}
	return nil
}

func (s *AutopilotService) ListRuns(ctx context.Context, ws, autopilotID string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	runs, err := s.repo.ListRunsByAutopilot(ctx, ws, autopilotID, limit)
	if err != nil {
		return nil, err
	}
	if runs == nil {
		runs = []Run{}
	}
	return runs, nil
}

func (s *AutopilotService) GetRun(ctx context.Context, ws, runID string) (*Run, error) {
	r, ok, err := s.repo.GetRun(ctx, ws, runID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "run not found")
	}
	return &r, nil
}

func (s *AutopilotService) StartRun(ctx context.Context, autopilotID, triggerID, source string) (*Run, error) {
	ap, ok, err := s.repo.GetAutopilot(ctx, "", autopilotID)
	if err != nil || !ok || !ap.Enabled {
		return nil, errors.New("autopilot unavailable")
	}
	ws, we := domain.QuotaWindow(s.now())
	run := Run{AutopilotID: ap.ID, TriggerID: triggerID, Source: source, Status: domain.RunPending, PlannedAt: s.now()}
	if err := s.dispatchRun(ctx, ap, &run, ws, we); err != nil {
		if errors.Is(err, domain.ErrQuotaExceeded) {

			sk, _ := s.repo.InsertRun(ctx, Run{AutopilotID: ap.ID, TriggerID: triggerID, Source: source,
				Status: domain.RunSkipped, PlannedAt: s.now(), ReasonCode: domain.ReasonQuotaExceeded})
			return &sk, fmt.Errorf("start run: %w", err)
		}
		return nil, err
	}
	return &run, nil
}

func (s *AutopilotService) dispatchRun(ctx context.Context, ap Autopilot, run *Run, windowStart, windowEnd time.Time) error {
	steps := func(ctx context.Context) error {

		resID, err := s.repo.ReserveQuota(ctx, ap.ID, windowStart, windowEnd, ap.DailyLimit)
		if err != nil {
			return err
		}
		run.QuotaReservationID = resID
		out, err := s.repo.InsertRun(ctx, *run)
		if err != nil {
			return err
		}
		*run = out
		creator := ap.CreatedBy
		if creator == "" {
			creator = "00000000-0000-0000-0000-000000000000"
		}
		var wiID string
		var evidence string
		switch ap.ExecMode {
		case "create_work_item":
			title := domain.Interpolate(strOr(ap.Template["title"], "automation task {{date}}"), s.now())
			wiID, err = s.createWorkItem(ctx, ap, title, creator)
			if err != nil {
				return err
			}
			evidence = "autopilot trigger"
		case "task_only":
			wiID = strOr(ap.Template["work_item_id"], "")
			evidence = "autopilot direct run"
		default:
			return fmt.Errorf("unknown exec_mode %q", ap.ExecMode)
		}

		taskID, _, err := s.enq.EnqueueForWakeup(ctx, ap.WorkspaceID, wiID, ap.TargetAgentID, creator, evidence)
		if err != nil {
			return err
		}
		if err := s.repo.SetRunTask(ctx, run.ID, taskID, wiID); err != nil {
			return err
		}
		if err := s.repo.SetRunStatus(ctx, run.ID, domain.RunRunning, ""); err != nil {
			return err
		}
		run.TaskID, run.WorkItemID, run.Status = taskID, wiID, domain.RunRunning
		return nil
	}
	if s.uow == nil {
		return steps(ctx)
	}
	return s.uow(ctx, steps)
}

func (s *AutopilotService) createWorkItem(ctx context.Context, ap Autopilot, title, creator string) (string, error) {
	desc := strOr(ap.Template["description"], "")
	if sac, ok := s.creator.(SubscriberAwareCreator); ok {
		subs, err := s.repo.ListTemplateSubscribers(ctx, ap.ID)
		if err != nil {
			return "", err
		}
		if subs == nil {
			subs = []SubscriberRef{}
		}
		return sac.CreateForAutopilotWithSubscribers(ctx, ap.WorkspaceID, title, desc,
			ap.TargetAgentID, creator, "autopilot", subs)
	}
	return s.creator.CreateForAutopilot(ctx, ap.WorkspaceID, title, desc,
		ap.TargetAgentID, creator, "autopilot")
}

func (s *AutopilotService) BackfillFromTask(ctx context.Context, taskID, taskStatus string) {
	status := map[string]string{"completed": domain.RunCompleted, "failed": domain.RunFailed, "cancelled": domain.RunFailed}[taskStatus]
	if status == "" {
		return
	}
	_ = s.repo.SetRunStatusByTask(ctx, taskID, status)
}

func (t *Trigger) Workspace() string { return t.workspace }

func ruleVersionSummary(a Autopilot) []byte {
	tpl := a.Template
	if tpl == nil {
		tpl = map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{
		"name": a.Name, "exec_mode": a.ExecMode, "template": tpl,
		"target_agent_id": a.TargetAgentID, "daily_limit": a.DailyLimit,
	})
	return b
}

func (s *AutopilotService) ListCollaborators(ctx context.Context, ws, autopilotID string) ([]Collaborator, error) {
	if _, ok, err := s.repo.GetAutopilot(ctx, ws, autopilotID); err != nil {
		return nil, err
	} else if !ok {
		return nil, apierr.New(apierr.NotFound, "autopilot not found")
	}
	out, err := s.repo.ListCollaborators(ctx, ws, autopilotID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Collaborator{}
	}
	return out, nil
}

func (s *AutopilotService) AddCollaborator(ctx context.Context, ws, autopilotID, userID, grantedBy string) (*Collaborator, error) {
	if userID == "" {
		return nil, apierr.NewValidation("user_id required")
	}
	ok, err := s.repo.AddCollaborator(ctx, ws, autopilotID, userID, grantedBy)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "autopilot not found")
	}
	return &Collaborator{AutopilotID: autopilotID, UserID: userID, UserType: "member", GrantedBy: grantedBy}, nil
}

func (s *AutopilotService) RemoveCollaborator(ctx context.Context, ws, autopilotID, userID string) error {
	if userID == "" {
		return apierr.NewValidation("user_id required")
	}
	ok, err := s.repo.RemoveCollaborator(ctx, ws, autopilotID, userID)
	if err != nil {
		return err
	}
	if !ok {

		if _, found, gerr := s.repo.GetAutopilot(ctx, ws, autopilotID); gerr == nil && !found {
			return apierr.New(apierr.NotFound, "autopilot not found")
		}
	}
	return nil
}
