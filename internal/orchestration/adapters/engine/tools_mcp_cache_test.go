package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/egressx"
	orchapp "orkome/internal/orchestration/app"
)

func TestMCPCatalogCacheHit(t *testing.T) {
	var initializes int32
	s := server.NewMCPServer("fake-plugin", "1.0")
	s.AddTool(mcp.NewTool("fakeplug_probe",
		mcp.WithDescription("cache probe tool"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("pong"), nil
	})
	handler := server.NewStreamableHTTPServer(s)

	counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			b := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(b)
			r.Body = io.NopCloser(bytes.NewReader(b))
			var body struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(b, &body) == nil && body.Method == "initialize" {
				atomic.AddInt32(&initializes, 1)
			}
		}
		handler.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(counting)
	t.Cleanup(srv.Close)

	guard, err := egressx.NewGuard([]string{"127.0.0.1/8"})
	require.NoError(t, err)
	e := &EinoEngine{
		MCPResolver: &fakeMCPResolver{servers: []orchapp.ResolvedMCPServer{
			{Name: "fakeplug", Transport: "http", URL: srv.URL + "/mcp"},
		}},
		Egress: guard,
	}

	br1 := e.buildMCPTools(context.Background(), orchapp.Task{WorkspaceID: "ws"}, orchapp.Agent{ID: "a"})
	require.NotNil(t, br1)
	require.Len(t, br1.tools, 1, "first install produces tools")
	assert.Equal(t, mcpStatusOK, br1.snapshot[0].Status)

	br2 := e.buildMCPTools(context.Background(), orchapp.Task{WorkspaceID: "ws"}, orchapp.Agent{ID: "a"})
	require.NotNil(t, br2)
	require.Len(t, br2.tools, 1, "cache hits still produce tools")
	assert.Equal(t, mcpStatusOK, br2.snapshot[0].Status)

	assert.EqualValues(t, 1, atomic.LoadInt32(&initializes),
		"two assemblies trigger Initialize only once (catalog cache effective)")

	e2 := &EinoEngine{
		MCPResolver: &fakeMCPResolver{servers: []orchapp.ResolvedMCPServer{
			{Name: "fakeplug", Transport: "http", URL: srv.URL + "/mcp?other=1"},
		}},
		Egress: guard,
	}
	br3 := e2.buildMCPTools(context.Background(), orchapp.Task{WorkspaceID: "ws"}, orchapp.Agent{ID: "a"})
	require.NotNil(t, br3)
	require.Len(t, br3.tools, 1)
	assert.EqualValues(t, 2, atomic.LoadInt32(&initializes), "config change (new key) re-handshakes")
}

// newWideTestMCPServer 起一台带 n 个工具的内存 MCP 服务器（截断用例的远端）。
func newWideTestMCPServer(t *testing.T, n int) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("wide-plugin", "1.0")
	for i := 0; i < n; i++ {
		s.AddTool(mcp.NewTool(fmt.Sprintf("wide_tool_%02d", i)), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("pong"), nil
		})
	}
	srv := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(srv.Close)
	return srv
}

// P0-5：装满 32 即截断必须可见化——snapshot note 写 "truncated at 32/N
// tools"（N=远端 ListTools 总数），ToolCount 停在 32。
func TestBuildMCPTools_TruncationNote(t *testing.T) {
	wide := newWideTestMCPServer(t, 40)
	guard, err := egressx.NewGuard([]string{"127.0.0.1/8"})
	require.NoError(t, err)
	e := &EinoEngine{
		MCPResolver: &fakeMCPResolver{servers: []orchapp.ResolvedMCPServer{
			{Name: "wide", Transport: "streamable_http", URL: wide.URL + "/mcp"},
		}},
		Egress: guard,
	}
	br := e.buildMCPTools(context.Background(), orchapp.Task{WorkspaceID: "ws"}, orchapp.Agent{ID: "a"})
	require.NotNil(t, br)
	require.Len(t, br.tools, mcpToolsPerServerMax, "运行期桥接照常停在 32")
	require.Len(t, br.snapshot, 1)
	entry := br.snapshot[0]
	assert.Equal(t, mcpStatusOK, entry.Status)
	assert.Equal(t, mcpToolsPerServerMax, entry.ToolCount)
	assert.Equal(t, "truncated at 32/40 tools", entry.Note)
}

// 恰好 32 个工具：不截断、无 note（截断只在装满后仍有剩余时发生）。
func TestBuildMCPTools_ExactCapNoTruncationNote(t *testing.T) {
	wide := newWideTestMCPServer(t, mcpToolsPerServerMax)
	guard, err := egressx.NewGuard([]string{"127.0.0.1/8"})
	require.NoError(t, err)
	e := &EinoEngine{
		MCPResolver: &fakeMCPResolver{servers: []orchapp.ResolvedMCPServer{
			{Name: "exact", Transport: "streamable_http", URL: wide.URL + "/mcp"},
		}},
		Egress: guard,
	}
	br := e.buildMCPTools(context.Background(), orchapp.Task{WorkspaceID: "ws"}, orchapp.Agent{ID: "a"})
	require.NotNil(t, br)
	require.Len(t, br.tools, mcpToolsPerServerMax)
	require.Len(t, br.snapshot, 1)
	assert.Empty(t, br.snapshot[0].Note)
}

// P0-5：probe 带总数变体——tools 截到 32、total 返回远端全量数；
// 旧签名 ProbeServerTools 只是丢弃 total 的薄包装。
func TestProbeServerToolsWithTotal(t *testing.T) {
	wide := newWideTestMCPServer(t, 40)
	guard, err := egressx.NewGuard([]string{"127.0.0.1/8"})
	require.NoError(t, err)
	srv := orchapp.ResolvedMCPServer{Name: "wide", Transport: "streamable_http", URL: wide.URL + "/mcp"}

	tools, total, err := ProbeServerToolsWithTotal(context.Background(), srv, guard, nil)
	require.NoError(t, err)
	assert.Len(t, tools, mcpToolsPerServerMax)
	assert.Equal(t, 40, total, "截断前总数 = 远端 ListTools 结果长度")

	legacy, err := ProbeServerTools(context.Background(), srv, guard, nil)
	require.NoError(t, err)
	assert.Len(t, legacy, mcpToolsPerServerMax, "旧签名行为不变")

	small := newWideTestMCPServer(t, 3)
	tools, total, err = ProbeServerToolsWithTotal(context.Background(),
		orchapp.ResolvedMCPServer{Name: "small", Transport: "streamable_http", URL: small.URL + "/mcp"}, guard, nil)
	require.NoError(t, err)
	assert.Len(t, tools, 3)
	assert.Equal(t, 3, total, "未截断时 total 即条数")
}
