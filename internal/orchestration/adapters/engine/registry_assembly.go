package engine

// C1：engine.go 装配段抽出的统一注册表装配（行为不变优先）。
//
// buildBuiltinTools 原样搬移 engine.go:221-260 的内置工具逐个装配逻辑
// （条件、顺序、依赖注入完全一致，diff 应显示纯搬移），并在装配完成的
// 同时向统一注册表登记契约元数据（ContractTool 可选接口回收）。
// MCP 桥接工具经 registerMCPBridge 进同一注册表。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"

	orchapp "orkome/internal/orchestration/app"
)

// buildBuiltinTools 按任务上下文装配内置工具并登记注册表。
func (e *EinoEngine) buildBuiltinTools(ctx context.Context, task orchapp.Task, agent orchapp.Agent,
	reg *toolRegistry, squadMembers []orchapp.SquadMember) []tool.BaseTool {

	toolList := []tool.BaseTool{&approvalTool{}}
	if e.WorkspaceSearch != nil {
		toolList = append(toolList, &searchWorkspaceTool{search: e.WorkspaceSearch, task: task})
	}
	if e.WorkBench != nil && task.WorkItemID != "" {
		toolList = append(toolList,
			&postCommentTool{wb: e.WorkBench, task: task, agentID: agent.ID},
			&getWorkItemTool{wb: e.WorkBench, task: task})
	}

	if e.StatusUpdate != nil && task.WorkItemID != "" {
		toolList = append(toolList, &updateWorkItemStatusTool{update: e.StatusUpdate, task: task, agent: agent})
	}
	if e.Checkiner != nil && task.WorkItemID != "" {
		if wid, _ := task.Context["wakeup_id"].(string); wid != "" {
			toolList = append(toolList, &checkinWakeupTool{checkiner: e.Checkiner, task: task, wakeupID: wid})
		}
	}

	// 批量任务工具面（batch v2.0）：绑定当前工单的扇出执行通道。
	// MaterializeFromSource 由 cmd 侧注入（app 层实现，engine 只约定形状）。
	if e.BatchTools != nil && task.WorkItemID != "" {
		toolList = append(toolList,
			&batchCreateTool{batches: e.BatchTools, materialze: e.materializeOf(task, agent), task: task, agent: agent},
			&batchStatusTool{batches: e.BatchTools, task: task},
			&batchResultsTool{batches: e.BatchTools, task: task})
	}

	if task.IsLeaderTask && task.SquadID != "" {
		if e.LeaderDispatch != nil {
			toolList = append(toolList,
				&dispatchToMemberTool{dispatch: e.LeaderDispatch, task: task, agent: agent, members: squadMembers})
		}
		if e.ChildItems != nil {
			toolList = append(toolList, &createChildWorkItemTool{children: e.ChildItems, task: task, agent: agent})
		}
		if e.SquadEvals != nil {
			toolList = append(toolList, &recordEvaluationTool{recorder: e.SquadEvals, task: task, agent: agent})
		}
	}

	for _, t := range toolList {
		reg.registerBuiltin(ctx, t)
	}
	return toolList
}

// registerMCPBridge 把 MCP 桥接工具的最终生效 schema 登记进统一注册表。
// schemaOf 的优先级（与 A1 冻结文档 §3 一致）：协议自描述（实时握手回收）
// → B1 存档（pack_tool_schema，经 PackToolSchemas 端口注入）→ 无（运行
// 期结构化探测回落，由校验器兜底）。登记不影响桥接工具的执行行为。
func registerMCPBridge(ctx context.Context, reg *toolRegistry, bridge *mcpBridge, archived map[string]json.RawMessage) {
	if bridge == nil {
		return
	}
	servers := mcpServerToolOwners(bridge)
	for _, t := range bridge.tools {
		info, err := t.Info(ctx)
		if err != nil || info == nil || info.Name == "" {
			continue
		}
		schema := bridge.schemaOf(info.Name)
		if len(schema) == 0 {
			schema = archived[info.Name]
		}
		reg.registerMCP(info.Name, servers[info.Name], schema)
	}
}

// patchToolSnapshot 把统一注册表全量清单 + MCP 桥接细化态回写进
// task.context（C1，快照语义理顺的落点）：
//
//   - tools（新键）：注册表全量工具清单（内置 + MCP，含契约与并发分型
//     标记），一次写入即完整——这是任务真实可用的工具面；
//   - mcp_servers（旧键）：用桥接的细化态（ok/ssrf_rejected/
//     connect_failed/list_failed + tool_count）覆盖派发期 dispatcher 写入
//     的 resolved 粗快照——统一写入方：引擎侧为权威（离执行最近、状态
//     最细），dispatcher 的 resolved 态只作引擎未跑（排队/取消）时的
//     占位。不搬死数据：br.snapshot 此前的无消费者问题就此收口。
//
// 端口未接线（TaskContextPatcher=nil，如未装配的部署与大多数既有测试）
// 或无任何工具/服务器时不写，行为与 C1 之前一致。回写失败只记日志，
// 不影响任务执行（快照是观测面，不是控制面）。
func (e *EinoEngine) patchToolSnapshot(ctx context.Context, task orchapp.Task, reg *toolRegistry, bridge *mcpBridge) {
	if e.TaskContextPatcher == nil {
		return
	}
	var mcpEntries []orchapp.MCPSnapshotEntry
	if bridge != nil {
		mcpEntries = bridge.snapshot
	}
	tools := reg.snapshot()
	if len(tools) == 0 && len(mcpEntries) == 0 {
		return
	}
	patch := orchapp.ToolContextSnapshot(mcpEntries, tools)
	if patch == nil {
		return
	}
	if err := e.TaskContextPatcher.PatchTaskContext(ctx, task.ID, patch); err != nil {
		e.auditLogger().LogAttrs(ctx, slog.LevelWarn, "task tool snapshot patch failed",
			slog.String("task", task.ID), slog.String("err", err.Error()))
	}
}

// mcpServerToolOwners 建桥接快照的 toolName → 服务器名归属。快照按服务器
// 聚合（无逐工具清单），v0 以"工具名前缀匹配服务器名"尽力归属——server
// 名可含下划线，取最长前缀命中；归属信息仅用于快照展示，不参与任何判定。
func mcpServerToolOwners(bridge *mcpBridge) map[string]string {
	if bridge == nil || len(bridge.snapshot) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, srv := range bridge.snapshot {
		for _, t := range bridge.tools {
			info, err := t.Info(context.Background())
			if err != nil || info == nil {
				continue
			}
			if other, ok := out[info.Name]; ok && len(other) > len(srv.Name) {
				continue // 已有更长前缀命中，保留
			}
			if strings.HasPrefix(info.Name, srv.Name+"_") {
				out[info.Name] = srv.Name
			}
		}
	}
	return out
}

// —— MCP 桥装配的导出面（batch mcp_tool 执行器用，batch v2.0 设计 §4）——
// 与包内组装同一条链（ResolveForEngine 合并 + SSRF 门 + mcpCatalog 缓存 +
// 输出契约校验），跨包调用不引入第二个组装实现。
// 历史注记：D2 联调门面（BuildMCPTools/NewToolRegistryForTest/
// RegisterMCPBridgeForTest 等）已随 pack-provision CLI 退役删除（2026-10-15）。

// BuildMCPToolsForAgent 是 buildMCPTools 的导出版：透传真实 agentID——
// agent 级 MCP 配置参与工具面合成（白名单语义：agent 扇不出它看不见的
// 工具）。task 只需 WorkspaceID/ID（批次关联）。
func (e *EinoEngine) BuildMCPToolsForAgent(ctx context.Context, task orchapp.Task, agentID string) *MCPBridgeForTest {
	br := e.buildMCPTools(ctx, task, orchapp.Agent{ID: agentID})
	if br == nil {
		return nil
	}
	return &MCPBridgeForTest{br: br}
}

// InvokeMCPTool 在桥接工具清单里按 Info().Name() 定位目标工具并以 argsJSON
// 直调（eino tool.InvokableRun）。找不到 → 明确错误（batch 条目 failed 带
// 原因，绝不静默换工具）。执行路径与模型调用完全一致（含输出契约校验
// 装饰器；装饰器把 MCP 业务级失败降级为错误串返回，传输级错误原样抛出）。
func InvokeMCPTool(ctx context.Context, bridge *MCPBridgeForTest, name, argsJSON string) (string, error) {
	if bridge == nil || bridge.br == nil {
		return "", fmt.Errorf("mcp tool %q: bridge not built (no live tool surface)", name)
	}
	for _, t := range bridge.br.tools {
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		if info.Name != name {
			continue
		}
		inv, ok := t.(tool.InvokableTool)
		if !ok {
			return "", fmt.Errorf("mcp tool %q is not invokable (streaming-only tools unsupported for batch fan-out)", name)
		}
		return inv.InvokableRun(ctx, argsJSON)
	}
	return "", fmt.Errorf("mcp tool %q not found in resolved tool surface (not in agent/workspace MCP allowlist)", name)
}

// MCPBridgeForTest 包装 mcpBridge 暴露只读观测面（不复制内部切片语义）。
type MCPBridgeForTest struct{ br *mcpBridge }


