package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

type fakeMCPResolver struct {
	servers []orchapp.ResolvedMCPServer
	err     error
}

func (f *fakeMCPResolver) ResolveMCP(context.Context, string, string) ([]orchapp.ResolvedMCPServer, error) {
	return f.servers, f.err
}

func TestMCPHeadersOf(t *testing.T) {
	h := mcpHeadersOf(map[string]any{
		"headers": map[string]any{
			"Authorization": "Bearer sk-secret",
			"X-Org":         "acme",
			"X-Bad":         42,
		},
		"api_key": "sk-should-not-leak",
	})
	assert.Equal(t, map[string]string{"Authorization": "Bearer sk-secret", "X-Org": "acme"}, h)

	assert.Nil(t, mcpHeadersOf(nil))
	assert.Nil(t, mcpHeadersOf(map[string]any{"api_key": "x"}))
	assert.Nil(t, mcpHeadersOf(map[string]any{"headers": "not-a-map"}))
}

func TestBuildMCPTools_SSRFGate(t *testing.T) {
	e := &EinoEngine{MCPResolver: &fakeMCPResolver{servers: []orchapp.ResolvedMCPServer{
		{Name: "loopback", Transport: "http", URL: "https://127.0.0.1/mcp"},
		{Name: "plain-http", Transport: "http", URL: "http://mcp.example.com/mcp"},
	}}}
	br := e.buildMCPTools(context.Background(),
		orchapp.Task{WorkspaceID: "ws-1"}, orchapp.Agent{ID: "a-1"})
	require.NotNil(t, br)
	assert.Empty(t, br.tools)
	require.Len(t, br.snapshot, 2)
	assert.Equal(t, "loopback", br.snapshot[0].Name)
	assert.Equal(t, mcpStatusSSRF, br.snapshot[0].Status)
	assert.Equal(t, "plain-http", br.snapshot[1].Name)
	assert.Equal(t, mcpStatusSSRF, br.snapshot[1].Status)

	snap := orchapp.MCPContextSnapshot(br.snapshot)
	require.NotNil(t, snap)
	assert.NotContains(t, snap, "credentials")
}

// Framework egressx (ploykit) is fail-closed on unresolvable https hosts; the
// old local gate passed them best-effort and the failure surfaced at connect
// time. Both of these test hosts are unresolvable domains, so they are now
// classified at the gate.
func TestBuildMCPTools_UnsupportedTransport(t *testing.T) {
	e := &EinoEngine{MCPResolver: &fakeMCPResolver{servers: []orchapp.ResolvedMCPServer{
		{Name: "weird", Transport: "stdio", URL: "https://mcp.example.com/x"},
	}}}
	br := e.buildMCPTools(context.Background(),
		orchapp.Task{WorkspaceID: "ws-1"}, orchapp.Agent{ID: "a-1"})
	require.NotNil(t, br)
	assert.Empty(t, br.tools)
	require.Len(t, br.snapshot, 1)
	assert.Equal(t, mcpStatusSSRF, br.snapshot[0].Status)
}

func TestBuildMCPTools_PublicHostUnresolvableFailClosed(t *testing.T) {
	e := &EinoEngine{MCPResolver: &fakeMCPResolver{servers: []orchapp.ResolvedMCPServer{
		{Name: "remote", Transport: "http", URL: "https://mcp.invalid.example/mcp"},
	}}}
	br := e.buildMCPTools(context.Background(),
		orchapp.Task{WorkspaceID: "ws-1"}, orchapp.Agent{ID: "a-1"})
	require.NotNil(t, br)
	assert.Empty(t, br.tools)
	require.Len(t, br.snapshot, 1)
	assert.Equal(t, mcpStatusSSRF, br.snapshot[0].Status, "unresolvable https host is rejected at the gate (fail-closed)")
}

func TestBuildMCPTools_NilResolver(t *testing.T) {
	e := &EinoEngine{}
	assert.Nil(t, e.buildMCPTools(context.Background(), orchapp.Task{}, orchapp.Agent{}))
	e2 := &EinoEngine{MCPResolver: &fakeMCPResolver{}}
	assert.Nil(t, e2.buildMCPTools(context.Background(), orchapp.Task{}, orchapp.Agent{}))
}

func TestNewMCPClient_TransportMap(t *testing.T) {
	srv := orchapp.ResolvedMCPServer{URL: "https://mcp.example.com/mcp"}
	c, err := newMCPClient(srv, nil)
	require.NoError(t, err)
	require.NotNil(t, c)

	c2, err := newMCPClient(orchapp.ResolvedMCPServer{URL: "https://mcp.example.com/sse", Transport: "sse"},
		map[string]string{"Authorization": "Bearer x"})
	require.NoError(t, err)
	require.NotNil(t, c2)

	_, err = newMCPClient(orchapp.ResolvedMCPServer{URL: "https://x/y", Transport: "carrier-pigeon"}, nil)
	assert.Error(t, err)
}

func TestMCPContextSnapshot(t *testing.T) {
	assert.Nil(t, orchapp.MCPContextSnapshot(nil))
	snap := orchapp.MCPContextSnapshot([]orchapp.MCPSnapshotEntry{{
		Name: "srv", Transport: "http", URL: "https://mcp.example.com",
		Status: "ok", ToolCount: 3,
	}})
	rows, ok := snap["mcp_servers"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, rows, 1)
	assert.Equal(t, "srv", rows[0]["name"])
	assert.Equal(t, 3, rows[0]["tool_count"])
	assert.Equal(t, "ok", rows[0]["status"])
	for k := range rows[0] {
		assert.NotContains(t, k, "credential", "snapshot face has no credential keys (I-E10)")
	}
}
