package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	"orkome/internal/conversation/app"
)

type Deps struct {
	// Chat 已退役：nil = /api/chat/* 不挂载（见 Mount 注释）。
	Chat *app.ChatService

	QuickActions *app.QuickActionService

	Pins *app.PinService

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)

	Canceller app.TaskCanceller

	TaskStatus func(ctx context.Context, workspaceID, sessionID string) (status, taskID string, ok bool, err error)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apierr.Error
	if errors.As(err, &ae) {
		status := http.StatusInternalServerError
		switch ae.Code {
		case apierr.Unauthenticated:
			status = http.StatusUnauthorized
		case apierr.Forbidden:
			status = http.StatusForbidden
		case apierr.NotFound:
			status = http.StatusNotFound
		case apierr.Validation:
			status = http.StatusBadRequest
		case apierr.Conflict, apierr.DuplicatePendingTask, apierr.ActiveDuplicate, apierr.AwaitingState:
			status = http.StatusConflict
		case apierr.RateLimited:
			status = http.StatusTooManyRequests
		case apierr.SearchTimeout:
			status = http.StatusServiceUnavailable
		}
		webx.WriteError(w, status, ae.Code, ae.Message, ae.Details)
		return
	}
	var coder interface{ Code() string }
	if errors.As(err, &coder) {
		status := map[string]int{
			apierr.NotFound: 404, apierr.Validation: 400,
			app.CodeSessionInUse: 409, app.CodeQuickActionUnavailable: http.StatusServiceUnavailable,
		}[coder.Code()]
		if status == 0 {
			status = 500
		}
		webx.WriteError(w, status, coder.Code(), err.Error(), nil)
		return
	}
	webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
}

func decode[T any](w http.ResponseWriter, r *http.Request) (*T, bool) {
	var in T
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return nil, false
	}
	return &in, true
}

// Mount 挂载 conversation 域的 HTTP 面。
//
// chat（直聊会话）已退役（2026-10-15 裁决：对话长在工单上——评论 @agent
// 派活、supplement 纠正、汇报 tab 看过程，不养独立聊天室）：Chat 为 nil 时
// /api/chat/* 整块不挂，代码与表结构留仓沉睡（同工人模型先例；e2e harness
// 仍自行传 Chat 以保引擎覆盖）。quick-actions 属同域独立服务——后端已就绪、
// Web 产品面待消费——不受 Chat nil 影响。
func Mount(mux *http.ServeMux, d Deps, auth func(http.Handler) http.Handler) {
	reg := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(webx.RequireAuth(d.memberMW(h))))
	}
	if d.Chat != nil {
		reg("POST /api/chat/sessions", d.handleCreateSession)
		reg("GET /api/chat/sessions", d.handleListSessions)

		reg("GET /api/chat/history", d.handleChatHistory)
		reg("GET /api/chat/thread", d.handleChatThread)

		reg("GET /api/chat/pending-tasks", d.handlePendingTasks)
		reg("GET /api/chat/pending-tasks/has-any", d.handlePendingHasAny)
		if d.Pins != nil {
			reg("GET /api/chat/pinned-agents", d.handlePinList)
			reg("POST /api/chat/pinned-agents", d.handlePinAdd)
			reg("DELETE /api/chat/pinned-agents", d.handlePinDelete)
		}
		reg("GET /api/chat/sessions/{sessionId}", d.handleGetSession)
		reg("PATCH /api/chat/sessions/{sessionId}", d.handleUpdateSession)
		reg("DELETE /api/chat/sessions/{sessionId}", d.handleDeleteSession)
		reg("POST /api/chat/sessions/{sessionId}/pin", d.handlePin(true))
		reg("POST /api/chat/sessions/{sessionId}/unpin", d.handlePin(false))
		reg("POST /api/chat/sessions/{sessionId}/archive", d.handleArchive(true))
		reg("POST /api/chat/sessions/{sessionId}/unarchive", d.handleArchive(false))
		reg("POST /api/chat/sessions/{sessionId}/messages", d.handleSendMessage)
		reg("GET /api/chat/sessions/{sessionId}/messages", d.handleListMessages)
		if d.TaskStatus != nil {
			reg("GET /api/chat/sessions/{sessionId}/pending-task", d.handlePendingTask)
		}
		if d.Canceller != nil {
			reg("DELETE /api/chat/sessions/{sessionId}/pending-task", d.handleCancel)
			reg("DELETE /api/chat/sessions/{sessionId}/queued-tasks", d.handleCancel)
		}

		reg("POST /api/chat/sessions/{sessionId}/queued-tasks/{taskId}/prioritize", d.handlePrioritize)
		reg("POST /api/chat/sessions/{sessionId}/read", d.handleRead)
		reg("GET /api/chat/sessions/{sessionId}/draft-restores", d.handleGetDraft)
		reg("DELETE /api/chat/sessions/{sessionId}/draft-restores", d.handleDiscardDraft)
	}
	if d.QuickActions != nil {
		reg("GET /api/quick-actions", d.handleQAList)
		reg("POST /api/quick-actions", d.handleQACreate)
		reg("PATCH /api/quick-actions/{quickActionId}", d.handleQAUpdate)
		reg("DELETE /api/quick-actions/{quickActionId}", d.handleQADelete)

		reg("POST /api/work-items/{workItemId}/quick-actions/{quickActionId}/run", d.handleQARun)
		reg("POST /api/work-items/{workItemId}/quick-actions/{quickActionId}/render", d.handleQARender)
	}
}

func (d Deps) memberMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		ws := r.Header.Get("X-Workspace-Id")
		if ws == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
			return
		}
		if d.MemberCheck != nil {
			ok, err := d.MemberCheck(r.Context(), ws, p.UserID)
			if err != nil || !ok {
				webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "not a member of this workspace", nil)
				return
			}
		}
		p.WorkspaceID = ws
		next.ServeHTTP(w, r)
	})
}

func (d Deps) ws(r *http.Request) string { return webx.PrincipalFrom(r.Context()).WorkspaceID }

func (d Deps) sid(r *http.Request) string { return r.PathValue("sessionId") }

func (d Deps) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		AgentID string `json:"agent_id"`
		Title   string `json:"title"`
	}](w, r)
	if !ok {
		return
	}
	if in.AgentID == "" {
		webx.WriteError(w, http.StatusBadRequest, "E_VALIDATION", "agent_id required", nil)
		return
	}
	p := webx.PrincipalFrom(r.Context())
	sess, err := d.Chat.CreateSession(r.Context(), d.ws(r), in.AgentID, p.UserID, in.Title)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, sess)
}

func (d Deps) handleListSessions(w http.ResponseWriter, r *http.Request) {
	list, err := d.Chat.ListSessions(r.Context(), d.ws(r), webx.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []app.Session{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": list})
}

func (d Deps) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, err := d.Chat.GetSession(r.Context(), d.ws(r), d.sid(r), webx.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, sess)
}

func (d Deps) handleArchive(archived bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		// W2-1 creator 私有：归属校验在 Archive 内（mustOwnSession），此处
		// 不再前置 GetSession 双查。
		if err := d.Chat.Archive(r.Context(), d.ws(r), d.sid(r), p.UserID, archived); err != nil {
			writeErr(w, err)
			return
		}
		webx.WriteJSON(w, http.StatusOK, map[string]string{"result": map[bool]string{true: "archived", false: "active"}[archived]})
	}
}

func (d Deps) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Title *string `json:"title"`
	}](w, r)
	if !ok {
		return
	}
	sess, err := d.Chat.UpdateSession(r.Context(), d.ws(r), d.sid(r), webx.PrincipalFrom(r.Context()).UserID, in.Title)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, sess)
}

func (d Deps) handlePin(pinned bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.Chat.SetPinned(r.Context(), d.ws(r), d.sid(r), webx.PrincipalFrom(r.Context()).UserID, pinned); err != nil {
			writeErr(w, err)
			return
		}
		webx.WriteJSON(w, http.StatusOK, map[string]string{"result": map[bool]string{true: "pinned", false: "unpinned"}[pinned]})
	}
}

func (d Deps) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	ws, sid := d.ws(r), d.sid(r)
	p := webx.PrincipalFrom(r.Context())
	if _, err := d.Chat.GetSession(r.Context(), ws, sid, p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	if d.TaskStatus != nil {
		status, _, ok, err := d.TaskStatus(r.Context(), ws, sid)
		if err != nil {
			writeErr(w, err)
			return
		}
		if ok && (status == "queued" || status == "dispatched" || status == "running") {
			webx.WriteError(w, http.StatusConflict, app.CodeSessionInUse,
				"session has a queued/dispatched/running task; cancel or wait for it first", nil)
			return
		}
	}
	if err := d.Chat.DeleteSession(r.Context(), ws, sid, p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}

func (d Deps) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Content string `json:"content"`
	}](w, r)
	if !ok {
		return
	}
	p := webx.PrincipalFrom(r.Context())
	msg, taskID, err := d.Chat.SendMessage(r.Context(), d.ws(r), d.sid(r), p.UserID, in.Content)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{"message": msg, "task_id": taskID})
}

func (d Deps) handleListMessages(w http.ResponseWriter, r *http.Request) {
	sid := d.sid(r)
	// W2-1 creator 私有：消息流只对 creator 可读（此前任何同区成员可读）。
	if _, err := d.Chat.GetSession(r.Context(), d.ws(r), sid, webx.PrincipalFrom(r.Context()).UserID); err != nil {
		writeErr(w, err)
		return
	}
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	before := int64(1 << 62)
	if s := r.URL.Query().Get("before_seq"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			before = n
		}
	}
	msgs, err := d.Chat.ListMessages(r.Context(), sid, limit, before)
	if err != nil {
		writeErr(w, err)
		return
	}
	if msgs == nil {
		msgs = []app.Message{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

func (d Deps) handlePendingTask(w http.ResponseWriter, r *http.Request) {
	// W2-1 creator 私有：pending-task 读同样不泄露他人会话存在性。
	if _, err := d.Chat.GetSession(r.Context(), d.ws(r), d.sid(r), webx.PrincipalFrom(r.Context()).UserID); err != nil {
		writeErr(w, err)
		return
	}
	status, taskID, ok, err := d.TaskStatus(r.Context(), d.ws(r), d.sid(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		webx.WriteJSON(w, http.StatusOK, map[string]any{"task": nil})
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"task": map[string]any{"id": taskID, "status": status}})
}

func (d Deps) handleCancel(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	outcome, err := d.Chat.Cancel(r.Context(), d.ws(r), d.sid(r), p.UserID, d.Canceller)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"outcome": outcome})
}

func (d Deps) handleRead(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	if err := d.Chat.MarkRead(r.Context(), d.ws(r), d.sid(r), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "read"})
}

func (d Deps) handleGetDraft(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	content, ok, err := d.Chat.RestoreDraft(r.Context(), d.ws(r), d.sid(r), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		webx.WriteJSON(w, http.StatusOK, map[string]any{"draft": nil})
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"draft": map[string]string{"content": content}})
}

func (d Deps) handleDiscardDraft(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	if err := d.Chat.DiscardDraft(r.Context(), d.ws(r), d.sid(r), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "discarded"})
}

func (d Deps) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "session_id required", nil)
		return
	}
	page, err := d.Chat.History(r.Context(), d.ws(r), sid, p.UserID,
		parseHistoryLimit(r), r.URL.Query().Get("before"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, page)
}

func (d Deps) handleChatThread(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "session_id required", nil)
		return
	}
	page, err := d.Chat.Thread(r.Context(), d.ws(r), sid, p.UserID,
		r.URL.Query().Get("id"), parseHistoryLimit(r), r.URL.Query().Get("before"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, page)
}

func parseHistoryLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return 50
	}
	if n > 200 {
		return 200
	}
	return n
}

func (d Deps) handlePendingTasks(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	list, err := d.Chat.ListPendingTasks(r.Context(), d.ws(r), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"tasks": list})
}

func (d Deps) handlePendingHasAny(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	ok, err := d.Chat.HasPendingTask(r.Context(), d.ws(r), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"has_any": ok})
}

func (d Deps) handlePrioritize(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	priority, err := d.Chat.Prioritize(r.Context(), d.ws(r), d.sid(r), r.PathValue("taskId"), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"priority": priority})
}

func (d Deps) handlePinList(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	list, err := d.Pins.List(r.Context(), d.ws(r), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"agents": list})
}

func (d Deps) handlePinAdd(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		AgentID string `json:"agent_id"`
	}](w, r)
	if !ok {
		return
	}
	p := webx.PrincipalFrom(r.Context())
	if err := d.Pins.Add(r.Context(), d.ws(r), p.UserID, in.AgentID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]string{"result": "pinned"})
}

func (d Deps) handlePinDelete(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		if in, ok := decode[struct {
			AgentID string `json:"agent_id"`
		}](w, r); ok {
			agentID = in.AgentID
		}
	}
	if agentID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "agent_id required", nil)
		return
	}
	p := webx.PrincipalFrom(r.Context())
	if err := d.Pins.Remove(r.Context(), d.ws(r), p.UserID, agentID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "unpinned"})
}

func (d Deps) handleQAList(w http.ResponseWriter, r *http.Request) {
	list, err := d.QuickActions.List(r.Context(), d.ws(r), webx.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []app.QuickAction{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"quick_actions": list})
}

func (d Deps) handleQACreate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[app.QuickAction](w, r)
	if !ok {
		return
	}
	in.WorkspaceID = d.ws(r)
	in.UserID = webx.PrincipalFrom(r.Context()).UserID
	q, err := d.QuickActions.Create(r.Context(), *in)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, q)
}

func (d Deps) handleQAUpdate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Label  string `json:"label"`
		Prompt string `json:"prompt"`
	}](w, r)
	if !ok {
		return
	}
	p := webx.PrincipalFrom(r.Context())
	q, err := d.QuickActions.Update(r.Context(), d.ws(r), r.PathValue("quickActionId"), p.UserID, in.Label, in.Prompt)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, q)
}

func (d Deps) handleQADelete(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	if err := d.QuickActions.Delete(r.Context(), d.ws(r), r.PathValue("quickActionId"), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}

func (d Deps) handleQARender(w http.ResponseWriter, r *http.Request) {
	q, err := d.QuickActions.Render(r.Context(), d.ws(r), r.PathValue("quickActionId"), webx.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"prompt": q.Prompt})
}

func (d Deps) handleQARun(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	taskID, err := d.QuickActions.Run(r.Context(), d.ws(r),
		r.PathValue("workItemId"), r.PathValue("quickActionId"), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]string{"task_id": taskID})
}
