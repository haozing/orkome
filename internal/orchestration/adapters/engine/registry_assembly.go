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

// —— D2 联调测试门面（跨包测试只读装配路径，非生产面）——
// 语义与包内调用完全一致：BuildMCPTools 即 buildMCPTools（C1 组装路径），
// RegisterMCPBridgeForTest 即 registerMCPBridge，NewToolRegistryForTest 即
// newToolRegistry；导出仅为 cmd/pack-provision 的 env 门控联调测试
// （agentblog_e2e_test.go）复用真实组装链路，不引入第二个组装实现。

// BuildMCPTools 是 buildMCPTools 的导出版（D2 联调测试用）。
func (e *EinoEngine) BuildMCPTools(ctx context.Context, task orchapp.Task) *MCPBridgeForTest {
	br := e.buildMCPTools(ctx, task, orchapp.Agent{ID: "d2-e2e"})
	if br == nil {
		return nil
	}
	return &MCPBridgeForTest{br: br}
}

// MCPBridgeForTest 包装 mcpBridge 暴露只读观测面（不复制内部切片语义）。
type MCPBridgeForTest struct{ br *mcpBridge }

// ToolsForTest 返回桥接工具清单长度与名字的只读投影。
func (b *MCPBridgeForTest) ToolsForTest() []string {
	out := make([]string, 0, len(b.br.tools))
	for _, t := range b.br.tools {
		if info, err := t.Info(context.Background()); err == nil && info != nil {
			out = append(out, info.Name)
		}
	}
	return out
}

// SnapshotForTest 返回桥接快照副本。
func (b *MCPBridgeForTest) SnapshotForTest() []orchapp.MCPSnapshotEntry {
	return append([]orchapp.MCPSnapshotEntry(nil), b.br.snapshot...)
}

// SchemaOf 透传 schemaOf（最终生效输出契约：协议自描述 → B1 存档）。
func (b *MCPBridgeForTest) SchemaOf(name string) json.RawMessage { return b.br.schemaOf(name) }

// RawTools 返回桥接内含的 eino 工具（供注册表登记测试用）。
func (b *MCPBridgeForTest) Raw() *mcpBridge { return b.br }

// NewToolRegistryForTest 是 newToolRegistry 的导出版（D2 联调测试用）。
func NewToolRegistryForTest() *ToolRegistryForTest { return &ToolRegistryForTest{r: newToolRegistry()} }

// ToolRegistryForTest 包装 toolRegistry 暴露登记与快照观测面。
type ToolRegistryForTest struct{ r *toolRegistry }

// RegisterMCPBridgeForTest 是 registerMCPBridge 的导出版（D2 联调测试用）。
func RegisterMCPBridgeForTest(ctx context.Context, reg *ToolRegistryForTest, bridge *MCPBridgeForTest) {
	if bridge == nil {
		return
	}
	registerMCPBridge(ctx, reg.r, bridge.br, nil)
}

// SnapshotForTest 返回注册表全量清单副本（task.context.tools 数据源）。
func (r *ToolRegistryForTest) SnapshotForTest() []orchapp.ToolSnapshotEntry { return r.r.snapshot() }
