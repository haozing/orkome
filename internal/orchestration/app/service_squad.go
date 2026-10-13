package app

import (
	"context"
	"strings"

	"github.com/haozing/ploykit/platform/redactx"
	"orkome/internal/contract/apierr"
	"orkome/internal/orchestration/domain"
)

type SquadMemberRow struct {
	AgentID  string `json:"agent_id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Archived bool   `json:"archived"`
}

type SquadActiveTask struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type SquadMemberView struct {
	AgentID     string            `json:"agent_id"`
	Name        string            `json:"name"`
	Role        string            `json:"role"`
	Status      string            `json:"status"`
	ActiveTasks []SquadActiveTask `json:"active_tasks"`
}

type SquadMemberTaskRow struct {
	AgentID    string
	Name       string
	Role       string
	TaskID     string
	TaskStatus string
}

type SquadEvalReport struct {
	TaskID  string `json:"task_id"`
	Member  string `json:"member,omitempty"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

type SquadDeleteResult int

const (
	SquadDeleted SquadDeleteResult = iota
	SquadNotFound
	SquadHasMembers
	SquadHasActiveTasks
)

type SquadUpdateInput struct {
	Name          string
	Instructions  string
	LeaderAgentID string
}

type SquadService struct {
	repo     Repo
	recorder SquadEvaluationRecorder
}

func NewSquadService(repo Repo, recorder SquadEvaluationRecorder) *SquadService {
	return &SquadService{repo: repo, recorder: recorder}
}

func (s *SquadService) Members(ctx context.Context, ws, squadID string) ([]SquadMemberRow, error) {
	return s.repo.ListSquadMembers(ctx, ws, squadID)
}

func (s *SquadService) AddMember(ctx context.Context, ws, squadID, agentID, role string) error {
	role, err := validSquadRole(role)
	if err != nil {
		return err
	}
	return s.repo.AddSquadMember(ctx, ws, squadID, agentID, role)
}

func (s *SquadService) RemoveMember(ctx context.Context, ws, squadID, agentID string) error {
	return s.repo.RemoveSquadMember(ctx, ws, squadID, agentID)
}

func (s *SquadService) SetMemberRole(ctx context.Context, ws, squadID, agentID, role string) error {
	role, err := validSquadRole(role)
	if err != nil {
		return err
	}
	ok, err := s.repo.UpdateSquadMemberRole(ctx, ws, squadID, agentID, role)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.New(apierr.NotFound, "squad member not found")
	}
	return nil
}

func (s *SquadService) MembersStatus(ctx context.Context, ws, squadID string) ([]SquadMemberView, error) {
	rows, err := s.repo.ListSquadMemberTasks(ctx, ws, squadID)
	if err != nil {
		return nil, err
	}
	var out []SquadMemberView
	idx := map[string]int{}
	for _, r := range rows {
		i, seen := idx[r.AgentID]
		if !seen {
			out = append(out, SquadMemberView{AgentID: r.AgentID, Name: r.Name, Role: r.Role})
			i = len(out) - 1
			idx[r.AgentID] = i
		}
		if r.TaskID != "" {
			out[i].ActiveTasks = append(out[i].ActiveTasks, SquadActiveTask{ID: r.TaskID, Status: r.TaskStatus})
		}
	}
	for i := range out {
		sts := make([]string, len(out[i].ActiveTasks))
		for j, t := range out[i].ActiveTasks {
			sts[j] = t.Status
		}
		out[i].Status = domain.SquadMemberStatus(sts)
	}
	return out, nil
}

func (s *SquadService) RecordEvaluation(ctx context.Context, ws, workItemID string, rep SquadEvalReport) error {
	switch rep.Outcome {
	case "action", "no_action", "failed":
	default:
		return apierr.NewValidation("outcome must be 'action', 'no_action', or 'failed'")
	}
	if s.recorder == nil {
		return apierr.New(apierr.Internal, "evaluation recorder not wired")
	}
	t, ok, err := s.repo.GetTask(ctx, ws, rep.TaskID)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.New(apierr.NotFound, "task not found")
	}

	if t.WorkItemID != workItemID {
		return apierr.NewValidation("task does not belong to this work item")
	}
	if !t.IsLeaderTask {
		return apierr.NewValidation("task is not a squad leader task")
	}
	if t.SquadID == "" {
		return apierr.NewValidation("leader task has no squad_id")
	}
	leader, ok, err := s.repo.SquadLeader(ctx, ws, t.SquadID)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.New(apierr.NotFound, "squad not found")
	}
	if leader == "" || leader != t.AgentID {
		return apierr.New(apierr.Forbidden, "only the squad leader agent can record evaluations")
	}
	rationale := redactx.Sanitize(strings.TrimSpace(rep.Reason))
	if n := len([]rune(rationale)); n > 2000 {
		return apierr.NewValidation("reason must be <= 2000 runes")
	}
	member := strings.TrimSpace(rep.Member)
	if n := len([]rune(member)); n > 200 {
		return apierr.NewValidation("member must be <= 200 runes")
	}
	return s.recorder.RecordSquadEvaluation(ctx, SquadEvaluation{
		WorkspaceID: ws, WorkItemID: workItemID, LeaderAgentID: t.AgentID,
		SquadID: t.SquadID, TaskID: t.ID, Member: member,
		Verdict: rep.Outcome, Rationale: rationale,
	})
}

func validSquadRole(role string) (string, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return "member", nil
	}
	if n := len([]rune(role)); n > 80 {
		return "", apierr.NewValidation("role must be <= 80 runes")
	}
	return role, nil
}

func (s *SquadService) Get(ctx context.Context, ws, squadID string) (map[string]any, error) {
	row, ok, err := s.repo.GetSquadRow(ctx, ws, squadID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "squad not found")
	}
	return row, nil
}

func (s *SquadService) Update(ctx context.Context, ws, squadID string, in SquadUpdateInput) (map[string]any, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apierr.NewValidation("name required")
	}
	cur, ok, err := s.repo.GetSquadRow(ctx, ws, squadID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "squad not found")
	}
	curLeader, _ := cur["leader_agent_id"].(string)

	joinLeader := false
	if in.LeaderAgentID != "" && in.LeaderAgentID != curLeader {
		if _, aok, err := s.repo.GetAgent(ctx, ws, in.LeaderAgentID); err != nil {
			return nil, err
		} else if !aok {
			return nil, apierr.NewValidation("leader agent not found in this workspace")
		}
		isMember := false
		members, err := s.repo.ListSquadMembers(ctx, ws, squadID)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			if m.AgentID == in.LeaderAgentID {
				isMember = true
				break
			}
		}
		joinLeader = !isMember
	}

	if curName, _ := cur["name"].(string); name != curName {
		taken, err := s.repo.SquadNameTaken(ctx, ws, name, squadID)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, apierr.New(apierr.Conflict, "squad name already taken")
		}
	}
	out, ok2, err := s.repo.UpdateSquad(ctx, ws, squadID, name, in.Instructions,
		in.LeaderAgentID, joinLeader)
	if err != nil {
		return nil, err
	}
	if !ok2 {
		return nil, apierr.New(apierr.NotFound, "squad not found")
	}
	return out, nil
}

func (s *SquadService) Delete(ctx context.Context, ws, squadID string) error {
	res, err := s.repo.DeleteSquad(ctx, ws, squadID)
	if err != nil {
		return err
	}
	switch res {
	case SquadDeleted:
		return nil
	case SquadNotFound:
		return apierr.New(apierr.NotFound, "squad not found")
	case SquadHasMembers:
		return apierr.New(apierr.Conflict, "squad still has members; remove them first")
	case SquadHasActiveTasks:
		return apierr.New(apierr.Conflict, "squad has active tasks")
	}
	return apierr.New(apierr.Internal, "unknown delete result")
}
