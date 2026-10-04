package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

func init() {

	schema.RegisterName[ApprovalDecision]("orkome_approval_decision")
	schema.RegisterName[ApprovalRequest]("orkome_approval_request")
}

type ApprovalRequest struct {
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

type ApprovalDecision struct {
	Approved bool   `json:"approved"`
	ByUserID string `json:"by_user_id"`
	Note     string `json:"note"`
}

type approvalTool struct {
	invocations int
}

func (t *approvalTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "request_approval",
		Desc: "request human approval before dangerous operations (pauses execution until approved/denied)",
	}, nil
}

func (t *approvalTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	t.invocations++
	var req ApprovalRequest
	_ = json.Unmarshal([]byte(args), &req)
	if req.Action == "" {
		req.Action = "unspecified"
	}
	wasInterrupted, _, _ := tool.GetInterruptState[any](ctx)
	if !wasInterrupted {
		return "", tool.Interrupt(ctx, req)
	}
	_, hasData, decision := tool.GetResumeContext[ApprovalDecision](ctx)
	if !hasData {
		return "", fmt.Errorf("resumed without decision")
	}
	if decision.Approved {
		return fmt.Sprintf("Approved (by %s). %s", decision.ByUserID, decision.Note), nil
	}
	return fmt.Sprintf("Denied (by %s): %s", decision.ByUserID, decision.Note), nil
}

type postCommentTool struct {
	wb      orchapp.WorkBench
	task    orchapp.Task
	agentID string
	calls   int
}

func (t *postCommentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "post_comment", Desc: "post a comment on the current work item as yourself",
	}, nil
}

func (t *postCommentTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil || in.Content == "" {
		return "", apierr.NewValidation("content required")
	}
	t.calls++
	if err := t.wb.PostAgentComment(ctx, t.task.WorkspaceID, t.task.WorkItemID, t.agentID, in.Content); err != nil {

		return toolFail("post_comment", err)
	}
	return "Comment posted.", nil
}

type getWorkItemTool struct {
	wb   orchapp.WorkBench
	task orchapp.Task
}

func (t *getWorkItemTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "get_work_item", Desc: "read the current work item title and description"}, nil
}

func (t *getWorkItemTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	title, desc, err := t.wb.GetWorkItemBrief(ctx, t.task.WorkspaceID, t.task.WorkItemID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Title: %s\nDescription: %s", title, desc), nil
}

type searchWorkspaceTool struct {
	search func(ctx context.Context, workspaceID, query string) ([]orchapp.SearchHit, error)
	task   orchapp.Task
}

func (t *searchWorkspaceTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "search_workspace",
		Desc: "search work items in the workspace (fused across title/body/comments); input {\"query\":\"keywords\"}",
	}, nil
}

func (t *searchWorkspaceTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil || in.Query == "" {
		return "", apierr.NewValidation("query required")
	}
	hits, err := t.search(ctx, t.task.WorkspaceID, in.Query)
	if err != nil {

		return toolFail("search_workspace", err)
	}
	if len(hits) == 0 {
		return "No hits.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d hit(s):\n", len(hits))
	for i, h := range hits {
		if i >= 10 {
			fmt.Fprintf(&b, "…(%d more omitted)\n", len(hits)-10)
			break
		}
		fmt.Fprintf(&b, "%d. %s", i+1, h.Title)
		if h.Snippet != "" {
			fmt.Fprintf(&b, "——%s", h.Snippet)
		}
		fmt.Fprintln(&b)
	}
	return b.String(), nil
}
