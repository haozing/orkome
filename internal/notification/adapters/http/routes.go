package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"orkome/internal/notification/app"
	"orkome/internal/platform/webx"
)

type Deps struct {
	Notification *app.NotificationService
	Prefs        *app.PreferenceService
	MemberCheck  func(ctx context.Context, workspaceID, userID string) (bool, error)
}

func Mount(mux *http.ServeMux, d Deps, auth func(http.Handler) http.Handler) {
	reg := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(webx.RequireAuth(d.wsFromHeader(h))))
	}

	reg("GET /api/notifications", d.handleList)
	reg("GET /api/notifications/archived", d.handleListArchived)
	reg("GET /api/notifications/unread-count", d.handleUnreadCount)
	reg("GET /api/notifications/summary", d.handleSummary)
	reg("GET /api/notifications/facets", d.handleFacets)
	reg("POST /api/notifications/mark-all-read", d.handleMarkAllRead)
	reg("POST /api/notifications/archive-all", d.handleArchiveAll)
	reg("POST /api/notifications/archive-all-read", d.handleArchiveAllRead)
	reg("POST /api/notifications/{itemId}/read", d.handleRead)
	reg("POST /api/notifications/{itemId}/unread", d.handleUnread)
	reg("POST /api/notifications/{itemId}/archive", d.handleArchive)
	reg("POST /api/notifications/{itemId}/unarchive", d.handleUnarchive)
	reg("GET /api/notification-preferences", d.handleGetPrefs)
	reg("PATCH /api/notification-preferences", d.handlePatchPrefs)
	reg("PUT /api/notification-preferences", d.handlePutPrefs)
}

func (d Deps) wsFromHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		ws := r.Header.Get("X-Workspace-Id")
		if p == nil || ws == "" {
			webx.WriteError(w, http.StatusBadRequest, "E_VALIDATION", "X-Workspace-Id header required", nil)
			return
		}

		if d.MemberCheck == nil {
			webx.WriteError(w, http.StatusInternalServerError, "E_INTERNAL", "member check not wired", nil)
			return
		}
		ok, err := d.MemberCheck(r.Context(), ws, p.UserID)
		if err != nil || !ok {
			webx.WriteError(w, http.StatusForbidden, "E_FORBIDDEN", "not a workspace member", nil)
			return
		}
		p.WorkspaceID = ws
		next.ServeHTTP(w, r)
	})
}

func writeErr(w http.ResponseWriter, err error) {
	var coder interface{ Code() string }
	if errors.As(err, &coder) {
		webx.WriteError(w, http.StatusBadRequest, coder.Code(), err.Error(), nil)
		return
	}
	webx.WriteError(w, http.StatusInternalServerError, "E_INTERNAL", "internal error", nil)
}

func (d Deps) handleList(w http.ResponseWriter, r *http.Request) {
	d.list(w, r, r.URL.Query().Get("archived") == "true")
}

func (d Deps) handleListArchived(w http.ResponseWriter, r *http.Request) {
	d.list(w, r, true)
}

func (d Deps) list(w http.ResponseWriter, r *http.Request, archived bool) {
	p := webx.PrincipalFrom(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := d.Notification.List(r.Context(), p.WorkspaceID, p.UserID,
		archived, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (d Deps) handleUnreadCount(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	n, err := d.Notification.UnreadCount(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"count": n})
}

func (d Deps) handleSummary(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	s, err := d.Notification.Summary(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, s)
}

func (d Deps) handleFacets(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	facets, err := d.Notification.Facets(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if facets == nil {
		facets = []app.Facet{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"facets": facets})
}

func (d Deps) handleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	if err := d.Notification.MarkAllRead(r.Context(), p.WorkspaceID, p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleArchiveAll(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	if err := d.Notification.ArchiveAll(r.Context(), p.WorkspaceID, p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleArchiveAllRead(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	n, err := d.Notification.ArchiveAllRead(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]int{"archived": n})
}

func (d Deps) handleRead(w http.ResponseWriter, r *http.Request) {
	d.mark(w, r, true)
}

func (d Deps) handleUnread(w http.ResponseWriter, r *http.Request) {
	d.mark(w, r, false)
}

func (d Deps) mark(w http.ResponseWriter, r *http.Request, read bool) {
	p := webx.PrincipalFrom(r.Context())
	if err := d.Notification.MarkRead(r.Context(), p.WorkspaceID, p.UserID, r.PathValue("itemId"), read); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleArchive(w http.ResponseWriter, r *http.Request)   { d.arch(w, r, true) }
func (d Deps) handleUnarchive(w http.ResponseWriter, r *http.Request) { d.arch(w, r, false) }

func (d Deps) arch(w http.ResponseWriter, r *http.Request, archived bool) {
	p := webx.PrincipalFrom(r.Context())
	if err := d.Notification.Archive(r.Context(), p.WorkspaceID, p.UserID, r.PathValue("itemId"), archived); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleGetPrefs(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	prefs, err := d.Prefs.Get(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, prefs)
}

func (d Deps) handlePatchPrefs(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	var patch app.PreferencePatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&patch); err != nil {
		webx.WriteError(w, http.StatusBadRequest, "E_VALIDATION", "invalid JSON body", nil)
		return
	}
	merged, err := d.Prefs.Patch(r.Context(), p.WorkspaceID, p.UserID, patch)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, merged)
}

func (d Deps) handlePutPrefs(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	var in app.Preference
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, "E_VALIDATION", "invalid JSON body", nil)
		return
	}
	if err := d.Prefs.Upsert(r.Context(), p.WorkspaceID, p.UserID, in); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, in)
}
