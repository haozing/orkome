package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// fakeBlogJobServer 起一个带三件套形状工具的 fake MCP server：
// tools 参数每个元素是 (工具名, 是否自描述 outputSchema)。
func fakeBlogJobServer(t *testing.T, tools [][2]string) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("fakeblog", "1.0")
	rawSchema := json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string"}},"required":["job_id"]}`)
	for _, spec := range tools {
		name, selfDescribed := spec[0], spec[1] == "true"
		opts := []mcp.ToolOption{mcp.WithDescription("fake tool")}
		if selfDescribed {
			opts = append(opts, mcp.WithRawOutputSchema(rawSchema))
		}
		s.AddTool(mcp.NewTool(name, opts...),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("ok"), nil
			})
	}
	srv := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(srv.Close)
	return srv
}

// writeV1Pack 写一个 pack/v1 pack（可带 mcp.tools 声明段与 ui.slots 段）。
func writeV1Pack(t *testing.T, mcpURL, toolsDecl string) string {
	t.Helper()
	dir := t.TempDir()
	pack := "apiVersion: pack/v1\nname: fakeblog\nversion: 0.2.0\n" +
		"mcp:\n  - name: fakeblog\n    transport: http\n    url: " + mcpURL + "\n" + toolsDecl +
		"ui:\n  entry:\n    label: 内容工厂\n    urlTemplate: https://fakeblog.example?ws={workspaceId}&t={uiToken}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(pack), 0o644))
	return dir
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

func TestParsePackVersionGate(t *testing.T) {
	t.Run("pack/v1 accepted with tools decl and slots section", func(t *testing.T) {
		fake := fakeBlogMCPServer(t, "fakeblog_get_post")
		decl := "    tools:\n" +
			"      - name: fakeblog_get_post\n" +
			"        output: |\n" +
			"          {\"type\":\"object\",\"properties\":{\"ok\":{\"type\":\"boolean\"}},\"required\":[\"ok\"]}\n"
		dir := writeV1Pack(t, fake.URL, decl)
		p, err := parsePack(dir)
		require.NoError(t, err)
		assert.Equal(t, "pack/v1", p.APIVersion)
		require.Len(t, p.MCP[0].Tools, 1)
		assert.Equal(t, "fakeblog_get_post", p.MCP[0].Tools[0].Name)
		decl2, ok := p.MCP[0].declaredOutput("fakeblog_get_post")
		require.True(t, ok)
		assert.Equal(t, "object", decl2["type"])
	})
	t.Run("slots section parsed with catalog-frozen identifier", func(t *testing.T) {
		// B1 时代 slots 段静默容忍；C2 起 v1 真解析——合法标识照常通过且 entry 不受影响。
		fake := fakeBlogMCPServer(t, "fakeblog_get_post")
		dir := writeV1Pack(t, fake.URL, "")
		raw := "apiVersion: pack/v1\nname: fakeblog\nversion: 0.2.0\n" +
			"mcp:\n  - name: fakeblog\n    transport: http\n    url: " + fake.URL + "\n" +
			"ui:\n  entry:\n    label: x\n    urlTemplate: https://x.example\n" +
			"  slots:\n    - slot: workitem.detail.tab\n      label: 文章预览\n      urlTemplate: https://x.example/slot?item={workItemId}\n      height: 480\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(raw), 0o644))
		p, err := parsePack(dir)
		require.NoError(t, err)
		require.NotNil(t, p.UI.Entry, "entry 仍正常解析")
		require.Len(t, p.UI.Slots, 1)
		assert.Equal(t, "workitem.detail.tab", p.UI.Slots[0].Slot)
		assert.Equal(t, 480, p.UI.Slots[0].Height)
	})
	t.Run("slots with unknown slot identifier rejected (catalog frozen)", func(t *testing.T) {
		fake := fakeBlogMCPServer(t, "fakeblog_get_post")
		dir := writeV1Pack(t, fake.URL, "")
		raw := "apiVersion: pack/v1\nname: fakeblog\nversion: 0.2.0\n" +
			"mcp:\n  - name: fakeblog\n    transport: http\n    url: " + fake.URL + "\n" +
			"ui:\n  entry:\n    label: x\n    urlTemplate: https://x.example\n" +
			"  slots:\n    - slot: workspace.dashboard.card\n      label: 卡片\n      urlTemplate: https://x.example/card\n      height: 300\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(raw), 0o644))
		_, err := parsePack(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown slot")
		assert.Contains(t, err.Error(), "workitem.detail.tab")
	})
	t.Run("v0 pack with slots section still silently ignored", func(t *testing.T) {
		// v0 行为不变锚点：slots 是 v1 语义，v0 pack 出现该段静默忽略、不落库。
		fake := fakeBlogMCPServer(t, "fakeblog_get_post")
		dir := writePack(t, fake.URL)
		raw, err := os.ReadFile(filepath.Join(dir, "pack.yaml"))
		require.NoError(t, err)
		raw = append(raw, []byte("ui:\n  entry:\n    label: x\n    urlTemplate: https://x.example\n  slots:\n    - slot: workitem.detail.tab\n      label: 文章预览\n      urlTemplate: https://x.example/slot\n      height: 480\n")...)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "pack.yaml"), raw, 0o644))
		p, err := parsePack(dir)
		require.NoError(t, err)
		assert.Empty(t, p.UI.Slots, "v0 静默忽略 slots 段")
	})
	t.Run("v1 slot missing height rejected", func(t *testing.T) {
		fake := fakeBlogMCPServer(t, "fakeblog_get_post")
		dir := writeV1Pack(t, fake.URL, "")
		raw := "apiVersion: pack/v1\nname: fakeblog\nversion: 0.2.0\n" +
			"mcp:\n  - name: fakeblog\n    transport: http\n    url: " + fake.URL + "\n" +
			"ui:\n  entry:\n    label: x\n    urlTemplate: https://x.example\n" +
			"  slots:\n    - slot: workitem.detail.tab\n      label: 文章预览\n      urlTemplate: https://x.example/slot\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(raw), 0o644))
		_, err := parsePack(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "height")
	})
	t.Run("unknown apiVersion rejected", func(t *testing.T) {
		fake := fakeBlogMCPServer(t, "fakeblog_get_post")
		dir := writeV1Pack(t, fake.URL, "")
		raw := "apiVersion: pack/v2\nname: fakeblog\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(raw), 0o644))
		_, err := parsePack(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pack/v0 or pack/v1")
	})
	t.Run("v1 tool decl without prefix rejected", func(t *testing.T) {
		fake := fakeBlogMCPServer(t, "fakeblog_get_post")
		decl := "    tools:\n      - name: get_post\n        output: |\n          {\"type\":\"object\"}\n"
		dir := writeV1Pack(t, fake.URL, decl)
		_, err := parsePack(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must start with")
	})
	t.Run("v1 tool decl with non-object output rejected", func(t *testing.T) {
		fake := fakeBlogMCPServer(t, "fakeblog_get_post")
		decl := "    tools:\n      - name: fakeblog_get_post\n        output: |\n          [1,2,3]\n"
		dir := writeV1Pack(t, fake.URL, decl)
		_, err := parsePack(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "JSON Schema object")
	})
}

func TestInstallGates(t *testing.T) {
	creds := map[string]string{"FAKEBLOG_MCP_TOKEN": "tok"}
	gate := func() gateOptions { return gateOptions{prefixCheck: true, enforceContracts: true} }

	t.Run("v1 legal pack: trio complete, all tools have contracts, protocol self-description wins", func(t *testing.T) {
		fake := fakeBlogJobServer(t, [][2]string{
			{"fakeblog_submit_generation", "true"},
			{"fakeblog_job_status", "true"},
			{"fakeblog_cancel_job", "true"},
		})
		m := packMCP{Name: "fakeblog", Transport: "http", URL: fake.URL, CredentialRef: "FAKEBLOG_MCP_TOKEN"}
		archive, err := handshakeAndGate(context.Background(), m, creds, gate())
		require.NoError(t, err)
		require.Len(t, archive, 3)
		assert.Equal(t, "protocol", archive[0].Source)
		assert.Equal(t, "object", archive[0].OutputSchema["type"])
	})

	t.Run("v1 legal pack: un-self-described tool passes via pack.yaml decl (pack_yaml source)", func(t *testing.T) {
		fake := fakeBlogJobServer(t, [][2]string{
			{"fakeblog_submit_generation", "true"},
			{"fakeblog_job_status", "false"},
			{"fakeblog_cancel_job", "false"},
		})
		m := packMCP{Name: "fakeblog", Transport: "http", URL: fake.URL, CredentialRef: "FAKEBLOG_MCP_TOKEN",
			Tools: []packMCPTool{
				{Name: "fakeblog_job_status", Output: map[string]any{"type": "object"}},
				{Name: "fakeblog_cancel_job", Output: map[string]any{"type": "object"}},
			}}
		archive, err := handshakeAndGate(context.Background(), m, creds, gate())
		require.NoError(t, err)
		byName := map[string]archivedToolSchema{}
		for _, a := range archive {
			byName[a.ToolName] = a
		}
		assert.Equal(t, "protocol", byName["fakeblog_submit_generation"].Source)
		assert.Equal(t, "pack_yaml", byName["fakeblog_job_status"].Source)
		assert.Equal(t, "pack_yaml", byName["fakeblog_cancel_job"].Source)
	})

	t.Run("v1 pack with submit but missing job_status fails", func(t *testing.T) {
		fake := fakeBlogJobServer(t, [][2]string{
			{"fakeblog_submit_generation", "true"},
			{"fakeblog_cancel_job", "true"},
		})
		m := packMCP{Name: "fakeblog", Transport: "http", URL: fake.URL}
		_, err := handshakeAndGate(context.Background(), m, creds, gate())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "job-handle")
		assert.Contains(t, err.Error(), "fakeblog_job_status")
	})

	t.Run("v1 tool with neither self-description nor declaration fails", func(t *testing.T) {
		fake := fakeBlogJobServer(t, [][2]string{
			{"fakeblog_ping", "false"},
		})
		m := packMCP{Name: "fakeblog", Transport: "http", URL: fake.URL}
		_, err := handshakeAndGate(context.Background(), m, creds, gate())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "output contract")
		assert.Contains(t, err.Error(), "fakeblog_ping")
	})

	t.Run("v1 pack.yaml declares a tool the server does not expose fails", func(t *testing.T) {
		fake := fakeBlogJobServer(t, [][2]string{
			{"fakeblog_ping", "true"},
		})
		m := packMCP{Name: "fakeblog", Transport: "http", URL: fake.URL,
			Tools: []packMCPTool{{Name: "fakeblog_pong", Output: map[string]any{"type": "object"}}}}
		_, err := handshakeAndGate(context.Background(), m, creds, gate())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not expose")
	})

	t.Run("v0 semantics unchanged: prefix-only gate passes schema-less tools without trio", func(t *testing.T) {
		// v0 兼容路径：enforceContracts=false，无 output/三件套检查（既有行为回归）。
		fake := fakeBlogJobServer(t, [][2]string{
			{"fakeblog_submit_generation", "false"},
		})
		m := packMCP{Name: "fakeblog", Transport: "http", URL: fake.URL}
		archive, err := handshakeAndGate(context.Background(), m, creds, gateOptions{prefixCheck: true})
		require.NoError(t, err)
		assert.Empty(t, archive, "v0 路径不产出存档")
	})

	t.Run("v1 gates survive --no-prefix-check (only prefix exempted)", func(t *testing.T) {
		// 前缀违规 + 新闸门同时违规：豁免前缀后 output 闸门仍然拦截。
		fake := fakeBlogJobServer(t, [][2]string{
			{"submit_generation", "false"},
		})
		m := packMCP{Name: "fakeblog", Transport: "http", URL: fake.URL}
		_, err := handshakeAndGate(context.Background(), m, creds, gateOptions{enforceContracts: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "output contract", "前缀豁免但 output 闸门仍硬卡")
	})
}

// TestInstallArchivesSchemas 验证 install 全链路：v1 pack 闸门通过后，
// schema 存档经 PUT /api/pack-tool-schemas/<pack> 真实落库（fake API server 捕获）。
func TestInstallArchivesSchemas(t *testing.T) {
	fake := fakeBlogJobServer(t, [][2]string{
		{"fakeblog_submit_generation", "true"},
		{"fakeblog_job_status", "false"},
		{"fakeblog_cancel_job", "false"},
	})
	decl := "    tools:\n" +
		"      - name: fakeblog_job_status\n" +
		"        output: |\n" +
		"          {\"type\":\"object\",\"required\":[\"job_id\",\"state\"],\"properties\":{\"job_id\":{\"type\":\"string\"},\"state\":{\"type\":\"string\"}}}\n" +
		"      - name: fakeblog_cancel_job\n" +
		"        output: |\n" +
		"          {\"type\":\"object\",\"properties\":{\"cancelled\":{\"type\":\"boolean\"}}}\n"
	dir := writeV1Pack(t, fake.URL, decl)
	p, err := parsePack(dir)
	require.NoError(t, err)

	var mu sync.Mutex
	var archived []archivedToolSchema
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.HasPrefix(r.PathValue("pack"), "") && strings.Contains(r.URL.Path, "/api/pack-tool-schemas/") {
			var body struct {
				Items []archivedToolSchema `json:"items"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			archived = body.Items
			mu.Unlock()
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(api.Close)

	c := &apiClient{Base: api.URL, WS: "ws1", PAT: "tok", HC: &http.Client{Timeout: 5e9}}
	require.NoError(t, install(context.Background(), c, p, map[string]string{}, true, ""))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, archived, 3, "三件套全部存档")
	byName := map[string]archivedToolSchema{}
	for _, a := range archived {
		byName[a.ToolName] = a
	}
	assert.Equal(t, "protocol", byName["fakeblog_submit_generation"].Source)
	assert.Equal(t, "pack_yaml", byName["fakeblog_job_status"].Source)
	assert.Equal(t, "object", byName["fakeblog_job_status"].OutputSchema["type"])
}

// TestInstallV0NoArchive 验证 v0 回归：install 不触发存档 PUT（行为不变）。
func TestInstallV0NoArchive(t *testing.T) {
	fake := fakeBlogMCPServer(t, "fakeblog_get_post")
	dir := writePack(t, fake.URL)
	p, err := parsePack(dir)
	require.NoError(t, err)

	putSeen := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.Contains(r.URL.Path, "/api/pack-tool-schemas/") {
			putSeen = true
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(api.Close)

	c := &apiClient{Base: api.URL, WS: "ws1", PAT: "tok", HC: &http.Client{Timeout: 5e9}}
	require.NoError(t, install(context.Background(), c, p, map[string]string{"FAKEBLOG_MCP_TOKEN": "tok"}, true, ""))
	assert.False(t, putSeen, "v0 安装不写 schema 存档")
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

	require.NoError(t, install(context.Background(), c, p, creds, true, ""), "first install")
	require.NoError(t, install(context.Background(), c, p, creds, true, ""), "second install (idempotent)")

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
