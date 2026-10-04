package pgrepo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedComments(t *testing.T, r *Repo, ws, wi, author string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		var rootID string
		require.NoError(t, r.q(ctx).QueryRow(ctx, `
INSERT INTO comment (workspace_id, work_item_id, author_type, author_id, content, created_at)
VALUES ($1,$2,'user',nullif($3,'')::uuid,$4, now() - make_interval(mins => $5)) RETURNING id::text`,
			ws, wi, author, fmt.Sprintf("root comment %d", i), (n-i)*3).Scan(&rootID))
		_, err := r.q(ctx).Exec(ctx, `
INSERT INTO comment (workspace_id, work_item_id, author_type, author_id, content, parent_id, created_at)
VALUES ($1,$2,'agent',nullif($3,'')::uuid,$4,$5::uuid, now() - make_interval(mins => $6))`,
			ws, wi, author, fmt.Sprintf("reply %d", i), rootID, (n-i)*3-1)
		require.NoError(t, err)
		if i == 0 {
			_, err = r.q(ctx).Exec(ctx, `
INSERT INTO comment (workspace_id, work_item_id, author_type, author_id, content, parent_id, created_at)
VALUES ($1,$2,'user',nullif($3,'')::uuid,'second-level reply',$4::uuid, now() - interval '1 minute')`,
				ws, wi, author, rootID)
			require.NoError(t, err)
		}
	}
}

func TestListCommentPageRootThreadPaging(t *testing.T) {
	r, ws, wi := timelineEnv(t)
	seedComments(t, r, ws, wi, seedUserID(t, r, ws), 5)

	ctx := context.Background()

	page1, err := r.ListCommentPage(ctx, wi, 2, "")
	require.NoError(t, err)
	roots1 := 0
	for _, c := range page1 {
		if c.ParentID == "" {
			roots1++
		}
	}
	assert.Equal(t, 2, roots1, "first page exactly 2 roots")
	assert.Len(t, page1, 4, "root4 + its reply + root3 + its reply = 4 rows (second-level reply belongs to root0, not on the first page)")

	var oldest time.Time
	for _, c := range page1 {
		if c.ParentID == "" && (oldest.IsZero() || c.CreatedAt.Before(oldest)) {
			oldest = c.CreatedAt
		}
	}
	before := oldest.UTC().Format(time.RFC3339Nano)
	rest, err := r.ListCommentPage(ctx, wi, 50, before)
	require.NoError(t, err)
	rootsRest := 0
	for _, c := range rest {
		if c.ParentID == "" {
			rootsRest++
		}
	}
	assert.Equal(t, 3, rootsRest, "next page has 3 roots left (2, 1, 0)")

	assert.Equal(t, 5, roots1+rootsRest)

	all, err := r.ListComments(ctx, wi)
	require.NoError(t, err)
	assert.Len(t, all, 11)
}
