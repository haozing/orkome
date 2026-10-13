package engine

// 批量任务工具面（batch v2.0，docs/batch-agent-tool-design-2026-10-13.md §2）：
// batch_create（双通道：inline 小批量 / source+template 大批量物化预览）、
// batch_status（对账计数）、batch_results（分页收割）。绑定当前 task 的
// work_item；通道 B 创建即 draft（worker 不取），预览确认后 batch_start。

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	orchapp "orkome/internal/orchestration/app"
)

// batchToolPort 是引擎侧对 BatchService 的窄接口（能力面：创建/查询/收割/
// 开跑）。装配根注入 *orchapp.BatchService。
type batchToolPort interface {
	CreateAndMaterialize(ctx context.Context, b orchapp.Batch, payloads []map[string]any) (*orchapp.Batch, error)
	CreateDraftFromSource(ctx context.Context, b orchapp.Batch, matSource func(ctx context.Context, b *orchapp.Batch) (*orchapp.MaterializedSource, error)) (*orchapp.Batch, error)
	GetBatch(ctx context.Context, ws, id string) (orchapp.Batch, bool, error)
	Start(ctx context.Context, ws, id string) error
	ListItemsPage(ctx context.Context, ws, batchID, status string, afterSeq, limit int) ([]orchapp.BatchItem, error)
}

// materializeFn 通道 B 的物化入口：包住 app.MaterializeFromSource 与本任务
// 的 ToolExec（代调数据源工具的执行器，engine 自身即实现——MCP 桥同链）。
type materializeFn = func(ctx context.Context, b *orchapp.Batch) (*orchapp.MaterializedSource, error)

type batchCreateTool struct {
	batches    batchToolPort
	materialze materializeFn
	task       orchapp.Task
	agent      orchapp.Agent
}

// materializeOf 为本任务构造物化入口：ToolExec 走 MCP 桥（与 mcp_tool 执行
// 器同链，白名单=该 agent 生效工具面）。
func (e *EinoEngine) materializeOf(task orchapp.Task, agent orchapp.Agent) materializeFn {
	exec := mcpToolExec{engine: e, task: task, agent: agent}
	return func(ctx context.Context, b *orchapp.Batch) (*orchapp.MaterializedSource, error) {
		return orchapp.MaterializeFromSource(ctx, exec, b)
	}
}

// mcpToolExec 让物化器代调数据源工具：直接复用 BuildMCPToolsForAgent +
// InvokeMCPTool（与 mcp_tool BatchProcessor 同链）。
type mcpToolExec struct {
	engine *EinoEngine
	task   orchapp.Task
	agent  orchapp.Agent
}

func (e mcpToolExec) Invoke(ctx context.Context, ws, workItemID, agentID, toolName string, args map[string]any) (json.RawMessage, error) {
	t := e.task
	t.WorkspaceID, t.WorkItemID = ws, workItemID
	bridge := e.engine.BuildMCPToolsForAgent(ctx, t, agentID)
	argsJSON, _ := json.Marshal(args)
	out, err := InvokeMCPTool(ctx, bridge, toolName, string(argsJSON))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

func (t *batchCreateTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "batch_create",
		Desc: "create a batch that fans out N lightweight executions bound to the current work item (NOT N sub work items — zero per-item lifecycle cost). Two channels: (A) inline payloads (≤20 items you already have) — starts immediately; (B) source + payload_template for large sets (hundreds+) — data is fetched server-side via a registered tool and materialized into a draft for your review (return sample), then call batch_start. Input {\"kind\":\"llm_comment|mcp_tool\", \"payloads\":[{...}], OR \"source\":{\"tool\":\"list_attachments\",\"args\":{},\"pick\":\"$.attachments[*]\"}, \"payload_template\":{\"image_ref\":\"$.file_id\",\"lang\":\"zh\"}, \"required\":[\"image_ref\"], \"tool\":\"<mcp tool to fan out, required for kind=mcp_tool>\", \"concurrency\":3}",
	}, nil
}

func (t *batchCreateTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in struct {
		Kind            string           `json:"kind"`
		Tool            string           `json:"tool"`
		Payloads        []map[string]any `json:"payloads"`
		Source          map[string]any   `json:"source"`
		PayloadTemplate map[string]any   `json:"payload_template"`
		Required        []string         `json:"required"`
		Concurrency     int              `json:"concurrency"`
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return toolFail("batch_create", err)
	}
	if in.Kind == "" {
		return "batch_create rejected: kind required (llm_comment | mcp_tool)", nil
	}
	b := orchapp.Batch{
		WorkspaceID:  t.task.WorkspaceID,
		WorkItemID:   t.task.WorkItemID,
		Kind:         in.Kind,
		OriginatorTaskID:  t.task.ID,
		OriginatorAgentID: t.task.AgentID,
	}
	if b.WorkItemID == "" {
		return "batch_create rejected: current task has no bound work item", nil
	}
	if in.Kind == "mcp_tool" {
		b.Params = map[string]any{"tool": in.Tool}
	}
	if in.Concurrency > 0 {
		b.Concurrency = in.Concurrency
	}
	var out *orchapp.Batch
	switch {
	case len(in.Payloads) > 0:
		created, err := t.batches.CreateAndMaterialize(ctx, b, in.Payloads)
		if err != nil {
			if isValidation(err) {
				return "batch_create rejected: " + err.Error(), nil
			}
			return toolFail("batch_create", err)
		}
		out = created
	case in.Source != nil:
		b.Source = map[string]any{
			"source":           in.Source,
			"payload_template": in.PayloadTemplate,
			"required":         in.Required,
		}
		created, err := t.batches.CreateDraftFromSource(ctx, b, t.materialze)
		if err != nil {
			if isValidation(err) {
				return "batch_create rejected: " + err.Error(), nil
			}
			return toolFail("batch_create", err)
		}
		out = created
	default:
		return "batch_create rejected: either payloads (channel A) or source+payload_template (channel B) required", nil
	}
	res, _ := json.Marshal(map[string]any{
		"batch_id": out.ID, "status": out.Status, "total": out.Total,
		"source_materialization": out.SourceMaterialization,
		"next": map[string]string{
			"llm_comment": "runs automatically; harvest with batch_status/batch_results when woken",
			"mcp_tool":    "runs automatically; harvest with batch_status/batch_results when woken",
			"draft":       "review sample then batch_start; harvest with batch_status/batch_results when woken",
		}[map[bool]string{true: "draft", false: out.Status}[out.Status == "draft" && in.Source != nil]],
	})
	return string(res), nil
}

func (t *batchCreateTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"string"}`)
}

// 有副作用（建批次/物化代调），不并行。
func (t *batchCreateTool) ConcurrencySafe() bool { return false }

type batchStatusTool struct {
	batches batchToolPort
	task    orchapp.Task
}

func (t *batchStatusTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "batch_status",
		Desc: "lightweight batch progress check (counts only, no results). Input {\"batch_id\":\"...\"}",
	}, nil
}

func (t *batchStatusTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in struct {
		BatchID string `json:"batch_id"`
	}
	_ = json.Unmarshal([]byte(args), &in)
	if in.BatchID == "" {
		return "batch_status rejected: batch_id required", nil
	}
	b, ok, err := t.batches.GetBatch(ctx, t.task.WorkspaceID, in.BatchID)
	if err != nil {
		return toolFail("batch_status", err)
	}
	if !ok {
		return "batch_status: batch not found", nil
	}
	res, _ := json.Marshal(map[string]any{
		"status": b.Status, "total": b.Total,
		"succeeded": b.Succeeded, "failed": b.Failed, "skipped": b.Skipped,
		"source_materialization": b.SourceMaterialization,
		"last_error":             b.LastError,
	})
	return string(res), nil
}

func (t *batchStatusTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"string"}`)
}

func (t *batchStatusTool) ConcurrencySafe() bool { return true }

type batchResultsTool struct {
	batches batchToolPort
	task    orchapp.Task
}

func (t *batchResultsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "batch_results",
		Desc: "harvest batch results page by page (max 100/page — NEVER request everything at once). Input {\"batch_id\":\"...\",\"status\":\"succeeded|failed|skipped\",\"after_seq\":0,\"limit\":20} — iterate by increasing after_seq to the last returned seq",
	}, nil
}

func (t *batchResultsTool) InvokableRun(ctx context.Context, args string, _ ...tool.Option) (string, error) {
	var in struct {
		BatchID  string `json:"batch_id"`
		Status   string `json:"status"`
		AfterSeq int    `json:"after_seq"`
		Limit    int    `json:"limit"`
	}
	_ = json.Unmarshal([]byte(args), &in)
	if in.BatchID == "" {
		return "batch_results rejected: batch_id required", nil
	}
	items, err := t.batches.ListItemsPage(ctx, t.task.WorkspaceID, in.BatchID, in.Status, in.AfterSeq, in.Limit)
	if err != nil {
		return toolFail("batch_results", err)
	}
	type row struct {
		Seq      int            `json:"seq"`
		Status   string         `json:"status"`
		Payload  map[string]any `json:"payload,omitempty"`
		Result   map[string]any `json:"result,omitempty"`
		SkipReason string       `json:"skip_reason,omitempty"`
		Error    string         `json:"error,omitempty"`
	}
	rows := make([]row, 0, len(items))
	last := in.AfterSeq
	for _, it := range items {
		if it.Seq > last {
			last = it.Seq
		}
		rows = append(rows, row{Seq: it.Seq, Status: it.Status, Payload: it.PayloadRef,
			Result: it.ResultRef, SkipReason: it.SkipReason, Error: it.Error})
	}
	res, _ := json.Marshal(map[string]any{
		"items": rows, "next_after_seq": last,
		"hint": "set after_seq=next_after_seq for the next page; empty items = no more",
	})
	return string(res), nil
}

func (t *batchResultsTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"string"}`)
}

// 只读，可并行。
func (t *batchResultsTool) ConcurrencySafe() bool { return true }
