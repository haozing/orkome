package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
)

func newB6H(t *testing.T) *b2h {
	t.Helper()
	h := newB2H(t)

	_, err := h.pool.Exec(context.Background(),
		`UPDATE workspace w SET work_item_counter = greatest(w.work_item_counter,
		  coalesce((SELECT max(number) FROM work_item i WHERE i.workspace_id=w.id),0))
		 WHERE w.id=$1`, h.wsID)
	require.NoError(t, err)

	mcp := orchapp.NewMCPServerService(h.repo)
	wRepo := &pgrepo.Repo{DB: h.repo.DB}
	sourceCtx := workapp.NewSourceContextService(wRepo, nil, nil)
	sourceCtx.Sign = func(msg string) string { return "sig:" + msg }

	retry := func(ctx context.Context, ws, taskID, userID string) (any, error) {
		var digest string
		err := h.pool.QueryRow(ctx,
			`SELECT coalesce(context->>'source_context_digest','') FROM agent_task_queue WHERE workspace_id=$1 AND id=$2`,
			ws, taskID).Scan(&digest)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && digest == "") {
			return nil, apierr.New(apierr.NotFound, "task carries no source context")
		}
		if err != nil {
			return nil, err
		}
		return sourceCtx.CreateFromDigest(ctx, ws, digest, userID, "")
	}

	r := http.NewServeMux()
	Mount(r, Deps{
		Settle:      orchapp.NewSettlementService(h.repo, h.bus, nil, nil, nil),
		Agents:      h.agents,
		MCP:         mcp,
		MemberCheck: h.memberCheck,
		TaskMessages: func(ctx context.Context, ws, taskID string, q orchapp.TaskMessagesQuery) ([]orchapp.TaskMessage, error) {
			return h.repo.ListTaskMessages(ctx, ws, taskID, q)
		},
		RetrySourceContext: retry,
	}, h.auth)
	h.handler = r
	return h
}

func TestB6_MCPLibraryCRUD(t *testing.T) {
	h := newB6H(t)

	code, out := h.do("POST", "/api/workspaces/"+h.wsID+"/mcp-servers", map[string]any{
		"name": "search", "transport": "streamable_http", "url": "https://example.com/sse",
		"credentials": map[string]any{"api_key": "sk-secret"},
	})
	require.Equal(t, 201, code, "create: %v", out)
	id, _ := out["id"].(string)
	require.NotEmpty(t, id)
	_, hasCreds := out["credentials"]
	assert.False(t, hasCreds, "credential fields never in responses (04: redaction)")
	assert.Equal(t, true, out["has_credentials"])

	code, out = h.do("GET", "/api/workspaces/"+h.wsID+"/mcp-servers", nil)
	require.Equal(t, 200, code)
	servers, _ := out["servers"].([]any)
	require.Len(t, servers, 1)

	code, out = h.do("PUT", "/api/workspaces/"+h.wsID+"/mcp-servers/"+id, map[string]any{
		"name": "search2", "transport": "streamable_http", "url": "https://example.com/v2",
	})
	require.Equal(t, 200, code, "update: %v", out)
	assert.Equal(t, "search2", out["name"])

	code, _ = h.do("DELETE", "/api/workspaces/"+h.wsID+"/mcp-servers/"+id, nil)
	require.Equal(t, 200, code)
	code, out = h.do("GET", "/api/workspaces/"+h.wsID+"/mcp-servers", nil)
	servers, _ = out["servers"].([]any)
	assert.Empty(t, servers)

	rd := httptest.NewRequest("GET", "/api/workspaces/00000000-0000-0000-0000-000000000009/mcp-servers", nil)
	rd.Header.Set("X-Workspace-Id", h.wsID)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, rd)
	assert.Equal(t, 403, w.Code)
}

func TestB6_MCPMergeResolution(t *testing.T) {
	h := newB6H(t)
	ctx := context.Background()

	svc := orchapp.NewMCPServerService(h.repo)
	_, err := svc.Create(ctx, orchapp.WorkspaceMCP{WorkspaceID: h.wsID, Name: "a", Transport: "http", URL: "https://example.com/a"})
	require.NoError(t, err)
	_, err = svc.Create(ctx, orchapp.WorkspaceMCP{WorkspaceID: h.wsID, Name: "b", Transport: "http", URL: "https://example.com/b"})
	require.NoError(t, err)

	a, err := h.agents.Create(ctx, orchapp.Agent{WorkspaceID: h.wsID, Name: "merge-agent",
		RuntimeConfig: map[string]any{"provider": "fake", "model": "m"}})
	require.NoError(t, err)
	_, err = h.pool.Exec(ctx, `
INSERT INTO agent_mcp_server (agent_id, name, transport, url, enabled) VALUES
($1,'a','streamable_http','https://example.com/a2',false), ($1,'c','streamable_http','https://example.com/c',true)`, a.ID)
	require.NoError(t, err)

	code, out := h.do("GET", "/api/agents/"+a.ID+"/mcp-servers?resolved=1", nil)
	require.Equal(t, 200, code, "resolved: %v", out)
	servers, _ := out["servers"].([]any)
	require.Len(t, servers, 2)
	names := map[string]bool{}
	for _, s := range servers {
		m, _ := s.(map[string]any)
		if n, ok := m["name"].(string); ok {
			names[n] = true
		}
	}
	assert.Equal(t, map[string]bool{"b": true, "c": true}, names)

	code, out = h.do("GET", "/api/agents/"+a.ID+"/mcp-servers", nil)
	require.Equal(t, 200, code)
	servers, _ = out["servers"].([]any)
	assert.Len(t, servers, 2, "two agent-level rows (a/c)")
}

func TestB6_AgentMCPCRUD(t *testing.T) {
	h := newB6H(t)
	ctx := context.Background()

	agent, err := h.agents.Create(ctx, orchapp.Agent{WorkspaceID: h.wsID, Name: "crud-agent",
		RuntimeConfig: map[string]any{"provider": "fake", "model": "m"}})
	require.NoError(t, err)
	base := "/api/agents/" + agent.ID + "/mcp-servers"

	code, out := h.do("POST", base, map[string]any{
		"name": "jira", "transport": "streamable_http", "url": "https://example.com/jira",
		"credentials": map[string]any{"api_key": "sk-agent-secret"},
	})
	require.Equal(t, 201, code, "create: %v", out)
	id, _ := out["id"].(string)
	require.NotEmpty(t, id)
	_, hasCreds := out["credentials"]
	assert.False(t, hasCreds, "credential fields never in responses (internal archive 04 §2.5 response redaction)")
	assert.Equal(t, true, out["enabled"], "enabled defaults to true")

	code, out = h.do("POST", base, map[string]any{
		"name": "jira", "url": "https://example.com/jira-v2", "enabled": false,
	})
	require.Equal(t, 409, code, "same-name POST = 409 not silent overwrite (FP-07 P1-4): %v", out)
	// 409 拒绝后改条目走 PUT（对齐 ws 级语义）：禁用 + 换 URL。
	code, out = h.do("PUT", base+"/"+id, map[string]any{
		"name": "jira", "url": "https://example.com/jira-v2", "enabled": false,
	})
	require.Equal(t, 200, code, "update: %v", out)
	code, out = h.do("GET", base, nil)
	require.Equal(t, 200, code)
	servers, _ := out["servers"].([]any)
	require.Len(t, servers, 1, "409 rejection does not produce a second row")
	row, _ := servers[0].(map[string]any)
	assert.Equal(t, "https://example.com/jira-v2", row["url"])
	assert.Equal(t, false, row["enabled"])
	assert.Equal(t, true, row["has_credentials"], "update without credentials = keep the original (mask position stays true)")

	code, out = h.do("GET", base+"?resolved=1", nil)
	require.Equal(t, 200, code)
	resolved, _ := out["servers"].([]any)
	for _, s := range resolved {
		if m, _ := s.(map[string]any); m["name"] == "jira" {
			t.Fatalf("disabled agent entries must not enter the merged resolution view: %v", m)
		}
	}

	code, out = h.do("PUT", base+"/"+id, map[string]any{
		"name": "jira", "url": "https://example.com/jira-v3",
	})
	require.Equal(t, 200, code, "update: %v", out)
	assert.Equal(t, true, out["enabled"], "PUT without enabled replaces with the default true")
	_, hasCreds = out["credentials"]
	assert.False(t, hasCreds)

	code, _ = h.do("DELETE", base+"/"+id, nil)
	require.Equal(t, 200, code)
	code, out = h.do("GET", base, nil)
	require.Equal(t, 200, code)
	servers, _ = out["servers"].([]any)
	assert.Empty(t, servers)

	code, _ = h.do("POST", "/api/agents/00000000-0000-0000-0000-0000000000c0/mcp-servers",
		map[string]any{"name": "x", "url": "https://example.com/x"})
	assert.Equal(t, 404, code)
	code, _ = h.do("PUT", base+"/00000000-0000-0000-0000-0000000000dd",
		map[string]any{"name": "x", "url": "https://example.com/x"})
	assert.Equal(t, 404, code)

	code, _ = h.do("POST", base, map[string]any{"name": "ssrf", "url": "https://127.0.0.1/mcp"})
	assert.Equal(t, 400, code)
}

func TestB6_RetrySourceContextEndpoint(t *testing.T) {
	h := newB6H(t)
	ctx := context.Background()

	wRepo := &pgrepo.Repo{DB: h.repo.DB}
	wi, err := wRepo.CreateWorkItem(ctx, workapp.NewWorkItem{
		WorkspaceID: h.wsID, Title: "retry host", StatusKey: "todo", CreatorType: "user", CreatorID: h.userID})
	require.NoError(t, err)
	c, err := wRepo.CreateComment(ctx, workapp.NewComment{
		WorkItemID: wi.ID, WorkspaceID: h.wsID, AuthorType: "user", AuthorID: h.userID, Content: "retry context"})
	require.NoError(t, err)
	digest := "b6retry00000000000000000000000000000000000000000000000000000000000"
	_, err = h.pool.Exec(ctx, `
INSERT INTO work_item_source_context (workspace_id, source_digest, snapshot, created_by)
VALUES ($1,$2, jsonb_build_object('comment_id',$3::text,'work_item_id',$4::text,'content','  retry context'), $5)`,
		h.wsID, digest, c.ID, wi.ID, h.userID)
	require.NoError(t, err)
	agent, err := h.agents.Create(ctx, orchapp.Agent{WorkspaceID: h.wsID, Name: "b6-retry",
		RuntimeConfig: map[string]any{"provider": "fake", "model": "m"}})
	require.NoError(t, err)
	task, _, err := h.enq.EnqueueAssignment(ctx, h.wsID, wi.ID, agent.ID, h.userID)
	require.NoError(t, err)
	_, err = h.pool.Exec(ctx, `
UPDATE agent_task_queue SET context = context || jsonb_build_object('source_context_digest',$2::text)
WHERE id=$1`, task.ID, digest)
	require.NoError(t, err)

	code, out := h.do("POST", "/api/tasks/"+task.ID+"/retry-source-context", nil)
	require.Equal(t, 201, code, "retry: %v", out)
	parent, _ := out["parent_work_item_id"].(string)
	assert.Equal(t, wi.ID, parent)

	wi2, err := wRepo.CreateWorkItem(ctx, workapp.NewWorkItem{
		WorkspaceID: h.wsID, Title: "no digest host", StatusKey: "todo", CreatorType: "user", CreatorID: h.userID})
	require.NoError(t, err)
	task2, _, err := h.enq.EnqueueAssignment(ctx, h.wsID, wi2.ID, agent.ID, h.userID)
	require.NoError(t, err)
	code, out = h.do("POST", "/api/tasks/"+task2.ID+"/retry-source-context", nil)
	assert.Equal(t, 404, code)
	assert.Equal(t, apierr.NotFound, out["error"])
}
