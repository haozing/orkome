package main

import (
	"context"
	"encoding/json"

	orchapp "orkome/internal/orchestration/app"
	orchengine "orkome/internal/orchestration/adapters/engine"
)

// batchToolExec 是 batch 通道 B 物化器的 ToolExec 组装根实现：代调数据源
// 工具走与 mcp_tool 执行器完全同链的 MCP 桥（BuildMCPToolsForAgent 解析
// ws+agent 生效工具面 → SSRF/缓存/输出契约校验全继承；InvokeMCPTool 按
// 名定位直调）。数据源工具限定 agent 工具面 = agent 不能扇出它看不见的
// 工具（batch v2.0 设计 §2.3 白名单口径）。
type batchToolExec struct{ engine *orchengine.EinoEngine }

func (e batchToolExec) Invoke(ctx context.Context, ws, workItemID, agentID, toolName string, args map[string]any) (json.RawMessage, error) {
	bridge := e.engine.BuildMCPToolsForAgent(ctx,
		orchapp.Task{ID: "batch-source:" + workItemID, WorkspaceID: ws, WorkItemID: workItemID}, agentID)
	out, err := orchengine.InvokeMCPTool(ctx, bridge, toolName, mustArgsJSON(args))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

func mustArgsJSON(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(b)
}

var _ orchapp.ToolExec = batchToolExec{}
