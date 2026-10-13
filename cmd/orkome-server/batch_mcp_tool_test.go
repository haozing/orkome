package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/haozing/ploykit/platform/egressx"

	orchengine "orkome/internal/orchestration/adapters/engine"
	orchapp "orkome/internal/orchestration/app"
)

// —— mcp_tool BatchProcessor 测试：工具定位失败路径（fail-fast 带原因）。
// 成功路径需要真实 MCP 服务器，属 D2 联调/集成测试口径；单测覆盖参数
// 校验与定位失败语义（设计 §7 验收：工具不在白名单 → 明确错误不沉默）。

func TestMCPToolBatchProcessor_Kind(t *testing.T) {
	p := &mcpToolBatchProcessor{}
	assert.Equal(t, "mcp_tool", p.Kind())
}

func TestMCPToolBatchProcessor_ParamsRequired(t *testing.T) {
	p := &mcpToolBatchProcessor{}
	ctx := context.Background()

	t.Run("missing context ids", func(t *testing.T) {
		_, err := p.Process(ctx, map[string]any{"tool": "x"}, map[string]any{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "workspace_id/work_item_id/agent_id required")
	})
	t.Run("missing params.tool", func(t *testing.T) {
		_, err := p.Process(ctx, map[string]any{
			"workspace_id": "ws", "work_item_id": "wi", "agent_id": "ag",
		}, map[string]any{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "params.tool required")
	})
}

func TestMCPToolBatchProcessor_ToolNotFound(t *testing.T) {
	var listCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[
			{"name":"agentblog_ocr","description":"ocr","inputSchema":{"type":"object"}}]}}`))
	}))
	defer srv.Close()

	e := &orchengine.EinoEngine{
		MCPResolver: resolverOf([]orchapp.ResolvedMCPServer{
			{Name: "agentblog", Transport: "http", URL: srv.URL},
		}),
	}
	// 引擎直连测试服务器需放开 egress 门（默认门禁 loopback）。
	guard, err := egressx.NewGuard([]string{"127.0.0.0/8"})
	require.NoError(t, err)
	e.Egress = guard

	p := &mcpToolBatchProcessor{engine: e}
	params := map[string]any{"workspace_id": "ws-1", "work_item_id": "wi-1", "agent_id": "ag-1"}

	t.Run("tool not in allowlist → failed with reason", func(t *testing.T) {
		_, err := p.Process(context.Background(), withTool(params, "nope_ocr"), map[string]any{"image": "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `not found in resolved tool surface`)
	})
	t.Run("nil bridge (no live servers) → failed with reason", func(t *testing.T) {
		empty := &orchengine.EinoEngine{
			MCPResolver: resolverOf(nil),
		}
		pe := &mcpToolBatchProcessor{engine: empty}
		_, err := pe.Process(context.Background(), withTool(params, "agentblog_ocr"), map[string]any{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bridge not built")
	})
	_ = listCalls
}

func TestInvokeMCPTool_NilBridge(t *testing.T) {
	_, err := orchengine.InvokeMCPTool(context.Background(), nil, "any", `{}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bridge not built")
}

func withTool(params map[string]any, tool string) map[string]any {
	out := map[string]any{}
	for k, v := range params {
		out[k] = v
	}
	out["tool"] = tool
	return out
}

func resolverOf(servers []orchapp.ResolvedMCPServer) orchapp.MCPResolver {
	return &staticResolver{servers: servers}
}

type staticResolver struct{ servers []orchapp.ResolvedMCPServer }

func (s *staticResolver) ResolveMCP(context.Context, string, string) ([]orchapp.ResolvedMCPServer, error) {
	return s.servers, nil
}

var _ = fmt.Sprintf
var _ = json.Marshal
