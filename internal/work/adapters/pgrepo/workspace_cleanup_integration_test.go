package pgrepo

// Integration test for CleanupWorkspaceTables (FP-02 P1-1 orkome half): seeds
// one row in every work-domain business table, runs the cleanup, asserts the
// workspace is empty of domain rows and that DELETE workspace — which dies with
// 23503 work_item_status_workspace_id_fkey on even an untouched workspace —
// now succeeds. Requires TEST_DATABASE_URL; skips otherwise.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPG_CleanupWorkspaceTables(t *testing.T) {
	db, pool, ws, wi1, user := batchEnv(t)
	ctx := context.Background()

	// second work item for dependency / child-event edges
	var wi2 string
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1, 2, 'wcu second', 'todo', 'user', $2) RETURNING id::text`, ws, user).Scan(&wi2))

	// seed one row in every work-domain business table
	var agentID string
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO agent (workspace_id, name, runtime_config) VALUES ($1,'wcu-agent','{"provider":"fake","model":"fake-1"}')
RETURNING id::text`, ws).Scan(&agentID))

	var labelID string
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO label (workspace_id, name) VALUES ($1,'wcu-label') RETURNING id::text`, ws).Scan(&labelID))
	_, err := pool.Exec(ctx,
		`INSERT INTO work_item_to_label (work_item_id, label_id) VALUES ($1,$2)`, wi1, labelID)
	require.NoError(t, err)

	var commentID string
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO comment (workspace_id, work_item_id, author_type, author_id, content)
VALUES ($1,$2,'user',$3,'wcu comment') RETURNING id::text`, ws, wi1, user).Scan(&commentID))
	_, err = pool.Exec(ctx,
		`INSERT INTO comment_reaction (comment_id, user_id, emoji) VALUES ($1,$2,'+1')`, commentID, user)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO work_item_subscriber (work_item_id, user_id, source) VALUES ($1,$2,'creator')`, wi1, user)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
INSERT INTO work_item_reaction (work_item_id, user_id, emoji) VALUES ($1,$2,'+1')`, wi1, user)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
INSERT INTO activity_log (event_id, workspace_id, work_item_id, actor_type, actor_id, kind)
VALUES ('wcu-event', $1, $2, 'user', $3, 'test')`, ws, wi1, user)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
INSERT INTO work_item_dependency (work_item_id, depends_on_id, workspace_id) VALUES ($1,$2,$3)`, wi1, wi2, ws)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
INSERT INTO work_item_child_event (parent_id, child_id, workspace_id) VALUES ($1,$2,$3)`, wi1, wi2, ws)
	require.NoError(t, err)

	var projectID string
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO project (workspace_id, name) VALUES ($1,'wcu-project') RETURNING id::text`, ws).Scan(&projectID))
	_, err = pool.Exec(ctx, `
INSERT INTO project_resource (project_id, kind, uri) VALUES ($1,'repo','https://x.example/r')`, projectID)
	require.NoError(t, err)

	var viewID string
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO work_item_view (workspace_id, user_id, name) VALUES ($1,$2,'wcu view') RETURNING id::text`,
		ws, user).Scan(&viewID))
	_, err = pool.Exec(ctx, `
INSERT INTO work_item_view_preference (user_id, workspace_id, default_view_id) VALUES ($1,$2,$3)`, user, ws, viewID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO work_item_property_def (workspace_id, key, name, type) VALUES ($1,'wcu_key','WCU','text')`, ws)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
INSERT INTO work_item_source_context (workspace_id, source_digest, snapshot, created_by)
VALUES ($1,'wcu-digest','{}',$2)`, ws, user)
	require.NoError(t, err)

	var wakeupID string
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO work_item_wakeup (workspace_id, work_item_id, agent_id, kind)
VALUES ($1,$2,$3,'time') RETURNING id::text`, ws, wi1, agentID).Scan(&wakeupID))
	_, err = pool.Exec(ctx, `
INSERT INTO work_item_wakeup_receipt (wakeup_id, revision, event_key, workspace_id)
VALUES ($1, 1, 'wcu-event-key', $2)`, wakeupID, ws)
	require.NoError(t, err)

	// batchEnv TRUNCATEs work_item_status without re-seeding; the seeded status
	// catalog is exactly what broke bare workspace deletes (fp11 §五#10), so
	// seed one row explicitly.
	_, err = pool.Exec(ctx, `
INSERT INTO work_item_status (workspace_id, key, name, category, is_builtin, position)
VALUES ($1,'wcu-custom','WCU Custom','started',false,10)`, ws)
	require.NoError(t, err)

	// observability faces owned by the work domain (the search-index trigger
	// from batchEnv's work_item INSERT may have already queued wi1 — upsert)
	_, err = pool.Exec(ctx, `
INSERT INTO search_index_change (entity_type, entity_id, workspace_id, change_xid)
VALUES ('work_item', $1, $2, pg_current_xact_id())
ON CONFLICT (entity_type, entity_id) DO UPDATE SET workspace_id = $2, change_xid = pg_current_xact_id()`, wi1, ws)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
INSERT INTO client_usage_daily (workspace_id, user_id, client_type, install_id, activity_date,
  client_version, os, first_active_at, last_active_at)
VALUES ($1, $2, 'web', gen_random_uuid(), current_date, '1.0.0-test', 'windows', now(), now())`, ws, user)
	require.NoError(t, err)

	countOf := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, pool.QueryRow(ctx, query, args...).Scan(&n))
		return n
	}
	wsArg := []any{ws}
	pre := map[string]int64{}
	checks := []struct{ table, sql string }{
		{"comment", `SELECT count(*) FROM comment WHERE workspace_id=$1`},
		{"comment_reaction", `SELECT count(*) FROM comment_reaction WHERE comment_id IN (SELECT id FROM comment WHERE workspace_id=$1)`},
		{"activity_log", `SELECT count(*) FROM activity_log WHERE workspace_id=$1`},
		{"work_item", `SELECT count(*) FROM work_item WHERE workspace_id=$1`},
		{"work_item_to_label", `SELECT count(*) FROM work_item_to_label wl JOIN work_item w ON w.id=wl.work_item_id WHERE w.workspace_id=$1`},
		{"work_item_subscriber", `SELECT count(*) FROM work_item_subscriber s JOIN work_item w ON w.id=s.work_item_id WHERE w.workspace_id=$1`},
		{"work_item_reaction", `SELECT count(*) FROM work_item_reaction r JOIN work_item w ON w.id=r.work_item_id WHERE w.workspace_id=$1`},
		{"work_item_dependency", `SELECT count(*) FROM work_item_dependency WHERE workspace_id=$1`},
		{"work_item_child_event", `SELECT count(*) FROM work_item_child_event WHERE workspace_id=$1`},
		{"work_item_view", `SELECT count(*) FROM work_item_view WHERE workspace_id=$1`},
		{"work_item_view_preference", `SELECT count(*) FROM work_item_view_preference WHERE workspace_id=$1`},
		{"work_item_source_context", `SELECT count(*) FROM work_item_source_context WHERE workspace_id=$1`},
		{"work_item_property_def", `SELECT count(*) FROM work_item_property_def WHERE workspace_id=$1`},
		{"label", `SELECT count(*) FROM label WHERE workspace_id=$1`},
		{"project", `SELECT count(*) FROM project WHERE workspace_id=$1`},
		{"project_resource", `SELECT count(*) FROM project_resource pr JOIN project p ON p.id=pr.project_id WHERE p.workspace_id=$1`},
		{"work_item_wakeup", `SELECT count(*) FROM work_item_wakeup WHERE workspace_id=$1`},
		{"work_item_wakeup_receipt", `SELECT count(*) FROM work_item_wakeup_receipt WHERE workspace_id=$1`},
		{"search_index_change", `SELECT count(*) FROM search_index_change WHERE workspace_id=$1`},
		{"client_usage_daily", `SELECT count(*) FROM client_usage_daily WHERE workspace_id=$1`},
		{"work_item_status", `SELECT count(*) FROM work_item_status WHERE workspace_id=$1`},
	}
	for _, c := range checks {
		n := countOf(c.sql, wsArg...)
		assert.Positive(t, n, "precondition: %s should hold seeded rows", c.table)
		pre[c.table] = n
	}
	assert.Greater(t, len(pre), 15, "the seed must cover (nearly) the whole domain surface")

	require.NoError(t, CleanupWorkspaceTables(ctx, db, ws))

	for _, c := range checks {
		assert.Zero(t, countOf(c.sql, wsArg...), "%s must be empty after cleanup", c.table)
	}

	// The fp11 §五#10 scenario: with the work domain cleaned, the workspace
	// delete no longer dies on work_item_status_workspace_id_fkey. The agent
	// row seeded above is orchestration-domain (out of this helper's scope —
	// FixD wires the remaining domains), so the test removes it itself first.
	_, err = pool.Exec(ctx, `DELETE FROM agent WHERE workspace_id=$1`, ws)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, ws)
	require.NoError(t, err, "workspace delete must succeed after CleanupWorkspaceTables")

	// cleanup of an already-empty workspace is idempotent
	require.NoError(t, CleanupWorkspaceTables(ctx, db, ws))
}
