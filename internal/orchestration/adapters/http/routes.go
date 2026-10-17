package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

type Deps struct {
	Settle *orchapp.SettlementService
	Agents *orchapp.AgentService
	Repo   orchapp.Repo
	MCP    *orchapp.MCPServerService

	Trigger *orchapp.TriggerService

	Builder *orchapp.AgentBuilderService

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)

	TaskMessages func(ctx context.Context, workspaceID, taskID string, q orchapp.TaskMessagesQuery) ([]orchapp.TaskMessage, error)

	RetrySourceContext func(ctx context.Context, workspaceID, taskID, userID string) (any, error)

	Batches *orchapp.BatchService
	// BatchSourceMater 通道 B 物化入口（cmd 侧注入：MaterializeFromSource
	// + ToolExec 组合）。nil = 通道 B 不可用（报 validation）。
	BatchSourceMater func(ctx context.Context, b *orchapp.Batch) (*orchapp.MaterializedSource, error)

	Squads *orchapp.SquadService

	AgentLabels *AgentLabelEndpoints

	Credentials *orchapp.CredentialService

	External *orchapp.ExternalTaskService

	PackUIEntries *orchapp.PackUIEntryService

	// PackToolSchemas 是 pack 工具输出 schema 存档（迁移 1035，B1）的服务面：
	// provision 写、C1/前端读。接口冻结见 docs/interfaces-job-handle-2026-10-11.md。
	PackToolSchemas *orchapp.PackToolSchemaService

	MintRuntimeCredential func(ctx context.Context, workspaceID, agentID, name string) (string, error)

	RoleOf func(ctx context.Context, workspaceID, userID string) (string, error)
}

// writeErr 兼容旧签名（无请求上下文）；新调用方请用 writeErrReq 以便日志带路径。
func writeErr(w http.ResponseWriter, err error) {
	writeErrReq(nil, w, err)
}

func writeErrReq(r *http.Request, w http.ResponseWriter, err error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		// 非 apierr 一律压成 500 且响应无细节，根因必须留服务端日志（否则 500 零痕）。
		slog.Error("orchestration http: internal error", "path", reqPath(r), "err", err.Error())
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
		return
	}
	status := map[string]int{
		apierr.Unauthenticated: 401, apierr.Forbidden: 403, apierr.NotFound: 404,
		apierr.Validation: 400, apierr.Conflict: 409, apierr.RateLimited: 429,
	}[ae.Code]
	if status == 0 {
		status = 500
	}
	webx.WriteError(w, status, ae.Code, ae.Message, ae.Details)
}

func reqPath(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Method + " " + r.URL.Path
}

func decode[T any](w http.ResponseWriter, r *http.Request) (*T, bool) {
	var in T
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return nil, false
	}
	return &in, true
}

func Mount(mux *http.ServeMux, d Deps, auth func(http.Handler) http.Handler) {
	guard := func(h http.HandlerFunc) http.Handler {
		return auth(webx.RequireAuth(d.memberMW(h)))
	}
	reg := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, guard(h)) }

	reg("POST /api/tasks/{taskId}/approve", webx.RequireHuman(d.handleApprove(true)))
	reg("POST /api/tasks/{taskId}/deny", webx.RequireHuman(d.handleApprove(false)))
	reg("GET /api/tasks/{taskId}/interrupt", d.handleInterrupt)
	if d.TaskMessages != nil {
		reg("GET /api/tasks/{taskId}/messages", d.handleTaskMessages)
	}

	if d.External != nil {
		reg("POST /api/tasks/{taskId}/claim", d.handleExternalClaim)
		reg("POST /api/tasks/{taskId}/messages", d.handleExternalMessage)
		reg("POST /api/tasks/{taskId}/complete", d.handleExternalComplete)
		reg("POST /api/tasks/{taskId}/fail", d.handleExternalFail)
	}
	if d.RetrySourceContext != nil {
		reg("POST /api/tasks/{taskId}/retry-source-context", webx.RequireHuman(d.handleRetrySourceContext))
	}

	if d.Batches != nil {
		reg("POST /api/work-items/{workItemId}/batches", d.handleCreateBatch)
		reg("GET /api/work-items/{workItemId}/batches", d.handleListBatches)
		reg("GET /api/batches/{batchId}", d.handleGetBatch)
		reg("GET /api/batches/{batchId}/items", d.handleBatchItems)
		reg("GET /api/batches/{batchId}/items/page", d.handleBatchItemsPage)
		reg("POST /api/batches/{batchId}/start", d.handleBatchStart)
		reg("POST /api/batches/{batchId}/pause", d.handleBatchPause)
		reg("POST /api/batches/{batchId}/resume", d.handleBatchResume)
		reg("POST /api/batches/{batchId}/cancel", d.handleBatchCancel)
		reg("POST /api/batches/{batchId}/retry-failed", d.handleBatchRetryFailed)
	}

	MountTaskViews(mux, d, auth)

	if d.Credentials != nil {
		reg("GET /api/credentials", d.handleCredentialList)
		reg("POST /api/credentials", d.handleCredentialCreate)
		reg("DELETE /api/credentials/{credentialId}", d.handleCredentialDelete)
	}

	if d.PackUIEntries != nil {
		reg("GET /api/ui-entries", d.handleUIEntryList)
		reg("PUT /api/ui-entries/{pack}", d.handleUIEntryUpsert)
		reg("DELETE /api/ui-entries/{pack}", d.handleUIEntryDelete)
	}

	if d.PackToolSchemas != nil {
		reg("GET /api/pack-tool-schemas", d.handlePackToolSchemaList)
		reg("PUT /api/pack-tool-schemas/{pack}", d.handlePackToolSchemaReplace)
		reg("DELETE /api/pack-tool-schemas/{pack}", d.handlePackToolSchemaDelete)
	}

	if d.Agents == nil {
		return
	}

	if d.MemberCheck != nil && d.Agents.MemberCheck == nil {
		d.Agents.MemberCheck = d.MemberCheck
	}

	reg("GET /api/agents", d.handleAgentList)
	reg("POST /api/agents", d.handleAgentCreate)
	reg("GET /api/agents/{agentId}", d.handleAgentGet)
	reg("PUT /api/agents/{agentId}", d.handleAgentUpdate)
	reg("POST /api/agents/{agentId}/archive", d.handleAgentArchive(true))
	reg("POST /api/agents/{agentId}/restore", d.handleAgentArchive(false))
	reg("POST /api/agents/{agentId}/cancel-tasks", d.handleAgentCancelTasks)
	if d.MCP != nil {
		reg("GET /api/agents/{agentId}/mcp-servers", d.handleAgentMCP)
		reg("POST /api/agents/{agentId}/mcp-servers", d.handleAgentMCPCreate)
		reg("PUT /api/agents/{agentId}/mcp-servers/{serverId}", d.handleAgentMCPUpdate)
		reg("DELETE /api/agents/{agentId}/mcp-servers/{serverId}", d.handleAgentMCPDelete)
		// P1-3：agent 级条目的实时工具探针——挂法随 agent 级 mcp-servers
		// 家族（reg = 成员闸 + X-Workspace-Id 头），引擎链路与 ws 级 probe 同参。
		reg("POST /api/agents/{agentId}/mcp-servers/{serverId}/probe-tools", d.handleAgentMCPProbeTools)
	}
	reg("GET /api/agents/{agentId}/env", d.handleAgentEnvGet)
	reg("PUT /api/agents/{agentId}/env", d.handleAgentEnvPut)

	if d.MintRuntimeCredential != nil {
		reg("POST /api/agents/{agentId}/runtime-credentials", d.handleMintRuntimeCredential)
	}

	d.mountAgentLabels(reg)
	if d.Builder != nil {
		reg("GET /api/agent-builder/sessions", d.handleBuilderList)
		reg("POST /api/agent-builder/sessions", d.handleBuilderCreate)
		reg("GET /api/agent-builder/sessions/{sessionId}/draft", d.handleBuilderDraft)
		reg("PUT /api/agent-builder/sessions/{sessionId}/draft", d.handleBuilderDraft)
	}

	if d.MCP != nil {
		// P2-D7：ws 级 mcp-servers 写面（POST/PUT/DELETE）自成员闸升 admin 闸
		// ——闸位在 handler 首行 requireWorkspaceAdmin（照抄同文件 credentials/
		// ui-entries 先例）；GET 与 probe 保持成员闸。Role 由 memberPathMW
		// 经 RoleOf 填充（见其注释）。
		authReg := func(pattern string, h http.HandlerFunc) {
			mux.Handle(pattern, auth(webx.RequireAuth(h)))
		}
		authReg("GET /api/workspaces/{workspaceId}/mcp-servers", d.memberPathMW(d.handleMCPList))
		authReg("POST /api/workspaces/{workspaceId}/mcp-servers", d.memberPathMW(d.handleMCPCreate))
		authReg("PUT /api/workspaces/{workspaceId}/mcp-servers/{serverId}", d.memberPathMW(d.handleMCPUpdate))
		authReg("DELETE /api/workspaces/{workspaceId}/mcp-servers/{serverId}", d.memberPathMW(d.handleMCPDelete))
		authReg("POST /api/workspaces/{workspaceId}/mcp-servers/{serverId}/probe-tools", d.memberPathMW(d.handleMCPProbeTools))
	}
}

// memberPathMW 路径含 workspaceId 的端点的成员闸。Role 填充与 memberMW
// 同款（P2-D7）：ws 级 mcp-servers 写面升 admin 闸后，requireWorkspaceAdmin
// 读 principal.Role——本闸不填 Role 则写面恒 403（连 owner 也进不去）。
func (d Deps) memberPathMW(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		ws := r.PathValue("workspaceId")
		if d.MemberCheck == nil {
			webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "member check not wired", nil)
			return
		}
		ok, err := d.MemberCheck(r.Context(), ws, p.UserID)
		if err != nil || !ok {
			webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "not a member of this workspace", nil)
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
		next(w, r)
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

		if d.MemberCheck == nil {
			webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "member check not wired", nil)
			return
		}
		ok, err := d.MemberCheck(r.Context(), ws, p.UserID)
		if err != nil || !ok {
			webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "not a member of this workspace", nil)
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

func (d Deps) ws(r *http.Request) string { return webx.PrincipalFrom(r.Context()).WorkspaceID }

func (d Deps) handleAgentList(w http.ResponseWriter, r *http.Request) {
	list, err := d.Agents.List(r.Context(), d.ws(r))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if list == nil {
		list = []orchapp.Agent{}
	}
	for i := range list {
		list[i] = list[i].Sanitized()
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"agents": list})
}

func (d Deps) handleAgentCreate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[orchapp.Agent](w, r)
	if !ok {
		return
	}
	in.WorkspaceID = d.ws(r)
	in.OwnerUserID = ownerFallback(in.OwnerUserID, webx.PrincipalFrom(r.Context()).UserID)
	a, err := d.Agents.Create(r.Context(), *in)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, a.Sanitized())
}

func (d Deps) handleAgentGet(w http.ResponseWriter, r *http.Request) {
	a, err := d.Agents.Get(r.Context(), d.ws(r), r.PathValue("agentId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if d.Repo == nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "repo not wired", nil)
		return
	}
	targets, err := d.Repo.GetInvocationTargets(r.Context(), d.ws(r), a.ID)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if targets == nil {
		targets = []orchapp.InvocationTarget{}
	}
	a.InvocationTargets = &targets
	webx.WriteJSON(w, http.StatusOK, a.Sanitized())
}

func (d Deps) handleAgentUpdate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[orchapp.Agent](w, r)
	if !ok {
		return
	}
	in.ID = r.PathValue("agentId")
	in.WorkspaceID = d.ws(r)
	a, err := d.Agents.Update(r.Context(), *in)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, a.Sanitized())
}

func (d Deps) handleAgentArchive(archived bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := d.Agents.Archive(r.Context(), d.ws(r), r.PathValue("agentId"), archived); err != nil {
			writeErrReq(r, w, err)
			return
		}
		webx.WriteJSON(w, http.StatusOK, map[string]string{"result": map[bool]string{true: "archived", false: "restored"}[archived]})
	}
}

func (d Deps) handleAgentCancelTasks(w http.ResponseWriter, r *http.Request) {
	n, err := d.Agents.CancelTasks(r.Context(), d.ws(r), r.PathValue("agentId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"cancelled": n})
}

func (d Deps) handleTaskMessages(w http.ResponseWriter, r *http.Request) {
	ws := r.Header.Get("X-Workspace-Id")
	if ws == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
		return
	}
	q := orchapp.TaskMessagesQuery{Limit: 200}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		q.Limit = n
	}
	if r.URL.Query().Get("order") == "desc" {
		q.Desc = true
	}
	if v := strings.TrimSpace(r.URL.Query().Get("before_seq")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "before_seq must be a positive integer", nil)
			return
		}
		q.BeforeSeq = n
	}
	if v := strings.TrimSpace(r.URL.Query().Get("kind")); v != "" {
		for _, k := range strings.Split(v, ",") {
			k = strings.TrimSpace(k)
			if !orchapp.TaskMessageKinds[k] {
				webx.WriteError(w, http.StatusBadRequest, apierr.Validation,
					"kind must be a comma-separated subset of text,tool_use,tool_result,error,approval", nil)
				return
			}
			q.Kinds = append(q.Kinds, k)
		}
	}
	msgs, err := d.TaskMessages(r.Context(), ws, r.PathValue("taskId"), q)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if msgs == nil {
		msgs = []orchapp.TaskMessage{}
	}
	resp := map[string]any{"messages": msgs}
	if q.Desc && len(msgs) == q.Limit {
		resp["next_before_seq"] = msgs[len(msgs)-1].Seq
	}
	webx.WriteJSON(w, http.StatusOK, resp)
}

func ownerFallback(owner, actor string) string {
	if owner != "" {
		return owner
	}
	return actor
}

func (d Deps) handleApprove(approved bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		var in struct {
			Note string `json:"note"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in)
		ws := r.Header.Get("X-Workspace-Id")
		if ws == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
			return
		}
		if err := d.Settle.Approve(r.Context(), ws, r.PathValue("taskId"), p.UserID, approved, in.Note); err != nil {
			writeErrReq(r, w, err)
			return
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{"result": map[bool]string{true: "approved", false: "denied"}[approved]})
	}
}

func (d Deps) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	ws := r.Header.Get("X-Workspace-Id")
	if ws == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
		return
	}
	if d.Repo == nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "repo not wired", nil)
		return
	}

	t, ok, err := d.Repo.GetTaskAwaiting(r.Context(), ws, r.PathValue("taskId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if !ok {

		if _, exists, gerr := d.Repo.GetTask(r.Context(), ws, r.PathValue("taskId")); gerr == nil && exists {
			writeErr(w, apierr.New(apierr.Conflict, "task not awaiting"))
			return
		}
		writeErr(w, apierr.New(apierr.NotFound, "task not found"))
		return
	}
	interruptMap, _ := t.Context["interrupt"].(map[string]any)
	if interruptMap == nil {
		writeErr(w, apierr.New(apierr.Conflict, "no interrupt payload"))
		return
	}
	targetID, _ := interruptMap["target_id"].(string)
	kind, _ := interruptMap["kind"].(string)
	payload, _ := interruptMap["payload"].(string)
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"task_id":   t.ID,
		"target_id": targetID,
		"kind":      kind,
		"payload":   payload,
	})
}

func (d Deps) handleRetrySourceContext(w http.ResponseWriter, r *http.Request) {
	ws := r.Header.Get("X-Workspace-Id")
	if ws == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
		return
	}
	out, err := d.RetrySourceContext(r.Context(), ws, r.PathValue("taskId"), webx.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, out)
}

func (d Deps) handleBuilderList(w http.ResponseWriter, r *http.Request) {
	list, err := d.Builder.List(r.Context(), d.ws(r), webx.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if list == nil {
		list = []orchapp.BuilderDraft{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": list})
}

func (d Deps) handleBuilderCreate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[orchapp.BuilderDraft](w, r)
	if !ok {
		return
	}
	in.WorkspaceID = d.ws(r)
	in.UserID = webx.PrincipalFrom(r.Context()).UserID
	sess, err := d.Builder.Create(r.Context(), *in)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, sess)
}

func (d Deps) handleBuilderDraft(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("sessionId")
	switch r.Method {
	case http.MethodGet:
		sess, err := d.Builder.Get(r.Context(), d.ws(r), id)
		if err != nil {
			writeErrReq(r, w, err)
			return
		}
		webx.WriteJSON(w, http.StatusOK, sess)
	case http.MethodPut:
		in, ok := decode[orchapp.BuilderDraft](w, r)
		if !ok {
			return
		}
		in.ID = id
		in.WorkspaceID = d.ws(r)
		in.UserID = webx.PrincipalFrom(r.Context()).UserID
		sess, err := d.Builder.UpsertDraft(r.Context(), *in)
		if err != nil {
			writeErrReq(r, w, err)
			return
		}
		webx.WriteJSON(w, http.StatusOK, sess)
	}
}
