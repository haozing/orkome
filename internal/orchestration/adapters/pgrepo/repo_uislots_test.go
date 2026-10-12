package pgrepo

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmigrations "github.com/haozing/ploykit/migrations"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"github.com/haozing/ploykit/platform/pg"

	"orkome/internal/orchestration/app"
	"orkome/migrations"
)

// 仿 repo_packtoolschema_test.go 的 TEST_DATABASE_URL 惯例：
// 未设置即跳过；建独立测试库 orkome_uislots，不触碰任何真实库、不跑 migrate-up 到共享库。
func newUISlotsEnv(t *testing.T) (*Repo, *pgxpool.Pool, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_uislots")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_uislots")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_uislots", 1)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(ctx, 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)

	idr := identitypg.New(pool, identitypg.Config{})
	u, err := idr.UpsertUserByEmail(ctx, "uislots@example.com", time.Now())
	require.NoError(t, err)
	slug := "ws-" + u.ID[:13] + "-uislots"
	var wsID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, created_by) VALUES ('uislots-ws', $1, $2) RETURNING id`,
		slug, u.ID).Scan(&wsID))
	return New(pg.WrapPool(pool)), pool, wsID
}

func TestPackUIEntrySlots_MigrationAndRepo(t *testing.T) {
	repo, pool, ws := newUISlotsEnv(t)
	ctx := context.Background()

	// 迁移 1036 形状：slots 列存在且可空。
	var nullAble string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT is_nullable FROM information_schema.columns
		 WHERE table_name='pack_ui_entry' AND column_name='slots'`).Scan(&nullAble))
	assert.Equal(t, "YES", nullAble, "迁移 1036 给 pack_ui_entry 加可空 slots 列")

	slots := []app.PackUISlot{{
		Slot: app.SlotWorkItemDetailTab, Label: "文章预览",
		URLTemplate: "https://ab.example/slot?item={workItemId}&t={uiToken}", Height: 480,
	}}

	// Upsert 带 slots：落库。
	require.NoError(t, repo.UpsertPackUIEntry(ctx, app.PackUIEntry{
		WorkspaceID: ws, Pack: "agentblog", Label: "内容工厂",
		URLTemplate: "https://ab.example/console", Slots: slots,
	}))

	// List：slots 随行返回。
	entries, err := repo.ListPackUIEntries(ctx, ws)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].Slots, 1)
	got := entries[0].Slots[0]
	assert.Equal(t, app.SlotWorkItemDetailTab, got.Slot)
	assert.Equal(t, "文章预览", got.Label)
	assert.Contains(t, got.URLTemplate, "{workItemId}")
	assert.Equal(t, 480, got.Height)

	// Upsert 不带 slots：整行覆盖语义，slots 清空（重装无 slots 版 pack 的卸载残留）。
	require.NoError(t, repo.UpsertPackUIEntry(ctx, app.PackUIEntry{
		WorkspaceID: ws, Pack: "agentblog", Label: "内容工厂",
		URLTemplate: "https://ab.example/console",
	}))
	entries, err = repo.ListPackUIEntries(ctx, ws)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Empty(t, entries[0].Slots, "重装不带 slots 覆盖为空")

	// 再写回 slots + 一个无 slots pack：List 空数组而非 nil（契约：前端零判空）。
	require.NoError(t, repo.UpsertPackUIEntry(ctx, app.PackUIEntry{
		WorkspaceID: ws, Pack: "agentblog", Label: "内容工厂",
		URLTemplate: "https://ab.example/console", Slots: slots,
	}))
	require.NoError(t, repo.UpsertPackUIEntry(ctx, app.PackUIEntry{
		WorkspaceID: ws, Pack: "plainpack", Label: "P",
		URLTemplate: "https://p.example",
	}))
	entries, err = repo.ListPackUIEntries(ctx, ws)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	byPack := map[string]app.PackUIEntry{}
	for _, e := range entries {
		byPack[e.Pack] = e
	}
	assert.Len(t, byPack["agentblog"].Slots, 1)
	require.NotNil(t, byPack["plainpack"].Slots, "NULL 列 COALESCE 为空数组")
	assert.Empty(t, byPack["plainpack"].Slots)

	// 跨 workspace 不可见。
	other, err := repo.ListPackUIEntries(ctx, "00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	assert.Empty(t, other)

	// Delete：uninstall 连 slots 一起删。
	require.NoError(t, repo.DeletePackUIEntry(ctx, ws, "agentblog"))
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM pack_ui_entry WHERE workspace_id=$1 AND pack='agentblog'`, ws).Scan(&n))
	assert.Zero(t, n)
}
