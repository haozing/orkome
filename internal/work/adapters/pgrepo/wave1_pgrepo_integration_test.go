package pgrepo

// Repo-level regression for the wave-1 fixes that live in pgrepo/repo.go
// (FP-11 P1-1 exhausted cursor sentinel + P1-2 last_activity_at bump).
// Requires TEST_DATABASE_URL; skips otherwise.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/work/app"
)

func TestPG_Wave1RepoFixes(t *testing.T) {
	db, pool, ws, wi, user := batchEnv(t)
	repo := &Repo{DB: db}
	ctx := context.Background()

	// seed a second todo item so paging has two rows
	var wi2 string
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1, 2, 'wave1 second', 'todo', 'user', $2) RETURNING id::text`, ws, user).Scan(&wi2))

	t.Run("list_work_items_exhausted_cursor_is_empty_sentinel", func(t *testing.T) {
		items, next, err := repo.ListWorkItems(ctx, app.ListQuery{
			WorkspaceID: ws, StatusKeys: []string{"todo"}, Limit: 1,
		})
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.NotEmpty(t, next, "a page that may have more rows carries a cursor")
		assert.NotContains(t, next, "+", "cursor timestamp must be UTC Z-form: a '+08:00' offset in a query param decodes '+' to a space and the replay 500s (PG 22007)")
		assert.Contains(t, next, "|", "cursor keeps the ts|uuid shape")

		// the sentinel contract: the repo returns "" when exhausted and the
		// HTTP layer omits the key (tested in adapters/http)
		items2, next2, err := repo.ListWorkItems(ctx, app.ListQuery{
			WorkspaceID: ws, StatusKeys: []string{"todo"}, Limit: 1, Cursor: next,
		})
		require.NoError(t, err)
		require.Len(t, items2, 1)
		assert.Empty(t, next2, "exhausted page must yield the empty sentinel")

		assert.Equal(t, wi, items2[0].ID, "second page returns the older row (created_at DESC keyset)")
	})

	t.Run("update_work_item_bumps_last_activity_at", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`UPDATE work_item SET last_activity_at = now() - interval '2 hours' WHERE id=$1`, wi)
		require.NoError(t, err)
		var before time.Time
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT last_activity_at FROM work_item WHERE id=$1`, wi).Scan(&before))

		title := "wave1 renamed"
		_, err = repo.UpdateWorkItem(ctx, ws, wi, app.WorkItemPatch{Title: &title}, 0)
		require.NoError(t, err)

		var after time.Time
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT last_activity_at FROM work_item WHERE id=$1`, wi).Scan(&after))
		assert.True(t, after.Sub(before) > time.Hour,
			"core PUT must bump last_activity_at: before=%v after=%v", before, after)
	})
}
