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
