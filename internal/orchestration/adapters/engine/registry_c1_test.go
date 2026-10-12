package engine

// C1 测试：统一 Tool 注册表 + middleware 管线 + ConcurrencySafe fail-closed
// + task.context.tools 新旧读路径。任务书：docs/pack-rollout-plan-2026-10-11.md §3 C1。
//
// 覆盖：
//  ① 输出校验 middleware 对内置工具生效（合法透传 / 违规替换 / 豁免 /
//     未登记放行 / 工具失败透传）＋ 全链路（engine.run 经 adk 真挂载）；
//  ② ConcurrencySafe fail-closed：未声明不并行 / 显式 true 才并行
//     （注册表层单测 + engine 全链路同消息双 tool_calls 的重叠度断言）；
//  ③ context.tools 新旧读路径双测（新键 tools / 旧键 mcp_servers 回落 /
//     双键并存新键优先 / 引擎回写细化态）；
//  ④ B1 存档 schema 补位（协议自描述优先级）。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/egressx"
	"orkome/internal/contract/modelmeta"
	orchapp "orkome/internal/orchestration/app"
)

// ---- ① 输出校验 middleware ------------------------------------------------

// stubBuiltin 是走注册表 builtin 路径的测试工具（可控 schema/输出）。
type stubBuiltin struct {
	name   string
	out    string
	execEr error
}

func (s *stubBuiltin) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name}, nil
}

func (s *stubBuiltin) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return s.out, s.execEr
}

func TestContractMiddleware_BuiltinOutputValidated(t *testing.T) {
	reg := newToolRegistry()
	// schema={"type":"string"} 的工具：字符串输出透传
	strTool := &stubBuiltin{name: "b_string", out: "plain text, not json"}
	reg.register("b_string", "builtin", "", json.RawMessage(`{"type":"string"}`), false)
	// schema=object 的工具：输出非 JSON → 违规替换
	objTool := &stubBuiltin{name: "b_object", out: "not a json object"}
	reg.register("b_object", "builtin", "", json.RawMessage(`{"type":"object","required":["job_id"]}`), false)
	// 豁免工具（nil schema）：原样放行
	exemptTool := &stubBuiltin{name: "b_exempt", out: "free text"}
	reg.register("b_exempt", "builtin", "", nil, false)

	audit := &fakeAuditHandler{}
	mw := newToolContractMiddleware(reg, nil, wrapAudit(t, audit))

	run := func(t *testing.T, name string, target tool.BaseTool) string {
		t.Helper()
		ep := adk.InvokableToolCallEndpoint(func(_ context.Context, args string, _ ...tool.Option) (string, error) {
			return target.(tool.InvokableTool).InvokableRun(context.Background(), args)
		})
		wrapped, err := mw.WrapInvokableToolCall(context.Background(), ep, &adk.ToolContext{Name: name, CallID: "c"})
		require.NoError(t, err)
		out, err := wrapped(context.Background(), `{}`)
		require.NoError(t, err)
		return out
	}

	// string 契约 + 文本输出 → 原样透传
	assert.Equal(t, "plain text, not json", run(t, "b_string", strTool))
	// object 契约 + 非 JSON 输出 → 标准违规串（A1 golden 格式）
	out := run(t, "b_object", objTool)
	assert.Contains(t, out, "tool output contract violation")
	assert.Contains(t, out, `"code":"TOOL_OUTPUT_CONTRACT_VIOLATION"`)
	assert.Contains(t, out, `"tool":"b_object"`)
	assert.NotContains(t, out, `"raw"`, "原文不进模型可见串")
	// 豁免（nil schema）→ 原样放行
	assert.Equal(t, "free text", run(t, "b_exempt", exemptTool))

	// 审计：仅违规一条，含原文全文与工具名
	require.Len(t, audit.recs, 1)
	assert.Equal(t, "b_object", audit.recs[0]["tool"])
	assert.Equal(t, "not a json object", audit.recs[0]["raw_output"])
	assert.Equal(t, "builtin tool output contract violation", audit.recs[0]["msg"])
}

func TestContractMiddleware_ObjectOutputAgainstStringSchema(t *testing.T) {
	// 契约说 string、工具改版后返回了 JSON object → 契约拦下（A1 红利
	// 对内置工具闭环的实义：改结构必须同步改契约）。
	reg := newToolRegistry()
	reg.register("b_string", "builtin", "", json.RawMessage(`{"type":"string"}`), false)
	mw := newToolContractMiddleware(reg, nil, nil)
	ep := adk.InvokableToolCallEndpoint(func(context.Context, string, ...tool.Option) (string, error) {
		return `{"structured": true}`, nil
	})
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), ep, &adk.ToolContext{Name: "b_string"})
	require.NoError(t, err)
	out, err := wrapped(context.Background(), `{}`)
	require.NoError(t, err)
	assert.Contains(t, out, "TOOL_OUTPUT_CONTRACT_VIOLATION")
	assert.Contains(t, out, "schema mismatch")
}

func TestContractMiddleware_UnregisteredAndErrorsPassThrough(t *testing.T) {
	reg := newToolRegistry()
	mw := newToolContractMiddleware(reg, nil, nil)

	// 未登记名（成员工具/未知名）：不校验，原样透传（fail-closed 只管并发，
	// 校验只在注册表有条目时生效）。
	ep := adk.InvokableToolCallEndpoint(func(context.Context, string, ...tool.Option) (string, error) {
		return "member reply, untouched", nil
	})
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), ep, &adk.ToolContext{Name: "consult_scout"})
	require.NoError(t, err)
	out, err := wrapped(context.Background(), `{}`)
	require.NoError(t, err)
	assert.Equal(t, "member reply, untouched", out)

	// MCP origin：middleware 跳过校验（A1 装饰器管），透传包装串
	reg.register("plug_ping", "mcp", "plug", json.RawMessage(`{"type":"object"}`), false)
	wrapped, err = mw.WrapInvokableToolCall(context.Background(), ep, &adk.ToolContext{Name: "plug_ping"})
	require.NoError(t, err)
	out, err = wrapped(context.Background(), `{}`)
	require.NoError(t, err)
	assert.Equal(t, "member reply, untouched", out)

	// 工具失败（error / eino Interrupt）原样透传，不走校验
	reg.register("b_fail", "builtin", "", json.RawMessage(`{"type":"object"}`), false)
	epErr := adk.InvokableToolCallEndpoint(func(context.Context, string, ...tool.Option) (string, error) {
		return "", fmt.Errorf("interrupted")
	})
	wrapped, err = mw.WrapInvokableToolCall(context.Background(), epErr, &adk.ToolContext{Name: "b_fail"})
	require.NoError(t, err)
	out, err = wrapped(context.Background(), `{}`)
	require.EqualError(t, err, "interrupted")
	assert.Empty(t, out)
}

// wrapAudit 把 fakeAuditHandler 包成 *slog.Logger（本文件内多处复用）。
func wrapAudit(t *testing.T, h *fakeAuditHandler) *slog.Logger {
	t.Helper()
	return slog.New(h)
}

// ---- ② ConcurrencySafe fail-closed ------------------------------------------

func TestRegistry_FailClosedDefaults(t *testing.T) {
	reg := newToolRegistry()
	// 内置真工具分型：只读 true，其余（副作用/中断型）一律 false
	ctx := context.Background()
	reg.registerBuiltin(ctx, &approvalTool{})
	reg.registerBuiltin(ctx, &searchWorkspaceTool{})
	reg.registerBuiltin(ctx, &getWorkItemTool{})
	reg.registerBuiltin(ctx, &postCommentTool{})
	reg.registerBuiltin(ctx, &updateWorkItemStatusTool{})
	reg.registerBuiltin(ctx, &checkinWakeupTool{})
	reg.registerBuiltin(ctx, &dispatchToMemberTool{})
	reg.registerBuiltin(ctx, &createChildWorkItemTool{})
	reg.registerBuiltin(ctx, &recordEvaluationTool{})

	assert.True(t, reg.concurrencySafeOf("search_workspace"), "只读检索显式可并行")
	assert.True(t, reg.concurrencySafeOf("get_work_item"), "只读工单显式可并行")
	for _, unsafe := range []string{
		"request_approval", "post_comment", "update_work_item_status",
		"checkin_wakeup", "dispatch_to_member", "create_child_work_item", "record_evaluation",
	} {
		assert.False(t, reg.concurrencySafeOf(unsafe), "%s 未声明可并行（fail-closed）", unsafe)
	}
	// 未登记与 MCP 一律不安全（fail-closed 语义可测）
	assert.False(t, reg.concurrencySafeOf("consult_scout"), "未登记工具按不安全处理")
	reg.registerMCP("plug_x", "plug", nil)
	assert.False(t, reg.concurrencySafeOf("plug_x"), "MCP 工具未显式声明一律不并行")

	// origin 判定：middleware 据此决定校验范围
	assert.Equal(t, "builtin", reg.originOf("post_comment"))
	assert.Equal(t, "mcp", reg.originOf("plug_x"))
	assert.Equal(t, "", reg.originOf("nope"))

	// 快照形状：全量清单含 origin/server/契约/并发分型
	snap := reg.snapshot()
	names := map[string]orchapp.ToolSnapshotEntry{}
	for _, e := range snap {
		names[e.Name] = e
	}
	require.Contains(t, names, "get_work_item")
	assert.Equal(t, "builtin", names["get_work_item"].Origin)
	assert.True(t, names["get_work_item"].ConcurrencySafe)
	assert.True(t, names["get_work_item"].HasOutputSchema)
	assert.True(t, names["request_approval"].HasOutputSchema == false, "审批工具显式豁免输出契约")
	assert.Equal(t, "plug", names["plug_x"].Server)
	assert.False(t, names["plug_x"].HasOutputSchema)
}

// overlapProbe 记录工具执行的重叠度（同消息多 tool_calls 并行度断言用）。
type overlapProbe struct {
	inside int64
	maxIn  int64
}

func (p *overlapProbe) enter() {
	cur := atomic.AddInt64(&p.inside, 1)
	for {
		m := atomic.LoadInt64(&p.maxIn)
		if cur <= m || atomic.CompareAndSwapInt64(&p.maxIn, m, cur) {
			break
		}
	}
	time.Sleep(80 * time.Millisecond) // 拉长执行窗口，重叠可观测
	atomic.AddInt64(&p.inside, -1)
}

func TestContractMiddleware_UnsafeToolsSerialize(t *testing.T) {
	reg := newToolRegistry()
	reg.register("t_unsafe", "builtin", "", nil, false)
	reg.register("t_safe", "builtin", "", nil, true)

	mw := newToolContractMiddleware(reg, nil, nil)
	wrap := func(name string, probe *overlapProbe) func() {
		ep := adk.InvokableToolCallEndpoint(func(context.Context, string, ...tool.Option) (string, error) {
			probe.enter()
			return "ok", nil
		})
		wrapped, err := mw.WrapInvokableToolCall(context.Background(), ep, &adk.ToolContext{Name: name})
		require.NoError(t, err)
		return func() { _, _ = wrapped(context.Background(), `{}`) }
	}

	// 两个不安全工具并发调用：串行闸保证绝不重叠
	p := &overlapProbe{}
	a, b := wrap("t_unsafe", p), wrap("t_unsafe", p)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a() }()
	go func() { defer wg.Done(); b() }()
	wg.Wait()
	assert.Equal(t, int64(1), atomic.LoadInt64(&p.maxIn), "未声明 ConcurrencySafe=true 的工具不并行（fail-closed）")

	// 两个安全工具并发调用：放行并行（显式 true 才并行）
	p2 := &overlapProbe{}
	c, d := wrap("t_safe", p2), wrap("t_safe", p2)
	wg.Add(2)
	go func() { defer wg.Done(); c() }()
	go func() { defer wg.Done(); d() }()
	wg.Wait()
	assert.Equal(t, int64(2), atomic.LoadInt64(&p2.maxIn), "显式声明 ConcurrencySafe=true 的只读工具可并行")

	// 未登记工具并发调用：fail-closed 串行
	p3 := &overlapProbe{}
	e, f2 := wrap("member_tool", p3), wrap("member_tool", p3)
	wg.Add(2)
	go func() { defer wg.Done(); e() }()
	go func() { defer wg.Done(); f2() }()
	wg.Wait()
	assert.Equal(t, int64(1), atomic.LoadInt64(&p3.maxIn), "未登记（成员工具等）按不安全处理")
}

// ---- ③ context.tools 新旧读路径双测 ------------------------------------------

func TestToolContextSnapshot_Shape(t *testing.T) {
	// 双键形状：mcp_servers 旧形状原样 + tools 新清单
	patch := orchapp.ToolContextSnapshot(
		[]orchapp.MCPSnapshotEntry{{Name: "plug", Transport: "http", URL: "https://p.example", Status: "ok", ToolCount: 2}},
		[]orchapp.ToolSnapshotEntry{
			{Name: "request_approval", Origin: "builtin", HasOutputSchema: false, ConcurrencySafe: false},
			{Name: "get_work_item", Origin: "builtin", HasOutputSchema: true, ConcurrencySafe: true},
			{Name: "plug_ping", Origin: "mcp", Server: "plug", HasOutputSchema: true, ConcurrencySafe: false},
		})
	require.NotNil(t, patch)
	require.Contains(t, patch, "mcp_servers")
	require.Contains(t, patch, "tools")

	rows, ok := patch["tools"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, rows, 3)
	approval := rows[0]
	assert.Equal(t, "request_approval", approval["name"])
	assert.Equal(t, "builtin", approval["origin"])
	assert.Equal(t, false, approval["has_output_schema"])
	assert.Equal(t, false, approval["concurrency_safe"])
	mcpRow := rows[2]
	assert.Equal(t, "plug_ping", mcpRow["name"])
	assert.Equal(t, "plug", mcpRow["server"])
	assert.Equal(t, true, mcpRow["has_output_schema"])

	// 快照面无凭证键（I-E10 回归锚定沿用）
	srv := patch["mcp_servers"].([]map[string]any)[0]
	for k := range srv {
		assert.NotContains(t, k, "credential")
	}
}

func TestTaskContextTools_NewKeyPreferred(t *testing.T) {
	// 新键存在 → 读新键（含双键并存时新键优先）
	ctx := map[string]any{
		"tools": []any{
			map[string]any{"name": "get_work_item", "origin": "builtin", "has_output_schema": true, "concurrency_safe": true},
			map[string]any{"name": "plug_ping", "origin": "mcp", "server": "plug", "has_output_schema": false, "concurrency_safe": false},
		},
		"mcp_servers": []any{map[string]any{"name": "plug", "status": "resolved"}},
	}
	tools := orchapp.TaskContextTools(ctx)
	require.Len(t, tools, 2)
	assert.Equal(t, "get_work_item", tools[0].Name)
	assert.True(t, tools[0].ConcurrencySafe)
	assert.Equal(t, "plug", tools[1].Server)
}

func TestTaskContextTools_LegacyFallback(t *testing.T) {
	// 旧键回落：无 tools 键时从 mcp_servers 合成服务器级聚合条目
	// （老任务快照兼容读——新代码不产出这种形状，读得懂即可）
	ctx := map[string]any{
		"mcp_servers": []any{
			map[string]any{"name": "plug", "status": "resolved", "tool_count": float64(3)},
		},
	}
	tools := orchapp.TaskContextTools(ctx)
	require.Len(t, tools, 1)
	assert.Equal(t, "plug", tools[0].Name)
	assert.Equal(t, "mcp", tools[0].Origin)
	assert.False(t, tools[0].ConcurrencySafe, "旧快照无声明 → 按不安全读")

	// 两键皆无 → nil（判定点：无工具快照）
	assert.Nil(t, orchapp.TaskContextTools(map[string]any{}))
	assert.Nil(t, orchapp.TaskContextTools(nil))
}

// fakeContextPatcher 捕获引擎回写的 patch（③ 引擎回写路径 + 全链路用）。
type fakeContextPatcher struct {
	mu     sync.Mutex
	patches []map[string]any
}

func (f *fakeContextPatcher) PatchTaskContext(_ context.Context, _ string, patch map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patches = append(f.patches, patch)
	return nil
}

func TestEngineRun_PatchesToolSnapshot(t *testing.T) {
	// 全链路：engine.run 组装注册表后回写 task.context（tools 全量清单 +
	// mcp_servers 细化态取代 dispatcher 的 resolved 占位）。
	RegisterFakeProviderToolsForTest(func(_ []*schema.Message) (FakeStep, schema.TokenUsage, error) {
		return FakeStep{Text: "done."}, schema.TokenUsage{}, nil
	})
	patcher := &fakeContextPatcher{}
	e := &EinoEngine{
		TaskContextPatcher: patcher,
		WorkspaceSearch: func(context.Context, string, string) ([]orchapp.SearchHit, error) {
			return nil, nil
		},
	}
	task := orchapp.Task{ID: "t-snap", WorkspaceID: "ws-1", Status: "running",
		Context: map[string]any{"trigger": "test"}}
	_, err := e.Execute(context.Background(), task,
		orchapp.Agent{ID: "agent-snap-00000000", RuntimeConfig: map[string]any{"provider": "fake", "model": "f"}}, "")
	require.NoError(t, err)

	require.Len(t, patcher.patches, 1)
	patch := patcher.patches[0]
	require.Contains(t, patch, "tools", "注册表全量清单进新键")
	tools := orchapp.TaskContextTools(patch)
	require.NotEmpty(t, tools)
	names := map[string]orchapp.ToolSnapshotEntry{}
	for _, t2 := range tools {
		names[t2.Name] = t2
	}
	require.Contains(t, names, "request_approval")
	assert.Equal(t, "builtin", names["request_approval"].Origin)
	assert.False(t, names["request_approval"].HasOutputSchema, "审批工具显式豁免")
	require.Contains(t, names, "search_workspace")
	assert.True(t, names["search_workspace"].ConcurrencySafe, "只读工具显式声明可并行")

	// 无 MCP 服务器时不写 mcp_servers（dispatcher 的 resolved 占位保留）
	_, hasMCP := patch["mcp_servers"]
	assert.False(t, hasMCP, "零 MCP 服务器不覆盖旧键占位")
}

// ---- ④ B1 存档补位 -----------------------------------------------------------

// fakePackToolSchemas 是 PackToolSchemas 端口的测试桩。
type fakePackToolSchemas struct{ items []orchapp.PackToolSchema }

func (f *fakePackToolSchemas) ListPackToolSchemas(context.Context, string) ([]orchapp.PackToolSchema, error) {
	return f.items, nil
}

func TestArchivedToolSchemas_BackfillPriority(t *testing.T) {
	// fake MCP server：archived_tool 未自描述，protocol_tool 自描述。
	s := mcpserver.NewMCPServer("c1-fixture", "1.0")
	s.AddTool(mcpgo.NewTool("plug_archived_tool", mcpgo.WithDescription("no self schema")),
		func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			return mcpgo.NewToolResultText(`{"ok":true}`), nil
		})
	s.AddTool(mcpgo.NewTool("plug_protocol_tool",
		mcpgo.WithDescription("self described"),
		mcpgo.WithRawOutputSchema(json.RawMessage(`{"type":"object","required":["self"]}`)),
	), func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(`{"self":1}`), nil
		})
	ts := mcpServerForTest(t, s)

	guard, err := egressx.NewGuard([]string{"127.0.0.1/8"})
	require.NoError(t, err)
	e := &EinoEngine{
		MCPResolver: &fakeMCPResolver{servers: []orchapp.ResolvedMCPServer{
			{Name: "plug", Transport: "http", URL: ts.URL + "/mcp"},
		}},
		Egress: guard,
		PackToolSchemas: &fakePackToolSchemas{items: []orchapp.PackToolSchema{{
			WorkspaceID: "ws", Pack: "plug", Server: "plug",
			ToolName: "plug_archived_tool",
			OutputSchema: map[string]any{"type": "object", "required": []any{"ok"}},
			Source: "pack_yaml",
		}}},
	}
	task := orchapp.Task{ID: "t-b1", WorkspaceID: "ws", Status: "running",
		Context: map[string]any{"trigger": "test"}}
	bridge := e.buildMCPTools(context.Background(), task, orchapp.Agent{ID: "a"})
	require.NotNil(t, bridge)
	require.Len(t, bridge.tools, 2)

	// 优先级：协议自描述优先，未自描述用 B1 存档补位
	assert.NotNil(t, bridge.schemaOf("plug_protocol_tool"), "协议自描述 schema 到手")
	require.NotNil(t, bridge.schemaOf("plug_archived_tool"), "B1 存档为未自描述工具补契约")
	require.JSONEq(t, `{"type":"object","required":["ok"]}`, string(bridge.schemaOf("plug_archived_tool")))

	// 存档契约生效：合法输出放行
	run := func(name string) string {
		for _, bt := range bridge.tools {
			info, ierr := bt.Info(context.Background())
			require.NoError(t, ierr)
			if info.Name != name {
				continue
			}
			out, rerr := bt.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
			require.NoError(t, rerr)
			return out
		}
		t.Fatalf("tool %s not found", name)
		return ""
	}
	assert.Contains(t, run("plug_archived_tool"), `{\"ok\":true}`, "存档契约下的合法输出原样透传（eino-ext 包装串内为转义 JSON）")
	assert.Contains(t, run("plug_protocol_tool"), `{\"self\":1}`)
}

func TestArchivedToolSchemas_NilPort(t *testing.T) {
	// 端口未接线 → 空映射，不阻塞（回落协议自描述/探测）
	e := &EinoEngine{}
	assert.Nil(t, e.archivedToolSchemas(context.Background(), orchapp.Task{WorkspaceID: "ws"}))
}

// ---- ⑤ 全链路（engine.run 经 adk 真挂载 middleware）--------------------------

// multiCallFakeModel 让一条 assistant 消息携带两个 tool_calls（既有
// fakeModel 一次只发一个）——同消息并行度（ConcurrencySafe fail-closed）
// 的端到端观测入口。
type multiCallFakeModel struct {
	mu    sync.Mutex
	step  int
	calls []schema.ToolCall
}

func (f *multiCallFakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.step++
	if f.step == 1 {
		return &schema.Message{Role: schema.Assistant, ToolCalls: f.calls,
			ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{}}}, nil
	}
	return &schema.Message{Role: schema.Assistant, Content: "all tools answered.",
		ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{}}}, nil
}

func (f *multiCallFakeModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := f.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// registerMultiCallModel 注册一次发多条 tool_calls 的 fake provider。
func registerMultiCallModel(calls []schema.ToolCall) {
	modelmeta.Register("fake", func(context.Context, modelmeta.RuntimeConfig, string) (model.BaseChatModel, error) {
		return &multiCallFakeModel{calls: calls}, nil
	})
}

// countingPort 是带重叠度观测的端口桩（串行/并行判定的执行侧探针）。
type countingPort struct {
	probe *overlapProbe
	calls int64
}

func (c *countingPort) UpdateStatusByAgent(context.Context, string, string, string, string) error {
	atomic.AddInt64(&c.calls, 1)
	c.probe.enter()
	return nil
}

func (c *countingPort) Search(context.Context, string, string) ([]orchapp.SearchHit, error) {
	atomic.AddInt64(&c.calls, 1)
	c.probe.enter()
	return nil, nil
}

func TestEngineRun_UnsafeToolsSerialized_EndToEnd(t *testing.T) {
	// 同消息两个 update_work_item_status（未声明可并行）→ 经 adk/eino
	// 真跑：串行闸生效，执行零重叠。
	registerMultiCallModel([]schema.ToolCall{
		{ID: "c1", Function: schema.FunctionCall{Name: "update_work_item_status", Arguments: `{"status":"in_progress"}`}},
		{ID: "c2", Function: schema.FunctionCall{Name: "update_work_item_status", Arguments: `{"status":"in_review"}`}},
	})
	port := &countingPort{probe: &overlapProbe{}}
	e := &EinoEngine{StatusUpdate: port}
	task := orchapp.Task{ID: "t-serial", WorkspaceID: "ws-1", WorkItemID: "wi-1", Status: "running",
		Context: map[string]any{"trigger": "test"}}
	out, err := e.Execute(context.Background(), task,
		orchapp.Agent{ID: "agent-ser-00000000", RuntimeConfig: map[string]any{"provider": "fake", "model": "f"}}, "")
	require.NoError(t, err)
	assert.Equal(t, "completed", out.Outcome)
	assert.Equal(t, int64(2), atomic.LoadInt64(&port.calls), "两次工具调用都执行")
	assert.Equal(t, int64(1), atomic.LoadInt64(&port.probe.maxIn),
		"fail-closed 全链路：未声明 ConcurrencySafe=true 的工具同消息不并行")
}

func TestEngineRun_SafeToolsParallel_EndToEnd(t *testing.T) {
	// 同消息两个 search_workspace（显式声明可并行的只读工具）→ 并行保留。
	registerMultiCallModel([]schema.ToolCall{
		{ID: "c1", Function: schema.FunctionCall{Name: "search_workspace", Arguments: `{"query":"a"}`}},
		{ID: "c2", Function: schema.FunctionCall{Name: "search_workspace", Arguments: `{"query":"b"}`}},
	})
	port := &countingPort{probe: &overlapProbe{}}
	e := &EinoEngine{WorkspaceSearch: port.Search}
	task := orchapp.Task{ID: "t-parallel", WorkspaceID: "ws-1", Status: "running",
		Context: map[string]any{"trigger": "test"}}
	out, err := e.Execute(context.Background(), task,
		orchapp.Agent{ID: "agent-par-00000000", RuntimeConfig: map[string]any{"provider": "fake", "model": "f"}}, "")
	require.NoError(t, err)
	assert.Equal(t, "completed", out.Outcome)
	assert.Equal(t, int64(2), atomic.LoadInt64(&port.calls))
	assert.Equal(t, int64(2), atomic.LoadInt64(&port.probe.maxIn),
		"显式 ConcurrencySafe=true 的只读工具并行放行（eino 原生并行路径）")
}

// violatingValidator 对指定工具全判违规（校验 middleware 生效性探针）。
type violatingValidator struct{ tool string }

func (v violatingValidator) Validate(vi Input) error {
	if vi.ToolName == v.tool {
		return &ViolationError{Reason: "unstructured text"}
	}
	return nil
}

func TestEngineRun_BuiltinOutputContractEnforced_EndToEnd(t *testing.T) {
	// 全链路：内置工具（有 schema 的 update_work_item_status）输出过
	// 注册表校验管线——正常输出透传（行为不变），validator 判违规时
	// 模型看到 A1 标准违规串（A1 红利对内置工具闭环）。
	run := func(t *testing.T, validator OutputValidator) string {
		t.Helper()
		var mu sync.Mutex
		var lastToolResult string
		step := 0
		RegisterFakeProviderToolsForTest(func(hist []*schema.Message) (FakeStep, schema.TokenUsage, error) {
			mu.Lock()
			defer mu.Unlock()
			for _, m := range hist {
				if m.Role == schema.Tool {
					lastToolResult = m.Content
				}
			}
			step++
			if step == 1 {
				return FakeStep{ToolName: "update_work_item_status", ToolArgs: `{"status":"in_progress"}`}, schema.TokenUsage{}, nil
			}
			return FakeStep{Text: "status updated."}, schema.TokenUsage{}, nil
		})
		up := &fakeStatusUpdater{}
		e := &EinoEngine{StatusUpdate: up, OutputValidator: validator}
		task := orchapp.Task{ID: "t-ctr", WorkspaceID: "ws-1", WorkItemID: "wi-1", Status: "running",
			Context: map[string]any{"trigger": "test"}}
		_, err := e.Execute(context.Background(), task,
			orchapp.Agent{ID: "agent-ctr-00000000", RuntimeConfig: map[string]any{"provider": "fake", "model": "f"}}, "")
		require.NoError(t, err)
		mu.Lock()
		defer mu.Unlock()
		return lastToolResult
	}

	// 默认校验器：正常输出过校验，模型看到原文（行为不变）
	out := run(t, nil)
	assert.Contains(t, out, "Work item status changed to in_progress.", "合法输出原样进模型上下文")

	// 违规校验器：标准违规串替换输出（证明 middleware 真挂载在 handler 链上）
	out = run(t, violatingValidator{tool: "update_work_item_status"})
	assert.Contains(t, out, "tool output contract violation: unstructured text")
	assert.Contains(t, out, `"code":"TOOL_OUTPUT_CONTRACT_VIOLATION"`)
	assert.Contains(t, out, `"tool":"update_work_item_status"`)
}
