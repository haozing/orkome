package main

// setup-status 端点（P1-6c）：GET /api/setup-status——新手引导/接入检查页
// 的聚合只读面。一次请求回答"这个工作区配置齐了吗"：
//
//	{"credential_count": N, "agents": [{"id","name","archived","ready"}...]}
//
// 数据源复用 orchestration 装配面（orchHTTPDeps.Repo.ListAgents +
// orchHTTPDeps.Credentials.List），不另起查询链。ready 判定同 A2 的定义
// （agentRuntimeReadyMe，wiring_mcp_me.go）：runtime_config 里
// model/credential_id/base_url 三键都非空。
//
// 挂法与 dispatch 端点同构：authGuardMw + RequireAuth + X-Workspace-Id
// 工作区头（member check）。

import (
	"context"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

// setupStatusDeps 是 setup-status 端点的最小依赖面。
type setupStatusDeps struct {
	Repo        orchapp.Repo
	Credentials *orchapp.CredentialService

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)
}

// mountSetupStatus 把 GET /api/setup-status 挂到 api mux（main.go 调用）。
func mountSetupStatus(mux *http.ServeMux, auth func(http.Handler) http.Handler, d setupStatusDeps) {
	wsGuard := func(next http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := webx.PrincipalFrom(r.Context())
			ws := r.Header.Get("X-Workspace-Id")
			if ws == "" {
				webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
				return
			}
			// PAT scope 闸（P1-1，同 me 端点 workspaceMW 口径）。
			if !p.Scope.AllowsWorkspace(ws) {
				webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "token is not scoped to this workspace", nil)
				return
			}
			ok, err := d.MemberCheck(r.Context(), ws, p.UserID)
			if err != nil || !ok {
				webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "not a member of this workspace", nil)
				return
			}
			p.WorkspaceID = ws
			next.ServeHTTP(w, r)
		})
	}
	mux.Handle("GET /api/setup-status", auth(webx.RequireAuth(wsGuard(handleSetupStatus(d)))))
}

func handleSetupStatus(d setupStatusDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := webx.PrincipalFrom(r.Context()).WorkspaceID
		ctx := r.Context()

		agents, err := d.Repo.ListAgents(ctx, ws)
		if err != nil {
			webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
			return
		}
		credentialCount := 0
		if d.Credentials != nil {
			creds, err := d.Credentials.List(ctx, ws)
			if err != nil {
				webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
				return
			}
			credentialCount = len(creds)
		}

		out := make([]map[string]any, 0, len(agents))
		for _, a := range agents {
			out = append(out, map[string]any{
				"id":       a.ID,
				"name":     a.Name,
				"archived": a.Archived,
				// ready 判定复用 A2 口径（agentRuntimeReadyMe）——与
				// 助理 MCP 的 list_agents 发现面同一套规则，不另立标准。
				"ready": agentRuntimeReadyMe(a.RuntimeConfig),
			})
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{
			"credential_count": credentialCount,
			"agents":           out,
		})
	}
}
