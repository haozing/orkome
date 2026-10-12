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

// 仿 agent_access_migration_test.go 的 TEST_DATABASE_URL 惯例：
// 未设置即跳过；建独立测试库 orkome_ptschema，不触碰任何真实库。
func newPackToolSchemaEnv(t *testing.T) (*Repo, *pgxpool.Pool, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_ptschema")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_ptschema")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_ptschema", 1)

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
	u, err := idr.UpsertUserByEmail(ctx, "ptschema@example.com", time.Now())
	require.NoError(t, err)
	slug := "ws-" + u.ID[:13] + "-ptschema"
	var wsID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, created_by) VALUES ('ptschema-ws', $1, $2) RETURNING id`,
		slug, u.ID).Scan(&wsID))
	return New(pg.WrapPool(pool)), pool, wsID
}

func TestPackToolSchema_MigrationAndRepo(t *testing.T) {
	repo, pool, ws := newPackToolSchemaEnv(t)
	ctx := context.Background()

	// 迁移形状：表存在、source CHECK 生效。
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name='pack_tool_schema'`).Scan(&n))
	assert.Equal(t, 1, n, "迁移 1035 建表 pack_tool_schema")
	_, err := pool.Exec(ctx,
		`INSERT INTO pack_tool_schema (workspace_id, pack, server, tool_name, output_schema, source)
		 VALUES ($1,'p','s','p_tool','{}'::jsonb,'weird')`, ws)
	require.Error(t, err, "source CHECK 只放行 protocol/pack_yaml/builtin_default")

	// Replace：写入 + 幂等覆盖（第二次替换掉旧行，不叠行）。
	mk := func(tool, source string) app.PackToolSchema {
		return app.PackToolSchema{Pack: "fakepack", Server: "fakeblog", ToolName: tool,
			OutputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"job_id": map[string]any{"type": "string"},
			}}, Source: source}
	}
	require.NoError(t, repo.ReplacePackToolSchemas(ctx, ws, "fakepack", []app.PackToolSchema{
		mk("fakeblog_submit_generation", app.PackToolSchemaSourceProtocol),
		mk("fakeblog_job_status", app.PackToolSchemaSourcePackYAML),
	}))
	require.NoError(t, repo.ReplacePackToolSchemas(ctx, ws, "fakepack", []app.PackToolSchema{
		mk("fakeblog_submit_generation", app.PackToolSchemaSourceProtocol),
		mk("fakeblog_job_status", app.PackToolSchemaSourcePackYAML),
		mk("fakeblog_cancel_job", app.PackToolSchemaSourcePackYAML),
	}))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM pack_tool_schema WHERE workspace_id=$1 AND pack='fakepack'`, ws).Scan(&n))
	assert.Equal(t, 3, n, "Replace 幂等覆盖")

	// List：C1 读取 API 的底层——按 workspace 全量返回。
	all, err := repo.ListPackToolSchemas(ctx, ws)
	require.NoError(t, err)
	assert.Len(t, all, 3)
	sources := map[string]string{}
	for _, it := range all {
		sources[it.ToolName] = it.Source
	}
	assert.Equal(t, app.PackToolSchemaSourceProtocol, sources["fakeblog_submit_generation"])
	assert.Equal(t, app.PackToolSchemaSourcePackYAML, sources["fakeblog_job_status"])
	assert.Equal(t, app.PackToolSchemaSourcePackYAML, sources["fakeblog_cancel_job"])

	// Get：按 toolName 精确取；未存档返回 ok=false。
	got, ok, err := repo.GetPackToolSchema(ctx, ws, "fakeblog_job_status")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "fakepack", got.Pack)
	assert.Equal(t, "object", got.OutputSchema["type"])
	_, ok, err = repo.GetPackToolSchema(ctx, ws, "missing_tool")
	require.NoError(t, err)
	assert.False(t, ok)

	// 跨 workspace 不可见。
	_, ok, err = repo.GetPackToolSchema(ctx, "00000000-0000-0000-0000-000000000000", "fakeblog_job_status")
	require.NoError(t, err)
	assert.False(t, ok, "跨 workspace 存档不可见")

	// Delete：uninstall 清档。
	require.NoError(t, repo.DeletePackToolSchemas(ctx, ws, "fakepack"))
	all, err = repo.ListPackToolSchemas(ctx, ws)
	require.NoError(t, err)
	assert.Empty(t, all)
}
