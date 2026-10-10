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
	"orkome/internal/orchestration/domain"
	"orkome/migrations"
)

func TestCancelByCommentSQLMirrorsPredicate(t *testing.T) {
	const frag = "status IN ('queued','deferred','dispatched')"
	if !strings.Contains(cancelByCommentSQL, frag) {
		t.Fatalf("cancelByCommentSQL missing the status vocabulary %q (drift vs domain.CancellableByTriggerComment)", frag)
	}
	for _, s := range domain.AllStatuses() {
		if strings.Contains(frag, "'"+s+"'") != domain.CancellableByTriggerComment(s) {
			t.Errorf("status %s: SQL vocabulary out of sync with domain.CancellableByTriggerComment", s)
		}
	}
}

type deferredEnv struct {
	ctx  context.Context
	repo *Repo
	pool *pgxpool.Pool
	ws   string
	user string
}

func newDeferredEnv(t *testing.T) *deferredEnv {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_odefer")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_odefer")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_odefer", 1)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `TRUNCATE agent_task_checkpoint, task_message, task_supplement, task_usage,
		agent_task_queue, agent_runtime, agent, squad, squad_member, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	idr := idpgrepo.New(pg.WrapPool(pool))
	u, err := idr.UpsertUserByEmail(ctx, "odefer@example.com")
	require.NoError(t, err)
	wsFull, _, err := idr.CreateWorkspace(ctx, "D", "odefer-ws", u.ID)
	require.NoError(t, err)

	e := &deferredEnv{ctx: ctx, repo: New(pg.WrapPool(pool)), pool: pool, user: u.ID}
	e.ws = wsFull.ID
	return e
}

func (e *deferredEnv) mkAgent(t *testing.T, name string) app.Agent {
	t.Helper()
	a, err := e.repo.CreateAgent(e.ctx, app.Agent{WorkspaceID: e.ws, Name: name, MaxConcurrent: 4,
		Visibility: "public", RuntimeConfig: map[string]any{"provider": "fake", "model": "m"}})
	require.NoError(t, err)
	return a
}

func (e *deferredEnv) mkWorkItem(t *testing.T) string {
	t.Helper()
	var id string
	require.NoError(t, e.pool.QueryRow(e.ctx,
		`INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
		 VALUES ($1, (SELECT coalesce(max(number),0)+1 FROM work_item WHERE workspace_id=$1), 'WI', 'todo', 'user', $2)
		 RETURNING id::text`,
		e.ws, e.user).Scan(&id))
	return id
}

func (e *deferredEnv) mkQueued(t *testing.T, agentID, wi string, ctxJSON map[string]any) app.Task {
	t.Helper()
	task, err := e.repo.InsertTask(e.ctx, app.Task{
		WorkspaceID: e.ws, AgentID: agentID, WorkItemID: wi, MaxAttempts: 2,
		Context: ctxJSON, OriginatorUserID: e.user, OriginatorSource: domain.SrcDirectHuman,
	})
	require.NoError(t, err)
	return task
}

func (e *deferredEnv) mkDeferred(t *testing.T, agentID, wi string, ctxJSON map[string]any) app.Task {
	t.Helper()
	task, err := e.repo.InsertDeferredTask(e.ctx, app.Task{
		WorkspaceID: e.ws, AgentID: agentID, WorkItemID: wi, MaxAttempts: 2,
		Context: ctxJSON, OriginatorUserID: e.user, OriginatorSource: domain.SrcDirectHuman,
		TriggerEvidenceKind: domain.EvidenceMemberComment,
	})
	require.NoError(t, err)
	return task
}

func (e *deferredEnv) mkRunning(t *testing.T, agentID, wi string, ctxJSON map[string]any) app.Task {
	t.Helper()
	task := e.mkQueued(t, agentID, wi, ctxJSON)
	claimed, ok, err := e.repo.ClaimNext(e.ctx, agentID, 6)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, task.ID, claimed.ID)
	started, err := e.repo.StartTask(e.ctx, task.ID, "inst-test", "eino")
	require.NoError(t, err)
	assert.Equal(t, domain.Running, started.Status)
	return task
}

func (e *deferredEnv) statuses(t *testing.T, wi, agentID string) map[string]int {
	t.Helper()
	rows, err := e.pool.Query(e.ctx,
		`SELECT status, count(*) FROM agent_task_queue WHERE work_item_id=$1::uuid AND agent_id=$2::uuid GROUP BY 1`, wi, agentID)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		require.NoError(t, rows.Scan(&s, &n))
		out[s] = n
	}
	return out
}

func TestDeferred_InsertDoesNotOccupyFence(t *testing.T) {
	e := newDeferredEnv(t)
	a := e.mkAgent(t, "d1")
	wi := e.mkWorkItem(t)

	e.mkRunning(t, a.ID, wi, map[string]any{"trigger": "assignment"})
	e.mkDeferred(t, a.ID, wi, map[string]any{"trigger": "comment", "trigger_comment_id": "c1"})
	q := e.mkQueued(t, a.ID, wi, map[string]any{"trigger": "comment", "trigger_comment_id": "c2"})
	assert.NotEmpty(t, q.ID, "deferred does not occupy the slot: newly created queued does not hit the I1 fence")
	st := e.statuses(t, wi, a.ID)
	assert.Equal(t, 1, st[domain.Queued], "newly created queued succeeds")
}

func TestPromoteDeferred_SlotFence(t *testing.T) {
	e := newDeferredEnv(t)
	a := e.mkAgent(t, "d2")
	wi := e.mkWorkItem(t)

	e.mkRunning(t, a.ID, wi, map[string]any{"trigger": "assignment"})
	e.mkDeferred(t, a.ID, wi, map[string]any{"trigger": "comment", "trigger_comment_id": "c1"})
	d2 := e.mkDeferred(t, a.ID, wi, map[string]any{"trigger": "comment", "trigger_comment_id": "c2"})

	_, err := e.pool.Exec(e.ctx,
		`UPDATE agent_task_queue SET created_at = created_at + interval '1 second' WHERE id=$1`, d2.ID)
	require.NoError(t, err)

	promoted, err := e.repo.PromoteDeferred(e.ctx)
	require.NoError(t, err)
	assert.Empty(t, promoted, "no promotion while a running task is in the slot")

	_, err = e.pool.Exec(e.ctx, `UPDATE agent_task_queue SET status='completed', completed_at=now() WHERE work_item_id=$1::uuid AND status='running'`, wi)
	require.NoError(t, err)
	promoted, err = e.repo.PromoteDeferred(e.ctx)
	require.NoError(t, err)
	require.Len(t, promoted, 1, "multiple deferred in the same slot promote only one row (internal archive 05 §2.7)")
	assert.Equal(t, domain.Queued, promoted[0].Status)
	assert.Equal(t, "c1", promoted[0].Context["trigger_comment_id"], "first registered first promoted (DISTINCT ON created_at)")

	promoted, err = e.repo.PromoteDeferred(e.ctx)
	require.NoError(t, err)
	assert.Empty(t, promoted)

	st := e.statuses(t, wi, a.ID)
	assert.Equal(t, 1, st[domain.Queued])
	assert.Equal(t, 1, st[domain.Deferred], "unpromoted stays deferred")
	assert.Equal(t, 1, st[domain.Completed])
}

func TestPromoteDeferred_ExcludesMediaPendingAndFutureFireAt(t *testing.T) {
	e := newDeferredEnv(t)
	a := e.mkAgent(t, "d3")
	wi := e.mkWorkItem(t)

	mediaRow, err := e.pool.Exec(e.ctx, `
INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status, max_attempts, context)
VALUES ($1, $2, $3::uuid, 'deferred', 2, jsonb_build_object('channel_task_media_pending','true'))`,
		e.ws, a.ID, wi)
	require.NoError(t, err)
	require.EqualValues(t, 1, mediaRow.RowsAffected())

	_, err = e.pool.Exec(e.ctx, `
INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status, max_attempts, context, fire_at)
VALUES ($1, $2, $3::uuid, 'deferred', 2, '{}'::jsonb, now() + interval '5 seconds')`,
		e.ws, a.ID, wi)
	require.NoError(t, err)

	promoted, err := e.repo.PromoteDeferred(e.ctx)
	require.NoError(t, err)
	assert.Empty(t, promoted, "no promotion while media pending / fire_at not due")

	_, err = e.pool.Exec(e.ctx, `UPDATE agent_task_queue SET fire_at=now() - interval '1 second' WHERE work_item_id=$1::uuid AND fire_at IS NOT NULL`, wi)
	require.NoError(t, err)
	promoted, err = e.repo.PromoteDeferred(e.ctx)
	require.NoError(t, err)
	assert.Empty(t, promoted, "in-slot media-pending deferred occupies the I1 fence — no promotion (otherwise UPDATE hits the unique index)")

	_, err = e.pool.Exec(e.ctx,
		`UPDATE agent_task_queue SET context = context - 'channel_task_media_pending' WHERE work_item_id=$1::uuid AND context->>'channel_task_media_pending'='true'`, wi)
	require.NoError(t, err)
	promoted, err = e.repo.PromoteDeferred(e.ctx)
	require.NoError(t, err)
	require.Len(t, promoted, 1, "promotable once media ready (exactly one row per statement per slot)")
	st := e.statuses(t, wi, a.ID)
	assert.Equal(t, 1, st[domain.Queued])
	assert.Equal(t, 1, st[domain.Deferred], "unpromoted stays deferred")
}

func TestClaimNext_DoesNotClaimDeferred(t *testing.T) {
	e := newDeferredEnv(t)
	a := e.mkAgent(t, "d4")
	wi := e.mkWorkItem(t)
	e.mkDeferred(t, a.ID, wi, map[string]any{"trigger": "comment", "trigger_comment_id": "c1"})

	_, ok, err := e.repo.ClaimNext(e.ctx, a.ID, 6)
	require.NoError(t, err)
	assert.False(t, ok, "ClaimNext must only take queued (deferred excepted)")

	promoted, err := e.repo.PromoteDeferred(e.ctx)
	require.NoError(t, err)
	require.Len(t, promoted, 1)
	claimed, ok, err := e.repo.ClaimNext(e.ctx, a.ID, 6)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, promoted[0].ID, claimed.ID)
}

func TestCancelTasksByTriggerComment(t *testing.T) {
	e := newDeferredEnv(t)
	a := e.mkAgent(t, "d5")
	wi := e.mkWorkItem(t)
	wi2 := e.mkWorkItem(t)

	e.mkRunning(t, a.ID, wi, map[string]any{"trigger": "comment", "trigger_comment_id": "cX"})
	d1 := e.mkDeferred(t, a.ID, wi, map[string]any{"trigger": "comment", "trigger_comment_id": "cX"})
	q1 := e.mkQueued(t, a.ID, wi2, map[string]any{"trigger": "comment", "trigger_comment_id": "cX"})
	e.mkDeferred(t, a.ID, wi2, map[string]any{"trigger": "comment", "trigger_comment_id": "cOther"})
	e.mkQueued(t, a.ID, wi, map[string]any{"trigger": "comment", "trigger_comment_id": "cOther"})

	ids, err := e.repo.CancelTasksByTriggerComment(e.ctx, e.ws, "cX")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{d1.ID, q1.ID}, ids, "cX's deferred+queued two rows cancelled; running not")

	st := e.statuses(t, wi, a.ID)
	assert.Equal(t, 1, st[domain.Cancelled], "cX deferred cancelled")
	assert.Equal(t, 1, st[domain.Running], "running does not take this face (stop-request semantics go elsewhere, internal archive 05 §2.5)")
	assert.Equal(t, 1, st[domain.Queued], "cOther's queued untouched")
	st2 := e.statuses(t, wi2, a.ID)
	assert.Equal(t, 1, st2[domain.Cancelled], "cX queued (another work item slot) cancelled")
	assert.Equal(t, 1, st2[domain.Deferred], "cOther's deferred untouched")

	ids, err = e.repo.CancelTasksByTriggerComment(e.ctx, e.ws, "cX")
	require.NoError(t, err)
	assert.Empty(t, ids)
}
