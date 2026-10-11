package http

import (
	"net/http"

	"github.com/haozing/ploykit/platform/webx"

	"orkome/internal/contract/apierr"
)

// MountExtensions registers the product account-extension routes — only
// what the framework lacks: sidebar pins, ui tokens (plugin embedding) and
// the CLI session→PAT exchange. Everything else (login, sessions, account,
// PAT CRUD, workspaces/members/invitations/share-links) is served by
// ploykit's own identity and workspace mounts.
//
// The caller is responsible for the ambient auth chain, exactly as for the
// framework mounts: wrap the shared root mux with
// webx.Authenticate(&authCfg, sessions, pats) (and the product CSRF policy)
// before serving, so principals are resolved here. Routes registered:
//
//	GET/PUT  /api/pins, PUT /api/pins/reorder, DELETE /api/pins/{itemType}/{itemId}
//	          (RequireAuth + X-Workspace-Id active-membership gate)
//	POST     /api/ui-tokens, POST /api/ui-tokens/consume   (RequireAuth)
//	POST     /cli-token                                        (RequireAuth)
//
// MountExtensions mounts the product account-extension routes. auth is the
// composition root's Authenticate middleware (the shared root mux carries no
// global auth chain, so it must be applied here).
func MountExtensions(mux webx.Router, auth func(http.Handler) http.Handler, d Deps) {
	// pins: RequireAuth + X-Workspace-Id header context
	pins := func(h http.HandlerFunc) http.Handler {
		return auth(webx.RequireAuth(d.wsHeaderCtx(h)))
	}
	mux.Handle("GET /api/pins", pins(d.handleListPins))
	mux.Handle("PUT /api/pins", pins(d.handleAddPin))
	mux.Handle("PUT /api/pins/reorder", pins(d.handleReorderPins))
	mux.Handle("DELETE /api/pins/{itemType}/{itemId}", pins(d.handleRemovePin))

	// ui tokens: RequireAuth (mint additionally checks body workspace membership)
	mux.Handle("POST /api/ui-tokens", auth(webx.RequireAuth(http.HandlerFunc(d.handleUITokenMint))))
	mux.Handle("POST /api/ui-tokens/consume", auth(webx.RequireAuth(http.HandlerFunc(d.handleUITokenConsume))))

	// CLI session→PAT exchange
	mux.Handle("POST /cli-token", auth(webx.RequireAuth(http.HandlerFunc(d.handleCLIToken))))
}

// wsHeaderCtx loads the X-Workspace-Id workspace into the principal after
// checking active membership (same contract as the framework workspace
// adapter's WorkspaceHeaderCtx, which products may reuse instead).
func (d Deps) wsHeaderCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFromRequest(r)
		if p == nil {
			webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "authentication required", nil)
			return
		}
		wsID := r.Header.Get("X-Workspace-Id")
		if wsID == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
			return
		}
		if !p.Scope.AllowsWorkspace(wsID) {
			webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "workspace not in credential scope", nil)
			return
		}
		if _, ok, err := d.Repo.GetMember(r.Context(), wsID, p.UserID); err != nil || !ok {
			webx.WriteError(w, http.StatusNotFound, apierr.NotFound, "workspace not found", nil)
			return
		}
		p.WorkspaceID = wsID
		next.ServeHTTP(w, r)
	})
}
