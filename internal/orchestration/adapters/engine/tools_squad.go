package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"orkome/internal/contract/failuretax"
	orchapp "orkome/internal/orchestration/app"
)

func toolFail(what string, err error) (string, error) {
	return fmt.Sprintf("%s: %s: %s", failuretax.AgentToolFailed, what, err.Error()), nil
}

func chainRootOf(t orchapp.Task) orchapp.ChildDelegation {
	root := t.OriginatorUserID
	return orchapp.ChildDelegation{
		OriginType:           "agent_create",
		ChainRootUserID:      root,
		ChainRootDirectHuman: root != "",
	}
}

type dispatchToMemberTool struct {
	dispatch orchapp.LeaderDispatcher
	task     orchapp.Task
	agent    orchapp.Agent

	members []orchapp.SquadMember
}

func (t *dispatchToMemberTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "dispatch_to_member",
		Desc: "formally dispatch work to a squad member (goes through platform dispatch and attribution; do not just @). Input {\"member\":\"member name\",\"instruction\":\"specific instruction\",\"work_item_id\":\"optional: attach to a sub work item\"}",
	}, nil
}

func (t *dispatchToMemberTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in struct {
		Member      string `json:"member"`
		Instruction string `json:"instruction"`
		WorkItemID  string `json:"work_item_id"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil || strings.TrimSpace(in.Member) == "" || strings.TrimSpace(in.Instruction) == "" {
		return "invalid args: member and instruction are required.", nil
	}
	memberID, memberName := t.resolveMember(in.Member)
	if memberID == "" {
		return fmt.Sprintf("Member %q is not on the squad roster. Available members: %s", in.Member, memberRosterOf(t.members)), nil
	}
	wi := in.WorkItemID
	if wi == "" {
		wi = t.task.WorkItemID
	}
	taskID, reason, err := t.dispatch.EnqueueLeaderDispatch(ctx, orchapp.LeaderDispatchInput{
		WorkspaceID: t.task.WorkspaceID, WorkItemID: wi, SquadID: t.task.SquadID,
		LeaderTaskID: t.task.ID, LeaderAgentID: t.agent.ID,
		MemberAgentID: memberID, MemberName: memberName, Instruction: in.Instruction,
		OriginatorUserID:  t.task.OriginatorUserID,
		AccountableUserID: t.task.AccountableUserID,
	})
	if err != nil {
		return toolFail("dispatch_to_member", err)
	}
	switch reason {
	case "queued":
		return fmt.Sprintf("Dispatched to %s (task %s). It will come back to you automatically when the member finishes.", memberName, taskID), nil
	default:

		return fmt.Sprintf("Could not dispatch to %s, reason: %s. Try another member or retry later.", memberName, reason), nil
	}
}

func (t *dispatchToMemberTool) resolveMember(m string) (string, string) {
	for _, mem := range t.members {
		if mem.AgentID == m || mem.Name == m {
			return mem.AgentID, mem.Name
		}
	}
	return "", ""
}

func memberRosterOf(members []orchapp.SquadMember) string {
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m.Name)
	}
	return strings.Join(names, "、")
}

type createChildWorkItemTool struct {
	children orchapp.ChildItemCreator
	task     orchapp.Task
	agent    orchapp.Agent
}

func (t *createChildWorkItemTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "create_child_work_item",
		Desc: "create a sub work item under the current one (human-visible; completion wakes you to summarize). Input {\"title\":\"title\",\"description\":\"description\"}",
	}, nil
}

func (t *createChildWorkItemTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil || strings.TrimSpace(in.Title) == "" {
		return "invalid args: title is required.", nil
	}
	wi, err := t.children.CreateChildWorkItem(ctx, orchapp.ChildItemInput{
		WorkspaceID: t.task.WorkspaceID, ParentID: t.task.WorkItemID,
		Title: in.Title, Description: in.Description, CreatorAgentID: t.agent.ID,
		Delegation: chainRootOf(t.task),
	})
	if err != nil {
		return toolFail("create_child_work_item", err)
	}
	return fmt.Sprintf("Sub work item created: %s (%s). You will be woken automatically when all sub-items complete.", wi.Title, wi.ID), nil
}

type recordEvaluationTool struct {
	recorder orchapp.SquadEvaluationRecorder
	task     orchapp.Task
	agent    orchapp.Agent
}

func (t *recordEvaluationTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "record_evaluation",
		Desc: "before finishing, report your evaluation of each member's work (mandatory; this is the only official ledger, do not substitute with comments). Input {\"member\":\"member name\",\"verdict\":\"action|no_action|failed\",\"rationale\":\"rationale\"}",
	}, nil
}

func (t *recordEvaluationTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in struct {
		Member    string `json:"member"`
		Verdict   string `json:"verdict"`
		Rationale string `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil || strings.TrimSpace(in.Member) == "" {
		return "invalid args: member is required.", nil
	}
	switch in.Verdict {
	case "action", "no_action", "failed":
	default:
		return "verdict must be action, no_action, or failed.", nil
	}

	err := t.recorder.RecordSquadEvaluation(ctx, orchapp.SquadEvaluation{
		WorkspaceID: t.task.WorkspaceID, WorkItemID: t.task.WorkItemID,
		LeaderAgentID: t.agent.ID, SquadID: t.task.SquadID, TaskID: t.task.ID,
		Member: in.Member, Verdict: in.Verdict, Rationale: in.Rationale,
	})
	if err != nil {
		return toolFail("record_evaluation", err)
	}
	return fmt.Sprintf("Evaluation recorded (%s → %s).", in.Member, in.Verdict), nil
}
