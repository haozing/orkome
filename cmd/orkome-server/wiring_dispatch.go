package main

// 派单显式化（P1-6a/P1-6b）：
//   - mountDispatch：POST /api/work-items/{workItemId}/dispatch——显式派单
//     端点。body {agent_id}，复用 main 作用域现成的 EnqueueAssignment
//     （与指派变化触发的隐式链同一条入队路径），把"派没派上、为什么没派
//     上"从事件总线的静默吞掉变成显式 HTTP 契约：
//       201 {"task_id": ..., "result": "queued"}
//       409 {"error":"E_ACTIVE_DUPLICATE","active_task_id":...,"reason":"already_active"}
//       422 {"error":"E_VALIDATION","reason_code": <dispatchreason 码>}
//     注意 EnqueueAssignment 对 Blocked/AlreadyActive 返回 (nil, reason, nil)
//     ——绝不解引用 nil task。
//   - writeDispatchSkipNotice：P1-6b，隐式指派链派单 skip（reason 非
//     queued）经 notifpgrepo.WriteNotice 落一条通知（先例：main.go autopilot
//     熔断 notifier），dedupe key 防重复轰炸。
//
// 挂法参照 workhttp/orchhttp：authGuardMw + RequireAuth + 工作区头
// （X-Workspace-Id + member check）。Go 1.22 mux 与 work 域
// /api/work-items/{workItemId} 模式共存（不同 method+path 精确匹配）。

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	"orkome/internal/contract/dispatchreason"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
)

// dispatchDeps 是 dispatch 端点的最小依赖面：入队服务 + 活跃任务查询
// （409 契约要带 active_task_id）+ 工作区成员校验。
type dispatchDeps struct {
	Enq  *orchapp.EnqueueService
	Repo orchapp.Repo

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)
}

// mountDispatch 把显式派单端点挂到 api mux（main.go 调用）。
func mountDispatch(mux *http.ServeMux, auth func(http.Handler) http.Handler, d dispatchDeps) {
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
	mux.Handle("POST /api/work-items/{workItemId}/dispatch", auth(webx.RequireAuth(wsGuard(handleDispatch(d)))))
}

func handleDispatch(d dispatchDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
			return
		}
		if in.AgentID == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "agent_id required",
				[]apierr.FieldError{{Field: "agent_id", Problem: "required"}})
			return
		}
		ws := webx.PrincipalFrom(r.Context()).WorkspaceID
		wi := r.PathValue("workItemId")
		actor := webx.PrincipalFrom(r.Context()).UserID

		// Blocked/AlreadyActive 是决策不是故障：(nil, reason, nil)。
		t, reason, err := d.Enq.EnqueueAssignment(r.Context(), ws, wi, in.AgentID, actor)
		if err != nil {
			webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
			return
		}
		if t == nil {
			switch reason {
			case dispatchreason.AlreadyActive:
				body := map[string]any{
					"error":  apierr.ActiveDuplicate,
					"reason": dispatchreason.AlreadyActive,
				}
				// 活跃任务由 GetActiveTask 查出（先例 handlers_taskview.go）；
				// 查不到（fence 命中但活跃面已流转的窄缝）不带该键。
				if active, found, qerr := d.Repo.GetActiveTask(r.Context(), ws, wi); qerr == nil && found {
					body["active_task_id"] = active.ID
				}
				webx.WriteJSON(w, http.StatusConflict, body)
			default:
				// blocked / model_not_configured 等：agent 侧配置问题，422。
				webx.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":       apierr.Validation,
					"reason_code": reason,
				})
			}
			return
		}
		webx.WriteJSON(w, http.StatusCreated, map[string]any{
			"task_id": t.ID,
			"result":  dispatchreason.Queued,
		})
	}
}

// writeDispatchSkipNotice P1-6b：隐式指派链的派单 skip 落通知（收件人 =
// 触发指派的 actor）。kind 固定 "dispatch_skipped"，dedupe key 按
// 工作区:工单:agent 维度防重复轰炸（ON CONFLICT DO NOTHING）；reason 非
// skip（含空串/queued）不落。
func writeDispatchSkipNotice(ctx context.Context, repo *notifpgrepo.Repo, ws, wi, agentID, reason, actorUserID string) {
	if repo == nil || actorUserID == "" || reason == "" || reason == dispatchreason.Queued {
		return
	}
	err := repo.WriteNotice(ctx, ws, actorUserID, "dispatch_skipped",
		"agent dispatch skipped: "+reason,
		"assignment dispatch skipped ("+reason+"); open the work item and dispatch manually once ready.",
		"dispatch_skip:"+ws+":"+wi+":"+agentID)
	if err != nil {
		// 通知失败不阻断事件链（同 autopilot 熔断 notifier 的容错口径）。
		slog.Warn("dispatch skip notice failed", "err", err)
	}
}
