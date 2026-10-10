package pgrepo

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/work/app"
)

func newSearchEnv(t *testing.T) (*pg.DB, string, string, *app.SearchService, *Repo) {
	t.Helper()
	db, pool, ws, _, user := batchEnv(t)
	repo := &Repo{DB: db}

	_, err := pool.Exec(context.Background(),
		`UPDATE workspace SET work_item_counter = 1 WHERE id=$1`, ws)
	require.NoError(t, err)
	return db, ws, user, app.NewSearchService(repo), repo
}

func seedItem(t *testing.T, db *pg.DB, ws, user, title, desc string) {
	t.Helper()
	_, err := db.Tx(context.Background()).Exec(context.Background(), `
INSERT INTO work_item (workspace_id, number, title, description, status_key, creator_type, creator_id)
VALUES ($1, (SELECT work_item_counter+1 FROM workspace WHERE id=$1), $2, $3, 'todo', 'user', $4)`,
		ws, title, desc, user)
	require.NoError(t, err)
	_, err = db.Tx(context.Background()).Exec(context.Background(),
		`UPDATE workspace SET work_item_counter = work_item_counter + 1 WHERE id=$1`, ws)
	require.NoError(t, err)
}

func TestB11_SearchSinglePathBelowThreshold(t *testing.T) {
	db, ws, user, svc, _ := newSearchEnv(t)
	seedItem(t, db, ws, user, "introduction to quantum computing", "")
	seedItem(t, db, ws, user, "irrelevant items", "quantum computing body hit")

	hits, err := svc.SearchFederated(context.Background(), ws, "quantum computing")
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "introduction to quantum computing", hits[0].Title)
	assert.Equal(t, "title", hits[0].Source)
}

func TestB11_SearchFederatedRRFAboveThreshold(t *testing.T) {
	db, ws, user, svc, _ := newSearchEnv(t)

	for i := 0; i < 21; i++ {
		seedItem(t, db, ws, user, fmt.Sprintf("quantum computing research %02d", i), "")
	}

	seedItem(t, db, ws, user, "quantum computing survey (dual-path hit)", "a systematic body survey of quantum computing")

	seedItem(t, db, ws, user, "body-only hit work item", "quantum computing body dedicated")

	_, err := db.Tx(context.Background()).Exec(context.Background(), `
INSERT INTO work_item (workspace_id, number, title, description, status_key, creator_type, creator_id)
VALUES ($1, (SELECT work_item_counter+1 FROM workspace WHERE id=$1), 'comment-only hit work item', '', 'todo', 'user', $2)`,
		ws, user)
	require.NoError(t, err)
	_, err = db.Tx(context.Background()).Exec(context.Background(), `
INSERT INTO comment (workspace_id, work_item_id, author_type, author_id, content)
SELECT $1, id, 'user', $2, 'comment discussing quantum computing hit' FROM work_item
WHERE workspace_id=$1 AND title='comment-only hit work item'`, ws, user)
	require.NoError(t, err)

	hits, err := svc.SearchFederated(context.Background(), ws, "quantum computing")
	require.NoError(t, err)
	require.Greater(t, len(hits), 21, "fusion should hit via body/comment paths")

	sources := map[string]int{}
	for _, h := range hits {
		sources[h.Source]++
	}
	assert.Equal(t, 22, sources["title"], "all 22 title routes included")
	assert.GreaterOrEqual(t, sources["body"], 1, "body path merged in")
	assert.GreaterOrEqual(t, sources["comment"], 1, "comment path merged in")

	assert.Equal(t, "quantum computing survey (dual-path hit)", hits[0].Title, "dual-path hits should rank first")

	assert.LessOrEqual(t, len(hits), 30)
}
