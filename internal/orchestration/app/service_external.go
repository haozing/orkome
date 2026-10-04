package app

import (
	"context"
	"strings"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
)

type ExternalTaskService struct {
	repo   Repo
	emit   Emitter
	outbox OutboxRecorder
	uow    Within
}

func NewExternalTaskService(repo Repo, emit Emitter) *ExternalTaskService {
	return &ExternalTaskService{repo: repo, emit: emit}
}

func (s *ExternalTaskService) SetOutbox(rec OutboxRecorder, uow Within) {
	s.outbox, s.uow = rec, uow
}

func (s *ExternalTaskService) guard(ctx context.Context, ws, taskID, agentID string, wantRunning bool) (Task, error) {
	t, ok, err := s.repo.GetTask(ctx, ws, taskID)
	if err != nil {
		return t, err
	}
	if !ok {
		return t, apierr.New(apierr.NotFound, "task not found")
	}
	if t.AgentID != agentID {
		return t, apierr.New(apierr.Forbidden, "task belongs to another agent")
	}
	if wantRunning && t.Status != "running" {
		return t, apierr.New(apierr.Conflict, "task not running")
	}
	return t, nil
}

func (s *ExternalTaskService) Claim(ctx context.Context, ws, taskID, agentID string) (*Task, error) {
	t, err := s.guard(ctx, ws, taskID, agentID, false)
	if err != nil {
		return nil, err
	}
	a, ok, err := s.repo.GetAgent(ctx, ws, t.AgentID)
	if err != nil {
		return nil, err
	}
	if !ok || a.RuntimeKind != "external" {
		return nil, apierr.New(apierr.Conflict, "task agent is not external runtime")
	}
	claimed, ok, err := s.repo.ClaimExternalTask(ctx, ws, taskID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.Conflict, "task already claimed or finalized")
	}
	s.emit.Emit(ctx, envelope(events.TaskRunning, ws, "", TaskPayload{
		ID: claimed.ID, WorkspaceID: ws, AgentID: claimed.AgentID,
		WorkItemID: claimed.WorkItemID, Status: claimed.Status}))
	return &claimed, nil
}

var TaskMessageKinds = map[string]bool{
	"text": true, "tool_use": true, "tool_result": true, "error": true, "approval": true,
}

func (s *ExternalTaskService) ReportMessage(ctx context.Context, ws, taskID, agentID, kind, callID, content string) (int64, error) {
	kind = strings.TrimSpace(kind)
	if !TaskMessageKinds[kind] {
		return 0, apierr.NewValidation("kind must be one of text,tool_use,tool_result,error,approval")
	}
	if _, err := s.guard(ctx, ws, taskID, agentID, true); err != nil {
		return 0, err
	}
	return s.repo.AppendTaskMessage(ctx, ws, taskID, kind, strings.TrimSpace(callID), content, false)
}

func (s *ExternalTaskService) Complete(ctx context.Context, ws, taskID, agentID, result string) error {
	t, err := s.guard(ctx, ws, taskID, agentID, true)
	if err != nil {
		return err
	}
	env := envelope(events.TaskCompleted, ws, "", TaskPayload{
		ID: t.ID, WorkspaceID: ws, AgentID: t.AgentID, WorkItemID: t.WorkItemID,
		Status: "completed", Result: result})
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		if _, err := s.repo.AppendTaskMessage(ctx, ws, t.ID, "text", "", result, false); err != nil {
			return err
		}
		return s.repo.CompleteRun(ctx, t.ID, result, "", CurrentEngineTag(), UsageReport{})
	}); err != nil {
		return err
	}
	s.promoteAfterTerminal(ctx, t)
	return nil
}

func (s *ExternalTaskService) Fail(ctx context.Context, ws, taskID, agentID, reason, detail string) error {
	t, err := s.guard(ctx, ws, taskID, agentID, true)
	if err != nil {
		return err
	}
	env := envelope(events.TaskFailed, ws, "", TaskPayload{
		ID: t.ID, WorkspaceID: ws, AgentID: t.AgentID, WorkItemID: t.WorkItemID, Status: "failed"})
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		_, serr := s.repo.FailRun(ctx, t.ID, reason, detail)
		return serr
	}); err != nil {
		return err
	}
	s.promoteAfterTerminal(ctx, t)
	return nil
}

func (s *ExternalTaskService) promoteAfterTerminal(ctx context.Context, t Task) {
	if _, err := s.repo.PromoteDeferred(ctx); err != nil {
		_ = err
	}
}
