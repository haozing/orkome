package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"orkome/internal/automation/app"
	"orkome/internal/automation/domain"
	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
)

type Deps struct {
	Wakeups    *app.WakeupService
	Autopilots *app.AutopilotService
	Webhooks   *app.WebhookService

	PublicRL *WebhookRateLimiter

	WakeupTasks func(ctx context.Context, workspaceID, wakeupID string) ([]app.WakeupTaskRef, error)

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)
}

func MountPublic(r chi.Router, d Deps) {
	rl := d.PublicRL
	if rl == nil {
		rl = NewWebhookRateLimiter(nil, DefaultWebhookRateConfig(), nil)
	}
	r.Post("/webhooks/autopilots/{token}", func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := rl.Allow(r.Context(), r); !ok {
			WriteRateLimited(w, retry)
			return
		}
		body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		code, err := d.Webhooks.Ingest(r.Context(), chi.URLParam(r, "token"),
			r.Header.Get("X-Signature"), r.Header.Get("X-Timestamp"), body)
		if err != nil {
			http.Error(w, err.Error(), map[int]int{403: 403, 404: 404, 500: 500}[code])
			return
		}
		w.WriteHeader(code)
	})
}

func Mount(r chi.Router, d Deps, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth, webx.RequireAuth)
		r.Use(d.memberMW)

		r.Post("/work-items/{workItemId}/wakeups", d.createWakeup)
		r.Get("/work-items/{workItemId}/wakeups", d.listWakeups)
		r.Get("/work-items/{workItemId}/wakeups/{wakeupId}", d.getWakeup)
		r.Put("/work-items/{workItemId}/wakeups/{wakeupId}", d.putWakeup)
		r.Delete("/work-items/{workItemId}/wakeups/{wakeupId}", d.deleteWakeup)
		r.Post("/work-items/{workItemId}/wakeups/{wakeupId}/enable", d.enableWakeup)
		r.Post("/work-items/{workItemId}/wakeups/{wakeupId}/disable", d.disableWakeup)
		r.Put("/work-items/{workItemId}/wakeups/{wakeupId}/instruction", d.putWakeupInstruction)
		r.Post("/work-items/{workItemId}/wakeups/{wakeupId}/checkin", d.checkinWakeup)
		r.Post("/work-items/{workItemId}/wakeups/{wakeupId}/trigger", d.triggerWakeup)
		r.Get("/work-items/{workItemId}/wakeups/{wakeupId}/tasks", d.wakeupTasks)

		r.Get("/work-item-wakeups", d.listWorkspaceWakeups)
		r.Get("/work-item-wakeup-paused", d.listPausedWakeups)
		r.Get("/work-item-wakeup-summaries", d.listWakeupSummaries)

		r.Get("/system-wakeups", d.getSystemWakeups)
		r.Put("/system-wakeups", d.putSystemDefault)
		r.Get("/system-wakeups/{wakeupId}", d.getSystemWakeup)
		r.Put("/system-wakeups/{wakeupId}", d.putSystemWakeup)
		r.Get("/work-items/{workItemId}/system-wakeups", d.listSystemByWorkItem)
		r.Post("/work-items/{workItemId}/system-wakeups", d.ensureSystemWakeup)

		r.Get("/autopilots", d.listAutopilots)
		r.Post("/autopilots", d.createAutopilot)
		r.Get("/autopilots/{autopilotId}", d.getAutopilot)
		r.Patch("/autopilots/{autopilotId}", d.patchAutopilot)
		r.Delete("/autopilots/{autopilotId}", d.deleteAutopilot)
		r.Post("/autopilots/cron-preview", d.cronPreview)
		r.Post("/autopilots/{autopilotId}/trigger", d.triggerAutopilot)
		r.Post("/autopilots/{autopilotId}/triggers", d.createTrigger)
		r.Patch("/autopilots/{autopilotId}/triggers/{triggerId}", d.patchTrigger)
		r.Delete("/autopilots/{autopilotId}/triggers/{triggerId}", d.deleteTrigger)

		r.Put("/autopilots/{autopilotId}/triggers/{triggerId}/signing-secret", d.putSigningSecret)
		r.Post("/autopilots/{autopilotId}/triggers/{triggerId}/rotate-webhook-token", d.rotateWebhookToken)

		r.Get("/autopilots/{autopilotId}/runs", d.listRuns)
		r.Get("/autopilots/{autopilotId}/runs/{runId}", d.getRun)
		r.Get("/deliveries", d.listDeliveries)
		r.Get("/deliveries/{deliveryId}", d.getDelivery)
		r.Post("/deliveries/{deliveryId}/replay", d.replayDelivery)

		r.Get("/autopilots/{autopilotId}/collaborators", d.listCollaborators)
		r.Post("/autopilots/{autopilotId}/collaborators", d.addCollaborator)
		r.Delete("/autopilots/{autopilotId}/collaborators", d.removeCollaborator)
	})
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
	var ae *apierr.Error
	if !errors.As(err, &ae) {
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

func (d Deps) createWakeup(w http.ResponseWriter, r *http.Request) {
	var in app.Wakeup
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in)
	in.WorkspaceID = wsOf(r)
	in.WorkItemID = chi.URLParam(r, "workItemId")
	out, err := d.Wakeups.Create(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, out)
}

func (d Deps) listWakeups(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.List(r.Context(), chi.URLParam(r, "workItemId"))
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) getWakeup(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.Get(r.Context(), wsOf(r), chi.URLParam(r, "workItemId"), chi.URLParam(r, "wakeupId"))
	if err != nil {
		writeErr(w, err)
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
	out, err := d.Wakeups.Update(r.Context(), wsOf(r), chi.URLParam(r, "workItemId"),
		chi.URLParam(r, "wakeupId"), in.Wakeup, in.ExpectedRevision)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) deleteWakeup(w http.ResponseWriter, r *http.Request) {
	if err := d.Wakeups.Delete(r.Context(), wsOf(r), chi.URLParam(r, "workItemId"), chi.URLParam(r, "wakeupId")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) enableWakeup(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<12))
	if err := d.Wakeups.SetEnabled(r.Context(), wsOf(r), chi.URLParam(r, "wakeupId"), true); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) disableWakeup(w http.ResponseWriter, r *http.Request) {
	if err := d.Wakeups.SetEnabled(r.Context(), wsOf(r), chi.URLParam(r, "wakeupId"), false); err != nil {
		writeErr(w, err)
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
	if err := d.Wakeups.EditInstruction(r.Context(), wsOf(r), chi.URLParam(r, "wakeupId"), in.Instruction); err != nil {
		writeErr(w, err)
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
	if err := d.Wakeups.CheckIn(r.Context(), wsOf(r), chi.URLParam(r, "workItemId"),
		chi.URLParam(r, "wakeupId"), in.TaskID, in.Note); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) triggerWakeup(w http.ResponseWriter, r *http.Request) {
	if err := d.Wakeups.Trigger(r.Context(), wsOf(r), chi.URLParam(r, "workItemId"), chi.URLParam(r, "wakeupId")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) wakeupTasks(w http.ResponseWriter, r *http.Request) {
	wu, err := d.Wakeups.Get(r.Context(), wsOf(r), chi.URLParam(r, "workItemId"), chi.URLParam(r, "wakeupId"))
	if err != nil {
		writeErr(w, err)
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
		writeErr(w, err)
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
	id := chi.URLParam(r, "wakeupId")
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
	out, err := d.Wakeups.UpdateSystem(r.Context(), wsOf(r), chi.URLParam(r, "wakeupId"), in.Instruction, in.Enabled)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) listSystemByWorkItem(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.ListSystemByWorkItem(r.Context(), wsOf(r), chi.URLParam(r, "workItemId"))
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) ensureSystemWakeup(w http.ResponseWriter, r *http.Request) {
	out, err := d.Wakeups.EnsureChildDoneRule(r.Context(), wsOf(r), chi.URLParam(r, "workItemId"))
	if err != nil {
		writeErr(w, err)
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
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, ap)
}

func (d Deps) listAutopilots(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := d.Autopilots.List(r.Context(), wsOf(r), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) getAutopilot(w http.ResponseWriter, r *http.Request) {
	out, err := d.Autopilots.Get(r.Context(), wsOf(r), chi.URLParam(r, "autopilotId"))
	if err != nil {
		writeErr(w, err)
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
	out, err := d.Autopilots.Update(r.Context(), wsOf(r), chi.URLParam(r, "autopilotId"),
		in.Name, in.ExecMode, in.Template, in.TargetAgentID, in.Enabled)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) deleteAutopilot(w http.ResponseWriter, r *http.Request) {
	if err := d.Autopilots.Delete(r.Context(), wsOf(r), chi.URLParam(r, "autopilotId")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) triggerAutopilot(w http.ResponseWriter, r *http.Request) {
	run, err := d.Autopilots.StartRun(r.Context(), chi.URLParam(r, "autopilotId"), "", "manual")
	if err != nil {
		if run != nil && errors.Is(err, domain.ErrQuotaExceeded) {

			webx.WriteJSON(w, http.StatusOK, run)
			return
		}
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
		writeErr(w, err)
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
	t, err := d.Autopilots.CreateTrigger(r.Context(), wsOf(r), chi.URLParam(r, "autopilotId"),
		in.Kind, in.CronExpression, in.Timezone, in.Enabled)
	if err != nil {
		writeErr(w, err)
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
	out, err := d.Autopilots.UpdateTrigger(r.Context(), wsOf(r), chi.URLParam(r, "autopilotId"),
		chi.URLParam(r, "triggerId"), in.CronExpression, in.Timezone, in.Enabled)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	if err := d.Autopilots.DeleteTrigger(r.Context(), wsOf(r), chi.URLParam(r, "autopilotId"),
		chi.URLParam(r, "triggerId")); err != nil {
		writeErr(w, err)
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
		chi.URLParam(r, "autopilotId"), chi.URLParam(r, "triggerId"), in.Secret)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, cred)
}

func (d Deps) rotateWebhookToken(w http.ResponseWriter, r *http.Request) {
	cred, err := d.Webhooks.RotateWebhookToken(r.Context(), wsOf(r),
		chi.URLParam(r, "autopilotId"), chi.URLParam(r, "triggerId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, cred)
}

func (d Deps) listRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := d.Autopilots.ListRuns(r.Context(), wsOf(r), chi.URLParam(r, "autopilotId"), limit)
	if err != nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) getRun(w http.ResponseWriter, r *http.Request) {
	out, err := d.Autopilots.GetRun(r.Context(), wsOf(r), chi.URLParam(r, "runId"))
	if err != nil {
		writeErr(w, err)
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
	out, err := d.Webhooks.GetDelivery(r.Context(), wsOf(r), chi.URLParam(r, "deliveryId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) replayDelivery(w http.ResponseWriter, r *http.Request) {
	out, err := d.Webhooks.ReplayDelivery(r.Context(), wsOf(r), chi.URLParam(r, "deliveryId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}
