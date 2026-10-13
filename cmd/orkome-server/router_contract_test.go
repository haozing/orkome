package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	adminhttp "github.com/haozing/ploykit/admin/adapters/http"
	audithttp "github.com/haozing/ploykit/audit/adapters/http"
	"github.com/haozing/ploykit/authz"
	fwnotifyhttp "github.com/haozing/ploykit/notify/adapters/http"
	fwnotifapp "github.com/haozing/ploykit/notify/app"
	"github.com/haozing/ploykit/platform/storagex"
	"github.com/haozing/ploykit/platform/webx"
	quotahttp "github.com/haozing/ploykit/quota/adapters/http"
	workspacehttp "github.com/haozing/ploykit/workspace/adapters/http"
	webhookhttp "github.com/haozing/ploykit/webhooks/adapters/http"
	webhookapp "github.com/haozing/ploykit/webhooks/app"
	autohttp "orkome/internal/automation/adapters/http"
	autoapp "orkome/internal/automation/app"
	chhttp "orkome/internal/channels/adapters/http"
	chapp "orkome/internal/channels/app"
	contenthttp "orkome/internal/content/adapters/http"
	contentapp "orkome/internal/content/app"
	"orkome/internal/contract/apierr"
	convhttp "orkome/internal/conversation/adapters/http"
	convapp "orkome/internal/conversation/app"
	notifhttp "orkome/internal/notification/adapters/http"
	notifapp "orkome/internal/notification/app"
	packhttp "orkome/internal/pack/adapters/http"
	packapp "orkome/internal/pack/app"
	orchhttp "orkome/internal/orchestration/adapters/http"
	orchapp "orkome/internal/orchestration/app"
	skillhttp "orkome/internal/skills/adapters/http"
	skillapp "orkome/internal/skills/app"
	idhttp "orkome/internal/identity/adapters/http"
	workhttp "orkome/internal/work/adapters/http"
	workapp "orkome/internal/work/app"
)

// rawRoutes — routeTable rows whose Pattern is an absolute composition-root
// path (product routes the real server mounts on the root mux); the forward
// walk must not prepend "/api".
var rawRoutes = map[string]bool{
	"POST /cli-token":                   true, // idhttp.MountExtensions，root 面
	"GET /uploads/*":                    true, // contenthttp.MountUploads（LOCAL 模式），root 面
	"HEAD /uploads/*":                   true,
	"POST /webhooks/autopilots/{token}": true, // autohttp.MountPublic，HMAC+限流自防护
}

// subRoutes — workspacehttp 子 mux 的叶子（挂在 /api/workspaces/{workspaceId}/
// 子树后面，顶层走查只见委托 mount）；TestWorkspaceSubroutesDeclared 用
// workspacehttp.SubrouteInfos() 逐条对账两侧。
var subRoutes = map[string]bool{
	"GET /workspaces/{id}/members":                 true,
	"PATCH /workspaces/{id}/members/{userId}":      true,
	"DELETE /workspaces/{id}/members/{userId}":     true,
	"GET /workspaces/{id}/roles":                   true,
	"PUT /workspaces/{id}/roles/{role}":            true,
	"DELETE /workspaces/{id}/roles/{role}":         true,
	"POST /workspaces/{id}/transfer-ownership":     true,
	"POST /workspaces/{id}/leave":                  true,
	"GET /workspaces/{id}/invitations":             true,
	"POST /workspaces/{id}/invitations":            true,
	"DELETE /workspaces/{id}/invitations/{invId}":  true,
	"GET /workspaces/{id}/share-links":             true,
	"POST /workspaces/{id}/share-links":            true,
	"DELETE /workspaces/{id}/share-links/{linkId}": true,
}

var routeTable = []struct{ Method, Pattern string }{

	{"GET", "/work-items"}, {"POST", "/work-items"},
	{"GET", "/work-items/{id}"}, {"PUT", "/work-items/{id}"}, {"DELETE", "/work-items/{id}"},
	{"POST", "/work-items/{id}/move"},
	{"POST", "/work-items/batch-update"}, {"POST", "/work-items/batch-delete"},
	{"POST", "/work-items/query"},
	{"POST", "/work-items/table/rows"}, {"POST", "/work-items/table/groups"}, {"POST", "/work-items/table/facets"},
	{"GET", "/work-items/search"},
	{"GET", "/work-items/child-progress"}, {"GET", "/work-items/children"},
	{"GET", "/work-items/grouped"}, {"GET", "/work-items/assignee-frequency"},
	{"POST", "/work-items/preview-trigger"},
	{"POST", "/work-items/{id}/comments/trigger-preview"},
	{"POST", "/work-items/{id}/reactions"}, {"DELETE", "/work-items/{id}/reactions"},
	{"GET", "/work-items/{id}/usage"},
	{"GET", "/work-items/{id}/metadata/{key}"}, {"PUT", "/work-items/{id}/metadata/{key}"},
	{"DELETE", "/work-items/{id}/metadata/{key}"},
	{"GET", "/work-items/{id}/duplicates"},
	{"GET", "/work-items/{id}/labels"}, {"POST", "/work-items/{id}/labels"},
	{"DELETE", "/work-items/{id}/labels/{labelId}"},
	{"POST", "/work-items/{id}/subscribers"},
	{"GET", "/work-items/{id}/properties/{key}"}, {"PUT", "/work-items/{id}/properties/{key}"},
	{"DELETE", "/work-items/{id}/properties/{key}"},
	{"POST", "/work-items/quick-create"},
	{"GET", "/work-items/{id}/comments"}, {"POST", "/work-items/{id}/comments"},
	{"GET", "/comments/{commentId}/sub-work-item-preview"}, {"POST", "/comments/{commentId}/sub-work-items"},
	{"PUT", "/comments/{commentId}"}, {"DELETE", "/comments/{commentId}"},
	{"POST", "/comments/{commentId}/resolve"}, {"DELETE", "/comments/{commentId}/resolve"},
	{"POST", "/comments/{commentId}/reactions"}, {"DELETE", "/comments/{commentId}/reactions"},
	{"DELETE", "/comments/{commentId}/keep-replies"},
	{"GET", "/labels"}, {"POST", "/labels"},
	{"GET", "/labels/{labelId}"},
	{"DELETE", "/labels/{labelId}"}, {"PUT", "/labels/{labelId}"},
	{"GET", "/projects"}, {"POST", "/projects"},
	{"GET", "/projects/{id}"}, {"PUT", "/projects/{id}"}, {"DELETE", "/projects/{id}"},
	{"GET", "/projects/search"},
	{"GET", "/projects/{id}/resources"}, {"POST", "/projects/{id}/resources"},
	{"PUT", "/projects/{id}/resources/{resourceId}"}, {"DELETE", "/projects/{id}/resources/{resourceId}"},
	{"GET", "/work-item-statuses"}, {"POST", "/work-item-statuses"},
	{"PATCH", "/work-item-statuses/{statusKey}"},
	{"POST", "/work-item-statuses/reorder"},
	{"GET", "/properties"}, {"POST", "/properties"},
	{"GET", "/properties/{key}"}, {"PATCH", "/properties/{key}"}, {"DELETE", "/properties/{key}"},
	{"GET", "/work-items/{id}/timeline"}, {"GET", "/work-items/{id}/subscribers"},

	{"GET", "/work-item-views"}, {"PUT", "/work-item-views"},
	{"GET", "/work-item-view-preferences"}, {"PUT", "/work-item-view-preferences"},
	{"POST", "/unsubscribe"}, {"POST", "/unsubscribe/subtree"},

	{"POST", "/work-items/{id}/batches"}, {"GET", "/work-items/{id}/batches"},
	{"GET", "/batches/{id}"}, {"GET", "/batches/{id}/items"},
	{"GET", "/batches/{id}/items/page"},
	{"POST", "/batches/{id}/start"}, {"POST", "/batches/{id}/pause"},
	{"POST", "/batches/{id}/resume"},
	{"POST", "/batches/{id}/cancel"}, {"POST", "/batches/{id}/retry-failed"},

	{"GET", "/agents"}, {"POST", "/agents"},
	{"GET", "/credentials"}, {"POST", "/credentials"}, {"DELETE", "/credentials/{credentialId}"},
	{"GET", "/agents/{agentId}"}, {"PUT", "/agents/{agentId}"},
	{"POST", "/agents/{agentId}/archive"}, {"POST", "/agents/{agentId}/restore"},
	{"POST", "/agents/{agentId}/runtime-credentials"},
	{"POST", "/mcp/w/{workspaceId}/agent"},
	{"GET", "/mcp/w/{workspaceId}/agent"},
	{"GET", "/ui-entries"},
	{"PUT", "/ui-entries/{pack}"},
	{"DELETE", "/ui-entries/{pack}"},

	{"GET", "/packs"}, {"PUT", "/packs"},
	{"GET", "/packs/{packId}"}, {"DELETE", "/packs/{packId}"},
	{"PUT", "/packs/{packId}/subscriptions"},

	{"GET", "/workspaces/{workspaceId}/webhooks/events"},
	{"GET", "/workspaces/{workspaceId}/webhooks/deliveries"},
	{"POST", "/workspaces/{workspaceId}/webhooks/deliveries/{deliveryId}/redeliver"},
	{"GET", "/workspaces/{workspaceId}/webhooks/subscriptions"},
	{"POST", "/workspaces/{workspaceId}/webhooks/subscriptions"},
	{"POST", "/workspaces/{workspaceId}/webhooks/subscriptions/{subscriptionId}/ping"},
	{"POST", "/workspaces/{workspaceId}/webhooks/subscriptions/{subscriptionId}/rotate-secret"},
	{"PATCH", "/workspaces/{workspaceId}/webhooks/subscriptions/{subscriptionId}"},
	{"DELETE", "/workspaces/{workspaceId}/webhooks/subscriptions/{subscriptionId}"},
	{"POST", "/agents/{agentId}/cancel-tasks"},
	{"GET", "/agents/{agentId}/mcp-servers"}, {"POST", "/agents/{agentId}/mcp-servers"},
	{"PUT", "/agents/{agentId}/mcp-servers/{id}"}, {"DELETE", "/agents/{agentId}/mcp-servers/{id}"},
	{"GET", "/agents/{agentId}/labels"}, {"POST", "/agents/{agentId}/labels"},
	{"DELETE", "/agents/{agentId}/labels/{labelId}"},
	{"GET", "/agents/{agentId}/env"}, {"PUT", "/agents/{agentId}/env"},
	{"GET", "/agents/{agentId}/tasks"},
	{"GET", "/working-agents"}, {"GET", "/agent-run-counts"}, {"GET", "/agent-activity-30d"},
	{"GET", "/tasks"},
	{"GET", "/tasks/{taskId}"},
	{"GET", "/squads"}, {"POST", "/squads"},
	{"GET", "/squads/{squadId}"}, {"PUT", "/squads/{squadId}"}, {"DELETE", "/squads/{squadId}"},
	{"GET", "/agent-run-snapshot"},
	{"GET", "/squads/{squadId}/members"}, {"GET", "/squads/{squadId}/members/status"},
	{"PATCH", "/squads/{squadId}/members/{agentId}"},
	{"POST", "/work-items/{id}/squad-evaluated"},
	{"POST", "/squads/{squadId}/members"}, {"DELETE", "/squads/{squadId}/members/{agentId}"},
	{"GET", "/work-items/{id}/active-task"}, {"GET", "/work-items/{id}/tasks"},
	{"POST", "/work-items/{id}/tasks/{taskId}/cancel"}, {"POST", "/work-items/{id}/rerun"},
	{"POST", "/work-items/{id}/tasks/{taskId}/supplements"},
	{"POST", "/work-items/{id}/tasks/{taskId}/supplements/{commentId}/retry"},
	{"POST", "/tasks/{taskId}/cancel"},
	{"POST", "/tasks/{taskId}/claim"},
	{"POST", "/tasks/{taskId}/complete"},
	{"POST", "/tasks/{taskId}/fail"},
	{"POST", "/tasks/{taskId}/messages"},
	{"GET", "/agent-builder/sessions"}, {"POST", "/agent-builder/sessions"},
	{"GET", "/agent-builder/sessions/{sessionId}/draft"}, {"PUT", "/agent-builder/sessions/{sessionId}/draft"},
	{"GET", "/workspaces/{workspaceId}/mcp-servers"}, {"POST", "/workspaces/{workspaceId}/mcp-servers"},
	{"PUT", "/workspaces/{workspaceId}/mcp-servers/{id}"}, {"DELETE", "/workspaces/{workspaceId}/mcp-servers/{id}"},
	{"POST", "/workspaces/{workspaceId}/mcp-servers/{id}/probe-tools"}, // fe80ac8 探针漏登契约表（reverse walk 实证）
	{"GET", "/tasks/{taskId}/messages"},
	{"POST", "/tasks/{taskId}/retry-source-context"},
	{"POST", "/tasks/{taskId}/approve"}, {"POST", "/tasks/{taskId}/deny"},
	{"GET", "/tasks/{taskId}/interrupt"},

	{"GET", "/chat/sessions"}, {"POST", "/chat/sessions"},
	{"GET", "/chat/sessions/{sessionId}"},
	{"PATCH", "/chat/sessions/{sessionId}"}, {"DELETE", "/chat/sessions/{sessionId}"},
	{"POST", "/chat/sessions/{sessionId}/pin"}, {"POST", "/chat/sessions/{sessionId}/unpin"},
	{"POST", "/chat/sessions/{sessionId}/archive"}, {"POST", "/chat/sessions/{sessionId}/unarchive"},
	{"POST", "/chat/sessions/{sessionId}/messages"}, {"GET", "/chat/sessions/{sessionId}/messages"},
	{"GET", "/chat/sessions/{sessionId}/pending-task"}, {"DELETE", "/chat/sessions/{sessionId}/pending-task"},
	{"DELETE", "/chat/sessions/{sessionId}/queued-tasks"},
	{"POST", "/chat/sessions/{sessionId}/queued-tasks/{taskId}/prioritize"},
	{"GET", "/chat/pending-tasks"}, {"GET", "/chat/pending-tasks/has-any"},
	{"GET", "/chat/pinned-agents"}, {"POST", "/chat/pinned-agents"},
	{"DELETE", "/chat/pinned-agents"},
	{"POST", "/chat/sessions/{sessionId}/read"},
	{"GET", "/chat/sessions/{sessionId}/draft-restores"}, {"DELETE", "/chat/sessions/{sessionId}/draft-restores"},
	{"GET", "/chat/history"}, {"GET", "/chat/thread"},
	{"GET", "/quick-actions"}, {"POST", "/quick-actions"},
	{"PATCH", "/quick-actions/{id}"}, {"DELETE", "/quick-actions/{id}"},
	{"POST", "/work-items/{id}/quick-actions/{quickActionId}/run"},
	{"POST", "/work-items/{id}/quick-actions/{quickActionId}/render"},

	{"GET", "/notifications"}, {"GET", "/notifications/unread-count"},
	{"GET", "/notifications/archived"}, {"GET", "/notifications/summary"}, {"GET", "/notifications/facets"},
	{"POST", "/notifications/mark-all-read"}, {"POST", "/notifications/archive-all"},
	{"POST", "/notifications/archive-all-read"},
	{"POST", "/notifications/{id}/read"}, {"POST", "/notifications/{id}/unread"},
	{"POST", "/notifications/{id}/archive"}, {"POST", "/notifications/{id}/unarchive"},
	{"GET", "/notification-preferences"}, {"PATCH", "/notification-preferences"},
	{"PUT", "/notification-preferences"},

	{"GET", "/notifications/badge"}, {"POST", "/notifications/read-all"},
	{"PUT", "/notification-preferences/{type}"},

	{"GET", "/skills"}, {"POST", "/skills"},
	{"GET", "/skills/{skillId}/labels"}, {"POST", "/skills/{skillId}/labels"},
	{"DELETE", "/skills/{skillId}/labels/{labelId}"},
	{"GET", "/skills/search"}, {"POST", "/skills/import"},
	{"GET", "/skills/{skillId}"}, {"DELETE", "/skills/{skillId}"},
	{"POST", "/skills/{skillId}/refresh"}, {"GET", "/skills/{skillId}/files"},
	{"PUT", "/skills/{skillId}"}, {"PUT", "/skills/{skillId}/files"},
	{"DELETE", "/skills/{skillId}/files/{fileId}"},
	{"GET", "/skills/{skillId}/agents"},
	{"GET", "/agents/{agentId}/skills"}, {"PUT", "/agents/{agentId}/skills"},
	{"POST", "/agents/{agentId}/skills/add"},
	{"PUT", "/agents/{agentId}/skills/{skillId}/enabled"},
	{"DELETE", "/agents/{agentId}/skills/{skillId}"},

	{"POST", "/upload-file"},
	{"GET", "/work-items/{id}/attachments"},
	{"GET", "/attachments/{id}"}, {"DELETE", "/attachments/{id}"},
	{"GET", "/attachments/{id}/download"}, {"GET", "/attachments/{id}/content"}, {"GET", "/attachments/{id}/signed-download"},
	{"GET", "/avatars/*"},

	{"POST", "/work-items/{id}/wakeups"}, {"GET", "/work-items/{id}/wakeups"},
	{"POST", "/work-items/{id}/wakeups/{wakeupId}/enable"},
	{"POST", "/autopilots"}, {"POST", "/autopilots/{id}/trigger"},
	{"GET", "/autopilots"}, {"GET", "/autopilots/{id}"},
	{"PATCH", "/autopilots/{id}"}, {"DELETE", "/autopilots/{id}"},
	{"PUT", "/autopilots/{id}/triggers/{triggerId}/signing-secret"},
	{"POST", "/autopilots/{id}/triggers/{triggerId}/rotate-webhook-token"},

	{"GET", "/work-items/{id}/wakeups/{wakeupId}"}, {"PUT", "/work-items/{id}/wakeups/{wakeupId}"},
	{"DELETE", "/work-items/{id}/wakeups/{wakeupId}"},
	{"POST", "/work-items/{id}/wakeups/{wakeupId}/disable"},
	{"PUT", "/work-items/{id}/wakeups/{wakeupId}/instruction"},
	{"POST", "/work-items/{id}/wakeups/{wakeupId}/checkin"},
	{"POST", "/work-items/{id}/wakeups/{wakeupId}/trigger"},
	{"GET", "/work-items/{id}/wakeups/{wakeupId}/tasks"},
	{"GET", "/work-item-wakeups"}, {"GET", "/work-item-wakeup-paused"},
	{"GET", "/work-item-wakeup-summaries"},
	{"GET", "/system-wakeups"}, {"PUT", "/system-wakeups"},
	{"GET", "/system-wakeups/{wakeupId}"}, {"PUT", "/system-wakeups/{wakeupId}"},
	{"GET", "/work-items/{id}/system-wakeups"}, {"POST", "/work-items/{id}/system-wakeups"},
	{"POST", "/autopilots/cron-preview"},
	{"POST", "/channels/title-callback"},
	{"GET", "/autopilots/{id}/collaborators"}, {"POST", "/autopilots/{id}/collaborators"},
	{"DELETE", "/autopilots/{id}/collaborators"},
	{"POST", "/autopilots/{id}/triggers"},
	{"PATCH", "/autopilots/{id}/triggers/{triggerId}"}, {"DELETE", "/autopilots/{id}/triggers/{triggerId}"},
	{"GET", "/autopilots/{id}/runs"}, {"GET", "/autopilots/{id}/runs/{runId}"},
	{"GET", "/deliveries"}, {"GET", "/deliveries/{deliveryId}"},
	{"POST", "/deliveries/{deliveryId}/replay"},

	// ── P4 双向守卫补齐（fix-wave3，2026-10-12）─────────────────────────────
	// 以下路由真实挂载但此前漏登记——TestRouteContractReverseWalk 首跑当场
	// 抓出 37 条，与 batch1 P1 / batch2 FP-19 的缺口清单一致。

	// work：客户端用量上报（batch2 FP-12 usage 链的写入口；wiring 装配
	// ClientUsage 非 nil 才挂）
	{"POST", "/client-usage"},

	// orchestration：pack 工具输出 schema 存档（迁移 1035 / B1 交付面）
	{"GET", "/pack-tool-schemas"},
	{"PUT", "/pack-tool-schemas/{pack}"}, {"DELETE", "/pack-tool-schemas/{pack}"},

	// identity 扩展面（idhttp.MountExtensions，root 挂载）：侧边栏 pins×4、
	// ui-token 三段票据流（FP-15 P1-1）、CLI 会话→PAT 交换
	{"GET", "/pins"}, {"PUT", "/pins"}, {"PUT", "/pins/reorder"},
	{"DELETE", "/pins/{itemType}/{itemId}"},
	{"POST", "/ui-tokens"}, {"POST", "/ui-tokens/consume"},
	{"POST", "/cli-token"},

	// 框架 workspace 域（workspacehttp.Mount）：工作区、邀请、分享链接
	// （batch1 根因 C：只有 /redeem，不存在 /join）
	{"GET", "/workspaces"}, {"POST", "/workspaces"},
	{"PATCH", "/workspaces/{id}"}, {"DELETE", "/workspaces/{id}"},
	{"GET", "/invitations/mine"},
	{"POST", "/invitations/{id}/accept"}, {"POST", "/invitations/{id}/decline"},
	{"POST", "/share-links/redeem"},

	// 商业化面（wiring_commercial → quotahttp/audithttp，P6-2 装配范围）
	{"GET", "/usage"},
	{"GET", "/audit"}, {"GET", "/audit/export.csv"},

	// 平台管理员面（adminhttp.Mount，root 挂载；FP-19 的 admin×10）
	{"GET", "/admin/stats"}, {"GET", "/admin/users"},
	{"PATCH", "/admin/users/{id}/status"}, {"PATCH", "/admin/users/{id}/admin"},
	{"POST", "/admin/users/{id}/impersonate"},
	{"GET", "/admin/workspaces"}, {"PATCH", "/admin/workspaces/{id}/plan"},
	{"GET", "/admin/audit"}, {"GET", "/admin/audit/export.csv"},
	{"GET", "/admin/analytics"},

	// content：/uploads/* 静态面（LOCAL 模式，root 挂载；MountUploads，
	// Local backend 为 nil 的部署形态不挂）
	{"GET", "/uploads/*"}, {"HEAD", "/uploads/*"},

	// automation：公开 webhook 入站（root 挂载，自带 HMAC + 限流防护，
	// 全局 CSRF 豁免面——FP-13）
	{"POST", "/webhooks/autopilots/{token}"},
}

func buildTestAPIRouter() *http.ServeMux {
	r := http.NewServeMux()
	auth := func(next http.Handler) http.Handler { return next }
	member := func(context.Context, string, string) (bool, error) { return true, nil }
	// 挂载顺序镜像 main.go：workspacehttp.Mount 最先（生产 main.go:243）。
	workspacehttp.Mount(r, workspacehttp.Deps{})
	workhttp.Mount(r, workhttp.Deps{
		WorkItems: &workapp.WorkItemService{}, Comments: &workapp.CommentService{},
		SourceCtx: &workapp.SourceContextService{},
		Catalog:   &workapp.CatalogService{},
		Views: &workapp.ViewService{}, Search: &workapp.SearchService{},
		Timeline:    func(context.Context, string, int, string) ([]workapp.TimelineEntry, error) { return nil, nil },
		Subscribers: func(context.Context, string) ([]workapp.Subscriber, error) { return nil, nil },
		ClientUsage: workapp.NewClientUsageService(nil), // 生产 wiring 装配非 nil → POST /api/client-usage
		MemberCheck: member,
	}, auth)
	orchhttp.Mount(r, orchhttp.Deps{
		Settle: &orchapp.SettlementService{}, Agents: &orchapp.AgentService{},
		Repo: stubRepo{}, MCP: &orchapp.MCPServerService{}, Builder: &orchapp.AgentBuilderService{},
		Batches:     &orchapp.BatchService{},
		Credentials: &orchapp.CredentialService{},
		PackToolSchemas: orchapp.NewPackToolSchemaService(nil), // 生产装配非 nil → /api/pack-tool-schemas×3
		AgentLabels: &orchhttp.AgentLabelEndpoints{
			List:   func(context.Context, string, string) ([]orchhttp.AgentLabelView, error) { return nil, nil },
			Attach: func(context.Context, string, string, string) ([]orchhttp.AgentLabelView, error) { return nil, nil },
			Detach: func(context.Context, string, string, string) ([]orchhttp.AgentLabelView, error) { return nil, nil },
		},
		MemberCheck:   member,
		External:      orchapp.NewExternalTaskService(nil, nil),
		PackUIEntries: orchapp.NewPackUIEntryService(nil),
		MintRuntimeCredential: func(context.Context, string, string, string) (string, error) {
			return "", nil
		},
		TaskMessages: func(context.Context, string, string, orchapp.TaskMessagesQuery) ([]orchapp.TaskMessage, error) {
			return nil, nil
		},
		RetrySourceContext: func(context.Context, string, string, string) (any, error) {
			return nil, apierr.New(apierr.NotFound, "x")
		},
	}, auth)
	convhttp.Mount(r, convhttp.Deps{
		Chat: &convapp.ChatService{}, QuickActions: &convapp.QuickActionService{},
		Pins:        &convapp.PinService{},
		MemberCheck: member, TaskStatus: func(context.Context, string, string) (string, string, bool, error) { return "", "", false, nil },
		Canceller: stubCanceller{},
	}, auth)
	notifhttp.Mount(r, notifhttp.Deps{Notification: &notifapp.NotificationService{}, Prefs: &notifapp.PreferenceService{}}, auth)
	packhttp.Mount(r, packhttp.Deps{Packs: &packapp.PackService{}, MemberCheck: member}, auth)

	// framework notify domain: Mount through the filtered router — the product
	// Mount above supersedes four of the framework patterns, so a direct
	// notifyhttp.Mount(r, ...) would panic on duplicate registration.
	fwNotifyDeps := fwnotifyhttp.Deps{Svc: fwnotifapp.NewNotifyService(nil, nil, nil)}
	fwnotifyhttp.Mount(NotificationWiring{Framework: fwNotifyDeps}.FrameworkRouter(r, auth), fwNotifyDeps)

	// framework webhooks domain: Mount(webx.Router, Deps) — auth comes from the
	// globally applied Authenticate middleware (PrincipalFromRequest).
	memberRole := func(context.Context, string, string) (string, bool, error) { return "owner", true, nil }
	webhookhttp.Mount(r, webhookhttp.Deps{
		Service:     webhookapp.NewWebhookService(nil, webhookapp.NewEventCatalog(), nil, time.Now),
		MemberCheck: memberRole,
		Authz:       authz.New(authz.NewCatalog(authz.DefaultPerms()...), authz.DefaultRoles()),
	})

	r.HandleFunc("POST /api/mcp/w/{workspaceId}/agent", func(http.ResponseWriter, *http.Request) {})
	r.HandleFunc("GET /api/mcp/w/{workspaceId}/agent", func(http.ResponseWriter, *http.Request) {})

	// 商业化面（main.go:253 comm.MountAPI → quotahttp + audithttp）：
	// 注册期不解引用 nil 服务，测试只关心 pattern 面。
	quotahttp.Mount(r, quotahttp.Deps{})
	audithttp.Mount(r, audithttp.Deps{})
	// 平台管理员面（main.go:254 comm.MountAdmin → adminhttp.Mount，root 挂载、
	// /api 前缀 pattern）：FP-19 的 admin×10。
	adminhttp.Mount(r, adminhttp.Deps{AuthCfg: &webx.AuthConfig{}})
	// 产品身份扩展面（main.go:311 idhttp.MountExtensions，root 挂载）：
	// 侧边栏 pins×4、ui-token 三段票据流×2、CLI 换 PAT。
	idhttp.MountExtensions(r, auth, idhttp.Deps{})

	autoDeps := autohttp.Deps{Wakeups: &autoapp.WakeupService{}, Autopilots: &autoapp.AutopilotService{}, Webhooks: &autoapp.WebhookService{}}
	autohttp.Mount(r, autoDeps, auth)
	// 公开 webhook 入站在生产挂在 root（main.go:307）；挂进同一测试 mux 以便
	// 枚举对账（pattern 与 Handler 解析语义不变）。
	autohttp.MountPublic(r, autoDeps)

	skillhttp.Mount(r, skillhttp.Deps{
		Skills: &skillapp.SkillService{}, Import: &skillapp.ImportService{}, MemberCheck: member,
		SkillLabels: skillhttp.SkillLabelEndpoints{
			List:   func(context.Context, string, string) ([]skillhttp.SkillLabelView, error) { return nil, nil },
			Attach: func(context.Context, string, string, string) ([]skillhttp.SkillLabelView, error) { return nil, nil },
			Detach: func(context.Context, string, string, string) ([]skillhttp.SkillLabelView, error) { return nil, nil },
		},
	}, auth)

	chhttp.Mount(r, chhttp.Deps{Titles: &chapp.TitleCallbackService{}})
	contentDeps := contenthttp.Deps{
		Attachments: &contentapp.AttachmentService{}, Avatars: &contentapp.AvatarService{},
		Local: &storagex.LocalBackend{}, MemberCheck: member,
	}
	contenthttp.MountAPI(r, contentDeps, auth)
	// /uploads/* 静态面（LOCAL 模式，root 挂载）同样纳入枚举对账。
	contenthttp.MountUploads(r, contentDeps)
	return r
}

type stubCanceller struct{}

func (stubCanceller) CancelChatTask(context.Context, string, string) (string, string, bool, error) {
	return "", "", false, nil
}

// TestRouteContract resolves every declared route against the mounted mux.
// stdlib ServeMux cannot enumerate registered patterns via its public API, so
// this direction (table -> mux) resolves through Handler(), which only
// matches and never invokes the handler. The reverse direction (mounted,
// undeclared) is guarded by TestRouteContractReverseWalk in
// route_bidirectional_test.go, which enumerates the mux via the internal
// routing tree.
func TestRouteContractWalk(t *testing.T) {
	mux := buildTestAPIRouter()
	var missing []string
	for _, e := range routeTable {
		key := e.Method + " " + e.Pattern
		path := e.Pattern
		if !rawRoutes[key] {
			path = "/api" + path
		}
		req := httptest.NewRequest(e.Method, fillParams(path), nil)
		_, pattern := mux.Handler(req)
		if i := strings.Index(pattern, " "); i >= 0 {
			pattern = pattern[i+1:] // Handler() returns "METHOD /path"; keep the path half
		}
		want := canonical(path)
		if subRoutes[key] {
			// Sub-mux leaves answer behind the workspace subtree mount; what
			// Handler() reports at the top level is the delegation pattern.
			want = canonical("/api/workspaces/{workspaceId}/")
		}
		if canonical(pattern) != want {
			missing = append(missing, key+" (resolved: "+pattern+")")
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "declared in the route table but not mounted (reconciliation missing): %v", missing)
}

var (
	paramSeg = regexp.MustCompile(`\{[a-zA-Z]+(\.{3})?\}`)
	wildSeg  = regexp.MustCompile(`/\*$`)
)

func fillParams(p string) string {
	return paramSeg.ReplaceAllString(p, "00000000-0000-0000-0000-000000000000")
}

// canonical collapses param names and chi-style wildcards so the table's {id}
// matches the registered {workItemId} etc. (same normalization the chi.Walk
// version applied).
func canonical(p string) string {
	p = paramSeg.ReplaceAllString(p, "{}")
	p = wildSeg.ReplaceAllString(p, "/{}")
	return strings.TrimSuffix(p, "/")
}

type stubRepo struct{ orchapp.Repo }

// TestShareLinksContractRedeem 钉死 batch1 根因 C 的装配层回归：框架
// workspacehttp.Mount 只有 POST /api/share-links/redeem（前端
// identity.ts joinByShareCode 已改调 redeem），不存在 /join，也不存在
// GET /share-links/{code}——契约清单（routes.go RouteContract）与测试
// 都不得再出现幽灵条目。
func TestShareLinksContractRedeem(t *testing.T) {
	r := http.NewServeMux()
	workspacehttp.Mount(r, workspacehttp.Deps{})

	resolve := func(method, path string) (string, bool) {
		_, pattern := r.Handler(httptest.NewRequest(method, path, nil))
		return pattern, pattern != ""
	}

	pattern, ok := resolve("POST", "/api/share-links/redeem")
	require.True(t, ok, "POST /api/share-links/redeem must be mounted")
	require.Equal(t, "POST /api/share-links/redeem", pattern)

	_, ok = resolve("POST", "/api/share-links/join")
	require.False(t, ok, "POST /api/share-links/join is a ghost route (contract says /redeem)")

	_, ok = resolve("GET", "/api/share-links/00000000-0000-0000-0000-000000000000")
	require.False(t, ok, "GET /api/share-links/{code} is a ghost route (removed from contract)")
}
