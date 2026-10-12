package pgrepo

// W2-3 / FP-09 P1-3 + P1-4 的 SQL 级集成回归（TEST_DATABASE_URL 门控，
// 无则整测跳过——沿用 newPackToolSchemaEnv 惯例；独立测试库 orkome_mcpcreds，
// 不触碰任何真实库）。
//
// 夹具纪律（fix-wave1-c 笔记）：orchestration pgrepo 包在带 PG 时整包很重
// （逐用例全量跑迁移），本文件全部子测试共享单夹具，勿再逐用例起库。
//
// 覆盖矩阵：
//   A 加密往返：写入即密封信封，库里查无明文，读出还原明文（对照模型凭证先例）
//   B 三态语义：Update nil=保留 / 空 map=清空 '{}' / 新值=覆盖（ws+agent 两级）
//   C 重名 409：Create 与 PUT 改名撞名，ws+agent 两级（FP-07 P1-4/P1-5）
//   D fail-closed：存量明文形状的行，读路径拒绝、绝不回落明文
//   E 迁移 1038：明文列清空 '{}'，重复执行幂等

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

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
	"orkome/migrations"
)

const mcpCredsPlainKey = "sk-mcpcred-plain-1234567890" // 各断言共用的"明文标记"

func newMCPCredsEnv(t *testing.T) (*Repo, *pgxpool.Pool, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_mcpcreds")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_mcpcreds")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_mcpcreds", 1)

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
	u, err := idr.UpsertUserByEmail(ctx, "mcpcreds@example.com", time.Now())
	require.NoError(t, err)
	slug := "ws-" + u.ID[:13] + "-mcpcreds"
	var wsID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, created_by) VALUES ('mcpcreds-ws', $1, $2) RETURNING id`,
		slug, u.ID).Scan(&wsID))

	repo := New(pg.WrapPool(pool))
	repo.Seal = &fakeSeal{} // 内存假 sealx（见 repo_mcp_seal_test.go）
	return repo, pool, wsID
}

func TestMCPCredsSeal_DB(t *testing.T) {
	repo, pool, ws := newMCPCredsEnv(t)
	ctx := context.Background()

	t.Run("A seal roundtrip: stored value is envelope, never plaintext", func(t *testing.T) {
		m, err := repo.CreateWorkspaceMCP(ctx, orchapp.WorkspaceMCP{
			WorkspaceID: ws, Name: "sealed-srv", URL: "https://sealed.example",
			Credentials: map[string]any{"api_key": mcpCredsPlainKey}})
		require.NoError(t, err)

		var raw string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT credentials::text FROM workspace_mcp_server WHERE id=$1`, m.ID).Scan(&raw))
		assert.Contains(t, raw, `"sealed"`, "落库形状是密封信封")
		assert.NotContains(t, raw, mcpCredsPlainKey, "库内查无明文（FP-09 P1-3 主断言）")
		assert.NotContains(t, raw, "api_key", "键名同样进密文")

		list, err := repo.ListWorkspaceMCP(ctx, ws)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.True(t, list[0].HasCreds)
		assert.Equal(t, mcpCredsPlainKey, list[0].Credentials["api_key"], "读路径解密还原明文 map")
	})

	t.Run("B three-state update semantics (ws + agent)", func(t *testing.T) {
		m, err := repo.CreateWorkspaceMCP(ctx, orchapp.WorkspaceMCP{
			WorkspaceID: ws, Name: "tristate-ws", URL: "https://t.example",
			Credentials: map[string]any{"api_key": "sk-old"}})
		require.NoError(t, err)

		// nil → coalesce 保留现值。
		kept, ok, err := repo.UpdateWorkspaceMCP(ctx, orchapp.WorkspaceMCP{
			WorkspaceID: ws, ID: m.ID, Name: "tristate-ws", URL: "https://t.example"})
		require.NoError(t, err)
		require.True(t, ok)
		assert.True(t, kept.HasCreds, "缺字段（nil）保留库中现值")
		assert.Equal(t, "sk-old", kept.Credentials["api_key"])

		// 空 map → 显式清空 '{}'。
		cleared, ok, err := repo.UpdateWorkspaceMCP(ctx, orchapp.WorkspaceMCP{
			WorkspaceID: ws, ID: m.ID, Name: "tristate-ws", URL: "https://t.example",
			Credentials: map[string]any{}})
		require.NoError(t, err)
		require.True(t, ok)
		assert.False(t, cleared.HasCreds, "空 map=显式清空")
		var raw string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT credentials::text FROM workspace_mcp_server WHERE id=$1`, m.ID).Scan(&raw))
		assert.Equal(t, "{}", raw)

		// 新值 → 覆盖并重新密封。
		over, ok, err := repo.UpdateWorkspaceMCP(ctx, orchapp.WorkspaceMCP{
			WorkspaceID: ws, ID: m.ID, Name: "tristate-ws", URL: "https://t.example",
			Credentials: map[string]any{"api_key": "sk-new"}})
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "sk-new", over.Credentials["api_key"])

		// agent 级同口径。
		ag, err := repo.CreateAgent(ctx, orchapp.Agent{
			WorkspaceID: ws, Name: "mcpcreds-agent", MaxConcurrent: 1, Visibility: "public", RuntimeConfig: map[string]any{}})
		require.NoError(t, err)
		am, err := repo.CreateAgentMCP(ctx, orchapp.AgentMCP{
			WorkspaceID: ws, AgentID: ag.ID, Name: "tristate-ag", URL: "https://t.example",
			Credentials: map[string]any{"api_key": "sk-ag-old"}})
		require.NoError(t, err)
		keptAg, okAg, err := repo.UpdateAgentMCP(ctx, orchapp.AgentMCP{
			WorkspaceID: ws, AgentID: ag.ID, ID: am.ID, Name: "tristate-ag", URL: "https://t.example"})
		require.NoError(t, err)
		require.True(t, okAg)
		assert.True(t, keptAg.HasCreds, "agent 级缺字段同样保留")
		assert.Equal(t, "sk-ag-old", keptAg.Credentials["api_key"])
		clearedAg, okAg, err := repo.UpdateAgentMCP(ctx, orchapp.AgentMCP{
			WorkspaceID: ws, AgentID: ag.ID, ID: am.ID, Name: "tristate-ag", URL: "https://t.example",
			Credentials: map[string]any{}})
		require.NoError(t, err)
		require.True(t, okAg)
		assert.False(t, clearedAg.HasCreds, "agent 级空 map 同样清空")
	})

	t.Run("C duplicate name 409 on both levels, create and rename", func(t *testing.T) {
		_, err := repo.CreateWorkspaceMCP(ctx, orchapp.WorkspaceMCP{
			WorkspaceID: ws, Name: "tristate-ws", URL: "https://dup.example"})
		require.Error(t, err)
		var wsErr *apierr.Error
		require.ErrorAs(t, err, &wsErr)
		assert.Equal(t, apierr.Conflict, wsErr.Code, "ws 级 Create 撞名 409")

		_, err = repo.CreateWorkspaceMCP(ctx, orchapp.WorkspaceMCP{
			WorkspaceID: ws, Name: "rename-src", URL: "https://r.example"})
		require.NoError(t, err)
		_, _, err = repo.UpdateWorkspaceMCP(ctx, orchapp.WorkspaceMCP{
			WorkspaceID: ws, ID: "00000000-0000-0000-0000-000000000001", Name: "sealed-srv", URL: "https://r.example"})
		require.NoError(t, err, "不存在的 id 走 noRow 分支（ok=false），不是重名")

		list, err := repo.ListWorkspaceMCP(ctx, ws)
		require.NoError(t, err)
		var renameSrcID string
		for _, s := range list {
			if s.Name == "rename-src" {
				renameSrcID = s.ID
			}
		}
		require.NotEmpty(t, renameSrcID)
		_, _, err = repo.UpdateWorkspaceMCP(ctx, orchapp.WorkspaceMCP{
			WorkspaceID: ws, ID: renameSrcID, Name: "sealed-srv", URL: "https://r.example"})
		require.Error(t, err, "FP-07 P1-5：PUT 改名撞名必须报错（原为 500）")
		var dupErr *apierr.Error
		require.ErrorAs(t, err, &dupErr)
		assert.Equal(t, apierr.Conflict, dupErr.Code, "改名撞名映射 409")

		// agent 级：Create 重名 409 且不得覆盖既有行（FP-07 P1-4）。
		ag, err := repo.CreateAgent(ctx, orchapp.Agent{
			WorkspaceID: ws, Name: "mcpcreds-dup-agent", MaxConcurrent: 1, Visibility: "public", RuntimeConfig: map[string]any{}})
		require.NoError(t, err)
		first, err := repo.CreateAgentMCP(ctx, orchapp.AgentMCP{
			WorkspaceID: ws, AgentID: ag.ID, Name: "dup-ag", URL: "https://first.example"})
		require.NoError(t, err)
		_, err = repo.CreateAgentMCP(ctx, orchapp.AgentMCP{
			WorkspaceID: ws, AgentID: ag.ID, Name: "dup-ag", URL: "https://second.example"})
		require.Error(t, err, "agent 级 Create 重名不再静默 upsert")
		require.ErrorAs(t, err, &dupErr)
		assert.Equal(t, apierr.Conflict, dupErr.Code)
		after, ok, err := repo.ListAgentMCP(ctx, ws, ag.ID)
		require.NoError(t, err)
		require.True(t, ok)
		require.Len(t, after, 1, "同名 POST 不产生第二行也不覆盖第一行")
		assert.Equal(t, first.ID, after[0].ID)
		assert.Equal(t, "https://first.example", after[0].URL, "原条目原封不动")

		// agent 级 PUT 改名撞名 409。
		_, err = repo.CreateAgentMCP(ctx, orchapp.AgentMCP{
			WorkspaceID: ws, AgentID: ag.ID, Name: "other-ag", URL: "https://o.example"})
		require.NoError(t, err)
		_, _, err = repo.UpdateAgentMCP(ctx, orchapp.AgentMCP{
			WorkspaceID: ws, AgentID: ag.ID, ID: after[0].ID, Name: "other-ag", URL: "https://first.example"})
		require.Error(t, err)
		require.ErrorAs(t, err, &dupErr)
		assert.Equal(t, apierr.Conflict, dupErr.Code)
	})

	t.Run("D legacy plaintext rejected fail-closed on read", func(t *testing.T) {
		// 存量明文形状（迁移 1038 的清空目标）：直接插一行明文 jsonb。
		_, err := pool.Exec(ctx, `
INSERT INTO workspace_mcp_server (workspace_id, name, transport, url, credentials)
VALUES ($1,'legacy-srv','http','https://legacy.example','{"api_key":"sk-legacy-plain"}'::jsonb)`, ws)
		require.NoError(t, err)
		_, err = repo.ListWorkspaceMCP(ctx, ws)
		require.Error(t, err, "明文形状的行读路径必须拒绝，绝不回落明文")
		assert.Contains(t, err.Error(), "legacy plaintext rejected")
	})

	t.Run("E migration 1038 clears plaintext, idempotent, read recovers", func(t *testing.T) {
		sqlBytes, err := migrations.FS.ReadFile("1038_mcp_creds_sealed.up.sql")
		require.NoError(t, err)
		// 拆成单语句执行（迁移文件就两条 UPDATE，无内嵌分号；
		// pgx 扩展协议不接受一串多语句）。
		for _, stmt := range strings.Split(string(sqlBytes), ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			var body []string
			for _, line := range strings.Split(stmt, "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), "--") {
					body = append(body, line)
				}
			}
			if len(body) == 0 {
				continue
			}
			_, err = pool.Exec(ctx, strings.Join(body, "\n"))
			require.NoError(t, err)
		}

		for _, tbl := range []string{"workspace_mcp_server", "agent_mcp_server"} {
			var leftovers int
			require.NoError(t, pool.QueryRow(ctx,
				`SELECT count(*) FROM `+tbl+` WHERE credentials::text NOT IN ('{}') AND credentials::text NOT LIKE '%"sealed"%'`).Scan(&leftovers),
				tbl)
			assert.Zero(t, leftovers, tbl+" 迁移 1038 后无明文残留")
		}

		// 幂等：重复执行不报错。
		for _, stmt := range strings.Split(string(sqlBytes), ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			_, err = pool.Exec(ctx, stmt)
			require.NoError(t, err)
		}

		// 清空后 D 的 legacy 行已变 '{}'，全表恢复可读。
		_, err = repo.ListWorkspaceMCP(ctx, ws)
		require.NoError(t, err, "存量清空后读路径恢复正常")
	})
}
