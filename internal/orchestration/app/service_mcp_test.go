package app

// W2-3 / FP-07 P1-4：agent 级 MCP 创建必须是 Create 语义（重名 409），
// 不再静默 upsert；Update 的三态 Credentials（nil=保留 / 空 map=清空 /
// 值=覆盖）在服务层原样透传给 repo，不得中途归一化。
// wire 层三态矩阵见 adapters/http/handlers_mcp_test.go；SQL 层见
// adapters/pgrepo/repo_mcp_creds_integration_test.go。

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

type mcpServiceFakeRepo struct {
	Repo
	agents       map[string]Agent
	createCalled bool
	upsertCalled bool
	lastWS       *WorkspaceMCP
	lastWSUpdate *WorkspaceMCP
	lastAgent    *AgentMCP
	lastAgentUpd *AgentMCP
	dupName      error
	wsRows       []WorkspaceMCP
	agentRows    []AgentMCP
}

func (f *mcpServiceFakeRepo) GetAgent(_ context.Context, _, id string) (Agent, bool, error) {
	a, ok := f.agents[id]
	return a, ok, nil
}

func (f *mcpServiceFakeRepo) CreateWorkspaceMCP(_ context.Context, m WorkspaceMCP) (WorkspaceMCP, error) {
	if f.dupName != nil {
		return m, f.dupName
	}
	f.lastWS = &m
	return m, nil
}

func (f *mcpServiceFakeRepo) UpdateWorkspaceMCP(_ context.Context, m WorkspaceMCP) (WorkspaceMCP, bool, error) {
	if f.dupName != nil {
		return m, false, f.dupName
	}
	f.lastWSUpdate = &m
	return m, true, nil
}

func (f *mcpServiceFakeRepo) CreateAgentMCP(_ context.Context, m AgentMCP) (AgentMCP, error) {
	if f.dupName != nil {
		return m, f.dupName
	}
	f.createCalled = true
	f.lastAgent = &m
	return m, nil
}

// UpsertAgentMCP 已从 Repo 接口删除（FP-07 P1-4）；此方法永远不会被服务层
// 调用——保留实现并在测试中断言，防止有人把接口/调用改回去。
func (f *mcpServiceFakeRepo) UpsertAgentMCP(context.Context, AgentMCP) (AgentMCP, error) {
	f.upsertCalled = true
	return AgentMCP{}, errors.New("upsert must not be called (create semantics per FP-07 P1-4)")
}

func (f *mcpServiceFakeRepo) UpdateAgentMCP(_ context.Context, m AgentMCP) (AgentMCP, bool, error) {
	f.lastAgentUpd = &m
	return m, true, nil
}

func (f *mcpServiceFakeRepo) ListWorkspaceMCP(context.Context, string) ([]WorkspaceMCP, error) {
	return f.wsRows, nil
}

func (f *mcpServiceFakeRepo) ListAgentMCP(_ context.Context, _ string, agentID string) ([]AgentMCP, bool, error) {
	if _, ok := f.agents[agentID]; !ok {
		return nil, false, nil
	}
	return f.agentRows, true, nil
}

func newMCPServiceFake() (*MCPServerService, *mcpServiceFakeRepo) {
	repo := &mcpServiceFakeRepo{agents: map[string]Agent{"ag-1": {ID: "ag-1"}}}
	s := NewMCPServerService(repo)
	s.Egress = nil // 单测不做出网 DNS 解析
	return s, repo
}

func TestMCPServerService_AgentCreateIsCreateNotUpsert(t *testing.T) {
	s, repo := newMCPServiceFake()

	_, err := s.CreateAgentMCP(context.Background(), AgentMCP{
		WorkspaceID: "ws-1", AgentID: "ag-1", Name: "fresh", URL: "https://x.example"})
	require.NoError(t, err)
	assert.True(t, repo.createCalled, "service 必须调 repo.CreateAgentMCP（create 语义）")
	assert.False(t, repo.upsertCalled, "禁止回退到 upsert 静默覆盖（FP-07 P1-4）")

	// repo 报重名（pg 23505 → 409 映射在 repo 层）原样透传给上层。
	repo.dupName = apierr.New(apierr.Conflict, "mcp server name already taken")
	_, err = s.CreateAgentMCP(context.Background(), AgentMCP{
		WorkspaceID: "ws-1", AgentID: "ag-1", Name: "dup", URL: "https://x.example"})
	require.Error(t, err)
	var apiErr *apierr.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, apierr.Conflict, apiErr.Code, "重名 409 透传，不吞不降级")
}

func TestMCPServerService_ThreeStatePassthrough(t *testing.T) {
	s, repo := newMCPServiceFake()
	ctx := context.Background()

	// nil=保留：不得被服务层偷换成空 map（否则 pg 层 coalesce 语义失效）。
	_, err := s.Update(ctx, WorkspaceMCP{WorkspaceID: "ws-1", ID: "s1", Name: "n", URL: "https://x.example"})
	require.NoError(t, err)
	assert.Nil(t, repo.lastWSUpdate.Credentials, "ws 级 Update：nil Credentials 原样透传=保留")

	_, err = s.UpdateAgentMCP(ctx, AgentMCP{WorkspaceID: "ws-1", AgentID: "ag-1", ID: "s1", Name: "n", URL: "https://x.example"})
	require.NoError(t, err)
	assert.Nil(t, repo.lastAgentUpd.Credentials, "agent 级 Update：nil Credentials 原样透传=保留")

	// 空 map=显式清空：不得被服务层偷换成 nil（否则误保留）。
	_, err = s.Update(ctx, WorkspaceMCP{WorkspaceID: "ws-1", ID: "s1", Name: "n", URL: "https://x.example", Credentials: map[string]any{}})
	require.NoError(t, err)
	require.NotNil(t, repo.lastWSUpdate.Credentials)
	assert.Empty(t, repo.lastWSUpdate.Credentials, "ws 级 Update：空 map 原样透传=清空")

	_, err = s.UpdateAgentMCP(ctx, AgentMCP{WorkspaceID: "ws-1", AgentID: "ag-1", ID: "s1", Name: "n", URL: "https://x.example", Credentials: map[string]any{}})
	require.NoError(t, err)
	require.NotNil(t, repo.lastAgentUpd.Credentials)
	assert.Empty(t, repo.lastAgentUpd.Credentials, "agent 级 Update：空 map 原样透传=清空")

	// 新值=覆盖。
	_, err = s.Update(ctx, WorkspaceMCP{WorkspaceID: "ws-1", ID: "s1", Name: "n", URL: "https://x.example",
		Credentials: map[string]any{"api_key": "sk-1"}})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"api_key": "sk-1"}, repo.lastWSUpdate.Credentials)
}

// P1-5：transport 枚举一刀切——合法值 ""/streamable_http/sse，"http" 与
// 其余乱值一律 validation 拒绝（ws/agent 两级、Create/Update 双侧）。
func TestValidateTransport(t *testing.T) {
	for _, v := range []string{"", "streamable_http", "sse"} {
		assert.NoError(t, validateTransport(v), "合法值 %q", v)
	}
	for _, v := range []string{"http", "stdio", "HTTP", "Streamable_HTTP", "websocket"} {
		assert.Error(t, validateTransport(v), "非法值 %q 必须 422 语义拒绝", v)
	}
}

func TestMCPServerService_TransportGate(t *testing.T) {
	s, repo := newMCPServiceFake()
	ctx := context.Background()

	// ws 级 Create / Update。
	_, err := s.Create(ctx, WorkspaceMCP{WorkspaceID: "ws-1", Name: "n", URL: "https://x.example", Transport: "http"})
	requireApiValidation(t, err)
	assert.Nil(t, repo.lastWS, "非法 transport 不得触达 repo")
	_, err = s.Update(ctx, WorkspaceMCP{WorkspaceID: "ws-1", ID: "s1", Name: "n", URL: "https://x.example", Transport: "stdio"})
	requireApiValidation(t, err)
	assert.Nil(t, repo.lastWSUpdate)

	// agent 级 Create / Update。
	_, err = s.CreateAgentMCP(ctx, AgentMCP{WorkspaceID: "ws-1", AgentID: "ag-1", Name: "n", URL: "https://x.example", Transport: "http"})
	requireApiValidation(t, err)
	assert.False(t, repo.createCalled)
	_, err = s.UpdateAgentMCP(ctx, AgentMCP{WorkspaceID: "ws-1", AgentID: "ag-1", ID: "s1", Name: "n", URL: "https://x.example", Transport: "carrier"})
	requireApiValidation(t, err)
	assert.Nil(t, repo.lastAgentUpd)

	// 合法值原样透传（缺省兜底 streamable_http 在 repo 层 orDefault）。
	_, err = s.Create(ctx, WorkspaceMCP{WorkspaceID: "ws-1", Name: "n", URL: "https://x.example", Transport: "sse"})
	require.NoError(t, err)
	assert.Equal(t, "sse", repo.lastWS.Transport)
	_, err = s.Create(ctx, WorkspaceMCP{WorkspaceID: "ws-1", Name: "n2", URL: "https://x.example"})
	require.NoError(t, err)
	assert.Equal(t, "", repo.lastWS.Transport, "缺省不偷换，repo 层兜底")
}

// requireApiValidation：断言 err 是 apierr.Validation（writeErrReq 映射 400）。
func requireApiValidation(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var apiErr *apierr.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, apierr.Validation, apiErr.Code)
}

// P1-3：ProbeDraft 用入参（含凭证）实连探测，不落库；transport 非法先于
// 探测拒绝；签名冻结（[]ProbeTool 返回形状）。
func TestMCPServerService_ProbeDraft(t *testing.T) {
	s, repo := newMCPServiceFake()
	ctx := context.Background()

	// ToolProbe 未接线 → 501 语义（Internal）。
	_, err := s.ProbeDraft(ctx, "ws-1", WorkspaceMCP{Name: "n", URL: "https://x.example"})
	require.Error(t, err)
	var apiErr *apierr.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, apierr.Internal, apiErr.Code)

	var got ResolvedMCPServer
	s.ToolProbe = func(_ context.Context, srv ResolvedMCPServer, ws string) ([]ProbeTool, error) {
		got = srv
		assert.Equal(t, "ws-1", ws)
		return []ProbeTool{{Name: "t1"}, {Name: "t2"}}, nil
	}
	tools, err := s.ProbeDraft(ctx, "ws-1", WorkspaceMCP{
		Name: "draft", Transport: "streamable_http", URL: "https://x.example",
		Credentials: map[string]any{"headers": map[string]any{"Authorization": "Bearer sk"}}})
	require.NoError(t, err)
	assert.Equal(t, []ProbeTool{{Name: "t1"}, {Name: "t2"}}, tools)
	assert.Equal(t, "draft", got.Name)
	assert.Equal(t, "streamable_http", got.Transport)
	assert.Equal(t, map[string]any{"headers": map[string]any{"Authorization": "Bearer sk"}}, got.Credentials,
		"入参凭证原样进探测链路")
	assert.Nil(t, repo.lastWS, "干跑不落库：repo Create 不得被调用")
	assert.False(t, repo.createCalled)

	// transport 非法 → 结构化 422，不触探测。
	_, err = s.ProbeDraft(ctx, "ws-1", WorkspaceMCP{Name: "n", URL: "https://x.example", Transport: "http"})
	requireApiValidation(t, err)
}

// P0-5 + P1-3：probe 链路的 total（截断前总数）与 agent 级条目探测。
func TestMCPServerService_ProbeTotalAndAgentLevel(t *testing.T) {
	s, repo := newMCPServiceFake()
	ctx := context.Background()

	repo.wsRows = []WorkspaceMCP{{ID: "ws-srv-1", Name: "w", Transport: "streamable_http", URL: "https://x.example",
		Credentials: map[string]any{"headers": map[string]any{"X-A": "1"}}}}
	repo.agentRows = []AgentMCP{{ID: "ag-srv-1", AgentID: "ag-1", Name: "a", Transport: "sse", URL: "https://y.example",
		Credentials: map[string]any{"headers": map[string]any{"X-B": "2"}}}}

	// 只接线 ToolProbe（2 返回值）→ total 以返回条数计。
	s.ToolProbe = func(_ context.Context, _ ResolvedMCPServer, _ string) ([]ProbeTool, error) {
		return []ProbeTool{{Name: "t1"}}, nil
	}
	tools, total, err := s.ProbeTools(ctx, "ws-1", "ws-srv-1")
	require.NoError(t, err)
	assert.Len(t, tools, 1)
	assert.Equal(t, 1, total)

	// 接线 ToolProbeCounted → total 透传引擎的截断前总数。
	s.ToolProbeCounted = func(_ context.Context, _ ResolvedMCPServer, _ string) ([]ProbeTool, int, error) {
		return make([]ProbeTool, 32), 40, nil
	}
	tools, total, err = s.ProbeTools(ctx, "ws-1", "ws-srv-1")
	require.NoError(t, err)
	assert.Len(t, tools, 32)
	assert.Equal(t, 40, total, "截断前总数（远端 40，数组 32）")

	// 不存在的 ws 级服务器 → 404。
	_, _, err = s.ProbeTools(ctx, "ws-1", "ghost")
	requireApiNotFound(t, err)

	// agent 级：条目存在 → 探测入参取自 agent 行（含凭证）。
	var got ResolvedMCPServer
	s.ToolProbe = func(_ context.Context, srv ResolvedMCPServer, _ string) ([]ProbeTool, error) {
		got = srv
		return nil, nil
	}
	s.ToolProbeCounted = nil
	_, _, err = s.ProbeAgentTools(ctx, "ws-1", "ag-1", "ag-srv-1")
	require.NoError(t, err)
	assert.Equal(t, "a", got.Name)
	assert.Equal(t, "sse", got.Transport)
	assert.Equal(t, map[string]any{"headers": map[string]any{"X-B": "2"}}, got.Credentials)

	// agent 级：服务器/agent 不存在 → 404。
	_, _, err = s.ProbeAgentTools(ctx, "ws-1", "ag-1", "ghost")
	requireApiNotFound(t, err)
	_, _, err = s.ProbeAgentTools(ctx, "ws-1", "ag-missing", "ag-srv-1")
	requireApiNotFound(t, err)
}

func requireApiNotFound(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var apiErr *apierr.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, apierr.NotFound, apiErr.Code)
}
