package http

// MCP 路由目录与闸位测试（P1-3 agent 级 probe 路由 / P2-D7 ws 级写面
// admin 闸）。cmd/orkome-server/router_contract_test.go 是全局路由契约的
// 统一收口（主会话维护）；本文件只锁 orchestration 域 MCP 端点的
// 「存在性 + 权限闸位」，端点内的 wire 语义见 handlers_mcp_test.go。

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

// TestMCPRoutesCatalog：MCP 两级端点必须全部挂载——请求打到不存在的
// 路径返回 404，挂载成功的端点（哪怕业务层报错）不返回 404。
func TestMCPRoutesCatalog(t *testing.T) {
	r, repo, svc := newMCPRouterRole("owner")
	// probe 端点的业务 404（查无此行）会与"路由丢失 404"混淆——给目标行。
	repo.wsMCPRows = []orchapp.WorkspaceMCP{{ID: "s1", Name: "w", URL: "https://x.example"}}
	repo.agentMCPRows = []orchapp.AgentMCP{{ID: "s1", AgentID: "ag-1", Name: "a", URL: "https://x.example"}}
	svc.ToolProbe = func(_ context.Context, _ orchapp.ResolvedMCPServer, _ string) ([]orchapp.ProbeTool, error) {
		return []orchapp.ProbeTool{{Name: "t"}}, nil
	}

	// (method, path, body)——body 为空串表示无请求体。
	catalog := []struct{ method, path, body string }{
		{"GET", "/api/workspaces/ws-1/mcp-servers", ""},
		{"POST", "/api/workspaces/ws-1/mcp-servers", `{"name":"c1","url":"https://x.example"}`},
		{"PUT", "/api/workspaces/ws-1/mcp-servers/s1", `{"name":"u1","url":"https://x.example"}`},
		{"DELETE", "/api/workspaces/ws-1/mcp-servers/s1", ""},
		{"POST", "/api/workspaces/ws-1/mcp-servers/s1/probe-tools", ""},
		{"GET", "/api/agents/ag-1/mcp-servers", ""},
		{"GET", "/api/agents/ag-1/mcp-servers?resolved=1", ""},
		{"POST", "/api/agents/ag-1/mcp-servers", `{"name":"a1","url":"https://x.example"}`},
		{"PUT", "/api/agents/ag-1/mcp-servers/s1", `{"name":"a1","url":"https://x.example"}`},
		{"DELETE", "/api/agents/ag-1/mcp-servers/s1", ""},
		// P1-3 新增：agent 级 probe。
		{"POST", "/api/agents/ag-1/mcp-servers/s1/probe-tools", ""},
	}
	for _, e := range catalog {
		rec := mcpReq(r, e.method, e.path, e.body)
		assert.NotEqual(t, http.StatusNotFound, rec.Code,
			"%s %s 必须已挂载（404=路由丢失）", e.method, e.path)
	}

	// 反向哨兵：未挂载的路径确实是 404（防止上面的断言恒真）。
	rec := mcpReq(r, "POST", "/api/workspaces/ws-1/mcp-servers/s1/probe-toolz", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = mcpReq(r, "PATCH", "/api/workspaces/ws-1/mcp-servers/s1", `{}`)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "同路径异动词 → 405（Go 1.22 mux），佐证精确挂载")
}

// TestMCPRoutes_WSWriteAdminGate：P2-D7——ws 级 mcp-servers 写面（POST/
// PUT/DELETE）owner/admin 放行、普通成员 403；读面 GET 与 probe 保持成员闸。
func TestMCPRoutes_WSWriteAdminGate(t *testing.T) {
	memberRouter, _, memberSvc := newMCPRouterRole("member")
	memberSvc.ToolProbe = func(_ context.Context, _ orchapp.ResolvedMCPServer, _ string) ([]orchapp.ProbeTool, error) {
		return []orchapp.ProbeTool{{Name: "t"}}, nil
	}

	t.Run("member is rejected on writes", func(t *testing.T) {
		rec := mcpReq(memberRouter, "POST", "/api/workspaces/ws-1/mcp-servers", `{"name":"n","url":"https://x.example"}`)
		assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "admin")
		rec = mcpReq(memberRouter, "PUT", "/api/workspaces/ws-1/mcp-servers/s1", `{"name":"n","url":"https://x.example"}`)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		rec = mcpReq(memberRouter, "DELETE", "/api/workspaces/ws-1/mcp-servers/s1", "")
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("member keeps read and probe", func(t *testing.T) {
		rec := mcpReq(memberRouter, "GET", "/api/workspaces/ws-1/mcp-servers", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		// probe 需要目标行存在：memberPathMW 通过后 handler 才查行。
		memberRouter2, repo, svc2 := newMCPRouterRole("member")
		repo.wsMCPRows = []orchapp.WorkspaceMCP{{ID: "s1", Name: "w", URL: "https://x.example"}}
		svc2.ToolProbe = func(_ context.Context, _ orchapp.ResolvedMCPServer, _ string) ([]orchapp.ProbeTool, error) {
			return []orchapp.ProbeTool{{Name: "t"}}, nil
		}
		rec = mcpReq(memberRouter2, "POST", "/api/workspaces/ws-1/mcp-servers/s1/probe-tools", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("owner passes writes", func(t *testing.T) {
		ownerRouter, _, _ := newMCPRouterRole("owner")
		rec := mcpReq(ownerRouter, "POST", "/api/workspaces/ws-1/mcp-servers", `{"name":"n","url":"https://x.example"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		rec = mcpReq(ownerRouter, "PUT", "/api/workspaces/ws-1/mcp-servers/s1", `{"name":"n","url":"https://x.example"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		rec = mcpReq(ownerRouter, "DELETE", "/api/workspaces/ws-1/mcp-servers/s1", "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("admin role passes writes too", func(t *testing.T) {
		adminRouter, _, _ := newMCPRouterRole("admin")
		rec := mcpReq(adminRouter, "POST", "/api/workspaces/ws-1/mcp-servers", `{"name":"n","url":"https://x.example"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	})

	t.Run("agent level writes stay member-gated (P2-D7 只收 ws 级)", func(t *testing.T) {
		rec := mcpReq(memberRouter, "POST", "/api/agents/ag-1/mcp-servers", `{"name":"n","url":"https://x.example"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	})
}
