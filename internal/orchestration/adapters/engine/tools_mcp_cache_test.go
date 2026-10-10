package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
	"github.com/haozing/ploykit/platform/egressx"
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
