package app

import (
	"context"
	"errors"

	"orkome/internal/contract/dispatchreason"
	"orkome/internal/contract/events"
	"orkome/internal/orchestration/domain"
)

type TriggerService struct {
	repo Repo
	emit Emitter
}

func NewTriggerService(repo Repo, emit Emitter) *TriggerService {
	return &TriggerService{repo: repo, emit: emit}
}

type CommentInput struct {
	WorkspaceID      string
	WorkItemID       string
	AuthorType       string
	AuthorID         string
	AuthorName       string
	Content          string
	ParentAuthorType string
	ParentAuthorID   string
	RootAuthorType   string
	RootAuthorID     string
	AssigneeType     string
	AssigneeID       string
}

type PreviewOutcome struct {
	Triggers     []TriggerHit `json:"triggers"`
	Suppressions []Suppressed `json:"suppressions"`
}

type TriggerHit struct {
	AgentID string `json:"agent_id"`
	Reason  string `json:"reason"`
}

type Suppressed struct {
	AgentID string `json:"agent_id"`
	Reason  string `json:"reason"`
}

func (s *TriggerService) Preview(ctx context.Context, in CommentInput) (*PreviewOutcome, error) {
	agents, err := s.repo.ListAgents(ctx, in.WorkspaceID)
	if err != nil {
		return nil, err
	}
	squads, err := s.repo.ListSquadFacts(ctx, in.WorkspaceID)
	if err != nil {
		return nil, err
	}
	history, err := s.repo.HistoryAgentIDs(ctx, in.WorkItemID)
	if err != nil {
		return nil, err
	}
	facts := domain.CommentFacts{
		WorkspaceID: in.WorkspaceID, WorkItemID: in.WorkItemID,
		AuthorType: in.AuthorType, AuthorID: in.AuthorID, Content: in.Content,
		ParentAuthorType: in.ParentAuthorType, ParentAuthorID: in.ParentAuthorID,
		RootAuthorType: in.RootAuthorType, RootAuthorID: in.RootAuthorID,
		AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
		HistoryAgentIDs: history,
	}
	agentFacts, invocable, err := s.loadFacts(ctx, in.WorkspaceID, agents, squads, humanActorOf(in))
	if err != nil {
		return nil, err
	}
	d := domain.DecisionTree(facts, agentFacts, squads, invocable)
	out := &PreviewOutcome{}
	for _, tr := range d.Triggers {
		out.Triggers = append(out.Triggers, TriggerHit{AgentID: tr.AgentID, Reason: tr.Reason})
	}
	for _, sp := range d.Suppressions {
		out.Suppressions = append(out.Suppressions, Suppressed{AgentID: sp.AgentID, Reason: sp.Reason})
	}
	return out, nil
}

func humanActorOf(in CommentInput) string {
	if in.AuthorType == "user" {
		return in.AuthorID
	}
	return ""
}

func (s *TriggerService) loadFacts(ctx context.Context, workspaceID string, agents []Agent, squads []domain.SquadFacts, actorID string) ([]domain.AgentFacts, func(domain.AgentFacts) bool, error) {
	agentFacts := make([]domain.AgentFacts, len(agents))
	byID := make(map[string]Agent, len(agents))
	for i, a := range agents {
		agentFacts[i] = domain.AgentFacts{ID: a.ID, Name: a.Name, Archived: a.Archived}
		byID[a.ID] = a
	}
	for i := range agentFacts {
		for _, sq := range squads {
			if sq.LeaderID == agentFacts[i].ID {
				agentFacts[i].LeaderOfSquads = append(agentFacts[i].LeaderOfSquads, sq.ID)
			}
			for _, m := range sq.MemberIDs {
				if m == agentFacts[i].ID {
					agentFacts[i].MemberOfSquads = append(agentFacts[i].MemberOfSquads, sq.ID)
				}
			}
		}
	}
	targets, err := s.repo.ListInvocationTargets(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	return agentFacts, func(a domain.AgentFacts) bool {
		agent, ok := byID[a.ID]
		if !ok {
			return false
		}
		// 助理接入（v4 §4.1 触发豁免修正）：proxy 行 owner=user，若按
		// private+owner 直判会让"用户本人评论"被当成该用户 proxy agent
		// 的属主调用而误豁免/误触发。proxy 行不参与触发面——一律不可调用。
		if agent.RuntimeKind == "assistant_proxy" {
			return false
		}
		return InvocationAllowed(agent, targets[agent.ID], actorID)
	}, nil
}

func InvocationAllowed(agent Agent, targets []InvocationTarget, actorID string) bool {
	switch agent.Visibility {
	case "", "public":
		return true
	case "public_to":
		for _, t := range targets {
			if t.Type == "workspace" {
				return true
			}
			if t.Type == "member" && t.UserID != "" && t.UserID == actorID {
				return true
			}
		}
		return false
	case "private":
		if actorID != "" && agent.OwnerUserID != "" && actorID == agent.OwnerUserID {
			return true
		}
		for _, t := range targets {
			if t.Type == "member" && t.UserID != "" && t.UserID == actorID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (s *TriggerService) OnComment(ctx context.Context, in CommentInput, commentID string) (result string, reason string, err error) {
	return s.onCommentProtocol(ctx, in, commentID, false)
}

func (s *TriggerService) onCommentProtocol(ctx context.Context, in CommentInput, commentID string, fallback bool) (result string, reason string, err error) {
	agents, err := s.repo.ListAgents(ctx, in.WorkspaceID)
	if err != nil {
		return "", "", err
	}
	squads, err := s.repo.ListSquadFacts(ctx, in.WorkspaceID)
	if err != nil {
		return "", "", err
	}
	history, err := s.repo.HistoryAgentIDs(ctx, in.WorkItemID)
	if err != nil {
		return "", "", err
	}

	facts := domain.CommentFacts{
		WorkspaceID: in.WorkspaceID, WorkItemID: in.WorkItemID,
		AuthorType: in.AuthorType, AuthorID: in.AuthorID,
		ParentAuthorType: in.ParentAuthorType, ParentAuthorID: in.ParentAuthorID,
		RootAuthorType: in.RootAuthorType, RootAuthorID: in.RootAuthorID,
		AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
		HistoryAgentIDs: history, Content: in.Content,
	}
	agentFacts, invocable, err := s.loadFacts(ctx, in.WorkspaceID, agents, squads, humanActorOf(in))
	if err != nil {
		return "", "", err
	}

	d := domain.DecisionTree(facts, agentFacts, squads, invocable)
	if len(d.Triggers) == 0 {
		if len(d.Suppressions) > 0 {
			return dispatchreason.Blocked, d.Suppressions[0].Reason, nil
		}
		return dispatchreason.Blocked, "no_trigger_target", nil
	}

	for _, tr := range d.Triggers {
		out, reason, err := s.protocol(ctx, in, commentID, tr, in.AuthorID, fallback)
		if err != nil {
			return "", "", err
		}
		if out != "" {
			return out, reason, nil
		}
	}
	return dispatchreason.Queued, "", nil
}

func triggerContext(commentID, reason, content string, fallback bool) map[string]any {
	kind := "comment"
	c := map[string]any{
		"trigger": kind, "trigger_comment_id": commentID,
		"trigger_reason": reason, "work_item_title": "…",
		"comment_excerpt": excerpt(content, 500),
	}
	if fallback {
		c["trigger"] = domain.TriggerDeferredFallback
		c["reconcile_attempt"] = true
	}
	return c
}

func (s *TriggerService) protocol(ctx context.Context, in CommentInput, commentID string, tr domain.Trigger, actorID string, fallback bool) (string, string, error) {

	if steered, err := s.repo.TrySteer(ctx, in.WorkspaceID, in.WorkItemID, tr.AgentID, in.Content, actorID); err != nil {
		return "", "", err
	} else if steered {
		return dispatchreason.Steered, tr.Reason, nil
	}

	for round := 0; round < domain.MaxProtocolRounds; round++ {
		merged, err := s.repo.MergeIntoPending(ctx, in.WorkspaceID, in.WorkItemID, tr.AgentID, commentID)
		if err != nil {
			return "", "", err
		}
		if merged {
			return dispatchreason.Coalesced, tr.Reason, nil
		}

		hasActive, err := s.repo.HasActiveTask(ctx, in.WorkspaceID, in.WorkItemID, tr.AgentID)
		if err != nil {
			return "", "", err
		}
		if domain.ClassifyMiss(false, hasActive) == domain.MissActive {
			return s.registerDeferred(ctx, in, commentID, tr, actorID, fallback)
		}

		rc, _ := s.agentRuntimeConfig(ctx, tr.AgentID)
		t, err := s.repo.InsertTask(ctx, Task{
			WorkspaceID: in.WorkspaceID, AgentID: tr.AgentID, WorkItemID: in.WorkItemID,
			Status: domain.Queued, MaxAttempts: 2,
			Context:          triggerContext(commentID, tr.Reason, in.Content, fallback),
			OriginatorUserID: actorID, AccountableUserID: actorID,
			OriginatorSource: domain.SrcDirectHuman, TriggerEvidenceKind: domain.EvidenceMemberComment,
			// 小队编排（真模型 E2E 断裂修复）：Trigger 携带的 squad 上下文
			// 必须落库，否则引擎 squadBrief/成员工具分支不触发，leader 单干。
			IsLeaderTask: tr.IsLeaderTask, SquadID: tr.SquadID,
		})
		if err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return "", "", err
		}
		_ = rc
		s.emit.Emit(ctx, envelope(events.TaskQueued, in.WorkspaceID, actorID, t))
		return dispatchreason.Queued, tr.Reason, nil
	}
	return dispatchreason.Blocked, "protocol_rounds_exhausted", nil
}

func (s *TriggerService) registerDeferred(ctx context.Context, in CommentInput, commentID string, tr domain.Trigger, actorID string, fallback bool) (string, string, error) {
	ctxJSON := triggerContext(commentID, tr.Reason, in.Content, fallback)
	ctxJSON["deferred_reason"] = dispatchreason.AlreadyActive
	ctxJSON["deferred_by_agent"] = tr.AgentID
	_, err := s.repo.InsertDeferredTask(ctx, Task{
		WorkspaceID: in.WorkspaceID, AgentID: tr.AgentID, WorkItemID: in.WorkItemID,
		Status: domain.Deferred, MaxAttempts: 2,
		Context:          ctxJSON,
		OriginatorUserID: actorID, AccountableUserID: actorID,
		OriginatorSource: domain.SrcDirectHuman, TriggerEvidenceKind: domain.EvidenceMemberComment,
	})
	if err != nil {
		if isUniqueViolation(err) {

			merged, merr := s.repo.MergeIntoPending(ctx, in.WorkspaceID, in.WorkItemID, tr.AgentID, commentID)
			if merr != nil {
				return "", "", merr
			}
			if merged {
				return dispatchreason.Coalesced, tr.Reason, nil
			}
			return dispatchreason.Blocked, dispatchreason.AlreadyActive, nil
		}
		return "", "", err
	}
	return dispatchreason.Deferred, dispatchreason.AlreadyActive, nil
}

func (s *TriggerService) agentRuntimeConfig(ctx context.Context, agentID string) (map[string]any, error) {

	return nil, nil
}

func isUniqueViolation(err error) bool { return errors.Is(err, domain.ErrFenceHit) }

func excerpt(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (s *TriggerService) ReconcileUncovered(ctx context.Context) (int, error) {
	rows, err := s.repo.ListUncoveredTriggerComments(ctx)
	if err != nil {
		return 0, err
	}
	if len(rows) > reconcileMaxPerSweep {
		rows = rows[:reconcileMaxPerSweep]
	}
	n := 0
	for _, u := range rows {
		in := CommentInput{
			WorkspaceID: u.WorkspaceID, WorkItemID: u.WorkItemID,
			AuthorType: u.AuthorType, AuthorID: u.AuthorID, Content: u.Content,
			ParentAuthorType: u.ParentAuthorType, ParentAuthorID: u.ParentAuthorID,
			RootAuthorType: u.RootAuthorType, RootAuthorID: u.RootAuthorID,
			AssigneeType: u.AssigneeType, AssigneeID: u.AssigneeID,
		}
		if _, _, err := s.onCommentProtocol(ctx, in, u.CommentID, true); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

const reconcileMaxPerSweep = 10

func (s *TriggerService) Rerun(ctx context.Context, wsID, workItemID, agentID, userID string) (*Task, error) {
	tasks, err := s.repo.ListTasksByWorkItem(ctx, workItemID, false)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.AgentID == agentID && (t.Status == domain.Queued || t.Status == domain.Dispatched) {
			if _, err := s.repo.CancelRun(ctx, wsID, t.ID); err != nil {
				return nil, err
			}
		}
	}
	nt, err := s.repo.InsertTask(ctx, Task{
		WorkspaceID: wsID, AgentID: agentID, WorkItemID: workItemID,
		Status: domain.Queued, MaxAttempts: 2,
		Context:          map[string]any{"trigger": "rerun", "force_fresh_session": true},
		OriginatorUserID: userID, AccountableUserID: userID,
		OriginatorSource: domain.SrcDirectHuman, TriggerEvidenceKind: domain.EvidenceRerun,
	})
	if err != nil {
		return nil, err
	}
	s.emit.Emit(ctx, envelope(events.TaskQueued, wsID, userID, nt))
	return &nt, nil
}

func (s *TriggerService) RecoverDelegated(ctx context.Context, failed Task, note string) (bool, error) {
	n, err := s.repo.CountRecoveriesFrom(ctx, failed.ID)
	if err != nil {
		return false, err
	}
	if n >= 3 {
		return false, nil
	}
	_, err = s.repo.InsertRecoveryTask(ctx, failed, note)
	if err != nil {
		return false, err
	}
	return true, nil
}
