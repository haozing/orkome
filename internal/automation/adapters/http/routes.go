package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/automation/app"
	"orkome/internal/automation/domain"
	"orkome/internal/contract/apierr"
)

type Deps struct {
	Wakeups    *app.WakeupService
	Autopilots *app.AutopilotService
	Webhooks   *app.WebhookService

	PublicRL *WebhookRateLimiter

	WakeupTasks func(ctx context.Context, workspaceID, wakeupID string) ([]app.WakeupTaskRef, error)

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)
}

func MountPublic(mux *http.ServeMux, d Deps) {
	rl := d.PublicRL
	if rl == nil {
		rl = NewWebhookRateLimiter(nil, DefaultWebhookRateConfig(), nil)
	}
	mux.HandleFunc("POST /webhooks/autopilots/{token}", func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := rl.Allow(r.Context(), r); !ok {
			WriteRateLimited(w, retry)
			return
		}
		body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		code, err := d.Webhooks.Ingest(r.Context(), r.PathValue("token"),
			r.Header.Get("X-Signature"), r.Header.Get("X-Timestamp"), body)
		if err != nil {
			http.Error(w, err.Error(), map[int]int{403: 403, 404: 404, 500: 500}[code])
			return
		}
		w.WriteHeader(code)
	})
}

func Mount(mux *http.ServeMux, d Deps, auth func(http.Handler) http.Handler) {
	reg := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(webx.RequireAuth(d.memberMW(h))))
	}

	reg("POST /api/work-items/{workItemId}/wakeups", d.createWakeup)
	reg("GET /api/work-items/{workItemId}/wakeups", d.listWakeups)
	reg("GET /api/work-items/{workItemId}/wakeups/{wakeupId}", d.getWakeup)
	reg("PUT /api/work-items/{workItemId}/wakeups/{wakeupId}", d.putWakeup)
	reg("DELETE /api/work-items/{workItemId}/wakeups/{wakeupId}", d.deleteWakeup)
	reg("POST /api/work-items/{workItemId}/wakeups/{wakeupId}/enable", d.enableWakeup)
	reg("POST /api/work-items/{workItemId}/wakeups/{wakeupId}/disable", d.disableWakeup)
	reg("PUT /api/work-items/{workItemId}/wakeups/{wakeupId}/instruction", d.putWakeupInstruction)
	reg("POST /api/work-items/{workItemId}/wakeups/{wakeupId}/checkin", d.checkinWakeup)
	reg("POST /api/work-items/{workItemId}/wakeups/{wakeupId}/trigger", d.triggerWakeup)
	reg("GET /api/work-items/{workItemId}/wakeups/{wakeupId}/tasks", d.wakeupTasks)

	reg("GET /api/work-item-wakeups", d.listWorkspaceWakeups)
	reg("GET /api/work-item-wakeup-paused", d.listPausedWakeups)
	reg("GET /api/work-item-wakeup-summaries", d.listWakeupSummaries)

	reg("GET /api/system-wakeups", d.getSystemWakeups)
	reg("PUT /api/system-wakeups", d.putSystemDefault)
	reg("GET /api/system-wakeups/{wakeupId}", d.getSystemWakeup)
	reg("PUT /api/system-wakeups/{wakeupId}", d.putSystemWakeup)
	reg("GET /api/work-items/{workItemId}/system-wakeups", d.listSystemByWorkItem)
	reg("POST /api/work-items/{workItemId}/system-wakeups", d.ensureSystemWakeup)

	reg("GET /api/autopilots", d.listAutopilots)
	reg("POST /api/autopilots", d.createAutopilot)
	reg("GET /api/autopilots/{autopilotId}", d.getAutopilot)
	reg("PATCH /api/autopilots/{autopilotId}", d.patchAutopilot)
	reg("DELETE /api/autopilots/{autopilotId}", d.deleteAutopilot)
	reg("POST /api/autopilots/cron-preview", d.cronPreview)
	reg("POST /api/autopilots/{autopilotId}/trigger", d.triggerAutopilot)
	reg("POST /api/autopilots/{autopilotId}/triggers", d.createTrigger)
	reg("PATCH /api/autopilots/{autopilotId}/triggers/{triggerId}", d.patchTrigger)
	reg("DELETE /api/autopilots/{autopilotId}/triggers/{triggerId}", d.deleteTrigger)

	reg("PUT /api/autopilots/{autopilotId}/triggers/{triggerId}/signing-secret", d.putSigningSecret)
	reg("POST /api/autopilots/{autopilotId}/triggers/{triggerId}/rotate-webhook-token", d.rotateWebhookToken)

	reg("GET /api/autopilots/{autopilotId}/runs", d.listRuns)
	reg("GET /api/autopilots/{autopilotId}/runs/{runId}", d.getRun)
	reg("GET /api/deliveries", d.listDeliveries)
	reg("GET /api/deliveries/{deliveryId}", d.getDelivery)
	reg("POST /api/deliveries/{deliveryId}/replay", d.replayDelivery)

	reg("GET /api/autopilots/{autopilotId}/collaborators", d.listCollaborators)
	reg("POST /api/autopilots/{autopilotId}/collaborators", d.addCollaborator)
	reg("DELETE /api/autopilots/{autopilotId}/collaborators", d.removeCollaborator)
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
		p.WorkspaceID = ws
		next.ServeHTTP(w, r)
	})
}

func wsOf(r *http.Request) string { return r.Header.Get("X-Workspace-Id") }

func writeErr(w http.ResponseWriter, err error) {
	writeErrReq(nil, w, err)
}

func writeErrReq(r *http.Request, w http.ResponseWriter, err error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		// 非 apierr 一律压成 500，根因必须留在服务端日志（否则 500 零痕）。
		slog.Error("automation http: internal error", "path", reqPath(r), "err", err.Error())
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
		return
	}
	status := http.StatusInternalServerError
	switch ae.Code {
	case apierr.Validation:
		status = http.StatusBadRequest
	case apierr.NotFound:
		status = http.StatusNotFound
	case apierr.RateLimited:
		status = http.StatusTooManyRequests
	case apierr.Conflict:
		status = http.StatusConflict
	}
	webx.WriteError(w, status, ae.Code, ae.Message, ae.Details)
}

func reqPath(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Method + " " + r.URL.Path
}

func (d Deps) createWakeup(w http.ResponseWriter, r *http.Request) {
	var in app.Wakeup
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in)
	in.WorkspaceID = wsOf(r)
	in.WorkItemID = r.PathValue("workItemId")
	if p := webx.PrincipalFrom(r.Context()); p != nil {
		in.CreatedBy = p.UserID
	}
	out, err := d.Wakeups.Create(r.Context(), in)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, out)
}

func (d Deps) listWakeups(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.List(r.Context(), r.PathValue("workItemId"))
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) getWakeup(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.Get(r.Context(), wsOf(r), r.PathValue("workItemId"), r.PathValue("wakeupId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

type wakeupUpdateIn struct {
	app.Wakeup
	ExpectedRevision int64 `json:"expected_revision"`
}

func (d Deps) putWakeup(w http.ResponseWriter, r *http.Request) {
	var in wakeupUpdateIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	out, err := d.Wakeups.Update(r.Context(), wsOf(r), r.PathValue("workItemId"),
		r.PathValue("wakeupId"), in.Wakeup, in.ExpectedRevision)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) deleteWakeup(w http.ResponseWriter, r *http.Request) {
	if err := d.Wakeups.Delete(r.Context(), wsOf(r), r.PathValue("workItemId"), r.PathValue("wakeupId")); err != nil {
		writeErrReq(r, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) enableWakeup(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<12))
	if err := d.Wakeups.SetEnabled(r.Context(), wsOf(r), r.PathValue("wakeupId"), true); err != nil {
		writeErrReq(r, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) disableWakeup(w http.ResponseWriter, r *http.Request) {
	if err := d.Wakeups.SetEnabled(r.Context(), wsOf(r), r.PathValue("wakeupId"), false); err != nil {
		writeErrReq(r, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) putWakeupInstruction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Instruction string `json:"instruction"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid instruction body", nil)
		return
	}
	if err := d.Wakeups.EditInstruction(r.Context(), wsOf(r), r.PathValue("wakeupId"), in.Instruction); err != nil {
		writeErrReq(r, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) checkinWakeup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TaskID string `json:"task_id"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid check-in body", nil)
		return
	}
	if err := d.Wakeups.CheckIn(r.Context(), wsOf(r), r.PathValue("workItemId"),
		r.PathValue("wakeupId"), in.TaskID, in.Note); err != nil {
		writeErrReq(r, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) triggerWakeup(w http.ResponseWriter, r *http.Request) {
	if err := d.Wakeups.Trigger(r.Context(), wsOf(r), r.PathValue("workItemId"), r.PathValue("wakeupId")); err != nil {
		writeErrReq(r, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) wakeupTasks(w http.ResponseWriter, r *http.Request) {
	wu, err := d.Wakeups.Get(r.Context(), wsOf(r), r.PathValue("workItemId"), r.PathValue("wakeupId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if d.WakeupTasks == nil {
		webx.WriteJSON(w, http.StatusOK, []app.WakeupTaskRef{})
		return
	}
	out, err := d.WakeupTasks(r.Context(), wsOf(r), wu.ID)
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	if out == nil {
		out = []app.WakeupTaskRef{}
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) listWorkspaceWakeups(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.ListWorkspace(r.Context(), wsOf(r))
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) listPausedWakeups(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.ListPaused(r.Context(), wsOf(r))
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) listWakeupSummaries(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.Summaries(r.Context(), wsOf(r))
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) getSystemWakeups(w http.ResponseWriter, r *http.Request) {
	def, err := d.Wakeups.GetSystemDefault(r.Context(), wsOf(r))
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	rules, err := d.Wakeups.ListSystem(r.Context(), wsOf(r))
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"default": def, "rules": rules})
}

func (d Deps) putSystemDefault(w http.ResponseWriter, r *http.Request) {
	var in app.SystemWakeupDefault
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid body", nil)
		return
	}
	if err := d.Wakeups.SetSystemDefault(r.Context(), wsOf(r), in); err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, in)
}

func (d Deps) getSystemWakeup(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.ListSystem(r.Context(), wsOf(r))
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	id := r.PathValue("wakeupId")
	for _, wu := range out {
		if wu.ID == id {
			webx.WriteJSON(w, http.StatusOK, wu)
			return
		}
	}
	webx.WriteError(w, http.StatusNotFound, apierr.NotFound, "system wakeup not found", nil)
}

func (d Deps) putSystemWakeup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Instruction *string `json:"instruction"`
		Enabled     *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid body", nil)
		return
	}
	out, err := d.Wakeups.UpdateSystem(r.Context(), wsOf(r), r.PathValue("wakeupId"), in.Instruction, in.Enabled)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) listSystemByWorkItem(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.ListSystemByWorkItem(r.Context(), wsOf(r), r.PathValue("workItemId"))
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) ensureSystemWakeup(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.EnsureChildDoneRule(r.Context(), wsOf(r), r.PathValue("workItemId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) createAutopilot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name           string         `json:"name"`
		ExecMode       string         `json:"exec_mode"`
		Template       map[string]any `json:"template"`
		TargetAgentID  string         `json:"target_agent_id"`
		CronExpression string         `json:"cron_expression"`
		Timezone       string         `json:"timezone"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in)
	p := webx.PrincipalFrom(r.Context())
	ap, _, err := d.Autopilots.Create(r.Context(), app.Autopilot{
		WorkspaceID: wsOf(r), Name: in.Name, ExecMode: in.ExecMode,
		Template: in.Template, TargetAgentID: in.TargetAgentID, CreatedBy: p.UserID,
	}, in.CronExpression, in.Timezone)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, ap)
}

func (d Deps) listAutopilots(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := d.Autopilots.List(r.Context(), wsOf(r), limit)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) getAutopilot(w http.ResponseWriter, r *http.Request) {
	out, err := d.Autopilots.Get(r.Context(), wsOf(r), r.PathValue("autopilotId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) patchAutopilot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          *string        `json:"name"`
		ExecMode      *string        `json:"exec_mode"`
		Template      map[string]any `json:"template"`
		TargetAgentID *string        `json:"target_agent_id"`
		Enabled       *bool          `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	out, err := d.Autopilots.Update(r.Context(), wsOf(r), r.PathValue("autopilotId"),
		in.Name, in.ExecMode, in.Template, in.TargetAgentID, in.Enabled)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) deleteAutopilot(w http.ResponseWriter, r *http.Request) {
	if err := d.Autopilots.Delete(r.Context(), wsOf(r), r.PathValue("autopilotId")); err != nil {
		writeErrReq(r, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) triggerAutopilot(w http.ResponseWriter, r *http.Request) {
	run, err := d.Autopilots.StartRun(r.Context(), r.PathValue("autopilotId"), "", "manual")
	if err != nil {
		if run != nil && errors.Is(err, domain.ErrQuotaExceeded) {

			webx.WriteJSON(w, http.StatusOK, run)
			return
		}
		// 400 "trigger failed" 会吞掉真实根因（PG 约束/调度错误），必须留痕。
		slog.Error("automation http: autopilot manual trigger failed", "path", reqPath(r), "err", err.Error())
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "trigger failed", nil)
		return
	}
	if run == nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "trigger failed", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, run)
}

func (d Deps) cronPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CronExpression string `json:"cron_expression"`
		Timezone       string `json:"timezone"`
		Count          int    `json:"count"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid body", nil)
		return
	}
	runs, err := d.Autopilots.CronPreview(r.Context(), in.CronExpression, in.Timezone, in.Count)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"next_runs": runs})
}

func (d Deps) createTrigger(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind           string `json:"kind"`
		CronExpression string `json:"cron_expression"`
		Timezone       string `json:"timezone"`
		Enabled        bool   `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid body", nil)
		return
	}
	t, err := d.Autopilots.CreateTrigger(r.Context(), wsOf(r), r.PathValue("autopilotId"),
		in.Kind, in.CronExpression, in.Timezone, in.Enabled)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	resp := map[string]any{"trigger": t}
	if in.Kind == domain.TriggerWebhook {
		resp["credential"] = app.TriggerCredential{TriggerID: t.ID,
			WebhookToken: t.WebhookToken, SecretFingerprint: domain.SecretFingerprint(t.SigningSecret),
			HasSigningSecret: true}
	}
	webx.WriteJSON(w, http.StatusCreated, resp)
}

func (d Deps) patchTrigger(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CronExpression *string `json:"cron_expression"`
		Timezone       *string `json:"timezone"`
		Enabled        *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid body", nil)
		return
	}
	out, err := d.Autopilots.UpdateTrigger(r.Context(), wsOf(r), r.PathValue("autopilotId"),
		r.PathValue("triggerId"), in.CronExpression, in.Timezone, in.Enabled)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	if err := d.Autopilots.DeleteTrigger(r.Context(), wsOf(r), r.PathValue("autopilotId"),
		r.PathValue("triggerId")); err != nil {
		writeErrReq(r, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) putSigningSecret(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	cred, err := d.Webhooks.SetSigningSecret(r.Context(), wsOf(r),
		r.PathValue("autopilotId"), r.PathValue("triggerId"), in.Secret)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, cred)
}

func (d Deps) rotateWebhookToken(w http.ResponseWriter, r *http.Request) {
	cred, err := d.Webhooks.RotateWebhookToken(r.Context(), wsOf(r),
		r.PathValue("autopilotId"), r.PathValue("triggerId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, cred)
}

func (d Deps) listRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := d.Autopilots.ListRuns(r.Context(), wsOf(r), r.PathValue("autopilotId"), limit)
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) getRun(w http.ResponseWriter, r *http.Request) {
	out, err := d.Autopilots.GetRun(r.Context(), wsOf(r), r.PathValue("runId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) listDeliveries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := d.Webhooks.ListDeliveries(r.Context(), wsOf(r), limit)
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) getDelivery(w http.ResponseWriter, r *http.Request) {
	out, err := d.Webhooks.GetDelivery(r.Context(), wsOf(r), r.PathValue("deliveryId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) replayDelivery(w http.ResponseWriter, r *http.Request) {
	out, err := d.Webhooks.ReplayDelivery(r.Context(), wsOf(r), r.PathValue("deliveryId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}
