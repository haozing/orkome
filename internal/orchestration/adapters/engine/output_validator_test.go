package engine

// A1 测试：输出契约强校验五态 + 标准违规错误格式 golden + 真跑 fake MCP
// server 的端到端路径。接口冻结文档：docs/interfaces-output-validator-2026-10-11.md。

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/egressx"
	orchapp "orkome/internal/orchestration/app"
)

// mcpServerForTest 把 fake MCP server 挂到本地 httptest 端点（复用既有
// tools_mcp_cache_test.go 的 streamable http 形态）。
func mcpServerForTest(t *testing.T, s *mcpserver.MCPServer) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(mcpserver.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

// ---- 两级实现单测 ---------------------------------------------------------

func TestDefaultValidator_SchemaPass(t *testing.T) {
	// ① 有 schema 且合法 → 通过
	v := NewDefaultValidator()
	schema := json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string"},"n":{"type":"integer"}},"required":["job_id"]}`)
	err := v.Validate(Input{ToolName: "demo", Schema: schema,
		Document: map[string]any{"job_id": "j-1", "n": float64(3)}})
	assert.NoError(t, err)
}

func TestDefaultValidator_SchemaViolation(t *testing.T) {
	// ② 有 schema 但违规 → *ViolationError，reason = schema mismatch
	v := NewDefaultValidator()
	schema := json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string"}},"required":["job_id"]}`)
	err := v.Validate(Input{ToolName: "demo", Schema: schema, Document: map[string]any{"other": true}})
	require.Error(t, err)
	var ve *ViolationError
	require.ErrorAs(t, err, &ve)
	assert.True(t, strings.HasPrefix(ve.Reason, "schema mismatch:"), "reason=%q", ve.Reason)
	assert.Contains(t, ve.Reason, "job_id")
}

func TestDefaultValidator_NoSchemaStructuredPass(t *testing.T) {
	// ③ 无 schema 合法 JSON（object/array）→ 通过
	v := NewDefaultValidator()
	assert.NoError(t, v.Validate(Input{ToolName: "demo", Document: map[string]any{"a": 1.0}}))
	assert.NoError(t, v.Validate(Input{ToolName: "demo", Document: []any{"x"}}))
}

func TestDefaultValidator_NoSchemaPlainTextViolation(t *testing.T) {
	// ④ 无 schema 纯文本（string）→ 违规 unstructured text
	v := NewDefaultValidator()
	err := v.Validate(Input{ToolName: "demo", Document: "pong (not json)"})
	var ve *ViolationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "unstructured text", ve.Reason)
}

func TestDefaultValidator_NoSchemaMalformedViolation(t *testing.T) {
	// ⑤ 无 schema 畸形 JSON：解包阶段（unwrapCallToolResultDocument）判违规
	doc, _, verr := unwrapCallToolResultDocument(`{"content":[{"type":"text","text":"pong (not json)"}]}`)
	require.Nil(t, doc)
	require.NotNil(t, verr)
	assert.Equal(t, "malformed json", verr.Reason)

	// 包装串本身不是 JSON → 违规（拿不到结构化文档）
	_, _, verr = unwrapCallToolResultDocument(`pong`)
	require.NotNil(t, verr)
	assert.Equal(t, "structured content missing", verr.Reason)
}

// ---- 标准违规错误格式 golden -------------------------------------------------

func TestViolationErrorModelFacingTextGolden(t *testing.T) {
	ve := &ViolationError{ToolName: "demo_ping", Reason: "malformed json"}
	got := ve.ModelFacingText("pong (not json)")
	want := "tool output contract violation: malformed json\n" +
		`{"code":"TOOL_OUTPUT_CONTRACT_VIOLATION","tool":"demo_ping","reason":"malformed json","output_excerpt":"pong (not json)"}`
	assert.Equal(t, want, got)
}

func TestViolationErrorExcerptTruncation(t *testing.T) {
	// 原文前 500 字符截断：模型可见串不超过限长，且含前缀。
	long := strings.Repeat("x", 800)
	ve := &ViolationError{ToolName: "t", Reason: "unstructured text"}
	got := ve.ModelFacingText(long)
	require.Contains(t, got, `"output_excerpt":"`)
	// JSON 编码后的 excerpt：原文 500 个 x + 转义引号，即 503 字符内容。
	idx := strings.Index(got, `"output_excerpt":"`)
	rest := got[idx+len(`"output_excerpt":"`):]
	end := strings.Index(rest, `"}`)
	require.Greater(t, end, 0)
	assert.Equal(t, 500, strings.Count(rest[:end], "x"))
	assert.False(t, strings.Contains(rest[end:], "x{800}"))
}

// ---- 装饰器管线（单元级，直接用包装函数） --------------------------------------

type scriptedInvokable struct {
	name string
	out  string
}

func (s *scriptedInvokable) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name}, nil
}

func (s *scriptedInvokable) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return s.out, nil
}

func TestWrapWithValidator_PlainTextReplaced(t *testing.T) {
	// 无 schema 纯文本：装饰后返回标准违规串而非原文
	inner := &scriptedInvokable{name: "demo_ping", out: `{"content":[{"type":"text","text":"pong (not json)"}]}`}
	wrapped := wrapWithValidator(inner, "demo_ping", nil, NewDefaultValidator(), nil)
	out, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
	require.NoError(t, err)
	want := "tool output contract violation: malformed json\n" +
		`{"code":"TOOL_OUTPUT_CONTRACT_VIOLATION","tool":"demo_ping","reason":"malformed json","output_excerpt":"pong (not json)"}`
	assert.Equal(t, want, out)
}

// ---- 校验器边界用例 ----------------------------------------------------------

func TestSchemaValidator_EdgeCases(t *testing.T) {
	v := NewDefaultValidator()

	// integer：整值 number 命中，非整值违规
	intSchema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`)
	assert.NoError(t, v.Validate(Input{Schema: intSchema, Document: map[string]any{"n": float64(7)}}))
	assert.Error(t, v.Validate(Input{Schema: intSchema, Document: map[string]any{"n": 7.5}}))

	// 内部 $ref
	refSchema := json.RawMessage(`{"type":"object","properties":{"job":{"$ref":"#/$defs/job"}},"required":["job"],"$defs":{"job":{"type":"object","properties":{"job_id":{"type":"string"}},"required":["job_id"]}}}`)
	assert.NoError(t, v.Validate(Input{Schema: refSchema, Document: map[string]any{"job": map[string]any{"job_id": "j"}}}))
	assert.Error(t, v.Validate(Input{Schema: refSchema, Document: map[string]any{"job": map[string]any{}}}))

	// 外部 $ref 不支持 → fail-closed 违规
	extRef := json.RawMessage(`{"$ref":"https://example.com/schema.json"}`)
	_, verr := unwrapForValidate(v, extRef)
	require.NotNil(t, verr)
	assert.Contains(t, verr.Reason, "schema mismatch:")

	// 不支持关键字（not）→ fail-closed 违规
	notSchema := json.RawMessage(`{"type":"object","not":{"type":"string"}}`)
	_, verr = unwrapForValidate(v, notSchema)
	require.NotNil(t, verr)
	assert.Contains(t, verr.Reason, "unsupported keyword")

	// enum / const
	enumSchema := json.RawMessage(`{"type":"object","properties":{"state":{"enum":["queued","running","failed"]}}}`)
	assert.NoError(t, v.Validate(Input{Schema: enumSchema, Document: map[string]any{"state": "queued"}}))
	assert.Error(t, v.Validate(Input{Schema: enumSchema, Document: map[string]any{"state": "zombie"}}))

	// additionalProperties:false
	closedSchema := json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`)
	assert.NoError(t, v.Validate(Input{Schema: closedSchema, Document: map[string]any{"a": "x"}}))
	assert.Error(t, v.Validate(Input{Schema: closedSchema, Document: map[string]any{"a": "x", "b": 1.0}}))

	// anyOf / oneOf
	anySchema := json.RawMessage(`{"type":"object","properties":{"v":{"anyOf":[{"type":"string"},{"type":"number"}]}}}`)
	assert.NoError(t, v.Validate(Input{Schema: anySchema, Document: map[string]any{"v": "s"}}))
	assert.NoError(t, v.Validate(Input{Schema: anySchema, Document: map[string]any{"v": 1.5}}))
	assert.Error(t, v.Validate(Input{Schema: anySchema, Document: map[string]any{"v": true}}))
}

func unwrapForValidate(v OutputValidator, schema json.RawMessage) (any, *ViolationError) {
	err := v.Validate(Input{Schema: schema, Document: map[string]any{}})
	if err == nil {
		return nil, nil
	}
	var ve *ViolationError
	if ErrorAsViolation(err, &ve) {
		return nil, ve
	}
	return nil, &ViolationError{Reason: err.Error()}
}

func TestViolation_AuditLoggerNil(t *testing.T) {
	// audit logger 缺省（nil）不 panic、行为一致
	inner := &scriptedInvokable{name: "t", out: `{"content":[{"type":"text","text":"plain"}]}`}
	wrapped := wrapWithValidator(inner, "t", nil, NewDefaultValidator(), nil)
	out, err := wrapped.(tool.InvokableTool).InvokableRun(context.Background(), `{}`)
	require.NoError(t, err)
	assert.Contains(t, out, "TOOL_OUTPUT_CONTRACT_VIOLATION")
}

// ---- 端到端：fake MCP server 真跑 ---------------------------------------------

// fakeAuditHandler 捕获违规审计日志（验证原文全文进审计、模型串不含全文）。
type fakeAuditHandler struct {
	mu   sync.Mutex
	recs []map[string]any
}

func (f *fakeAuditHandler) Handle(_ context.Context, r slog.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := map[string]any{"msg": r.Message}
	r.Attrs(func(a slog.Attr) bool {
		rec[a.Key] = a.Value.Any()
		return true
	})
	f.recs = append(f.recs, rec)
	return nil
}

func (f *fakeAuditHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (f *fakeAuditHandler) WithAttrs(_ []slog.Attr) slog.Handler         { return f }
func (f *fakeAuditHandler) WithGroup(_ string) slog.Handler              { return f }

func TestMCPBridgeOutputContract_EndToEnd(t *testing.T) {
	s := mcpserver.NewMCPServer("contract-fixture", "1.0")

	// structured_ok：自描述 schema，输出合规
	s.AddTool(mcpgo.NewTool("plug_structured_ok",
		mcpgo.WithDescription("structured ok"),
		mcpgo.WithRawOutputSchema(json.RawMessage(
			`{"type":"object","properties":{"job_id":{"type":"string"}},"required":["job_id"]}`)),
	), func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultStructuredOnly(map[string]any{"job_id": "j-1"}), nil
	})
	// structured_bad：自描述 schema，输出违规（缺 required 字段）
	s.AddTool(mcpgo.NewTool("plug_structured_bad",
		mcpgo.WithDescription("structured bad"),
		mcpgo.WithRawOutputSchema(json.RawMessage(
			`{"type":"object","properties":{"job_id":{"type":"string"}},"required":["job_id"]}`)),
	), func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultStructuredOnly(map[string]any{"unexpected": true}), nil
	})
	// json_text：无 schema，text 是合法 JSON object → 探测放行
	s.AddTool(mcpgo.NewTool("plug_json_text",
		mcpgo.WithDescription("json in text"),
	), func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(`{"ok":true}`), nil
	})
	// json_scalar：无 schema，text 是合法 JSON 标量 → 探测判违规（非 object/array）
	s.AddTool(mcpgo.NewTool("plug_json_scalar",
		mcpgo.WithDescription("json scalar"),
	), func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(`"just a string"`), nil
	})
	// plain_text：无 schema，纯文本 → 违规
	s.AddTool(mcpgo.NewTool("plug_plain_text",
		mcpgo.WithDescription("plain text"),
	), func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("pong (not json)"), nil
	})
	// bad_json：无 schema，畸形 JSON → 违规
	s.AddTool(mcpgo.NewTool("plug_bad_json",
		mcpgo.WithDescription("bad json"),
	), func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(`{"ok":`), nil
	})

	ts := mcpServerForTest(t, s)

	guard, err := egressx.NewGuard([]string{"127.0.0.1/8"})
	require.NoError(t, err)
	audit := &fakeAuditHandler{}
	e := &EinoEngine{
		MCPResolver: &fakeMCPResolver{servers: []orchapp.ResolvedMCPServer{
			{Name: "plug", Transport: "http", URL: ts.URL + "/mcp"},
		}},
		Egress:   guard,
		AuditLog: slog.New(audit),
	}

	br := e.buildMCPTools(context.Background(), orchapp.Task{WorkspaceID: "ws"}, orchapp.Agent{ID: "a"})
	require.NotNil(t, br)
	require.Len(t, br.tools, 6)
	assert.Equal(t, mcpStatusOK, br.snapshot[0].Status)

	run := func(t *testing.T, name string) string {
		t.Helper()
		for _, bt := range br.tools {
			info, ierr := bt.Info(context.Background())
			require.NoError(t, ierr)
			if info.Name != name {
				continue
			}
			it, ok := bt.(tool.InvokableTool)
			require.True(t, ok)
			out, rerr := it.InvokableRun(context.Background(), `{}`)
			require.NoError(t, rerr, "违规以标准错误串返回（非 error），模型看到明确失败")
			return out
		}
		t.Fatalf("tool %s not found", name)
		return ""
	}

	// ① 有 schema 合法 → 原样透传包装串
	out := run(t, "plug_structured_ok")
	assert.Contains(t, out, `"structuredContent":{"job_id":"j-1"}`)

	// ② 有 schema 违规 → 标准违规错误
	out = run(t, "plug_structured_bad")
	assert.Contains(t, out, "TOOL_OUTPUT_CONTRACT_VIOLATION")
	assert.Contains(t, out, `"tool":"plug_structured_bad"`)
	assert.Contains(t, out, `"reason":"schema mismatch:`)
	assert.Contains(t, out, "job_id")
	assert.NotContains(t, out, `"unexpected"`, "未验证的结构不得进模型可见串")

	// ③ 无 schema 合法 JSON → 放行
	out = run(t, "plug_json_text")
	assert.Contains(t, out, `"text":"{\"ok\":true}"`)

	// ④ 无 schema 合法 JSON 标量 → 违规 unstructured text
	out = run(t, "plug_json_scalar")
	assert.Contains(t, out, `"reason":"unstructured text"`)
	assert.Contains(t, out, `"output_excerpt":"\"just a string\""`)

	// ⑤ 无 schema 纯文本（非 JSON）→ 违规 malformed json
	out = run(t, "plug_plain_text")
	assert.Contains(t, out, `"reason":"malformed json"`)
	assert.Contains(t, out, `"output_excerpt":"pong (not json)"`)

	// ⑥ 无 schema 畸形 JSON → 违规 malformed json
	out = run(t, "plug_bad_json")
	assert.Contains(t, out, `"reason":"malformed json"`)
	assert.Contains(t, out, `"output_excerpt":"{\"ok\":"`)

	// 审计日志：违规各一条，含包装串全文与工具名；通过的工具不记。
	require.Len(t, audit.recs, 4)
	byTool := map[string]map[string]any{}
	for _, rec := range audit.recs {
		byTool[rec["tool"].(string)] = rec
	}
	assert.Contains(t, byTool, "plug_structured_bad")
	assert.Contains(t, byTool, "plug_json_scalar")
	assert.Contains(t, byTool, "plug_plain_text")
	assert.Contains(t, byTool, "plug_bad_json")
	bad := byTool["plug_structured_bad"]
	assert.Contains(t, bad["raw_output"], `"unexpected":true`, "包装串全文进审计日志")
	assert.Equal(t, "mcp tool output contract violation", bad["msg"])
}
