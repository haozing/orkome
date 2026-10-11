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

	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/internal/work/app"
	"orkome/migrations"
)

func timelineEnv(t *testing.T) (*Repo, string, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_wtl")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_wtl")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_wtl", 1)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `TRUNCATE activity_log, comment, work_item, work_item_status, project, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	idr := identitypg.New(pool, identitypg.Config{})
	u, err := idr.UpsertUserByEmail(ctx, "wtl@example.com", time.Now())
	require.NoError(t, err)
	wsFull := struct{ ID string }{}
	err = pool.QueryRow(ctx, `INSERT INTO workspace (slug, name, created_by) VALUES ($1,$2,$3) RETURNING id`,
		"wtl-ws", "T", u.ID).Scan(&wsFull.ID)
	require.NoError(t, err)
	ws := wsFull.ID
	var wi string
	err = pool.QueryRow(ctx, `INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1,1,'add RSS','todo','user',$2) RETURNING id::text`, ws, u.ID).Scan(&wi)
	require.NoError(t, err)
	return &Repo{DB: pg.WrapPool(pool)}, ws, wi
}

func TestListTimelineHydrationM1(t *testing.T) {
	r, ws, wi := timelineEnv(t)
	ctx := context.Background()

	for _, c := range []string{"decided to use sitemap instead of full replay", "acceptance-standard supplement: W3C validation passed"} {
		_, err := r.q(ctx).Exec(ctx, `INSERT INTO comment (workspace_id, work_item_id, author_type, author_id, content)
VALUES ($1,$2,'user',nullif($3,'')::uuid,$4)`, ws, wi, seedUserID(t, r, ws), c)
		require.NoError(t, err)
	}

	require.NoError(t, r.AppendActivity(ctx, "e1", ws, wi, "user", "", "work_item.created",
		map[string]any{"title": "add RSS", "status": "todo"}))
	require.NoError(t, r.AppendActivity(ctx, "task_settled:t1", ws, wi, "agent", "", "task_settled",
		map[string]any{"outcome": "completed", "agent_name": "verify-writer", "summary": "W3C validation passed, PR #142 opened"}))
	require.NoError(t, r.AppendActivity(ctx, "task_settled:t2", ws, wi, "agent", "", "task_settled",
		map[string]any{"outcome": "failed", "agent_name": "verify-writer", "failure_reason": "provider.timeout"}))

	entries, err := r.ListTimeline(ctx, wi, 50, "")
	require.NoError(t, err)

	var comments, activities, settled int
	content := map[string]app.TimelineEntry{}
	for _, e := range entries {
		content[e.Content] = e
		switch {
		case e.Kind == "comment":
			comments++
		case e.Kind == "activity":
			activities++
			if e.Payload["event"] == "task_settled" {
				settled++
			}
		}
	}

	assert.Equal(t, 2, comments, "exactly two comments (activity carries no comment-derived entries)")

	assert.Equal(t, 3, activities)
	assert.Equal(t, 2, settled, "both success and failure leave traces")

	assert.Equal(t, "created work item (add RSS)", content["created work item (add RSS)"].Content)
	assert.Equal(t, "task completed (verify-writer): W3C validation passed, PR #142 opened",
		content["task completed (verify-writer): W3C validation passed, PR #142 opened"].Content)
	assert.Equal(t, "task failed (verify-writer): provider.timeout",
		content["task failed (verify-writer): provider.timeout"].Content)

	ts := content["task failed (verify-writer): provider.timeout"]
	assert.Equal(t, "failed", ts.Payload["outcome"])
	assert.Equal(t, "task_settled", ts.Payload["event"])
}

func seedUserID(t *testing.T, r *Repo, ws string) string {
	t.Helper()
	var id string
	require.NoError(t, r.q(context.Background()).QueryRow(context.Background(),
		`SELECT creator_id::text FROM work_item WHERE workspace_id=$1 LIMIT 1`, ws).Scan(&id))
	return id
}
