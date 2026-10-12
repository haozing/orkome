package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
	"github.com/cloudwego/eino/components/tool"

	orchapp "orkome/internal/orchestration/app"
)

// ---------------------------------------------------------------------------
// 统一 Tool 注册表（C1，pack-tool-contract-design §3）。
//
// 内置工具与 MCP 桥接工具进同一个注册表、同一套管线，对模型无差别。
// Tool 接口叠加在既有 eino tool.BaseTool 之上（内置工具本就实现
// Info/InvokableRun）：契约元数据（输出 schema、并发安全分型）以可选
// 接口 + 注册表条目的形式供给，不改变工具的运行行为。
//
// 关键决策：
//   - OutputSchema 存 orkome 注册表侧，不进模型可见 ToolInfo
//     （eino schema.ToolInfo 没有输出 schema 字段）；
//   - ConcurrencySafe fail-closed：默认 false（不并行），显式声明 true
//     才允许进并行组——eino ToolsNode 对同消息多 tool_calls 默认并行
//     （vendor compose/tool_node.go executeSequentially 零值 false），
//     有副作用的工具未声明即不并行是补护栏而非预留。
// ---------------------------------------------------------------------------

// ContractTool 是内置工具的可选契约接口：在 eino tool.BaseTool 之上
// 声明输出契约与并发安全分型。实现方（各内置工具）只提供元数据，
// 执行路径仍是原 InvokableRun，行为不变。
type ContractTool interface {
	// OutputSchema 返回该工具输出的 JSON Schema（按实际返回形状写）。
	// 返回 nil 表示该工具显式豁免输出契约（仅限人类可读的纯文本答复
	// 类工具；豁免必须是有意识的决定，见各工具实现处注释）。
	OutputSchema() json.RawMessage

	// ConcurrencySafe 声明该工具是否可与同消息的其他 tool_call 并行
	// 执行。fail-closed：默认（未实现本接口或返回 false）不并行。
	ConcurrencySafe() bool
}

// toolContract 是一个已注册工具的契约条目（注册表侧存储形状）。
type toolContract struct {
	name            string
	origin          string // "builtin" | "mcp"（快照/诊断用）
	server          string // MCP 工具归属服务器名（内置为空）
	outputSchema    json.RawMessage
	concurrencySafe bool
}

// toolRegistry 是一次任务组装期的统一注册表：内置工具与 MCP 桥接工具
// 同一注册表。条目在装配阶段写入（builtins 由 buildBuiltinTools 报告，
// MCP 桥接工具按桥接清单+schema 来源登记），执行期只读。
type toolRegistry struct {
	entries map[string]toolContract
	order   []string // 保持登记顺序（快照稳定输出）
}

func newToolRegistry() *toolRegistry {
	return &toolRegistry{entries: map[string]toolContract{}}
}

// register 登记一个工具契约。同名后写覆盖先写（与装配段的覆盖顺序一致）。
func (r *toolRegistry) register(name, origin, server string, schema json.RawMessage, concurrencySafe bool) {
	if _, exists := r.entries[name]; !exists {
		r.order = append(r.order, name)
	}
	r.entries[name] = toolContract{
		name: name, origin: origin, server: server,
		outputSchema: schema, concurrencySafe: concurrencySafe,
	}
}

// registerBuiltin 从内置工具实例回收契约元数据（ContractTool 可选实现）。
func (r *toolRegistry) registerBuiltin(ctx context.Context, t tool.BaseTool) {
	info, err := t.Info(ctx)
	if err != nil || info == nil || info.Name == "" {
		return
	}
	var schema json.RawMessage
	var safe bool
	if ct, ok := t.(ContractTool); ok {
		schema = ct.OutputSchema()
		safe = ct.ConcurrencySafe()
	}
	r.register(info.Name, "builtin", "", schema, safe)
}

// registerMCP 登记一个 MCP 桥接工具。schema 是最终生效的输出契约
// （协议自描述优先，B1 存档补位，均无为 nil）。MCP 工具一律按并发
// 不安全处理（fail-closed：pack 侧尚无声明通道，声明机制留给后续版本）。
func (r *toolRegistry) registerMCP(name, server string, schema json.RawMessage) {
	r.register(name, "mcp", server, schema, false)
}

// lookup 返回工具的契约条目（未登记 → ok=false）。
func (r *toolRegistry) lookup(name string) (toolContract, bool) {
	c, ok := r.entries[name]
	return c, ok
}

// originOf 返回工具的来源（"builtin"/"mcp"；未登记返回空串）。
func (r *toolRegistry) originOf(name string) string {
	if c, ok := r.entries[name]; ok {
		return c.origin
	}
	return ""
}

// outputSchemaOf 返回工具的输出 schema（未登记/无 schema 返回 nil）。
func (r *toolRegistry) outputSchemaOf(name string) json.RawMessage {
	if c, ok := r.entries[name]; ok {
		return c.outputSchema
	}
	return nil
}

// concurrencySafeOf 返回工具是否并发安全（未登记 = false，fail-closed）。
func (r *toolRegistry) concurrencySafeOf(name string) bool {
	if c, ok := r.entries[name]; ok {
		return c.concurrencySafe
	}
	return false
}

// snapshot 返回注册表全量清单（task.context.tools 快照的数据源）。
func (r *toolRegistry) snapshot() []orchapp.ToolSnapshotEntry {
	out := make([]orchapp.ToolSnapshotEntry, 0, len(r.order))
	for _, name := range r.order {
		e := r.entries[name]
		out = append(out, orchapp.ToolSnapshotEntry{
			Name: e.name, Origin: e.origin, Server: e.server,
			HasOutputSchema: len(e.outputSchema) > 0,
			ConcurrencySafe: e.concurrencySafe,
		})
	}
	return out
}

const ToolSearchThreshold = 24

type toolPack struct {
	static []tool.BaseTool

	dynamic []tool.BaseTool

	searchMW adk.ChatModelAgentMiddleware
}

func (e *EinoEngine) buildToolPack(ctx context.Context, task orchapp.Task, agent orchapp.Agent,
	core []tool.BaseTool, memberTools []tool.BaseTool, bridge *mcpBridge) toolPack {
	pack := toolPack{static: core}
	pack.static = append(pack.static, memberTools...)
	mcpTools := []tool.BaseTool(nil)
	if bridge != nil {
		mcpTools = bridge.tools
	}
	if len(pack.static)+len(mcpTools) <= ToolSearchThreshold {
		pack.static = append(pack.static, mcpTools...)
		return pack
	}

	mw, err := toolsearch.New(ctx, &toolsearch.Config{DynamicTools: mcpTools})
	if err != nil {
		pack.static = append(pack.static, mcpTools...)
		return pack
	}
	pack.dynamic = mcpTools
	pack.searchMW = mw
	return pack
}

func unknownToolsHandler(known func() []string) func(ctx context.Context, name, input string) (string, error) {
	return func(_ context.Context, name, _ string) (string, error) {
		return fmt.Sprintf(
			"Tool %q does not exist. Available tools: %s. Pick from the list; for external capabilities check the MCP server configuration.",
			name, strings.Join(known(), "、")), nil
	}
}

func toolNamesOf(ctx context.Context, tools []tool.BaseTool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if info, err := t.Info(ctx); err == nil {
			names = append(names, info.Name)
		}
	}
	return names
}
