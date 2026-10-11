package pgrepo

import (
	"context"
	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	"os"
	"path/filepath"
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

type agentAccessEnv struct {
	ctx     context.Context
	repo    *Repo
	pool    *pgxpool.Pool
	ws      string
	otherWs string
	user    string
}

func newAgentAccessEnv(t *testing.T) *agentAccessEnv {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_oaccess")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_oaccess")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_oaccess", 1)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)

	idr := identitypg.New(pool, identitypg.Config{})
	u, err := idr.UpsertUserByEmail(ctx, "oaccess@example.com", time.Now())
	require.NoError(t, err)
	mkws := func(name string) string {
		var id string
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO workspace (name, created_by) VALUES ($1,$2) RETURNING id`, name, u.ID).Scan(&id))
		_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, id, u.ID)
		require.NoError(t, err)
		return id
	}
	wsFull, otherFull := mkws("oaccess-ws"), mkws("oaccess-ws2")
	return &agentAccessEnv{ctx: ctx, repo: New(pg.WrapPool(pool)), pool: pool,
		ws: wsFull.ID, otherWs: otherFull.ID, user: u.ID}
}

func (e *agentAccessEnv) mkAgent(t *testing.T, name, visibility string) app.Agent {
	t.Helper()
	a, err := e.repo.CreateAgent(e.ctx, app.Agent{WorkspaceID: e.ws, Name: name, MaxConcurrent: 4,
		Visibility: visibility, RuntimeConfig: map[string]any{"provider": "fake", "model": "m"}})
	require.NoError(t, err)
	return a
}

func columnExists(t *testing.T, pool *pgxpool.Pool, table, column string) bool {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.columns WHERE table_name=$1 AND column_name=$2`,
		table, column).Scan(&n))
	return n > 0
}

func TestAgentAccess_MigrationShapeAndReplay(t *testing.T) {
	e := newAgentAccessEnv(t)
	pool, ctx := e.pool, e.ctx

	require.True(t, columnExists(t, pool, "agent", "conversation_starters"), "conversation_starters column")
	require.True(t, columnExists(t, pool, "agent_invocation_target", "id"))
	require.True(t, columnExists(t, pool, "agent_invocation_target", "target_user_id"))
	require.False(t, columnExists(t, pool, "agent_invocation_target", "target_id"),
		"007 placeholder column (target_id text) should have been removed by the 028 reshape")

	a := e.mkAgent(t, "shape-agent", "public")
	agentID := a.ID

	_, err := pool.Exec(ctx, `INSERT INTO agent_invocation_target (agent_id, target_type) VALUES ($1,'member')`, agentID)
	require.Error(t, err, "member type missing user_id violates CHECK")
	_, err = pool.Exec(ctx,
		`INSERT INTO agent_invocation_target (agent_id, target_type, target_user_id) VALUES ($1,'workspace',$2)`, agentID, e.user)
	require.Error(t, err, "workspace type with user_id violates CHECK")

	_, err = pool.Exec(ctx, `INSERT INTO agent_invocation_target (agent_id, target_type) VALUES ($1,'workspace')`, agentID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO agent_invocation_target (agent_id, target_type) VALUES ($1,'workspace')`, agentID)
	require.Error(t, err, "duplicate workspace rows violate UNIQUE NULLS NOT DISTINCT")

	_, err = pool.Exec(ctx, `DELETE FROM agent WHERE id=$1`, agentID)
	require.NoError(t, err)
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM agent_invocation_target WHERE agent_id=$1`, agentID).Scan(&n))
	assert.Zero(t, n, "agent deletion cascades the allowlist clean")

	m := pgmigrate.New(pool, migrations.FS, ".")
	steps := migrationsFrom(t, "028")
	rolled, err := m.Down(ctx, steps)
	require.NoError(t, err)
	require.Len(t, rolled, steps)
	require.Equal(t, "028", rolled[len(rolled)-1], "the last rolled back should be 028")
	require.True(t, columnExists(t, pool, "agent_invocation_target", "target_id"), "down restores the 007 placeholder shape")
	require.True(t, columnExists(t, pool, "agent_invocation_target", "agent_id"))
	require.True(t, columnExists(t, pool, "agent", "conversation_starters"),
		"conversation_starters column declared by 007, not rolled back by 028 down")

	applied, err := m.Up(ctx, 0)
	require.NoError(t, err)
	require.Len(t, applied, steps)
	require.Equal(t, "028", applied[0])
	require.True(t, columnExists(t, pool, "agent_invocation_target", "target_user_id"))
	require.False(t, columnExists(t, pool, "agent_invocation_target", "target_id"))
}

func migrationsFrom(t *testing.T, from string) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "migrations", "*_*.up.sql"))
	require.NoError(t, err)
	n := 0
	for _, f := range files {
		ver := strings.Split(filepath.Base(f), "_")[0]
		if ver >= from {
			n++
		}
	}
	require.Positive(t, n)
	return n
}

func TestAgentAccess_StartersRoundtrip(t *testing.T) {
	e := newAgentAccessEnv(t)

	created, err := e.repo.CreateAgent(e.ctx, app.Agent{WorkspaceID: e.ws, Name: "starter-a",
		MaxConcurrent: 2, Visibility: "public",
		RuntimeConfig:        map[string]any{"provider": "fake", "model": "m"},
		ConversationStarters: []app.ConversationStarter{{Label: "summarize"}, {Label: "dedup check"}}})
	require.NoError(t, err)
	assert.Equal(t, []app.ConversationStarter{{Label: "summarize"}, {Label: "dedup check"}}, created.ConversationStarters)

	got, ok, err := e.repo.GetAgent(e.ctx, e.ws, created.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, []app.ConversationStarter{{Label: "summarize"}, {Label: "dedup check"}}, got.ConversationStarters)

	plain := e.mkAgent(t, "starter-b", "public")
	got, ok, err = e.repo.GetAgent(e.ctx, e.ws, plain.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Empty(t, got.ConversationStarters)
	assert.NotNil(t, got.ConversationStarters, "read-back normalized to [] (responses render arrays not null)")

	rc := map[string]any{"provider": "fake", "model": "m"}
	updated, ok, err := e.repo.UpdateAgent(e.ctx, app.Agent{ID: created.ID, WorkspaceID: e.ws,
		Name: "starter-a", Visibility: "public", MaxConcurrent: 2, RuntimeConfig: rc,
		ConversationStarters: []app.ConversationStarter{{Label: "new"}}})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, []app.ConversationStarter{{Label: "new"}}, updated.ConversationStarters)

	updated, ok, err = e.repo.UpdateAgent(e.ctx, app.Agent{ID: created.ID, WorkspaceID: e.ws,
		Name: "starter-a", Visibility: "public", MaxConcurrent: 2, RuntimeConfig: rc,
		ConversationStarters: []app.ConversationStarter{}})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Empty(t, updated.ConversationStarters)
}

func TestAgentAccess_TargetsReplaceListGet(t *testing.T) {
	e := newAgentAccessEnv(t)

	a1 := e.mkAgent(t, "target-a", "public_to")
	a2 := e.mkAgent(t, "target-b", "public_to")

	require.NoError(t, e.repo.ReplaceInvocationTargets(e.ctx, e.ws, a1.ID,
		[]app.InvocationTarget{{Type: "member", UserID: e.user}, {Type: "workspace"}}))
	got, err := e.repo.GetInvocationTargets(e.ctx, e.ws, a1.ID)
	require.NoError(t, err)
	assert.Equal(t, []app.InvocationTarget{{Type: "member", UserID: e.user}, {Type: "workspace"}}, got)

	all, err := e.repo.ListInvocationTargets(e.ctx, e.ws)
	require.NoError(t, err)
	assert.Equal(t, []app.InvocationTarget{{Type: "member", UserID: e.user}, {Type: "workspace"}}, all[a1.ID])
	assert.Empty(t, all[a2.ID], "non-allowlist agents do not enter the map")

	require.NoError(t, e.repo.ReplaceInvocationTargets(e.ctx, e.ws, a1.ID, nil))
	got, err = e.repo.GetInvocationTargets(e.ctx, e.ws, a1.ID)
	require.NoError(t, err)
	assert.Empty(t, got)

	require.NoError(t, e.repo.ReplaceInvocationTargets(e.ctx, e.ws, a1.ID,
		[]app.InvocationTarget{{Type: "workspace"}}))
	foreign, err := e.repo.GetInvocationTargets(e.ctx, e.otherWs, a1.ID)
	require.NoError(t, err)
	assert.Empty(t, foreign, "cross-workspace predicate (I12): allowlist invisible")

	foreignAll, err := e.repo.ListInvocationTargets(e.ctx, e.otherWs)
	require.NoError(t, err)
	assert.NotContains(t, foreignAll, a1.ID)

	require.NoError(t, e.repo.ReplaceInvocationTargets(e.ctx, e.otherWs, a1.ID,
		[]app.InvocationTarget{{Type: "workspace"}}))
	got, err = e.repo.GetInvocationTargets(e.ctx, e.ws, a1.ID)
	require.NoError(t, err)
	assert.Len(t, got, 1, "ws predicate-mismatched replacement must not write rows")

	require.NoError(t, e.repo.ReplaceInvocationTargets(e.ctx, e.ws, a2.ID,
		[]app.InvocationTarget{{Type: "member", UserID: e.user}}))
	all, err = e.repo.ListInvocationTargets(e.ctx, e.ws)
	require.NoError(t, err)
	assert.Len(t, all[a2.ID], 1)
}
