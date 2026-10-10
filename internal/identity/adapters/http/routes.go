package http

import (
	"net/http"

	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
)

// Routes builds the combined root + /api surface (root auth/config endpoints
// plus the full /api identity face) on a single flat mux — middleware that chi
// applied per-group is wrapped around each handler at registration time.
func Routes(d Deps, csrfSecure bool) http.Handler {
	mux := http.NewServeMux()
	mountRootRoutes(mux, d, csrfSecure)
	MountAPI(mux, d, csrfSecure)
	return mux
}

func authChainOf(d Deps, csrfSecure bool) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return webx.Authenticate(d.Auth, d.Tokens)(webx.CSRFConditional(
			&webx.CSRFConfig{Key: d.CSRFKey}, csrfSecure)(h))
	}
}

// RootRouter builds the mux holding the un-prefixed root endpoints
// (/auth/*, /config, /cli-token).
func RootRouter(d Deps, csrfSecure bool) *http.ServeMux {
	mux := http.NewServeMux()
	mountRootRoutes(mux, d, csrfSecure)
	return mux
}

func mountRootRoutes(mux *http.ServeMux, d Deps, csrfSecure bool) {
	if d.SendCodePerMinute <= 0 {
		d.SendCodePerMinute = 10
	}
	if d.VerifyCodePerMinute <= 0 {
		d.VerifyCodePerMinute = 20
	}
	auth := authChainOf(d, csrfSecure)
	ipKey := func(r *http.Request) string { return webx.ClientIP(r, d.TrustedProxies...) }

	mux.Handle("POST /auth/send-code", auth(webx.RateLimit(d.Limiter, "sendcode-ip",
		d.SendCodePerMinute, ipKey)(http.HandlerFunc(d.handleSendCode))))
	mux.Handle("POST /auth/verify-code", auth(webx.RateLimit(d.Limiter, "verifycode-ip",
		d.VerifyCodePerMinute, ipKey)(http.HandlerFunc(d.handleVerifyCode))))
	mux.Handle("POST /auth/logout", auth(http.HandlerFunc(d.handleLogout)))
	mux.Handle("GET /config", auth(http.HandlerFunc(d.handleConfig)))
	mux.Handle("POST /cli-token", auth(webx.RequireAuth(http.HandlerFunc(d.handleCLIToken))))
}

// APIRouter builds a standalone mux holding just the /api identity face.
func APIRouter(d Deps, csrfSecure bool) *http.ServeMux {
	mux := http.NewServeMux()
	MountAPI(mux, d, csrfSecure)
	return mux
}

// MountAPI mounts the /api-prefixed identity endpoints.
func MountAPI(mux *http.ServeMux, d Deps, csrfSecure bool) {
	auth := authChainOf(d, csrfSecure)
	authed := func(h http.HandlerFunc) http.Handler { return auth(h) }
	require := func(h http.HandlerFunc) http.Handler { return auth(webx.RequireAuth(h)) }

	// auth chain only (no RequireAuth)
	mux.Handle("POST /api/auth/refresh", authed(d.handleRefresh))
	mux.Handle("GET /api/share-links/{code}", authed(d.handlePeekShareLink))

	// auth + RequireAuth
	mux.Handle("GET /api/me", require(d.handleMe))
	mux.Handle("PATCH /api/me", require(d.handlePatchMe))

	if d.UITokens != nil {
		mux.Handle("POST /api/ui-tokens", require(d.handleUITokenMint))
		mux.Handle("POST /api/ui-tokens/consume", require(d.handleUITokenConsume))
	}

	mux.Handle("GET /api/invitations", require(d.handleMyInvitations))
	mux.Handle("POST /api/invitations/{invitationId}/accept", require(d.handleAcceptInvitation))
	mux.Handle("POST /api/invitations/{invitationId}/decline", require(d.handleDeclineInvitation))

	mux.Handle("POST /api/share-links/join", require(d.handleJoinByCode))

	mux.Handle("GET /api/tokens", require(d.handleListTokens))
	mux.Handle("POST /api/tokens", require(d.handleCreateToken))
	mux.Handle("POST /api/tokens/current/renew", require(d.handleRenewToken))
	mux.Handle("DELETE /api/tokens/{tokenId}", require(d.handleRevokeToken))

	// pins: RequireAuth + X-Workspace-Id header context
	pins := func(h http.HandlerFunc) http.Handler { return auth(webx.RequireAuth(d.wsHeaderCtx(h))) }
	mux.Handle("GET /api/pins", pins(d.handleListPins))
	mux.Handle("PUT /api/pins", pins(d.handleAddPin))
	mux.Handle("PUT /api/pins/reorder", pins(d.handleReorderPins))
	mux.Handle("DELETE /api/pins/{itemType}/{itemId}", pins(d.handleRemovePin))

	// workspaces: RequireAuth + path workspace context
	ws := func(h http.HandlerFunc) http.Handler { return auth(webx.RequireAuth(d.workspaceCtx(h))) }
	mux.Handle("GET /api/workspaces", require(d.handleListWorkspaces))
	mux.Handle("POST /api/workspaces", require(d.handleCreateWorkspace))
	mux.Handle("GET /api/workspaces/{workspaceId}", ws(d.handleGetWorkspace))
	mux.Handle("PATCH /api/workspaces/{workspaceId}", ws(d.handlePatchWorkspace))
	mux.Handle("DELETE /api/workspaces/{workspaceId}", ws(d.handleDeleteWorkspace))
	mux.Handle("GET /api/workspaces/{workspaceId}/members", ws(d.handleListMembers))
	mux.Handle("POST /api/workspaces/{workspaceId}/members", ws(d.handleInviteMember))
	mux.Handle("PATCH /api/workspaces/{workspaceId}/members/{memberId}", ws(d.handleUpdateMember))
	mux.Handle("DELETE /api/workspaces/{workspaceId}/members/{memberId}", ws(d.handleRemoveMember))
	mux.Handle("POST /api/workspaces/{workspaceId}/leave", ws(d.handleLeave))
	mux.Handle("GET /api/workspaces/{workspaceId}/invitations", ws(d.handleListInvitations))
	mux.Handle("POST /api/workspaces/{workspaceId}/invitations", ws(d.handleInviteMember))
	mux.Handle("DELETE /api/workspaces/{workspaceId}/invitations/{invitationId}", ws(d.handleRevokeInvitation))
	mux.Handle("GET /api/workspaces/{workspaceId}/share-links", ws(d.handleListShareLinks))
	mux.Handle("POST /api/workspaces/{workspaceId}/share-links", ws(d.handleCreateShareLink))
	mux.Handle("DELETE /api/workspaces/{workspaceId}/share-links/{linkId}", ws(d.handleDeleteShareLink))
}

func (d Deps) workspaceCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		if p == nil {
			webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "authentication required", nil)
			return
		}
		wsID := r.PathValue("workspaceId")
		m, ok, err := d.Repo.GetMember(r.Context(), wsID, p.UserID)
		if err != nil || !ok {

			webx.WriteError(w, http.StatusNotFound, apierr.NotFound, "workspace not found", nil)
			return
		}
		p.WorkspaceID = wsID
		p.Role = m.Role
		next.ServeHTTP(w, r)
	})
}

func (d Deps) wsHeaderCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		if p == nil {
			webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "authentication required", nil)
			return
		}
		wsID := r.Header.Get("X-Workspace-Id")
		if wsID == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
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
