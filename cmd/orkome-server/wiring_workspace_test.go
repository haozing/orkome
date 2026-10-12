package main

// 工作区硬删接线（wave2-ws-delete）的测试。
//
//   - TestWorkspaceHardDeleteStatementHygiene：无库单测。守护跨域清理语句清单的
//     形状（单一 $1、不碰 workspace 本表、子表语句必须先于父表）与覆盖面
//     （52 处无级联 workspace FK 中、FixE work 域 21 表之外的全部业务表都要
//     出现在清单里）——未来迁移新增无级联表而漏接时，这里先红。
//
//   - TestWorkspaceHardDeleteIntegration：TEST_DATABASE_URL 门控集成测试。
//     走真实 HTTP 链（identityhttp 登录 → workspacehttp DELETE），挂的钩子就是
//     生产接线 workspaceHardDeleteHooks：
//     建工作区 → 造全型数据（工单/标签/状态自定义/agent/小队/技能绑定/chat/
//     channel/autopilot/…82 表）→ DELETE 204 且全表零残留 → 重复 DELETE 404；
//     再造第二个工作区，DROP 一张跨域表注入失败 → 500 且工作区保留，
//     验证三层事务边界的传播语义（详见 docs/fix-wave2-ws-delete-2026-10-12.md）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/authz"
	identityhttp "github.com/haozing/ploykit/identity/adapters/http"
	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	fwapp "github.com/haozing/ploykit/identity/app"
	pkgmigrations "github.com/haozing/ploykit/migrations"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"github.com/haozing/ploykit/platform/webx"
	workspacehttp "github.com/haozing/ploykit/workspace/adapters/http"
	workspacepg "github.com/haozing/ploykit/workspace/adapters/pgrepo"
	wsapp "github.com/haozing/ploykit/workspace/app"

	"orkome/migrations"
)

// --- 无库单测：语句清单形状与拓扑序守护 ---------------------------------

func TestWorkspaceHardDeleteStatementHygiene(t *testing.T) {
	idx := map[string]int{}
	for i, s := range workspaceCrossDomainCleanup {
		// 单参数语句：只允许 $1（可重复出现），不得出现 $2 及以后
		require.Contains(t, s.sql, "$1", s.table)
		require.NotRegexp(t, `\$\d+`, strings.ReplaceAll(s.sql, "$1", ""), s.table)

		// 每条语句的目标表必须与声明一致，且绝不直接删 workspace 本表
		rest := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s.sql)), "delete from ")
		target := rest[:strings.IndexByte(rest, ' ')]
		require.Equal(t, s.table, target, "statement %d targets %s but declares %s", i, target, s.table)
		require.NotEqual(t, "workspace", s.table, "teardown must not delete the workspace row itself")

		// 表不得重复出现（重复既是死代码也会掩盖拓扑错误）
		_, dup := idx[s.table]
		require.False(t, dup, "table %s appears twice in the cleanup list", s.table)
		idx[s.table] = i
	}

	// 关键拓扑对：无 ON DELETE CASCADE 的引用边，子表语句必须先于父表。
	// （有 CASCADE 的边刻意不入表：顺序无关，写了反而绑死未来迁移。）
	pairs := [][2]string{
		{"notification", "agent"},                            // recipient_agent_id 无级联
		{"automation_webhook_delivery", "autopilot_trigger"}, // trigger_id 无级联
		{"autopilot_run", "autopilot_quota_reservation"},     // run 引用 reservation
		{"autopilot_quota_reservation", "autopilot"},
		{"autopilot_subscriber", "autopilot"},
		{"autopilot_collaborator", "autopilot"},
		{"autopilot_rule_version", "autopilot"},
		{"autopilot_quota_period", "autopilot"},
		{"autopilot_trigger", "autopilot"},
		{"autopilot", "agent"}, // target_agent_id 无级联
		{"task_usage", "agent"},
		{"agent_task_queue", "agent"}, // agent_id/squad_id 均无级联
		{"agent_task_queue", "squad"},
		{"chat_cancel_pending", "chat_session"},
		{"chat_draft_restore", "chat_session"},
		{"channel_chat_context_generation", "chat_session"},
		{"channel_outbound_card_message", "chat_session"},
		{"chat_message", "chat_session"}, // session_id 无级联
		{"channel_chat_session_binding", "chat_session"},
		{"channel_chat_session_binding", "channel_installation"},
		{"chat_pinned_agent", "agent"},
		{"chat_session", "agent"}, // agent_id NOT NULL 无级联
		{"channel_inbound_audit", "channel_installation"},
		{"channel_outbound_message", "channel_installation"},
		{"channel_reply_delivery", "channel_installation"},
		{"channel_task_delivery", "channel_installation"},
		{"channel_media_pending_object", "channel_installation"},
		{"channel_binding_token", "channel_installation"},
		{"channel_inbound_message_dedup", "channel_installation"},
		{"channel_user_binding", "channel_installation"},
		{"agent_skill", "skill"},
		{"agent_skill", "agent"}, // agent 侧无级联
		{"skill_file", "skill"},
		{"skill_to_label", "skill"},
		{"skill", "agent"},
		{"agent_invocation_target", "agent"},
		{"agent_mcp_server", "agent"},
		{"agent_mcp_server", "workspace_mcp_server"},
		{"squad_member", "squad"},
		{"squad", "agent"}, // leader_agent_id 无级联
		{"agent_to_label", "agent"},
		{"agent_credential", "agent"},
		{"work_item_batch_item", "work_item_batch"},
	}
	for _, p := range pairs {
		ci, okc := idx[p[0]]
		pi, okp := idx[p[1]]
		require.True(t, okc, "child table %s missing from cleanup list", p[0])
		require.True(t, okp, "parent table %s missing from cleanup list", p[1])
		require.Less(t, ci, pi, "%s must be cleaned before %s", p[0], p[1])
	}

	// 覆盖面：除 FixE work 域 21 表、框架 CASCADE 表、有意保留的 audit/analytics
	// 之外，库目录里每张无级联 workspace-FK 业务表都必须在清单里（实测于
	// 2026-10-12，见交接笔记 §二；新增迁移再添无级联表时此清单需同步）。
	expected := []string{
		"agent", "agent_builder_draft", "agent_credential", "agent_invocation_target",
		"agent_mcp_server", "agent_model_credential", "agent_skill", "agent_task_checkpoint",
		"agent_task_queue", "agent_to_label", "attachment", "automation_webhook_delivery",
		"autopilot", "autopilot_collaborator", "autopilot_quota_period", "autopilot_quota_reservation",
		"autopilot_rule_version", "autopilot_run", "autopilot_subscriber", "autopilot_trigger",
		"channel_binding_token", "channel_chat_context_generation", "channel_chat_session_binding",
		"channel_inbound_audit", "channel_inbound_message_dedup", "channel_installation",
		"channel_media_pending_object", "channel_outbound_card_message", "channel_outbound_message",
		"channel_reply_delivery", "channel_task_delivery", "channel_user_binding",
		"chat_cancel_pending", "chat_draft_restore", "chat_message", "chat_pinned_agent",
		"chat_session", "client_usage_daily", "comment", "comment_reaction", "event_outbox",
		"label", "notification", "notification_preference", "pack_tool_schema", "pack_ui_entry",
		"pinned_item", "project", "project_resource", "quick_action", "search_index_change",
		"skill", "skill_file", "skill_to_label", "source_context_object_intent", "squad",
		"squad_member", "system_wakeup_setting", "task_message", "task_supplement", "task_usage",
		"task_usage_hourly", "ui_token", "work_item", "work_item_batch", "work_item_batch_item",
		"work_item_child_event", "work_item_dependency", "work_item_property_def",
		"work_item_reaction", "work_item_source_context", "work_item_status",
		"work_item_subscriber", "work_item_to_label", "work_item_view",
		"work_item_view_preference", "work_item_wakeup", "work_item_wakeup_receipt",
		"workspace_mcp_server",
	}
	// FixE work 域 21 表不在跨域清单里（由 CleanupWorkspaceTables 负责）；
	// task_supplement / work_item_reaction 等 CASCADE 子表允许缺席跨域清单。
	fixeOwned := map[string]bool{
		"work_item_wakeup_receipt": true, "work_item_wakeup": true, "comment_reaction": true,
		"comment": true, "activity_log": true, "work_item_reaction": true,
		"work_item_subscriber": true, "work_item_to_label": true, "work_item_dependency": true,
		"work_item_child_event": true, "work_item": true, "work_item_view_preference": true,
		"work_item_view": true, "work_item_source_context": true, "work_item_property_def": true,
		"label": true, "project_resource": true, "project": true, "search_index_change": true,
		"client_usage_daily": true, "work_item_status": true,
	}
	// 仅随父表 CASCADE、无 workspace_id 的 join 表也允许缺席。
	cascadeOnly := map[string]bool{"task_supplement": true, "skill_file": true, "agent_skill": true}
	for _, tbl := range expected {
		if fixeOwned[tbl] || cascadeOnly[tbl] {
			continue
		}
		_, ok := idx[tbl]
		require.True(t, ok, "table %s needs an explicit cross-domain cleanup statement", tbl)
	}

	// BeforeDelete 预清：两张 label 侧无级联的跨域 join 表必须在列
	var precleaned []string
	for _, stmt := range workspaceLabelRefPreclean {
		switch {
		case strings.Contains(stmt, "agent_to_label"):
			precleaned = append(precleaned, "agent_to_label")
		case strings.Contains(stmt, "skill_to_label"):
			precleaned = append(precleaned, "skill_to_label")
		default:
			t.Fatalf("unexpected preclean statement: %s", stmt)
		}
	}
	require.ElementsMatch(t, []string{"agent_to_label", "skill_to_label"}, precleaned)
}

// --- 集成测试（TEST_DATABASE_URL 门控） ----------------------------------

type wsdelMail struct{}

func (wsdelMail) SendLoginCode(context.Context, string, string) error         { return nil }
func (wsdelMail) SendInvite(context.Context, string, string, string) error    { return nil }
func (wsdelMail) SendPasswordReset(context.Context, string, string) error     { return nil }
func (wsdelMail) SendEmailVerification(context.Context, string, string) error { return nil }

type wsdelStack struct {
	t       *testing.T
	pool    *pgxpool.Pool
	handler http.Handler
	jar     map[string][]*http.Cookie
	csrf    map[string]string
}

func newWsDelStack(t *testing.T) *wsdelStack {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	require.NoError(t, err)
	// 独立库名：与 e2e 包的 orkome_e2e 并行运行互不干扰
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS orkome_wsdel")
	_, _ = admin.Exec(ctx, "CREATE DATABASE orkome_wsdel")
	require.NoError(t, admin.Close(ctx))
	dsn := strings.Replace(base, "/orkome?", "/orkome_wsdel?", 1)

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(ctx, 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)

	db := pg.WrapPool(pool)
	t.Cleanup(pool.Close)

	mail := wsdelMail{}
	secret := "wsdel-secret-32-bytes-aaaaaaaaa"
	auth := &webx.AuthConfig{CookieName: "orkome_auth", SessionTTL: 720 * time.Hour}
	fwRepo := identitypg.New(pool, identitypg.Config{SessionTTL: 720 * time.Hour})
	sessions := fwapp.NewSessionService(fwRepo, mail, fwapp.SessionConfig{
		AllowSignup: true, DevCode: "654321", SecretPepper: secret,
	}, 720*time.Hour, 0, time.Now)
	tokens := fwapp.NewTokenService(fwRepo, nil, time.Now)

	az := authz.New(authz.DefaultCatalog(), authz.DefaultRoles())
	// 生产同款钩子（cancelActiveTasks 传 nil：栈里没有引擎在跑）
	wsSvc := wsapp.NewWorkspaceService(workspacepg.New(pool), az, mail, nil, wsapp.WorkspaceConfig{}, time.Now).
		WithHooks(workspaceHardDeleteHooks(db, nil, nil))

	api := http.NewServeMux()
	workspacehttp.Mount(api, workspacehttp.Deps{
		Svc: wsSvc, Members: workspacepg.New(pool), Authz: az,
	})
	root := http.NewServeMux()
	root.Handle("/api/", api)
	root.HandleFunc("GET /config", webx.ConfigHandler(webx.ConfigDeps{}))
	identityhttp.Mount(root, identityhttp.Deps{
		AuthCfg: *auth, Sessions: fwRepo, PATs: fwRepo, SessionsSvc: sessions, Tokens: tokens,
	})
	authMw := webx.Authenticate(auth, fwRepo, fwRepo)
	handler := webx.ClientMetadata(webx.CSRFConditional(&webx.CSRFConfig{
		Key: webx.DeriveCSRFKey([]byte(secret)), TrustLocalhostAnyPort: true,
	}, false)(authMw(root)))

	return &wsdelStack{t: t, pool: pool, handler: handler,
		jar: map[string][]*http.Cookie{}, csrf: map[string]string{}}
}

func (s *wsdelStack) do(client, method, path string, body any) *httptest.ResponseRecorder {
	s.t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "192.0.2.9:5555"
	for _, c := range s.jar[client] {
		req.AddCookie(c)
	}
	if s.csrf[client] != "" {
		req.Header.Set("X-CSRF-Token", s.csrf[client])
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "orkome_auth" || c.Name == "ploykit_csrf" {
			s.jar[client] = append(s.jar[client], c)
		}
	}
	return rec
}

func (s *wsdelStack) login(client, email string) {
	s.t.Helper()
	rec := s.do(client, http.MethodGet, "/config", nil)
	require.Equal(s.t, 200, rec.Code)
	var cfg struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cfg)
	s.csrf[client] = cfg.CSRFToken
	rec = s.do(client, http.MethodPost, "/auth/verify-code",
		map[string]string{"email": email, "code": "654321"})
	require.Equal(s.t, 200, rec.Code, rec.Body.String())
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.CSRFToken != "" {
		s.csrf[client] = out.CSRFToken
	}
}

func (s *wsdelStack) createWS(client, slug string) string {
	s.t.Helper()
	rec := s.do(client, http.MethodPost, "/api/workspaces",
		map[string]string{"name": "WSDEL-" + slug, "slug": slug})
	require.Equal(s.t, 201, rec.Code, rec.Body.String())
	var ws struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ws)
	require.NotEmpty(s.t, ws.ID)
	return ws.ID
}

func (s *wsdelStack) deleteWS(client, wsID string) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.do(client, http.MethodDelete, "/api/workspaces/"+wsID, nil)
}

// count 返回整表行数。集成库是一次性新建的空库（每轮 DROP/CREATE），全表计数
// 即工作区残留计数，无需按 workspace_id 过滤（join 表尤其受益——它们没有该列）。
func (s *wsdelStack) count(t *testing.T, table string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM `+table).Scan(&n))
	return n
}

// seedFullTypeWorkspace 造全型数据：工单/标签/状态自定义/agent/小队/技能绑定，
// 加上 chat/channel/autopilot/任务链/通知/产品杂项面——teardown 清单里每张表
// 至少一行（含引用 nullable FK 的行，专测无级联引用边）。
func (s *wsdelStack) seedFullTypeWorkspace(t *testing.T, ws string) {
	t.Helper()
	ctx := context.Background()
	var owner string
	require.NoError(t, s.pool.QueryRow(ctx,
		`SELECT user_id::text FROM member WHERE workspace_id=$1 AND role='owner'`, ws).Scan(&owner))

	// ---- work 域（FixE 21 表）----
	var wi1, wi2 string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1, 1, 'wsdel one', 'todo', 'user', $2) RETURNING id::text`, ws, owner).Scan(&wi1))
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1, 2, 'wsdel two', 'todo', 'user', $2) RETURNING id::text`, ws, owner).Scan(&wi2))

	var labelID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO label (workspace_id, name) VALUES ($1,'wsdel-label') RETURNING id::text`, ws).Scan(&labelID))
	var commentID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO comment (workspace_id, work_item_id, author_type, author_id, content)
VALUES ($1,$2,'user',$3,'wsdel comment') RETURNING id::text`, ws, wi1, owner).Scan(&commentID))
	var viewID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO work_item_view (workspace_id, user_id, name) VALUES ($1,$2,'wsdel view')
RETURNING id::text`, ws, owner).Scan(&viewID))
	var projectID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO project (workspace_id, name) VALUES ($1,'wsdel-project') RETURNING id::text`, ws).Scan(&projectID))
	var wakeupID string
	var agentID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO agent (workspace_id, name) VALUES ($1,'wsdel-agent') RETURNING id::text`, ws).Scan(&agentID))
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO work_item_wakeup (workspace_id, work_item_id, agent_id, created_by, kind)
VALUES ($1,$2,$3,$4,'time') RETURNING id::text`, ws, wi1, agentID, owner).Scan(&wakeupID))

	seed := func(sql string, args ...any) {
		t.Helper()
		_, err := s.pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	seed(`INSERT INTO work_item_status (workspace_id, key, name, category, is_builtin, position)
VALUES ($1,'wsdel-custom','WSDEL Custom','started',false,10)`, ws)
	seed(`INSERT INTO work_item_to_label (work_item_id, label_id) VALUES ($1,$2)`, wi1, labelID)
	seed(`INSERT INTO work_item_subscriber (work_item_id, user_id, source) VALUES ($1,$2,'creator')`, wi1, owner)
	seed(`INSERT INTO work_item_reaction (work_item_id, user_id, emoji) VALUES ($1,$2,'+1')`, wi1, owner)
	seed(`INSERT INTO comment_reaction (comment_id, user_id, emoji) VALUES ($1,$2,'+1')`, commentID, owner)
	seed(`INSERT INTO activity_log (event_id, workspace_id, work_item_id, actor_type, actor_id, kind)
VALUES ($1,$2,$3,'user',$4,'wsdel')`, "wsdel-evt-"+ws, ws, wi1, owner)
	seed(`INSERT INTO work_item_dependency (work_item_id, depends_on_id, workspace_id) VALUES ($1,$2,$3)`, wi1, wi2, ws)
	seed(`INSERT INTO work_item_child_event (parent_id, child_id, workspace_id) VALUES ($1,$2,$3)`, wi1, wi2, ws)
	seed(`INSERT INTO project_resource (project_id, kind, uri) VALUES ($1,'repo','https://x.example/wsdel')`, projectID)
	seed(`INSERT INTO work_item_view_preference (user_id, workspace_id, default_view_id) VALUES ($1,$2,$3)`, owner, ws, viewID)
	seed(`INSERT INTO work_item_property_def (workspace_id, key, name, type) VALUES ($1,'wsdel_key','WSDEL','text')`, ws)
	seed(`INSERT INTO work_item_source_context (workspace_id, source_digest, snapshot, created_by)
VALUES ($1,'wsdel-digest','{}',$2)`, ws, owner)
	var batchID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO work_item_batch (workspace_id, work_item_id, kind, status, source, total)
VALUES ($1,$2,'status','pending','{}',1) RETURNING id::text`, ws, wi1).Scan(&batchID))
	seed(`INSERT INTO work_item_batch_item (batch_id, workspace_id, seq, payload_ref, status)
VALUES ($1,$2,1,'{}','pending')`, batchID, ws)
	seed(`INSERT INTO work_item_wakeup_receipt (wakeup_id, revision, event_key, workspace_id)
VALUES ($1,1,'wsdel-key-'||$2,$2::uuid)`, wakeupID, ws)
	// search-index 触发器可能已因 work_item INSERT 排队 wi1，upsert 幂等
	seed(`INSERT INTO search_index_change (entity_type, entity_id, workspace_id, change_xid)
VALUES ('work_item',$1,$2,pg_current_xact_id())
ON CONFLICT (entity_type, entity_id) DO UPDATE SET workspace_id=$2`, wi1, ws)
	seed(`INSERT INTO client_usage_daily (workspace_id, user_id, client_type, install_id, activity_date,
  client_version, os, first_active_at, last_active_at)
VALUES ($1,$2,'web',gen_random_uuid(),current_date,'1.0.0-test','windows',now(),now())`, ws, owner)

	// ---- skills 域 + label 跨域 join（BeforeDelete 预清的目标）----
	var skillID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO skill (workspace_id, name) VALUES ($1,'wsdel-skill') RETURNING id::text`, ws).Scan(&skillID))
	seed(`INSERT INTO skill_file (skill_id, path, content, content_sha256, size_bytes)
VALUES ($1,'SKILL.md','hello','abc123',5)`, skillID)
	seed(`INSERT INTO agent_skill (agent_id, skill_id, enabled) VALUES ($1,$2,true)`, agentID, skillID)
	seed(`INSERT INTO skill_to_label (skill_id, label_id) VALUES ($1,$2)`, skillID, labelID)
	seed(`INSERT INTO agent_to_label (agent_id, label_id) VALUES ($1,$2)`, agentID, labelID)

	// ---- orchestration 域（任务链 / 小队 / 凭证 / MCP 绑定）----
	var patID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO personal_access_token (user_id, name, token_hash, prefix)
VALUES ($1,'wsdel-pat','wsdel-hash-'||$2::text,'ork_') RETURNING id::text`, owner, ws).Scan(&patID))
	var wsMcpID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO workspace_mcp_server (workspace_id, name, transport, url)
VALUES ($1,'wsdel-mcp','http','https://mcp.example') RETURNING id::text`, ws).Scan(&wsMcpID))
	seed(`INSERT INTO agent_invocation_target (agent_id, target_type, target_user_id) VALUES ($1,'member',$2)`, agentID, owner)
	seed(`INSERT INTO agent_mcp_server (agent_id, name, transport, url, workspace_server_id)
VALUES ($1,'wsdel-agent-mcp','http','https://mcp.example',$2)`, agentID, wsMcpID)
	seed(`INSERT INTO agent_credential (workspace_id, agent_id, pat_id, name) VALUES ($1,$2,$3,'wsdel-cred')`, ws, agentID, patID)
	seed(`INSERT INTO agent_model_credential (workspace_id, provider, encrypted_secret, created_by)
VALUES ($1,'fake','\x00',$2)`, ws, owner)
	var squadID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO squad (workspace_id, name, leader_agent_id) VALUES ($1,'wsdel-squad',$2)
RETURNING id::text`, ws, agentID).Scan(&squadID))
	seed(`INSERT INTO squad_member (squad_id, agent_id, role) VALUES ($1,$2,'leader')`, squadID, agentID)
	var taskID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO agent_task_queue (workspace_id, agent_id, status, context)
VALUES ($1,$2,'queued','{}') RETURNING id::text`, ws, agentID).Scan(&taskID))
	seed(`INSERT INTO task_message (workspace_id, task_id, seq, kind, content) VALUES ($1,$2,1,'text','hi')`, ws, taskID)
	seed(`INSERT INTO task_supplement (task_id, content) VALUES ($1,'supplement')`, taskID)
	seed(`INSERT INTO task_usage (workspace_id, task_id, agent_id, provider, model) VALUES ($1,$2,$3,'fake','fake-1')`, ws, taskID, agentID)
	seed(`INSERT INTO agent_task_checkpoint (id, workspace_id, agent_id, task_id, scope_kind, scope_id, state)
VALUES ('wsdel-cp-'||$1,$1::uuid,$2,$3,'work_item',$4,'\x00')`, ws, agentID, taskID, wi1)
	seed(`INSERT INTO agent_builder_draft (workspace_id, user_id, title) VALUES ($1,$2,'wsdel-draft')`, ws, owner)

	// ---- conversation 域 ----
	var sessionID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
VALUES ($1,$2,$3,'wsdel chat') RETURNING id::text`, ws, agentID, owner).Scan(&sessionID))
	seed(`INSERT INTO chat_message (session_id, workspace_id, role, kind, content, seq)
VALUES ($1,$2,'user','message','hello',1)`, sessionID, ws)
	seed(`INSERT INTO chat_draft_restore (workspace_id, session_id, user_id, content, finalized)
VALUES ($1,$2,$3,'draft','pending')`, ws, sessionID, owner)
	seed(`INSERT INTO chat_pinned_agent (user_id, workspace_id, agent_id) VALUES ($1,$2,$3)`, owner, ws, agentID)
	seed(`INSERT INTO quick_action (workspace_id, user_id, label, prompt) VALUES ($1,$2,'qa','do it')`, ws, owner)
	seed(`INSERT INTO chat_cancel_pending (task_id, workspace_id, session_id, initiator_user_id)
VALUES ($1,$2,$3,$4)`, taskID, ws, sessionID, owner)

	// ---- channels 域 ----
	var instID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO channel_installation (workspace_id, kind, name) VALUES ($1,'feishu','wsdel-channel')
RETURNING id::text`, ws).Scan(&instID))
	seed(`INSERT INTO channel_user_binding (workspace_id, member_user_id, channel_installation_id, kind, external_user_id)
VALUES ($1,$2,$3,'dm','ext-u-1')`, ws, owner, instID)
	seed(`INSERT INTO channel_binding_token (token_hash, workspace_id, channel_installation_id, external_user_id, expires_at)
VALUES ('wsdel-tok-'||$1,$1::uuid,$2,'ext-u-1',now()+interval '10 minutes')`, ws, instID)
	seed(`INSERT INTO channel_inbound_audit (channel_installation_id, kind, drop_reason)
VALUES ($1,'message','dedup')`, instID)
	seed(`INSERT INTO channel_inbound_message_dedup (provider, external_id, workspace_id, channel_installation_id)
VALUES ('feishu','wsdel-dedup-'||$1,$1::uuid,$1::uuid)`, ws)
	seed(`INSERT INTO channel_outbound_message (channel_installation_id, kind, external_message_id, binding_id, route_revision, outbound_kind)
VALUES ($1,'text','ext-m-1',gen_random_uuid(),1,'reply')`, instID)
	seed(`INSERT INTO channel_reply_delivery (turn_id, task_id, binding_id, channel_installation_id, kind, external_chat_id, phase)
VALUES (gen_random_uuid(),$1,gen_random_uuid(),$2,'text','ext-c-1','terminal')`, taskID, instID)
	seed(`INSERT INTO channel_task_delivery (task_id, binding_id, channel_installation_id, kind, external_chat_id, chat_type, route_revision)
VALUES ($1,gen_random_uuid(),$2,'text','ext-c-1','group',1)`, taskID, instID)
	seed(`INSERT INTO channel_media_pending_object (storage_key, workspace_id, channel_installation_id, state)
VALUES ('wsdel-media-'||$1,$1::uuid,$1::uuid,'pending')`, ws)
	seed(`INSERT INTO channel_chat_session_binding (channel_installation_id, external_chat_id, workspace_id, chat_session_id)
VALUES ($1,'ext-c-1',$2,$3)`, instID, ws, sessionID)
	seed(`INSERT INTO channel_chat_context_generation (chat_session_id, revision) VALUES ($1,1)`, sessionID)
	seed(`INSERT INTO channel_outbound_card_message (chat_session_id, kind, external_chat_id, external_card_message_id)
VALUES ($1,'card','ext-c-1','ext-card-1')`, sessionID)

	// ---- automation 域 ----
	var autoID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO autopilot (workspace_id, name, exec_mode, target_agent_id)
VALUES ($1,'wsdel-auto','task_only',$2) RETURNING id::text`, ws, agentID).Scan(&autoID))
	var trigID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO autopilot_trigger (autopilot_id, kind) VALUES ($1,'manual') RETURNING id::text`, autoID).Scan(&trigID))
	var resID string
	require.NoError(t, s.pool.QueryRow(ctx, `
INSERT INTO autopilot_quota_reservation (autopilot_id, window_start, window_end)
VALUES ($1,now(),now()+interval '1 hour') RETURNING id::text`, autoID).Scan(&resID))
	seed(`INSERT INTO autopilot_run (autopilot_id, trigger_id, source, work_item_id, quota_reservation_id)
VALUES ($1,$2,'manual',$3,$4)`, autoID, trigID, wi1, resID)
	seed(`INSERT INTO autopilot_subscriber (autopilot_id, user_type, user_id) VALUES ($1,'member',$2)`, autoID, owner)
	seed(`INSERT INTO autopilot_collaborator (autopilot_id, workspace_id, user_type, user_id, granted_by)
VALUES ($1,$2,'member',$3,$3)`, autoID, ws, owner)
	seed(`INSERT INTO autopilot_rule_version (workspace_id, autopilot_id, published_by_type, published_by_id)
VALUES ($1,$2,'user',$3)`, ws, autoID, owner)
	seed(`INSERT INTO autopilot_quota_period (workspace_id, period_day) VALUES ($1,current_date)`, ws)
	seed(`INSERT INTO automation_webhook_delivery (workspace_id, trigger_id, dedupe_key, payload)
VALUES ($1::uuid,$2,'wsdel-dedup-'||$1,'{}')`, ws, trigID)
	seed(`INSERT INTO system_wakeup_setting (workspace_id, enabled) VALUES ($1,true)`, ws)

	// ---- 通知 / 产品杂项面 ----
	seed(`INSERT INTO notification (user_id, type, title, workspace_id, recipient_agent_id)
VALUES ($1,'wsdel','wsdel notice',$2,$3)`, owner, ws, agentID)
	seed(`INSERT INTO notification_preference (user_id, notification_type, workspace_id)
VALUES ($1,'wsdel',$2)`, owner, ws)
	seed(`INSERT INTO pinned_item (workspace_id, user_id, item_type, item_id) VALUES ($1,$2,'work_item',$3)`, ws, owner, wi1)
	seed(`INSERT INTO ui_token (jti, workspace_id, user_id, plugin, expires_at)
VALUES ('wsdel-jti-'||$1,$1::uuid,$2,'test',now()+interval '1 hour')`, ws, owner)
	seed(`INSERT INTO pack_ui_entry (workspace_id, pack, label, url_template)
VALUES ($1,'demo','Demo','/demo?id={id}')`, ws)
	seed(`INSERT INTO pack_tool_schema (workspace_id, pack, server, tool_name, output_schema, source)
VALUES ($1,'demo','srv','tool','{}','protocol')`, ws)
	seed(`INSERT INTO attachment (workspace_id, host_kind, host_id, filename, storage_key)
VALUES ($1::uuid,'work_item',$2,'a.txt','wsdel-att-'||$1)`, ws, wi1)
	seed(`INSERT INTO source_context_object_intent (workspace_id, storage_key)
VALUES ($1::uuid,'wsdel-intent-'||$1)`, ws)
	seed(`INSERT INTO task_usage_hourly (workspace_id, hour_bucket, agent_key, provider, model)
VALUES ($1,date_trunc('hour',now()),$2,'fake','fake-1')`, ws, agentID)
	seed(`INSERT INTO event_outbox (id, workspace_id, event_type, payload)
VALUES ('wsdel-outbox-'||$1,$1::uuid,'wsdel.test','{}')`, ws)
}

// 每张被播种的表（+workspace/member 框架行）都必须清零。个人维度的
// personal_access_token 是用户资产、随用户存活，刻意不在列。
var wsdelResidueTables = []string{
	"activity_log", "agent", "agent_builder_draft", "agent_credential",
	"agent_invocation_target", "agent_mcp_server", "agent_model_credential", "agent_skill",
	"agent_task_checkpoint", "agent_task_queue", "agent_to_label", "attachment",
	"automation_webhook_delivery", "autopilot", "autopilot_collaborator",
	"autopilot_quota_period", "autopilot_quota_reservation", "autopilot_rule_version",
	"autopilot_run", "autopilot_subscriber", "autopilot_trigger", "channel_binding_token",
	"channel_chat_context_generation", "channel_chat_session_binding", "channel_inbound_audit",
	"channel_inbound_message_dedup", "channel_installation", "channel_media_pending_object",
	"channel_outbound_card_message", "channel_outbound_message", "channel_reply_delivery",
	"channel_task_delivery", "channel_user_binding", "chat_cancel_pending", "chat_draft_restore",
	"chat_message", "chat_pinned_agent", "chat_session", "client_usage_daily", "comment",
	"comment_reaction", "event_outbox", "label", "member", "notification",
	"notification_preference", "pack_tool_schema", "pack_ui_entry", "pinned_item", "project",
	"project_resource", "quick_action", "search_index_change", "skill", "skill_file",
	"skill_to_label", "source_context_object_intent", "squad", "squad_member",
	"system_wakeup_setting", "task_message", "task_supplement", "task_usage",
	"task_usage_hourly", "ui_token", "work_item", "work_item_batch", "work_item_batch_item",
	"work_item_child_event", "work_item_dependency", "work_item_property_def",
	"work_item_reaction", "work_item_source_context", "work_item_status",
	"work_item_subscriber", "work_item_to_label", "work_item_view",
	"work_item_view_preference", "work_item_wakeup", "work_item_wakeup_receipt",
	"workspace", "workspace_mcp_server",
}

func TestWorkspaceHardDeleteIntegration(t *testing.T) {
	s := newWsDelStack(t)
	ctx := context.Background()

	t.Run("full type data hard delete then 404", func(t *testing.T) {
		s.login("owner", "wsdel-owner@test.orkome.dev")
		ws := s.createWS("owner", "wsdel-full")
		s.seedFullTypeWorkspace(t, ws)

		// 前置：残留下数得出（种子确实活了）
		for _, tbl := range wsdelResidueTables {
			assert.Positive(t, s.count(t, tbl), "precondition: %s should hold seeded rows", tbl)
		}

		rec := s.deleteWS("owner", ws)
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

		for _, tbl := range wsdelResidueTables {
			assert.Zero(t, s.count(t, tbl), "%s must be empty after hard delete", tbl)
		}

		// 重复删除 → 404
		rec = s.deleteWS("owner", ws)
		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	})

	t.Run("failure injection keeps workspace and propagates", func(t *testing.T) {
		s.login("owner2", "wsdel-owner2@test.orkome.dev")
		ws := s.createWS("owner2", "wsdel-fail")
		s.seedFullTypeWorkspace(t, ws)

		// 失败注入：拆掉跨域清单里的 autopilot（连级 FK 一并拆除，
		// 其余表保留）。跨域清理语句执行到 DELETE FROM autopilot 必 42P01。
		_, err := s.pool.Exec(ctx, `DROP TABLE autopilot CASCADE`)
		require.NoError(t, err)

		rec := s.deleteWS("owner2", ws)
		require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

		// 工作区必须整体保留（框架事务回滚：DELETE workspace 未生效）
		var alive bool
		require.NoError(t, s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM workspace WHERE id=$1)`, ws).Scan(&alive))
		assert.True(t, alive, "workspace must survive a failed teardown")

		// 跨域行原样（失败发生在清单前段，后续语句未执行 + 框架事务回滚）
		assert.Equal(t, int64(1), s.count(t, "agent"), "cross-domain rows must roll back")
		assert.Equal(t, int64(1), s.count(t, "squad"))
		assert.Equal(t, int64(1), s.count(t, "skill"))
		assert.Equal(t, int64(1), s.count(t, "chat_session"))
		assert.Equal(t, int64(1), s.count(t, "channel_installation"))

		// work 域行已随 FixE 原语的独立事务先行提交 —— 文件头注"既定接缝"：
		// 失败路径下工作区壳保留，work 域内容先清，重试删除即自愈。
		assert.Zero(t, s.count(t, "work_item"), "FixE cleanup commits independently (documented seam)")
		assert.Zero(t, s.count(t, "label"))
		assert.Zero(t, s.count(t, "work_item_status"))

		// BeforeDelete 预清已独立提交（跨域 label join 表在失败路径下也先没了）
		assert.Zero(t, s.count(t, "agent_to_label"))
		assert.Zero(t, s.count(t, "skill_to_label"))
	})
}
