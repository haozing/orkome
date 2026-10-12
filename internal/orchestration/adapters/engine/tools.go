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
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"content": {Type: schema.String, Desc: "comment text (plain text)", Required: true},
		}),
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

// ---- C1 契约元数据（ContractTool 可选接口实现，行为不变，仅注册表侧声明）------
//
// OutputSchema 按 InvokableRun 既有返回串的实际形状写（常量文本 → const
// 锚定），使内置工具输出从此也受 A1 校验管线约束（C1 目标 1 的红利闭环）。

// approvalTool 的输出是人类审批决定的转述（Approved/Denied 文本），
// 豁免输出契约（纯文本答复类）；审批中断语义由 eino Interrupt 保证，
// 中断型工具保守不并行。
func (t *approvalTool) OutputSchema() json.RawMessage { return nil }

// approvalTool 中断型工具，保守不并行。
func (t *approvalTool) ConcurrencySafe() bool { return false }

// postCommentTool 写评论（有副作用）。成功输出是常量文本，但端口失败时
// 返回 toolFail 错误串（同为 string）——契约按"字符串"形状声明，锚定
// 形状不锚定具体文案（改文案不需动契约，改结构则会被拦下）。
func (t *postCommentTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"string"}`)
}

// postCommentTool 有副作用，不并行。
func (t *postCommentTool) ConcurrencySafe() bool { return false }

// getWorkItemTool 只读，输出 Title/Description 两行人类可读文本（自由文本
// 不可枚举），按字符串形状声明。
func (t *getWorkItemTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"string"}`)
}

// getWorkItemTool 只读无副作用，可并行。
func (t *getWorkItemTool) ConcurrencySafe() bool { return true }

// searchWorkspaceTool 只读检索，输出是人类可读的命中清单文本（含截断省略
// 号等动态格式），豁免输出契约。
func (t *searchWorkspaceTool) OutputSchema() json.RawMessage { return nil }

// searchWorkspaceTool 只读无副作用，可并行。
func (t *searchWorkspaceTool) ConcurrencySafe() bool { return true }

func (t *searchWorkspaceTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "search_workspace",
		Desc: "search work items in the workspace (fused across title/body/comments)",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {Type: schema.String, Desc: "keywords to search for", Required: true},
		}),
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
