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
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	convapp "orkome/internal/conversation/app"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/migrations"
)

type b4h struct {
	t       *testing.T
	handler http.Handler
	chat    *convapp.ChatService
	settle  *orchapp.SettlementService
	pool    *pgxpool.Pool
	wsID    string
	userID  string
	userB   string
	agentID string

	qa *convapp.QuickActionService
}

func newB4H(t *testing.T) *b4h {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_b4h")
	admin.Close(context.Background())

	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	pool, err := pgxpool.New(context.Background(), strings.Replace(base, "/"+cfg.Database, "/orkome_b4h", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(context.Background(), 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `TRUNCATE task_message, event_outbox, task_supplement,
		agent_task_queue, agent_runtime, agent, chat_pinned_agent, quick_action,
		chat_draft_restore, chat_message, chat_session,
		notification, member, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	db := pg.WrapPool(pool)
	log := logx.New(logx.Options{Level: "error"})
	bus := eventbus.New(log)
	ctx := context.Background()

	var userID, wsID, agentID, userB string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('b4@x.io','B4') RETURNING id::text`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('b4b@x.io','B4B') RETURNING id::text`).Scan(&userB))
	require.NoError(t, pool.QueryRow(ctx,
		`WITH u AS (INSERT INTO "user" (email, display_name) VALUES ('seed-b4-h@test.dev','SEED') RETURNING id) INSERT INTO workspace (name, slug, created_by) SELECT 'b4','b4-h', id FROM u RETURNING id::text`).Scan(&wsID))
	_, err = pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, wsID, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'member')`, wsID, userB)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO agent (workspace_id, name, description, runtime_config)
VALUES ($1, 'b4-chat', '', '{"provider":"fake","model":"m"}'::jsonb) RETURNING id::text`, wsID).Scan(&agentID))

	convRepo := convpgrepo.New(db)
	orchRepo := orchpgrepo.New(db)
	enq := orchapp.NewEnqueueService(orchRepo, bus)
	settle := orchapp.NewSettlementService(orchRepo, bus, nil, nil, nil)
	chat := convapp.NewChatService(convRepo, enq)

	ops := b4TaskOps{pool: pool}
	chat.SetPendingChatReader(ops)
	chat.SetTaskPrioritizer(ops)

	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid := userID
			if v := r.Header.Get("X-Test-User"); v != "" {
				uid = v
			}
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(), &webx.Principal{
				UserID: uid, Source: webx.SourceSession,
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
	qa := convapp.NewQuickActionService(convRepo)
	Mount(r, Deps{
		Chat: chat, MemberCheck: memberCheck,
		Canceller:    chatCanceller{settle: settle},
		TaskStatus:   settle.ChatTaskStatus,
		Pins:         convapp.NewPinService(convRepo),
		QuickActions: qa,
	}, auth)
	return &b4h{t: t, handler: r, chat: chat, settle: settle, pool: pool,
		wsID: wsID, userID: userID, userB: userB, agentID: agentID, qa: qa}
}

type chatCanceller struct{ settle *orchapp.SettlementService }

func (c chatCanceller) CancelChatTask(ctx context.Context, ws, sessionID string) (string, string, bool, error) {
	return c.settle.CancelChatTask(ctx, ws, sessionID)
}

func (h *b4h) do(method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var rd *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		rd = httptest.NewRequest(method, "/api"+path, bytes.NewReader(b))
	} else {
		rd = httptest.NewRequest(method, "/api"+path, nil)
	}
	rd.Header.Set("X-Workspace-Id", h.wsID)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, rd)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (h *b4h) doAs(uid, method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var rd *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		rd = httptest.NewRequest(method, "/api"+path, bytes.NewReader(b))
	} else {
		rd = httptest.NewRequest(method, "/api"+path, nil)
	}
	rd.Header.Set("X-Workspace-Id", h.wsID)
	rd.Header.Set("X-Test-User", uid)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, rd)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestB4_ChatSessionLifecycle(t *testing.T) {
	h := newB4H(t)

	code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID, "title": "research"})
	require.Equal(t, 201, code, "create: %v", out)
	sid, _ := out["id"].(string)
	require.NotEmpty(t, sid)

	code, out = h.do("GET", "/chat/sessions", nil)
	require.Equal(t, 200, code)
	sessions, _ := out["sessions"].([]any)
	assert.Len(t, sessions, 1)

	code, _ = h.do("GET", "/chat/sessions/"+sid, nil)
	assert.Equal(t, 200, code)
	code, _ = h.do("GET", "/chat/sessions/00000000-0000-0000-0000-000000000009", nil)
	assert.Equal(t, 404, code)

	code, out = h.do("POST", "/chat/sessions/"+sid+"/messages", map[string]any{"content": "summarize for me"})
	require.Equal(t, 201, code, "send: %v", out)
	taskID, _ := out["task_id"].(string)
	require.NotEmpty(t, taskID, "response should contain the task ID")

	code, out = h.do("GET", "/chat/sessions/"+sid+"/messages", nil)
	require.Equal(t, 200, code)
	msgs, _ := out["messages"].([]any)
	require.Len(t, msgs, 1)
	first, _ := msgs[0].(map[string]any)
	assert.Equal(t, "user", first["role"])
	assert.Equal(t, "summarize for me", first["content"])

	code, out = h.do("GET", "/chat/sessions/"+sid+"/pending-task", nil)
	require.Equal(t, 200, code)
	task, _ := out["task"].(map[string]any)
	require.NotNil(t, task)
	assert.Equal(t, "queued", task["status"])
	assert.Equal(t, taskID, task["id"])

	code, out = h.do("DELETE", "/chat/sessions/"+sid+"/pending-task", nil)
	require.Equal(t, 200, code, "cancel: %v", out)
	assert.Equal(t, "restored", out["outcome"], "empty transcript cancel should restore the draft")

	code, out = h.do("GET", "/chat/sessions/"+sid+"/draft-restores", nil)
	require.Equal(t, 200, code)
	draft, _ := out["draft"].(map[string]any)
	require.NotNil(t, draft, "recovery content should be retrievable")
	assert.Equal(t, "summarize for me", draft["content"])
	code, _ = h.do("DELETE", "/chat/sessions/"+sid+"/draft-restores", nil)
	assert.Equal(t, 200, code)

	code, out = h.do("GET", "/chat/sessions/"+sid+"/messages", nil)
	require.Equal(t, 200, code)
	msgs, _ = out["messages"].([]any)
	assert.Empty(t, msgs, "user row should be deleted after restored")

	code, _ = h.do("POST", "/chat/sessions/"+sid+"/read", nil)
	assert.Equal(t, 200, code)

	code, out = h.do("POST", "/chat/sessions/"+sid+"/archive", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, "archived", out["result"])
	code, out = h.do("GET", "/chat/sessions/"+sid, nil)
	require.Equal(t, 200, code)
	assert.Equal(t, "archived", out["status"])
	code, _ = h.do("POST", "/chat/sessions/"+sid+"/unarchive", nil)
	assert.Equal(t, 200, code)
}

func TestB4_PendingTaskEmpty(t *testing.T) {
	h := newB4H(t)
	code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID})
	require.Equal(t, 201, code)
	sid, _ := out["id"].(string)
	code, out = h.do("GET", "/chat/sessions/"+sid+"/pending-task", nil)
	require.Equal(t, 200, code)
	assert.Nil(t, out["task"], "taskless sessions should return null")
}

func TestB4_SendMessageValidation(t *testing.T) {
	h := newB4H(t)
	code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID})
	require.Equal(t, 201, code)
	sid, _ := out["id"].(string)
	code, out = h.do("POST", "/chat/sessions/"+sid+"/messages", map[string]any{"content": "  "})
	assert.Equal(t, 400, code)
	assert.Equal(t, apierr.Validation, out["error"])
}

func TestB4_ForbiddenNonMember(t *testing.T) {
	h := newB4H(t)
	rd := httptest.NewRequest("GET", "/api/chat/sessions", nil)
	rd.Header.Set("X-Workspace-Id", "00000000-0000-0000-0000-000000000003")
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, rd)
	assert.Equal(t, 403, w.Code)
}
