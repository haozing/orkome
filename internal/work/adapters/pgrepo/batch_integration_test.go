package pgrepo

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/migrations"
)

func batchEnv(t *testing.T) (*pg.DB, *pgxpool.Pool, string, string, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_m85")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_m85")
	admin.Close(context.Background())

	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_m85", 1)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `TRUNCATE work_item_batch_item, work_item_batch,
		activity_log, comment, work_item, work_item_status, project, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	idr := identitypg.New(pool, identitypg.Config{})
	u, err := idr.UpsertUserByEmail(ctx, "m85@example.com", time.Now())
	require.NoError(t, err)
	wsFull := struct{ ID string }{}
	err = pool.QueryRow(ctx, `INSERT INTO workspace (slug, name, created_by) VALUES ($1,$2,$3) RETURNING id`,
		"m85-ws", "B", u.ID).Scan(&wsFull.ID)
	require.NoError(t, err)
	ws := wsFull.ID
	var wi string
	err = pool.QueryRow(ctx, `INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1,1,'batch host','todo','user',$2) RETURNING id::text`, ws, u.ID).Scan(&wi)
	require.NoError(t, err)
	return pg.WrapPool(pool), pool, ws, wi, u.ID
}
