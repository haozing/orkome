// Package http 是 pack 域的 HTTP 面（pack 一等公民化 P1 + 平台化）：
//
//	GET    /api/packs                              member  列表（含资产计数）
//	GET    /api/packs/{packId}                     member  详情聚合（五区块）
//	PUT    /api/packs                              admin   provision upsert（第 0 步拿 pack_id）
//	DELETE /api/packs/{packId}                     admin   P1 最小卸载（FK 分层删除语义）
//	PUT    /api/packs/{packId}/subscriptions       admin   provision 订阅关联（连接表）
//	GET    /api/packs/{packId}/usage-document      member  LLM 用法文档（平台化）
//	POST   /api/agents/{agentId}/pack              admin   agent 挂载/卸载 pack（平台化）
//	POST   /api/skills/{skillId}/pack              admin   skill 挂载/卸载 pack（平台化）
//
// auth+member 模式对齐 internal/notification/adapters/http/routes.go；
// admin 门槛对齐 internal/orchestration/adapters/http 的 requireWorkspaceAdmin。
package http

import (
	"context"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"

	"orkome/internal/contract/apierr"
	"orkome/internal/pack/app"
)

type Deps struct {
	Packs *app.PackService

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)

	// RoleOf 解析 workspace 内角色（principal.Role 为空时兜底——PAT 调用方
	// principal 不带 role，provision 全靠 admin PAT 走 PUT/DELETE）。
	RoleOf func(ctx context.Context, workspaceID, userID string) (string, error)

	// ---- 平台化增补：全部可选，nil = 对应端点 503 ----

	// AgentBinder / SkillBinder：agent/skill 归属写的窄端口（各自域 repo
	// 的 SetXxxPack 经组合根适配注入——packhttp 不 import 那两个域）。
	AgentBinder AgentBinder
	SkillBinder SkillBinder

	// ToolDetailsWired：usage-document 的端口在 Ports.ToolDetails 侧装配，
	// HTTP 层只需要一个"是否接线"的旗子做 503 短路。
	ToolDetailsWired bool
}

// Mount 注册 pack 面。auth 是组合根的 auth guard；member 路由额外过
// X-Workspace-Id 头 + active-membership 闸，admin 路由再加 owner/admin 门槛。
func Mount(mux *http.ServeMux, d Deps, auth func(http.Handler) http.Handler) {
	reg := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(webx.RequireAuth(d.wsFromHeader(h))))
	}

	reg("GET /api/packs", d.handleList)
	reg("GET /api/packs/{packId}", d.handleGet)
	reg("PUT /api/packs", d.handleUpsert)
	reg("DELETE /api/packs/{packId}", d.handleDelete)
	reg("PUT /api/packs/{packId}/subscriptions", d.handleLinkSubscription)
	// 平台化端点（install 向导端点已退役，见 handlers_platform.go 头注）
	reg("GET /api/packs/{packId}/usage-document", d.handleUsageDocument)
	reg("POST /api/agents/{agentId}/pack", d.handleAgentPack)
	reg("POST /api/skills/{skillId}/pack", d.handleSkillPack)
}

// wsFromHeader 对齐 notification 的同名中间件：X-Workspace-Id 必带 +
// membership 校验 + principal 注入 workspace（含 role 兜底解析，供 admin 门槛用）。
func (d Deps) wsFromHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		ws := r.Header.Get("X-Workspace-Id")
		if p == nil || ws == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
			return
		}
		if d.MemberCheck == nil {
			webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "member check not wired", nil)
			return
		}
		ok, err := d.MemberCheck(r.Context(), ws, p.UserID)
		if err != nil || !ok {
			webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "not a workspace member", nil)
			return
		}
		if p.Role == "" && d.RoleOf != nil {
			role, err := d.RoleOf(r.Context(), ws, p.UserID)
			if err != nil {
				webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "role lookup failed", nil)
				return
			}
			p.Role = role
		}
		p.WorkspaceID = ws
		next.ServeHTTP(w, r)
	})
}

// requireWorkspaceAdmin 对齐 orchestration http 的同名闸（owner/admin 放行）。
func requireWorkspaceAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := webx.PrincipalFrom(r.Context())
	if p.Role != "owner" && p.Role != "admin" {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "workspace owner or admin role required", nil)
		return false
	}
	return true
}
