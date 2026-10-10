package pgrepo

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/internal/work/app"
	"orkome/migrations"
)

type obsFixture struct {
	pool *pgxpool.Pool
	repo *Repo
	ctx  context.Context
	wsID string
}

func newObsFixture(t *testing.T) *obsFixture {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_workobs")
	_ = admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	pool, err := pgxpool.New(context.Background(), strings.Replace(base, "/"+cfg.Database, "/orkome_workobs", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)

	_, err = pool.Exec(context.Background(), `TRUNCATE search_index_change, search_index_prune_mark,
		client_usage_daily, agent_to_label, skill_to_label, autopilot_rule_version, autopilot_collaborator,
		workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	f := &obsFixture{pool: pool, repo: &Repo{DB: pg.WrapPool(pool)}, ctx: context.Background()}
	require.NoError(t, pool.QueryRow(f.ctx,
		`INSERT INTO workspace (name, slug) VALUES ('w','work-obs') RETURNING id::text`).Scan(&f.wsID))
	return f
}

func (f *obsFixture) insertWorkItem(t *testing.T, title string) string {
	t.Helper()
	var id string
	require.NoError(t, f.pool.QueryRow(f.ctx, `
INSERT INTO work_item (workspace_id, number, title, status_key, creator_id)
VALUES ($1, (SELECT coalesce(max(number),0)+1 FROM work_item w2 WHERE w2.workspace_id=$1), $2, 'todo', gen_random_uuid())
RETURNING id::text`, f.wsID, title).Scan(&id))
	return id
}

func (f *obsFixture) insertComment(t *testing.T, wiID string) string {
	t.Helper()
	var id string
	require.NoError(t, f.pool.QueryRow(f.ctx, `
INSERT INTO comment (work_item_id, workspace_id, author_type, content) VALUES ($1,$2,'user','c') RETURNING id::text`,
		wiID, f.wsID).Scan(&id))
	return id
}

func (f *obsFixture) insertProject(t *testing.T) string {
	t.Helper()
	var id string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO project (workspace_id, name) VALUES ($1,'p') RETURNING id::text`, f.wsID).Scan(&id))
	return id
}

func (f *obsFixture) ledgerCount(t *testing.T, entityID string) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM search_index_change WHERE entity_id=$1::uuid`, entityID).Scan(&n))
	return n
}

func (f *obsFixture) ledgerCountByType(t *testing.T, entityType string) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM search_index_change WHERE entity_type=$1`, entityType).Scan(&n))
	return n
}

func TestSearchIndexLedger_TriggersWritePerEntity(t *testing.T) {
	f := newObsFixture(t)
	wi := f.insertWorkItem(t, "title")
	cm := f.insertComment(t, wi)
	pj := f.insertProject(t)

	assert.Equal(t, 1, f.ledgerCount(t, wi), "work_item write path lands one ledger row")
	assert.Equal(t, 1, f.ledgerCount(t, cm))
	assert.Equal(t, 1, f.ledgerCount(t, pj))

	var xid1, xid2 string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT change_xid::text FROM search_index_change WHERE entity_type='work_item' AND entity_id=$1`, wi).Scan(&xid1))
	_, err := f.pool.Exec(f.ctx, `UPDATE work_item SET title='new  title' WHERE id=$1`, wi)
	require.NoError(t, err)
	assert.Equal(t, 1, f.ledgerCount(t, wi), "at most one row per entity (ON CONFLICT overwrite)")
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT change_xid::text FROM search_index_change WHERE entity_type='work_item' AND entity_id=$1`, wi).Scan(&xid2))
	assert.NotEqual(t, xid1, xid2, "update advances change_xid")

	_, err = f.pool.Exec(f.ctx, `UPDATE comment SET content='  change' WHERE id=$1`, cm)
	require.NoError(t, err)
	assert.Equal(t, 1, f.ledgerCount(t, cm))

	_, err = f.pool.Exec(f.ctx, `DELETE FROM comment WHERE id=$1`, cm)
	require.NoError(t, err)
	_, err = f.pool.Exec(f.ctx, `DELETE FROM work_item WHERE id=$1`, wi)
	require.NoError(t, err)
	assert.Equal(t, 1, f.ledgerCount(t, wi), "deletion also lands in the ledger (semantics: row missing from live table = deleted)")
	assert.Equal(t, 1, f.ledgerCount(t, cm))
}

func TestSearchIndexSweepOrphans_OnlyDeletedEntities(t *testing.T) {
	f := newObsFixture(t)
	wi := f.insertWorkItem(t, "alive")
	gone := f.insertWorkItem(t, "will be deleted")
	cm := f.insertComment(t, wi)
	pj := f.insertProject(t)

	_, err := f.pool.Exec(f.ctx, `DELETE FROM work_item WHERE id=$1`, gone)
	require.NoError(t, err)

	n, err := f.repo.SearchIndexSweepOrphans(f.ctx, time.Now().Add(-time.Hour), 100)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	n, err = f.repo.SearchIndexSweepOrphans(f.ctx, time.Now().Add(time.Hour), 100)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, f.ledgerCount(t, gone))
	assert.Equal(t, 1, f.ledgerCount(t, wi))
	assert.Equal(t, 1, f.ledgerCount(t, cm))
	assert.Equal(t, 1, f.ledgerCount(t, pj))

	_, err = f.pool.Exec(f.ctx, `DELETE FROM comment WHERE id=$1`, cm)
	require.NoError(t, err)
	n, err = f.repo.SearchIndexSweepOrphans(f.ctx, time.Now().Add(time.Hour), 100)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, f.ledgerCount(t, wi), "live rows unaffected by catch-up (retention window belongs to the prune worker)")
}

func TestPruneSearchIndexChanges_AdvancesMark(t *testing.T) {
	f := newObsFixture(t)
	old := f.insertWorkItem(t, "old")
	fresh := f.insertWorkItem(t, "new")

	_, err := f.pool.Exec(f.ctx, `
UPDATE search_index_change SET changed_at = now() - interval '40 days' WHERE entity_id=$1`, old)
	require.NoError(t, err)
	var oldXID string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT change_xid::text FROM search_index_change WHERE entity_id=$1`, old).Scan(&oldXID))

	_, ok, err := f.repo.SearchIndexPruneMark(f.ctx)
	require.NoError(t, err)
	assert.False(t, ok, "never cleaned: no watermark row")

	n, err := f.repo.PruneSearchIndexChanges(f.ctx, time.Now().Add(-30*24*time.Hour), 100)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only rows older than 40 days due")
	assert.Equal(t, 0, f.ledgerCount(t, old))
	assert.Equal(t, 1, f.ledgerCount(t, fresh), "rows inside the retention window untouched")

	mark, ok, err := f.repo.SearchIndexPruneMark(f.ctx)
	require.NoError(t, err)
	assert.True(t, ok, "watermark row exists after cleanup")
	assert.Equal(t, oldXID, mark, "watermark = highest xid deleted in this batch")

	n, err = f.repo.PruneSearchIndexChanges(f.ctx, time.Now().Add(-30*24*time.Hour), 100)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	mark2, _, err := f.repo.SearchIndexPruneMark(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, mark, mark2)
}

func TestClientUsageUpsert_SingleDailyRow(t *testing.T) {
	f := newObsFixture(t)
	var userID string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('obs@x.dev','obs') RETURNING id::text`).Scan(&userID))

	mk := func(ver string) app.ClientUsageReport {
		return app.ClientUsageReport{WorkspaceID: f.wsID, UserID: userID, ClientType: "web",
			InstallID: "11111111-1111-1111-1111-111111111111", ClientVersion: ver, OS: "windows"}
	}
	require.NoError(t, f.repo.ClientUsageUpsert(f.ctx, mk("1.0.0")))
	require.NoError(t, f.repo.ClientUsageUpsert(f.ctx, mk("1.1.0")))

	var n int
	var ver string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT count(*), max(client_version) FROM client_usage_daily WHERE user_id=$1`, userID).Scan(&n, &ver))
	assert.Equal(t, 1, n, "same identity key refreshes the same row (exactly one daily usage row)")
	assert.Equal(t, "1.1.0", ver, "metadata takes the new value")

	other := mk("1.1.0")
	other.InstallID = "22222222-2222-2222-2222-222222222222"
	require.NoError(t, f.repo.ClientUsageUpsert(f.ctx, other))
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM client_usage_daily WHERE user_id=$1`, userID).Scan(&n))
	assert.Equal(t, 2, n, "install dimension rows")
}

func TestResourceLabelMount_AgentAndSkill(t *testing.T) {
	f := newObsFixture(t)
	var agentID, skillID, agentOtherWs string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO agent (workspace_id, name) VALUES ($1,'a') RETURNING id::text`, f.wsID).Scan(&agentID))
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO skill (workspace_id, name) VALUES ($1,'s') RETURNING id::text`, f.wsID).Scan(&skillID))

	var otherWs string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO workspace (name, slug) VALUES ('w2','work-obs-2') RETURNING id::text`).Scan(&otherWs))
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO agent (workspace_id, name) VALUES ($1,'a2') RETURNING id::text`, otherWs).Scan(&agentOtherWs))

	lab, err := f.repo.UpsertLabel(f.ctx, f.wsID, "core", "#f00")
	require.NoError(t, err)

	ok, err := f.repo.AgentLabelTarget(f.ctx, f.wsID, agentID)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = f.repo.AgentLabelTarget(f.ctx, f.wsID, agentOtherWs)
	require.NoError(t, err)
	assert.False(t, ok, "cross-workspace agents invisible")
	ok, err = f.repo.SkillLabelTarget(f.ctx, f.wsID, skillID)
	require.NoError(t, err)
	assert.True(t, ok)

	require.NoError(t, f.repo.AttachAgentLabel(f.ctx, f.wsID, agentID, lab.ID))
	require.NoError(t, f.repo.AttachAgentLabel(f.ctx, f.wsID, agentID, lab.ID))
	require.NoError(t, f.repo.AttachSkillLabel(f.ctx, f.wsID, skillID, lab.ID))

	agentLabels, err := f.repo.ListAgentLabels(f.ctx, f.wsID, agentID)
	require.NoError(t, err)
	require.Len(t, agentLabels, 1)
	assert.Equal(t, lab.ID, agentLabels[0].ID)
	skillLabels, err := f.repo.ListSkillLabels(f.ctx, f.wsID, skillID)
	require.NoError(t, err)
	require.Len(t, skillLabels, 1)

	got, ok, err := f.repo.GetLabel(f.ctx, f.wsID, lab.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "core", got.Name)
	_, ok, err = f.repo.GetLabel(f.ctx, otherWs, lab.ID)
	require.NoError(t, err)
	assert.False(t, ok, "cross-workspace signatures invisible")

	require.NoError(t, f.repo.DetachAgentLabel(f.ctx, f.wsID, agentID, lab.ID))
	require.NoError(t, f.repo.DetachAgentLabel(f.ctx, f.wsID, agentID, lab.ID))
	agentLabels, err = f.repo.ListAgentLabels(f.ctx, f.wsID, agentID)
	require.NoError(t, err)
	assert.Empty(t, agentLabels)

	wi := f.insertWorkItem(t, "with signature")
	require.NoError(t, f.repo.SetWorkItemLabels(f.ctx, wi, []string{lab.ID}))
	require.NoError(t, f.repo.AttachAgentLabel(f.ctx, f.wsID, agentID, lab.ID))
	require.NoError(t, f.repo.AttachSkillLabel(f.ctx, f.wsID, skillID, lab.ID))
	require.NoError(t, f.repo.DeleteLabel(f.ctx, f.wsID, lab.ID))
	for _, q := range []string{
		`SELECT count(*) FROM work_item_to_label WHERE label_id=$1`,
		`SELECT count(*) FROM agent_to_label WHERE label_id=$1`,
		`SELECT count(*) FROM skill_to_label WHERE label_id=$1`,
	} {
		var n int
		require.NoError(t, f.pool.QueryRow(f.ctx, q, lab.ID).Scan(&n))
		assert.Equal(t, 0, n, "label definition deletion sweeps all attached faces")
	}
}
