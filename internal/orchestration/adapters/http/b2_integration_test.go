package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmigrations "github.com/haozing/ploykit/migrations"
	"github.com/haozing/ploykit/platform/logx"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	"orkome/internal/orchestration/adapters/engine"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/migrations"
)

type b2h struct {
	t       *testing.T
	handler http.Handler
	repo    *orchpgrepo.Repo
	agents  *orchapp.AgentService
	enq     *orchapp.EnqueueService
	bus     *eventbus.Bus
	wsID    string
	userID  string

	pool        *pgxpool.Pool
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)
	auth        func(http.Handler) http.Handler
}

func newB2H(t *testing.T) *b2h {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_b2h")
	admin.Close(context.Background())
	pool, err := pgxpool.New(context.Background(), strings.Replace(base, "/orkome", "/orkome_b2h", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(context.Background(), 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `TRUNCATE task_message, task_supplement, agent_task_queue,
		agent_runtime, agent, notification, member, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	db := pg.WrapPool(pool)
	log := logx.New(logx.Options{Level: "error"})
	bus := eventbus.New(log)
	repo := orchpgrepo.New(db)
	agents := orchapp.NewAgentService(repo, bus)

	engine.RegisterFakeProviderForTest(func(string) (string, schema.TokenUsage, error) {
		return "ok", schema.TokenUsage{}, nil
	})

	var userID, wsID string
	ctx := context.Background()
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('b2@x.io','B2') RETURNING id::text`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`WITH u AS (INSERT INTO "user" (email, display_name) VALUES ('seed-b2-h@test.dev','SEED') RETURNING id) INSERT INTO workspace (name, slug, created_by) SELECT 'b2','b2-h', id FROM u RETURNING id::text`).Scan(&wsID))
	_, err = pool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, wsID, userID)
	require.NoError(t, err)

	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(), &webx.Principal{
				// Role=owner 与上面 member 行的 DB 角色对齐——P2-D7 后
				// ws 级 mcp-servers 写面要求 admin（requireWorkspaceAdmin
				// 读 principal.Role，本 harness 不接 RoleOf）。
				UserID: userID, Source: webx.SourceSession, Role: "owner",
			})))
		})
	}
	memberCheck := func(ctx context.Context, ws, uid string) (bool, error) {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM member WHERE workspace_id=$1 AND user_id=$2)`, ws, uid).Scan(&exists)
		return exists, err
	}

	r := http.NewServeMux()
	Mount(r, Deps{
		Agents: agents, Repo: repo, MemberCheck: memberCheck,
		TaskMessages: func(ctx context.Context, ws, taskID string, q orchapp.TaskMessagesQuery) ([]orchapp.TaskMessage, error) {
			return repo.ListTaskMessages(ctx, ws, taskID, q)
		},
	}, auth)

	return &b2h{t: t, handler: r, repo: repo, agents: agents, bus: bus,
		enq: orchapp.NewEnqueueService(repo, bus), wsID: wsID, userID: userID,
		pool: pool, memberCheck: memberCheck, auth: auth}
}

func (h *b2h) do(method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var rd *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		rd = httptest.NewRequest(method, path, bytes.NewReader(b))
	} else {
		rd = httptest.NewRequest(method, path, nil)
	}
	rd.Header.Set("X-Workspace-Id", h.wsID)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, rd)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func errCodeOf(out map[string]any) string {
	c, _ := out["error"].(string)
	return c
}

var b2wiSeq int

func createWIForAgent(t *testing.T, h *b2h) string {
	t.Helper()
	b2wiSeq++
	var id string
	require.NoError(t, h.repo.DB.Tx(context.Background()).QueryRow(context.Background(), `
INSERT INTO work_item (workspace_id, number, title, status_key, creator_id)
VALUES ($1, $2, 'b2 fixture', 'todo', $3) RETURNING id::text`, h.wsID, b2wiSeq, h.userID).Scan(&id))
	return id
}

func TestB2_AgentCRUDHTTP(t *testing.T) {
	h := newB2H(t)

	code, out := h.do("POST", "/api/agents", map[string]any{
		"name":           "researcher",
		"description":    "researcher",
		"runtime_config": map[string]any{"provider": "fake", "model": "m"},
	})
	require.Equal(t, 201, code, "create: %v", out)
	id, _ := out["id"].(string)
	require.NotEmpty(t, id)

	code, out = h.do("POST", "/api/agents", map[string]any{"name": "researcher"})
	assert.Equal(t, 400, code)
	assert.Equal(t, apierr.Validation, errCodeOf(out), "duplicate names should be E_VALIDATION")

	code, out = h.do("GET", "/api/agents/"+id, nil)
	require.Equal(t, 200, code)
	assert.Equal(t, "researcher", out["name"])
	assert.Equal(t, h.userID, out["owner_user_id"], "attribution defaults to the creator (internal archive 05 §2.1)")

	code, out = h.do("GET", "/api/agents", nil)
	require.Equal(t, 200, code)
	list, _ := out["agents"].([]any)
	assert.Len(t, list, 1)

	code, out = h.do("PUT", "/api/agents/"+id, map[string]any{
		"name": "researcher-2", "description": "senior researcher",
		"runtime_config":       map[string]any{"provider": "fake", "model": "m2"},
		"max_concurrent_tasks": 3, "visibility": "public",
	})
	require.Equal(t, 200, code, "update: %v", out)
	assert.Equal(t, "researcher-2", out["name"])
	assert.EqualValues(t, 3, out["max_concurrent_tasks"])

	code, _ = h.do("PUT", "/api/agents/00000000-0000-0000-0000-000000000000", map[string]any{"name": "x"})
	assert.Equal(t, 404, code)

	code, out = h.do("POST", "/api/agents/"+id+"/archive", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, "archived", out["result"])
	got, _, _ := h.repo.GetAgent(context.Background(), h.wsID, id)
	assert.True(t, got.Archived)
	code, out = h.do("POST", "/api/agents/"+id+"/restore", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, "restored", out["result"])

	for i := 0; i < 2; i++ {
		wi := createWIForAgent(t, h)
		_, _, err := h.enq.EnqueueAssignment(
			context.Background(), h.wsID, wi, id, h.userID)
		require.NoError(t, err)
	}
	code, out = h.do("POST", "/api/agents/"+id+"/cancel-tasks", nil)
	require.Equal(t, 200, code, "cancel-tasks: %v", out)
	assert.EqualValues(t, 2, out["cancelled"], "two unstarted tasks should be cancelled")

	_, out = h.do("POST", "/api/agents/"+id+"/cancel-tasks", nil)
	assert.EqualValues(t, 0, out["cancelled"])
}

func TestB2_TaskMessagesEndpoint(t *testing.T) {
	h := newB2H(t)
	ctx := context.Background()
	wi := createWIForAgent(t, h)
	a, err := h.agents.Create(ctx, orchapp.Agent{
		WorkspaceID: h.wsID, Name: "b2-msg",
		RuntimeConfig: map[string]any{"provider": "fake", "model": "m"},
	})
	require.NoError(t, err)
	task, _, err := h.enq.EnqueueAssignment(ctx, h.wsID, wi, a.ID, h.userID)
	require.NoError(t, err)

	code, out := h.do("GET", "/api/tasks/"+task.ID+"/messages", nil)
	require.Equal(t, 200, code)
	msgs, _ := out["messages"].([]any)
	assert.Empty(t, msgs)

	_, err = h.repo.AppendTaskMessage(ctx, h.wsID, task.ID, "tool_use", "call_1", "post_comment", false)
	require.NoError(t, err)
	_, err = h.repo.AppendTaskMessage(ctx, h.wsID, task.ID, "text", "", "final conclusion", false)
	require.NoError(t, err)
	code, out = h.do("GET", "/api/tasks/"+task.ID+"/messages", nil)
	require.Equal(t, 200, code)
	msgs, _ = out["messages"].([]any)
	require.Len(t, msgs, 2)
	first, _ := msgs[0].(map[string]any)
	assert.Equal(t, "tool_use", first["kind"])
	assert.Equal(t, "call_1", first["call_id"])
	assert.EqualValues(t, 1, first["seq"])

	rd := httptest.NewRequest("GET", "/api/tasks/"+task.ID+"/messages", nil)
	rd.Header.Set("X-Workspace-Id", "00000000-0000-0000-0000-000000000002")
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, rd)
	require.Equal(t, 403, w.Code, "foreign workspace header rejected at the member gate (ADR 0005)")
}

func TestB2_AgentHTTP_ForbiddenNonMember(t *testing.T) {
	h := newB2H(t)
	rd := httptest.NewRequest("GET", "/api/agents", nil)
	rd.Header.Set("X-Workspace-Id", "00000000-0000-0000-0000-000000000001")
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, rd)
	assert.Equal(t, 403, w.Code)
}
