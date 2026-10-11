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

	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/migrations"
)

type sqhEnv struct {
	t        *testing.T
	handler  http.Handler
	pool     *pgxpool.Pool
	repo     *orchpgrepo.Repo
	wsUserID string
	wsID     string
}

func newSqhEnv(t *testing.T) *sqhEnv {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_sqh")
	admin.Close(context.Background())
	pool, err := pgxpool.New(context.Background(), strings.Replace(base, "/orkome", "/orkome_sqh", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `TRUNCATE agent_task_queue, squad_member, squad,
		activity_log, agent, member, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	db := pg.WrapPool(pool)
	repo := orchpgrepo.New(db)

	var userID, wsID string
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO "user" (email, display_name) VALUES ('sqh@example.com','sqh') RETURNING id::text`).Scan(&userID))
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO workspace (name, slug) VALUES ('sqh-ws','sqh-ws') RETURNING id::text`).Scan(&wsID))
	_, err = pool.Exec(context.Background(),
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, wsID, userID)
	require.NoError(t, err)

	svc := orchapp.NewSquadService(repo, poolRecorder{pool})
	d := Deps{Repo: repo, Squads: svc,
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil }}

	r := http.NewServeMux()
	Mount(r, d, passAuth)
	return &sqhEnv{t: t, handler: r, pool: pool, repo: repo, wsUserID: userID, wsID: wsID}
}

func passAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(),
			&webx.Principal{UserID: "sqh-user", Source: webx.SourceSession})))
	})
}

type poolRecorder struct{ pool *pgxpool.Pool }

func (p poolRecorder) RecordSquadEvaluation(ctx context.Context, e orchapp.SquadEvaluation) error {
	_, err := p.pool.Exec(ctx, `
INSERT INTO activity_log (event_id, workspace_id, work_item_id, actor_type, actor_id, kind, payload)
VALUES ($1,$2,$3,'agent',$4,'squad_leader_evaluated',$5::jsonb)
ON CONFLICT (event_id) DO NOTHING`,
		"squad-eval:"+e.TaskID+"\x1f"+e.Member, e.WorkspaceID, e.WorkItemID, e.LeaderAgentID,
		`{"squad_id":"`+e.SquadID+`","task_id":"`+e.TaskID+`","verdict":"`+e.Verdict+`"}`)
	return err
}

func (e *sqhEnv) do(method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("X-Workspace-Id", e.wsID)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (e *sqhEnv) mkAgent(name string) string {
	e.t.Helper()
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(),
		`INSERT INTO agent (workspace_id, name) VALUES ($1,$2) RETURNING id::text`, e.wsID, name).Scan(&id))
	return id
}

func (e *sqhEnv) mkTask(agentID, status string, leader bool, squadID string) string {
	e.t.Helper()
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `
INSERT INTO agent_task_queue (workspace_id, agent_id, status, is_leader_task, squad_id)
VALUES ($1,$2,$3,$4,nullif($5,'')::uuid) RETURNING id::text`,
		e.wsID, agentID, status, leader, squadID).Scan(&id))
	return id
}

func TestSquadMembersEndpoints(t *testing.T) {
	e := newSqhEnv(t)
	leader := e.mkAgent("sqh-leader")
	worker := e.mkAgent("sqh-worker")

	code, out := e.do("POST", "/api/squads", map[string]any{
		"name": "review squad", "instructions": "review", "leader_agent_id": leader})
	require.Equal(t, http.StatusCreated, code, out)
	squadID := out["id"].(string)

	code, out = e.do("POST", "/api/squads/"+squadID+"/members", map[string]any{"agent_id": worker})
	require.Equal(t, http.StatusOK, code, out)

	code, out = e.do("GET", "/api/squads/"+squadID+"/members", nil)
	require.Equal(t, http.StatusOK, code, out)
	members := out["members"].([]any)
	require.Len(t, members, 1)
	m0 := members[0].(map[string]any)
	assert.Equal(t, worker, m0["agent_id"])
	assert.Equal(t, "member", m0["role"], "role defaults to member")

	code, _ = e.do("PATCH", "/api/squads/"+squadID+"/members/"+worker,
		map[string]any{"role": "reviewer"})
	assert.Equal(t, http.StatusOK, code)
	code, out = e.do("GET", "/api/squads/"+squadID+"/members", nil)
	assert.Equal(t, "reviewer", out["members"].([]any)[0].(map[string]any)["role"])

	code, _ = e.do("PATCH", "/api/squads/"+squadID+"/members/"+leader, map[string]any{"role": "x"})
	assert.Equal(t, http.StatusNotFound, code)

	code, _ = e.do("DELETE", "/api/squads/"+squadID+"/members/"+worker, nil)
	assert.Equal(t, http.StatusOK, code)
	code, out = e.do("GET", "/api/squads/"+squadID+"/members", nil)
	assert.Empty(t, out["members"])
}

func TestSquadMemberStatusDerivation(t *testing.T) {
	e := newSqhEnv(t)
	leader := e.mkAgent("busy-leader")
	idler := e.mkAgent("idle-member")
	queued := e.mkAgent("queued-member")

	code, out := e.do("POST", "/api/squads", map[string]any{"name": "status squad", "leader_agent_id": leader})
	require.Equal(t, http.StatusCreated, code)
	squadID := out["id"].(string)
	for _, ag := range []string{leader, idler, queued} {
		code, _ = e.do("POST", "/api/squads/"+squadID+"/members", map[string]any{"agent_id": ag})
		require.Equal(t, http.StatusOK, code)
	}
	e.mkTask(leader, "running", false, "")
	e.mkTask(queued, "queued", false, "")

	code, out = e.do("GET", "/api/squads/"+squadID+"/members/status", nil)
	require.Equal(t, http.StatusOK, code, out)
	st := map[string]string{}
	for _, m := range out["members"].([]any) {
		mm := m.(map[string]any)
		st[mm["name"].(string)] = mm["status"].(string)
	}
	assert.Equal(t, "running", st["busy-leader"], "running task → running")
	assert.Equal(t, "queued", st["queued-member"], "queued task → queued")
	assert.Equal(t, "idle", st["idle-member"], "no tasks → idle")
}

func TestSquadEvaluatedEndpoint(t *testing.T) {
	e := newSqhEnv(t)
	leader := e.mkAgent("eval-leader")
	other := e.mkAgent("eval-other")
	code, out := e.do("POST", "/api/squads", map[string]any{"name": "evaluation squad", "leader_agent_id": leader})
	require.Equal(t, http.StatusCreated, code)
	squadID := out["id"].(string)

	var wiID string
	require.NoError(t, e.pool.QueryRow(context.Background(), `
INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1, 1, 'evaluation carrier', 'todo', 'user', $2) RETURNING id::text`,
		e.wsID, e.wsUserID).Scan(&wiID))
	taskID := e.mkTask(leader, "completed", true, squadID)
	_, err := e.pool.Exec(context.Background(),
		`UPDATE agent_task_queue SET work_item_id=$1 WHERE id=$2`, wiID, taskID)
	require.NoError(t, err)

	code, out = e.do("POST", "/api/work-items/"+wiID+"/squad-evaluated",
		map[string]any{"task_id": taskID, "outcome": "no_action", "reason": "no action required"})
	require.Equal(t, http.StatusCreated, code, out)
	var rows int
	require.NoError(t, e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM activity_log WHERE kind='squad_leader_evaluated'`).Scan(&rows))
	assert.Equal(t, 1, rows)

	code, _ = e.do("POST", "/api/work-items/"+wiID+"/squad-evaluated",
		map[string]any{"task_id": taskID, "outcome": "no_action"})
	assert.Equal(t, http.StatusCreated, code)
	require.NoError(t, e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM activity_log WHERE kind='squad_leader_evaluated'`).Scan(&rows))
	assert.Equal(t, 1, rows, "duplicate reporting idempotent")

	plain := e.mkTask(other, "completed", false, "")
	_, _ = e.pool.Exec(context.Background(), `UPDATE agent_task_queue SET work_item_id=$1 WHERE id=$2`, wiID, plain)
	code, _ = e.do("POST", "/api/work-items/"+wiID+"/squad-evaluated",
		map[string]any{"task_id": plain, "outcome": "action"})
	assert.Equal(t, http.StatusBadRequest, code)

	otherTask := e.mkTask(leader, "completed", true, squadID)
	code, _ = e.do("POST", "/api/work-items/"+wiID+"/squad-evaluated",
		map[string]any{"task_id": otherTask, "outcome": "action"})
	assert.Equal(t, http.StatusBadRequest, code, "the task does not belong to this work item")

	_, err = e.pool.Exec(context.Background(),
		`UPDATE squad SET leader_agent_id=$1 WHERE id=$2`, other, squadID)
	require.NoError(t, err)
	code, _ = e.do("POST", "/api/work-items/"+wiID+"/squad-evaluated",
		map[string]any{"task_id": taskID, "outcome": "action"})
	assert.Equal(t, http.StatusForbidden, code)

	code, out = e.do("POST", "/api/work-items/"+wiID+"/squad-evaluated",
		map[string]any{"task_id": taskID, "outcome": "maybe"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, apierr.Validation, out["error"])
}
