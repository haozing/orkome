package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	"orkome/internal/webhooks/app"
)

func decode[T any](w http.ResponseWriter, r *http.Request) (*T, bool) {
	var in T
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return nil, false
	}
	return &in, true
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) {
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

type Deps struct {
	Service     *app.Service
	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)

	RoleOf func(ctx context.Context, workspaceID, userID string) (string, error)
}

type deps = Deps

func Mount(mux *http.ServeMux, d Deps, auth func(http.Handler) http.Handler) {
	if d.Service == nil {
		return
	}
	reg := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(webx.RequireAuth(d.memberMW(h))))
	}
	reg("POST /api/webhook-subscriptions", d.handleCreate)
	reg("GET /api/webhook-subscriptions", d.handleList)
	reg("DELETE /api/webhook-subscriptions/{subscriptionId}", d.handleRevoke)
	reg("POST /api/webhook-subscriptions/{subscriptionId}/test", d.handleTest)
	reg("GET /api/webhook-subscriptions/{subscriptionId}/deliveries", d.handleDeliveries)
	reg("POST /api/webhook-subscriptions/{subscriptionId}/deliveries/{deliveryId}/replay", d.handleReplay)

	reg("GET /api/events", d.handleEvents)
}

func (d deps) memberMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws := r.Header.Get("X-Workspace-Id")
		if ws == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
			return
		}
		p := webx.PrincipalFrom(r.Context())
		ok, err := d.MemberCheck(r.Context(), ws, p.UserID)
		if err != nil || !ok {
			webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "workspace membership required", nil)
			return
		}
		p.WorkspaceID = ws
		next.ServeHTTP(w, r)
	})
}

func (d deps) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := webx.PrincipalFrom(r.Context())
	if p == nil || p.WorkspaceID == "" {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "workspace required", nil)
		return false
	}
	if d.RoleOf == nil {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "role unavailable (fail-closed)", nil)
		return false
	}
	role, err := d.RoleOf(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil || (role != "owner" && role != "admin") {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "workspace owner or admin role required", nil)
		return false
	}
	return true
}

func (d deps) ws(r *http.Request) string { return webx.PrincipalFrom(r.Context()).WorkspaceID }

func (d deps) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !d.requireAdmin(w, r) {
		return
	}
	in, ok := decode[struct {
		URL         string   `json:"url"`
		Events      []string `json:"events"`
		Description string   `json:"description"`
	}](w, r)
	if !ok {
		return
	}
	p := webx.PrincipalFrom(r.Context())
	res, err := d.Service.Create(r.Context(), d.ws(r), in.URL, in.Events, in.Description, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, res)
}

func (d deps) handleList(w http.ResponseWriter, r *http.Request) {
	subs, err := d.Service.List(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if subs == nil {
		subs = []app.Subscription{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": subs})
}

func (d deps) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if !d.requireAdmin(w, r) {
		return
	}
	if err := d.Service.Revoke(r.Context(), d.ws(r), r.PathValue("subscriptionId")); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "revoked"})
}

func (d deps) handleTest(w http.ResponseWriter, r *http.Request) {
	if !d.requireAdmin(w, r) {
		return
	}
	if err := d.Service.Test(r.Context(), d.ws(r), r.PathValue("subscriptionId")); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "ping_queued"})
}

func (d deps) handleDeliveries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := d.Service.Deliveries(r.Context(), d.ws(r), r.PathValue("subscriptionId"),
		r.URL.Query().Get("status"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []app.Delivery{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d deps) handleReplay(w http.ResponseWriter, r *http.Request) {
	if !d.requireAdmin(w, r) {
		return
	}
	if err := d.Service.Replay(r.Context(), d.ws(r), r.PathValue("subscriptionId"), r.PathValue("deliveryId")); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "replayed"})
}

func (d deps) handleEvents(w http.ResponseWriter, r *http.Request) {
	var afterAt time.Time
	afterID := ""
	if since := strings.TrimSpace(r.URL.Query().Get("since")); since != "" {
		parts := strings.SplitN(since, "|", 2)
		if len(parts) != 2 {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "since must be <unixnano>|<id>", nil)
			return
		}
		ns, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || ns <= 0 || parts[1] == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "since must be <unixnano>|<id>", nil)
			return
		}
		afterAt = time.Unix(0, ns).UTC()
		afterID = parts[1]
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, next, err := d.Service.Events(r.Context(), d.ws(r), afterAt, afterID, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	if events == nil {
		events = []app.CatalogEvent{}
	}
	resp := map[string]any{"events": events, "next_cursor": nil}
	if next != "" {
		resp["next_cursor"] = next
	}
	webx.WriteJSON(w, http.StatusOK, resp)
}
