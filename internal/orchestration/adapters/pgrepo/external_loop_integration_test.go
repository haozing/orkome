package pgrepo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/orchestration/app"
)

func externalEnv(t *testing.T) (*pg.DB, string, string, string) {
	t.Helper()
	db, _, ws, engineAg := summaryEnv(t)
	r := New(db)
	ctx := context.Background()
	ext, err := r.CreateAgent(ctx, app.Agent{
		WorkspaceID: ws, Name: "ext-runner", RuntimeConfig: map[string]any{},
		MaxConcurrent: 2, Visibility: "public", RuntimeKind: "external",
	})
	require.NoError(t, err)
	assert.Equal(t, "external", ext.RuntimeKind)
	return db, ws, engineAg, ext.ID
}

func TestExternalClaimCAS(t *testing.T) {
	db, ws, _, extAg := externalEnv(t)
	r := New(db)
	ctx := context.Background()
	task, err := r.InsertTask(ctx, app.Task{
		WorkspaceID: ws, AgentID: extAg, Priority: 1, MaxAttempts: 1,
		Context: map[string]any{"trigger": "assignment"},
	})
	require.NoError(t, err)

	claimed, ok, err := r.ClaimExternalTask(ctx, ws, task.ID)
	require.NoError(t, err)
	require.True(t, ok, "first claim succeeds")
	assert.Equal(t, "running", claimed.Status)

	_, ok, err = r.ClaimExternalTask(ctx, ws, task.ID)
	require.NoError(t, err)
	assert.False(t, ok, "second claim rejected by CAS")
}

func TestLivenessSweepExemptsExternal(t *testing.T) {
	db, ws, engineAg, extAg := externalEnv(t)
	r := New(db)
	ctx := context.Background()

	mkRunning := func(agentID string) string {
		task, err := r.InsertTask(ctx, app.Task{
			WorkspaceID: ws, AgentID: agentID, Priority: 1, MaxAttempts: 1,
			Context: map[string]any{"trigger": "test"},
		})
		require.NoError(t, err)
		_, err = r.q(ctx).Exec(ctx,
			`UPDATE agent_task_queue SET status='running', started_at=now() WHERE id=$1`, task.ID)
		require.NoError(t, err)
		return task.ID
	}
	engineTask, extTask := mkRunning(engineAg), mkRunning(extAg)

	ids, err := r.FailStaleRunning(ctx, 0)
	require.NoError(t, err)
	assert.Contains(t, ids, engineTask, "engine tasks (no instance heartbeat) enter death candidates")
	assert.NotContains(t, ids, extTask, "external task exemption — no false kill (contract Phase 2a mandatory fix verification)")

	n, err := r.FailRunningAtStartup(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n, "startup recovery only terminalizes engine tasks")
	t2, ok, err := r.GetTask(ctx, ws, extTask)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "running", t2.Status, "external tasks survive server restarts")
}

func TestCountStaleExternalQueued(t *testing.T) {
	db, ws, _, extAg := externalEnv(t)
	r := New(db)
	ctx := context.Background()
	fresh, err := r.InsertTask(ctx, app.Task{WorkspaceID: ws, AgentID: extAg, Priority: 1, MaxAttempts: 1, Context: map[string]any{}})
	require.NoError(t, err)
	old, err := r.InsertTask(ctx, app.Task{WorkspaceID: ws, AgentID: extAg, Priority: 1, MaxAttempts: 1, Context: map[string]any{}})
	require.NoError(t, err)
	_, err = r.q(ctx).Exec(ctx,
		`UPDATE agent_task_queue SET created_at = now() - interval '2 hours' WHERE id=$1`, old.ID)
	require.NoError(t, err)

	n, err := r.CountStaleExternalQueued(ctx, 60)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n, "only over-age rows counted (fresh row %s excluded)", fresh)
}

func TestGetAgentIDByPATID(t *testing.T) {
	db, ws, _, extAg := externalEnv(t)
	r := New(db)
	ctx := context.Background()
	idr := identitypg.New(db.Pool(), identitypg.Config{})
	u, err := idr.UpsertUserByEmail(ctx, "binding-test@runtime.internal", time.Now())
	require.NoError(t, err)
	pat, err := idr.CreatePAT(ctx, u.ID, "bind", "hash-x", "ork_xxxx", nil, nil)
	require.NoError(t, err)

	_, err = db.Tx(ctx).Exec(ctx,
		`INSERT INTO agent_credential (workspace_id, agent_id, pat_id, name) VALUES ($1,$2,$3,'t')`,
		ws, extAg, pat.ID)
	require.NoError(t, err)

	got, ok, err := r.GetAgentIDByPATID(ctx, pat.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, extAg, got)

	_, ok, err = r.GetAgentIDByPATID(ctx, "00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	assert.False(t, ok, "unbound PAT finds nothing")
}
