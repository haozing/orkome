package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	orchengine "orkome/internal/orchestration/adapters/engine"
	orchapp "orkome/internal/orchestration/app"
)

// mcpToolBatchProcessor BatchProcessor（kind=mcp_tool，batch v2.0 P2 设计 §4）：
// 逐条以 payload 为入参调用 params.tool 指定的 MCP 工具——OCR 等 MCP 能力
// 的批量化执行面。工具定位复用引擎 MCP 桥接（BuildMCPToolsForAgent：
// ResolveForEngine 合并工具面 + SSRF 门 + mcpCatalog 缓存 + 输出契约校验
// 装饰器全继承），按 Info().Name() 定位后 eino InvokableRun 直调。
//
// params 约定：{workspace_id, work_item_id, agent_id, tool}——前三个由
// ParamsFiller 的 mcp_tool 分支补全（agent 取工单指派人，无指派 fail-fast）。
//
// 并发安全：processor 自身无共享状态（MCP 工具默认非并发安全，扇出并发由
// batch.concurrency 控制，默认 4）——无需额外锁。
type mcpToolBatchProcessor struct {
	engine *orchengine.EinoEngine
	log    *slog.Logger
}

func (p *mcpToolBatchProcessor) Kind() string { return "mcp_tool" }

func (p *mcpToolBatchProcessor) Process(ctx context.Context, params, payload map[string]any) (map[string]any, error) {
	ws, _ := params["workspace_id"].(string)
	wi, _ := params["work_item_id"].(string)
	agentID, _ := params["agent_id"].(string)
	toolName, _ := params["tool"].(string)
	if ws == "" || wi == "" || agentID == "" {
		return nil, fmt.Errorf("params.workspace_id/work_item_id/agent_id required")
	}
	if toolName == "" {
		return nil, fmt.Errorf("params.tool required (the MCP tool to fan out)")
	}

	// 最小 task 触发引擎桥接装配（桥接只读 WorkspaceID/ID；ID 承载批次
	// 关联，落快照/日志可追溯）。agentID 透传参与工具面合成——白名单语义。
	task := orchapp.Task{ID: "batch:" + wi, WorkspaceID: ws, WorkItemID: wi}
	bridge := p.engine.BuildMCPToolsForAgent(ctx, task, agentID)

	argsJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload not JSON-marshalable: %w", err)
	}
	out, err := orchengine.InvokeMCPTool(ctx, bridge, toolName, string(argsJSON))
	if err != nil {
		return nil, err
	}
	// 结果落返回值（worker 存 batch_item.result_ref）。工具结果是字符串
	// 形态（eino 桥接口径），能解析为 JSON 对象就结构化带出 result，否则
	// 原样放 output。
	result := map[string]any{"tool": toolName, "output": out}
	var doc map[string]any
	if json.Unmarshal([]byte(out), &doc) == nil && doc != nil {
		result["result"] = doc
	}
	p.log.Info("batch mcp_tool processed", "tool", toolName, "wi", wi, "out_len", len(out))
	return result, nil
}
