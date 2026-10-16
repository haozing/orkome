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

// 集成页终态 W1（docs/integrations-page-design-2026-10-15.md §2.3）：手动入口
// （PackID 空）按 (workspace_id, pack) 部分唯一索引幂等；provision 路径
// （PackID 非空）仍按 (workspace_id, pack_id) 冲突写，互不干扰。
func TestPackUIEntryManualEntry_UpsertAndList(t *testing.T) {
	repo, pool, ws := newUISlotsEnv(t)
	ctx := context.Background()

	// 迁移 1047 落地：部分唯一索引存在且谓词只覆盖手动行。
	var idxDef string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes
		 WHERE tablename='pack_ui_entry' AND indexname='pack_ui_entry_ws_manual_pack_key'`).Scan(&idxDef))
	assert.Contains(t, idxDef, "UNIQUE")
	assert.Contains(t, idxDef, "pack_id IS NULL")

	// 同名两次 upsert：幂等覆盖，只有一行。
	require.NoError(t, repo.UpsertPackUIEntry(ctx, app.PackUIEntry{
		WorkspaceID: ws, Pack: "custom-side", Label: "旧标签",
		URLTemplate: "https://old.example",
	}))
	require.NoError(t, repo.UpsertPackUIEntry(ctx, app.PackUIEntry{
		WorkspaceID: ws, Pack: "custom-side", Label: "新标签",
		URLTemplate: "https://new.example",
		Slots: []app.PackUISlot{{
			Slot: app.SlotWorkItemDetailTab, Label: "文章预览",
			URLTemplate: "https://new.example/slot?item={workItemId}", Height: 480,
		}},
	}))
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM pack_ui_entry WHERE workspace_id=$1 AND pack='custom-side'`, ws).Scan(&n))
	assert.Equal(t, 1, n, "同名手动入口 upsert 覆盖不新建行")

	// 不同名 → 两行手动入口。
	require.NoError(t, repo.UpsertPackUIEntry(ctx, app.PackUIEntry{
		WorkspaceID: ws, Pack: "custom-tab", Label: "工单 tab",
		URLTemplate: "https://tab.example",
	}))

	// List：手动行 pack_id 为空串（列 NULL 经 COALESCE 读出，不炸扫描），
	// label 为覆盖后的整行语义。
	entries, err := repo.ListPackUIEntries(ctx, ws)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	byPack := map[string]app.PackUIEntry{}
	for _, e := range entries {
		byPack[e.Pack] = e
	}
	require.Contains(t, byPack, "custom-side")
	require.Contains(t, byPack, "custom-tab")
	assert.Equal(t, "新标签", byPack["custom-side"].Label)
	assert.Equal(t, "https://new.example", byPack["custom-side"].URLTemplate)
	assert.Len(t, byPack["custom-side"].Slots, 1)
	assert.Empty(t, byPack["custom-side"].PackID, "手动行 pack_id NULL → 空串")
	assert.Empty(t, byPack["custom-tab"].PackID)

	// provision 路径回归：pack_id 非空按 (workspace_id, pack_id) 冲突写
	// （原 SQL 一字未改），与手动行互不干扰。
	var packID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO pack (workspace_id, name) VALUES ($1, 'agentblog') RETURNING id::text`, ws).Scan(&packID))
	require.NoError(t, repo.UpsertPackUIEntry(ctx, app.PackUIEntry{
		WorkspaceID: ws, Pack: "agentblog", PackID: packID, Label: "内容工厂",
		URLTemplate: "https://ab.example/console",
	}))
	require.NoError(t, repo.UpsertPackUIEntry(ctx, app.PackUIEntry{
		WorkspaceID: ws, Pack: "agentblog", PackID: packID, Label: "内容工厂 v2",
		URLTemplate: "https://ab.example/console2",
	}))
	entries, err = repo.ListPackUIEntries(ctx, ws)
	require.NoError(t, err)
	require.Len(t, entries, 3, "provision 行按 pack_id 覆盖 + 两条手动行")
	byPack = map[string]app.PackUIEntry{}
	for _, e := range entries {
		byPack[e.Pack] = e
	}
	assert.Equal(t, packID, byPack["agentblog"].PackID)
	assert.Equal(t, "内容工厂 v2", byPack["agentblog"].Label)
	assert.Equal(t, "https://ab.example/console2", byPack["agentblog"].URLTemplate)

	// Delete 对手动入口按 pack 名可用（集成页移除路径）。
	require.NoError(t, repo.DeletePackUIEntry(ctx, ws, "custom-side"))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM pack_ui_entry WHERE workspace_id=$1 AND pack='custom-side'`, ws).Scan(&n))
	assert.Zero(t, n)
}
