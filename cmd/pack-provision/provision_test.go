package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writePack(t *testing.T, mcpURL string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644))
	}
	write("pack.yaml", "apiVersion: pack/v0\nname: fakeblog\nversion: 0.1.0\n"+
		"credentials:\n  - key: FAKEBLOG_MCP_TOKEN\n    description: test token\n"+
		"mcp:\n  - name: fakeblog\n    transport: http\n    url: "+mcpURL+"\n    credentialRef: FAKEBLOG_MCP_TOKEN\n"+
		"events:\n  - url: http://127.0.0.1:19998/hook\n    types: [task.completed, task.failed]\n")
	write("agents/writer.yaml", "name: fakeblog_writer\ndescription: fake plugin writer\nmax_concurrent_tasks: 3\nskills: [fakeblog_generation]\n")
	write("skills/generation/SKILL.md", "---\nname: fakeblog_generation\ndescription: generation methodology (use when writing the first draft)\n---\n# Method\nRealistic.")
	return dir
}

func fakeBlogMCPServer(t *testing.T, toolName string) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("fakeblog", "1.0")
	s.AddTool(mcp.NewTool(toolName, mcp.WithDescription("fake tool")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		})
	srv := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(srv.Close)
	return srv
}

func TestParsePack(t *testing.T) {
	fake := fakeBlogMCPServer(t, "fakeblog_get_post")
	dir := writePack(t, fake.URL)
	p, err := parsePack(dir)
	require.NoError(t, err)
	assert.Equal(t, "fakeblog", p.Name)
	require.Len(t, p.MCP, 1)
	assert.Equal(t, "FAKEBLOG_MCP_TOKEN", p.MCP[0].CredentialRef)
	require.Len(t, p.Agents, 1)
	assert.Equal(t, "fakeblog_writer", p.Agents[0].Name)
	require.Len(t, p.Skills, 1)
	assert.Equal(t, "fakeblog_generation", p.Skills[0].Name)
	assert.Contains(t, p.Skills[0].File, "generation methodology")
}

func TestCheckPrefix(t *testing.T) {
	creds := map[string]string{"FAKEBLOG_MCP_TOKEN": "tok"}

	t.Run("compliant prefix passes", func(t *testing.T) {
		fake := fakeBlogMCPServer(t, "fakeblog_get_post")
		m := packMCP{Name: "fakeblog", Transport: "http", URL: fake.URL, CredentialRef: "FAKEBLOG_MCP_TOKEN"}
		require.NoError(t, checkPrefix(context.Background(), m, creds))
	})
	t.Run("violating prefix rejected", func(t *testing.T) {
		fake := fakeBlogMCPServer(t, "get_post")
		m := packMCP{Name: "fakeblog", Transport: "http", URL: fake.URL, CredentialRef: "FAKEBLOG_MCP_TOKEN"}
		err := checkPrefix(context.Background(), m, creds)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prefix violation")
	})
	t.Run("endpoint unreachable rejected", func(t *testing.T) {
		m := packMCP{Name: "fakeblog", Transport: "http", URL: "http://127.0.0.1:1/mcp"}
		require.Error(t, checkPrefix(context.Background(), m, creds))
	})
}

func TestProvisionE2E(t *testing.T) {
	base := os.Getenv("ORCH_E2E_BASE")
	if base == "" {
		t.Skip("ORCH_E2E_BASE not set (e2e gate)")
	}
	ws, pat := os.Getenv("ORCH_E2E_WS"), os.Getenv("ORCH_E2E_PAT")
	require.NotEmpty(t, ws, "ORCH_E2E_WS")
	require.NotEmpty(t, pat, "ORCH_E2E_PAT")

	fake := fakeBlogMCPServer(t, "fakeblog_get_post")
	dir := writePack(t, fake.URL)
	p, err := parsePack(dir)
	require.NoError(t, err)
	c := &apiClient{Base: base, WS: ws, PAT: pat, HC: &http.Client{Timeout: 15e9}}
	creds := map[string]string{"FAKEBLOG_MCP_TOKEN": "tok"}

	require.NoError(t, install(context.Background(), c, p, creds, true), "first install")
	require.NoError(t, install(context.Background(), c, p, creds, true), "second install (idempotent)")

	var ml listMCP
	require.NoError(t, c.do("GET", "/api/workspaces/"+ws+"/mcp-servers", nil, &ml))
	n := 0
	for _, s := range ml.Servers {
		if s.Name == "fakeblog" {
			n++
		}
	}
	assert.EqualValues(t, 1, n, "exactly one MCP registration")
	var al listAgents
	require.NoError(t, c.do("GET", "/api/agents", nil, &al))
	n = 0
	for _, a := range al.Agents {
		if a.Name == "fakeblog_writer" {
			n++
		}
	}
	assert.EqualValues(t, 1, n, "exactly one agent")
	var sl listSkills
	require.NoError(t, c.do("GET", "/api/skills", nil, &sl))
	n = 0
	for _, s := range sl.Skills {
		if s.Name == "fakeblog_generation" {
			n++
		}
	}
	assert.EqualValues(t, 1, n, "exactly one skill")
	var subs listSubs
	require.NoError(t, c.do("GET", "/api/workspaces/"+ws+"/webhooks/subscriptions", nil, &subs))
	n = 0
	for _, s := range subs.Items {
		if s.URL == "http://127.0.0.1:19998/hook" {
			n++
		}
	}
	assert.EqualValues(t, 1, n, "exactly one subscription")

	require.NoError(t, uninstall(context.Background(), c, p), "uninstall")

	require.NoError(t, c.do("GET", "/api/workspaces/"+ws+"/webhooks/subscriptions", nil, &subs))
	for _, s := range subs.Items {
		assert.NotEqual(t, "http://127.0.0.1:19998/hook", s.URL, "subscription deleted")
	}
	require.NoError(t, c.do("GET", "/api/skills", nil, &sl))
	for _, s := range sl.Skills {
		assert.NotEqual(t, "fakeblog_generation", s.Name, "skill deleted")
	}
	require.NoError(t, c.do("GET", "/api/workspaces/"+ws+"/mcp-servers", nil, &ml))
	for _, s := range ml.Servers {
		assert.NotEqual(t, "fakeblog", s.Name, "MCP deleted")
	}
}
