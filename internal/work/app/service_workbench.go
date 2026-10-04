package app

import (
	"context"
	contractevents "orkome/internal/contract/events"

	"orkome/internal/contract/apierr"
	"orkome/internal/work/domain"
)

const childItemDefaultStatus = domain.StatusTodo

func (s *WorkItemService) CreateChildByAgent(ctx context.Context, workspaceID, parentID, title, description, agentID string, delegation domain.DelegationChain) (*WorkItem, error) {
	if parentID == "" {
		return nil, apierr.NewValidation("parent work item required")
	}
	if _, ok, err := s.repo.GetWorkItem(ctx, workspaceID, parentID); err != nil {
		return nil, err
	} else if !ok {
		return nil, apierr.New(apierr.NotFound, "parent work item not found")
	}

	return s.Create(ctx, NewWorkItem{
		WorkspaceID: workspaceID, Title: title, Description: description,
		StatusKey:   childItemDefaultStatus,
		CreatorType: "agent", CreatorID: agentID,
		ParentID:   parentID,
		Delegation: delegation,
	}, "")
}

func (s *WorkItemService) UpdateStatusByAgent(ctx context.Context, workspaceID, workItemID, agentID, statusKey string) (*WorkItem, error) {
	current, ok, err := s.repo.GetWorkItem(ctx, workspaceID, workItemID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "work item not found")
	}

	if domain.InTriage(current.TriageState) {
		return nil, apierr.NewValidation("work item in triage: status locked",
			apierr.FieldError{Field: "status", Problem: "locked while triage_state is set"})
	}
	if err := s.repo.EnsureStatusSeed(ctx, workspaceID); err != nil {
		return nil, err
	}
	defs, err := s.repo.ListStatuses(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if _, ok := domain.WritableCategory(toDomainDefs(defs), statusKey); !ok {
		return nil, apierr.NewValidation("unknown status key")
	}
	patch := WorkItemPatch{StatusKey: &statusKey}

	var wi WorkItem
	env := ev(WorkItemUpdatedByAgent, workspaceID, agentID, nil)
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		updated, err := s.repo.UpdateWorkItem(ctx, workspaceID, workItemID, patch, 0)
		if err != nil {
			return err
		}
		wi = updated
		env.Payload = wiEvent(wi)
		return nil
	}); err != nil {
		return nil, err
	}

	if statusKey != current.StatusKey {
		if max, err := s.repo.NextPositionInStatus(ctx, workspaceID, statusKey); err == nil {
			_ = s.repo.MovePosition(ctx, workspaceID, workItemID, max+1)
		}
	}
	return &wi, nil
}

const WorkItemUpdatedByAgent = contractevents.WorkItemUpdatedByAgent

var squadEvaluationVerdicts = map[string]bool{"action": true, "no_action": true, "failed": true}

type SquadEvaluationRecord struct {
	WorkspaceID   string
	WorkItemID    string
	LeaderAgentID string
	SquadID       string
	TaskID        string
	Member        string
	Verdict       string
	Rationale     string
}

func (s *WorkItemService) RecordSquadEvaluationByAgent(ctx context.Context, r SquadEvaluationRecord) error {
	if !squadEvaluationVerdicts[r.Verdict] {
		return apierr.NewValidation("verdict must be action|no_action|failed")
	}
	if r.WorkspaceID == "" || r.WorkItemID == "" || r.LeaderAgentID == "" || r.TaskID == "" {
		return apierr.NewValidation("evaluation provenance required")
	}

	eventID := evaluationEventID(r.TaskID, r.Member)
	payload := map[string]any{
		"squad_id": r.SquadID, "task_id": r.TaskID, "member": r.Member,
		"verdict": r.Verdict, "rationale": r.Rationale,
	}
	return s.repo.AppendActivity(ctx, eventID, r.WorkspaceID, r.WorkItemID,
		"agent", r.LeaderAgentID, "squad_leader_evaluated", payload)
}

func evaluationEventID(taskID, member string) string {
	return "squad-eval:" + taskID + "\x1f" + member
}
