package pgrepo

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	idpgrepo "orkome/internal/identity/adapters/pgrepo"
	"orkome/internal/orchestration/app"
	"orkome/migrations"
)

func summaryEnv(t *testing.T) (*pg.DB, *pgxpool.Pool, string, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_osummary")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_osummary")
	admin.Close(context.Background())

	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_osummary", 1)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `TRUNCATE task_message, agent_task_queue, agent, work_item, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	idr := idpgrepo.New(pg.WrapPool(pool))
	u, err := idr.UpsertUserByEmail(ctx, "osummary@example.com")
	require.NoError(t, err)
	wsFull, _, err := idr.CreateWorkspace(ctx, "S", "osummary-ws", u.ID)
	require.NoError(t, err)
	ws := wsFull.ID
	r := New(pg.WrapPool(pool))
	ag, err := r.CreateAgent(ctx, app.Agent{WorkspaceID: ws, Name: "sum-agent", RuntimeConfig: map[string]any{}, MaxConcurrent: 2, Visibility: "public"})
	require.NoError(t, err)
	return pg.WrapPool(pool), pool, ws, ag.ID
}

func seedRunningTask(t *testing.T, r *Repo, ws, agentID string) string {
	t.Helper()
	task, err := r.InsertTask(context.Background(), app.Task{
		WorkspaceID: ws, AgentID: agentID, Priority: 1, MaxAttempts: 1,
		Context: map[string]any{"trigger": "test"},
	})
	require.NoError(t, err)
	_, err = r.q(context.Background()).Exec(context.Background(),
		`UPDATE agent_task_queue SET status='dispatched' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	_, err = r.StartTask(context.Background(), task.ID, "it-test", "eino")
	require.NoError(t, err)
	return task.ID
}

func TestCompleteRunSummaryFromLastText(t *testing.T) {
	db, _, ws, ag := summaryEnv(t)
	r := New(db)
	ctx := context.Background()
	id := seedRunningTask(t, r, ws, ag)

	_, err := r.AppendTaskMessage(ctx, ws, id, "text", "", "midway thought: still researching library selection", false)
	require.NoError(t, err)
	_, err = r.AppendTaskMessage(ctx, ws, id, "text", "", "conclusion: adopt go-feed, defer Atom to the next iteration", false)
	require.NoError(t, err)
	_, err = r.AppendTaskMessage(ctx, ws, id, "tool_result", "c1", "raw tool output", false)
	require.NoError(t, err)

	require.NoError(t, r.CompleteRun(ctx, id, "Sprint 2: implemented /feed.xml with go-feed, summary mode only. Added 5-minute in-memory cache with stampede protection. Unit tests cover escaping, date formatting and empty-corpus. Integration test green locally. Remaining: full-text mode blocked on paywall flag API, W3C validation pending CI wiring.", "sess-1", "eino", app.UsageReport{Provider: "p", Model: "m"}))

	task, ok, err := r.GetTask(ctx, ws, id)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "conclusion: adopt go-feed, defer Atom to the next iteration", task.Summary)
}

func TestCompleteRunSummaryFallbackToResult(t *testing.T) {
	db, _, ws, ag := summaryEnv(t)
	r := New(db)
	ctx := context.Background()
	id := seedRunningTask(t, r, ws, ag)

	long := strings.Repeat("R", 600)
	require.NoError(t, r.CompleteRun(ctx, id, long, "", "eino", app.UsageReport{}))
	task, ok, err := r.GetTask(ctx, ws, id)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, strings.Repeat("R", 500), task.Summary)
}

func TestFailRunSummaryLeavesTrace(t *testing.T) {
	db, _, ws, ag := summaryEnv(t)
	r := New(db)
	ctx := context.Background()
	id := seedRunningTask(t, r, ws, ag)

	_, err := r.AppendTaskMessage(ctx, ws, id, "text", "", "halfway through: cache layer ready, waiting for upstream response", false)
	require.NoError(t, err)

	attempted, err := r.FailRun(ctx, id, "provider.timeout", "context deadline exceeded")
	require.NoError(t, err)
	assert.False(t, attempted)

	task, ok, err := r.GetTask(ctx, ws, id)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "halfway through: cache layer ready, waiting for upstream response", task.Summary)
}

func TestFailRunRetryChildCarriesPriorSummary(t *testing.T) {
	db, _, ws, ag := summaryEnv(t)
	r := New(db)
	ctx := context.Background()
	task, err := r.InsertTask(ctx, app.Task{
		WorkspaceID: ws, AgentID: ag, Priority: 1, MaxAttempts: 3,
		Context: map[string]any{"trigger": "test"},
	})
	require.NoError(t, err)
	_, err = r.q(ctx).Exec(ctx, `UPDATE agent_task_queue SET status='dispatched' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	_, err = r.StartTask(ctx, task.ID, "it-test", "eino")
	require.NoError(t, err)
	_, err = r.AppendTaskMessage(ctx, ws, task.ID, "text", "", "cache layer ready, waiting for upstream response", false)
	require.NoError(t, err)

	attempted, err := r.FailRun(ctx, task.ID, "runtime_recovery", "engine instance heartbeat absent beyond grace window")
	require.NoError(t, err)
	assert.True(t, attempted, "runtime_recovery is in the retry allowlist with MaxAttempts=3, should produce a retry subtask")

	kids, err := r.q(ctx).Query(ctx,
		`SELECT context->>'prior_summary', context->>'prior_failure_reason' FROM agent_task_queue WHERE retry_of_task_id=$1`, task.ID)
	require.NoError(t, err)
	defer kids.Close()
	require.True(t, kids.Next())
	var prior, reason string
	require.NoError(t, kids.Scan(&prior, &reason))
	assert.Equal(t, "cache layer ready, waiting for upstream response", prior)
	assert.Equal(t, "runtime_recovery", reason)
}

func TestFailRunSummaryFallbackReasonError(t *testing.T) {
	db, _, ws, ag := summaryEnv(t)
	r := New(db)
	ctx := context.Background()
	id := seedRunningTask(t, r, ws, ag)
	_, err := r.FailRun(ctx, id, "engine_error", "boom")
	require.NoError(t, err)
	task, ok, err := r.GetTask(ctx, ws, id)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "engine_error: boom", task.Summary)
}
