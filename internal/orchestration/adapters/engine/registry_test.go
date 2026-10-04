package engine

import (
	"context"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

type stubTool struct{ name string }

func (s *stubTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name}, nil
}

func (s *stubTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return "ok", nil
}

type badInfoTool struct{}

func (badInfoTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return nil, fmt.Errorf("no info")
}

func (badInfoTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return "", nil
}

func makeTools(n int, prefix string) []tool.BaseTool {
	out := make([]tool.BaseTool, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, &stubTool{name: fmt.Sprintf("%s_%02d", prefix, i)})
	}
	return out
}

func TestUnknownToolsHandler_CorrectiveHint(t *testing.T) {
	h := unknownToolsHandler(func() []string {
		return toolNamesOf(context.Background(), makeTools(2, "core"))
	})
	out, err := h(context.Background(), "delete_everything", `{}`)
	require.NoError(t, err, "no hard error")
	assert.Contains(t, out, "does not exist")
	assert.Contains(t, out, "core_00")
	assert.Contains(t, out, "core_01")
	assert.Contains(t, out, "MCP")
}

func TestBuildToolPack_WithinThreshold(t *testing.T) {
	e := &EinoEngine{}
	core := makeTools(3, "core")
	mcp := makeTools(2, "mcp")
	bridge := &mcpBridge{tools: mcp}
	pack := e.buildToolPack(context.Background(), orchapp.Task{}, orchapp.Agent{}, core, nil, bridge)
	assert.Nil(t, pack.searchMW)
	assert.Empty(t, pack.dynamic)
	require.Len(t, pack.static, 5, "core+MCP all static")
}

func TestBuildToolPack_OverThreshold(t *testing.T) {
	e := &EinoEngine{}
	core := makeTools(4, "core")
	mcp := makeTools(ToolSearchThreshold, "mcp")
	bridge := &mcpBridge{tools: mcp}
	pack := e.buildToolPack(context.Background(), orchapp.Task{}, orchapp.Agent{}, core, nil, bridge)
	require.NotNil(t, pack.searchMW, "toolsearch assembled above the threshold (internal archive 09 §7.3)")
	require.Len(t, pack.dynamic, ToolSearchThreshold)
	assert.Len(t, pack.static, 4, "static pool keeps only core surface")
}

func TestBuildToolPack_SearchFallback(t *testing.T) {
	e := &EinoEngine{}
	core := makeTools(4, "core")
	mcp := append(makeTools(ToolSearchThreshold-3, "mcp"), badInfoTool{})
	bridge := &mcpBridge{tools: mcp}
	pack := e.buildToolPack(context.Background(), orchapp.Task{}, orchapp.Agent{}, core, nil, bridge)
	assert.Nil(t, pack.searchMW, "toolsearch construction failure → fallback")
	assert.Len(t, pack.static, len(core)+len(mcp), "rollback keeps tools")
}

func TestBuildToolPack_MemberToolsStatic(t *testing.T) {
	e := &EinoEngine{}
	core := makeTools(2, "core")
	members := makeTools(2, "member")
	bridge := &mcpBridge{tools: makeTools(ToolSearchThreshold, "mcp")}
	pack := e.buildToolPack(context.Background(), orchapp.Task{}, orchapp.Agent{}, core, members, bridge)
	require.NotNil(t, pack.searchMW)
	for _, want := range []string{"member_00", "member_01", "core_00"} {
		found := false
		for _, t2 := range pack.static {
			if info, err := t2.Info(context.Background()); err == nil && info.Name == want {
				found = true
			}
		}
		assert.True(t, found, "%s should be in the static pool", want)
	}
}

func newInProcessMCPClient(t *testing.T, n int, prefix string) *mcpclient.Client {
	t.Helper()
	srv := mcpserver.NewMCPServer("registry-test", "1.0")
	for i := 0; i < n; i++ {
		tool := mcpgo.NewTool(fmt.Sprintf("%s_%02d", prefix, i),
			mcpgo.WithDescription(fmt.Sprintf("%s tool #%d", prefix, i)))
		srv.AddTool(tool, func(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			return &mcpgo.CallToolResult{
				Content: []mcpgo.Content{&mcpgo.TextContent{Text: "ok"}},
			}, nil
		})
	}
	cli, err := mcpclient.NewInProcessClient(srv)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

func TestToolSearchThresholdGuardrail(t *testing.T) {
	assert.Equal(t, 24, ToolSearchThreshold, "internal archive 09 §7.3: toolsearch assembled above the tool threshold (default 24)")
}

func TestToolSearchThirtyRealMCPTools(t *testing.T) {
	cli := newInProcessMCPClient(t, 30, "real")
	ctx := context.Background()

	tools, err := mcpConnect(ctx, cli, nil)
	require.NoError(t, err, "real MCP handshake + inventory (in-process transport)")
	require.Len(t, tools, 30, "all 30 tools arrive (within the 32-per-server cap)")

	e := &EinoEngine{}
	core := makeTools(5, "core")
	bridge := &mcpBridge{tools: tools}
	pack := e.buildToolPack(ctx, orchapp.Task{}, orchapp.Agent{}, core, nil, bridge)

	require.NotNil(t, pack.searchMW, "35>24: adk toolsearch constructed successfully (internal archive 09 §7.3 search pool)")
	require.Len(t, pack.dynamic, 30, "MCP all 30 tools go to the search pool")
	assert.Len(t, pack.static, 5, "static pool keeps only core surface")

	names := toolNamesOf(ctx, pack.dynamic)
	assert.Contains(t, names, "real_00")
	assert.Contains(t, names, "real_29")
}

func TestToolSearchConstructionFailureFallback(t *testing.T) {
	cli := newInProcessMCPClient(t, 29, "real")
	ctx := context.Background()

	tools, err := mcpConnect(ctx, cli, nil)
	require.NoError(t, err)
	require.Len(t, tools, 29)
	pool := append(append([]tool.BaseTool(nil), tools...), badInfoTool{})

	e := &EinoEngine{}
	core := makeTools(3, "core")
	pack := e.buildToolPack(ctx, orchapp.Task{}, orchapp.Agent{}, core, nil, &mcpBridge{tools: pool})

	assert.Nil(t, pack.searchMW, "construction failure (Info unavailable) → no search pool assembled")
	assert.Empty(t, pack.dynamic)
	assert.Len(t, pack.static, len(core)+len(pool), "fallback to full static: all 30+3 kept (anti-hallucination must not lose tools either)")
}

func TestToolSearchDuplicateNamesConstructionFail(t *testing.T) {
	pool := append(makeTools(ToolSearchThreshold, "dup"), &stubTool{name: "dup_00"})
	e := &EinoEngine{}
	core := makeTools(1, "core")
	pack := e.buildToolPack(context.Background(), orchapp.Task{}, orchapp.Agent{}, core, nil, &mcpBridge{tools: pool})
	assert.Nil(t, pack.searchMW, "duplicate names → toolsearch construction failure fallback")
	assert.Len(t, pack.static, len(core)+len(pool), "rollback keeps tools")
}
