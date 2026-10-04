package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
)

func Routes(d Deps, csrfSecure bool) http.Handler {
	r := chi.NewRouter()
	r.Mount("/", RootRouter(d, csrfSecure))
	r.Mount("/api", APIRouter(d, csrfSecure))
	return r
}

func authChainOf(d Deps, csrfSecure bool) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return webx.Authenticate(d.Auth, d.Tokens)(webx.CSRFConditional(
			&webx.CSRFConfig{Key: d.CSRFKey}, csrfSecure)(h))
	}
}

func RootRouter(d Deps, csrfSecure bool) chi.Router {
	if d.SendCodePerMinute <= 0 {
		d.SendCodePerMinute = 10
	}
	if d.VerifyCodePerMinute <= 0 {
		d.VerifyCodePerMinute = 20
	}
	r := chi.NewRouter()
	r.Use(authChainOf(d, csrfSecure))

	ipKey := func(r *http.Request) string { return webx.ClientIP(r, d.TrustedProxies...) }
	r.Post("/auth/send-code", webx.RateLimit(d.Limiter, "sendcode-ip", d.SendCodePerMinute,
		ipKey)(http.HandlerFunc(d.handleSendCode)).ServeHTTP)
	r.Post("/auth/verify-code", webx.RateLimit(d.Limiter, "verifycode-ip", d.VerifyCodePerMinute,
		ipKey)(http.HandlerFunc(d.handleVerifyCode)).ServeHTTP)
	r.Post("/auth/logout", d.handleLogout)
	r.Get("/config", d.handleConfig)
	r.Group(func(r chi.Router) {
		r.Use(webx.RequireAuth)
		r.Post("/cli-token", d.handleCLIToken)
	})
	return r
}

func APIRouter(d Deps, csrfSecure bool) chi.Router {
	r := chi.NewRouter()
	MountAPI(r, d, csrfSecure)
	return r
}

func MountAPI(r chi.Router, d Deps, csrfSecure bool) {
	r.Group(func(r chi.Router) {
		r.Use(authChainOf(d, csrfSecure))

		r.Post("/auth/refresh", d.handleRefresh)

		r.Get("/share-links/{code}", d.handlePeekShareLink)

		r.Group(func(r chi.Router) {
			r.Use(webx.RequireAuth)
			r.Get("/me", d.handleMe)
			r.Patch("/me", d.handlePatchMe)

			if d.UITokens != nil {
				r.Post("/ui-tokens", d.handleUITokenMint)
				r.Post("/ui-tokens/consume", d.handleUITokenConsume)
			}

			r.Get("/invitations", d.handleMyInvitations)
			r.Post("/invitations/{invitationId}/accept", d.handleAcceptInvitation)
			r.Post("/invitations/{invitationId}/decline", d.handleDeclineInvitation)

			r.Post("/share-links/join", d.handleJoinByCode)

			r.Get("/tokens", d.handleListTokens)
			r.Post("/tokens", d.handleCreateToken)
			r.Post("/tokens/current/renew", d.handleRenewToken)
			r.Delete("/tokens/{tokenId}", d.handleRevokeToken)

			r.Group(func(r chi.Router) {
				r.Use(d.wsHeaderCtx)
				r.Get("/pins", d.handleListPins)
				r.Put("/pins", d.handleAddPin)
				r.Put("/pins/reorder", d.handleReorderPins)
				r.Delete("/pins/{itemType}/{itemId}", d.handleRemovePin)
			})

			r.Route("/workspaces", func(r chi.Router) {
				r.Get("/", d.handleListWorkspaces)
				r.Post("/", d.handleCreateWorkspace)
				r.Route("/{workspaceId}", func(r chi.Router) {
					r.Use(d.workspaceCtx)
					r.Get("/", d.handleGetWorkspace)
					r.Patch("/", d.handlePatchWorkspace)
					r.Delete("/", d.handleDeleteWorkspace)
					r.Get("/members", d.handleListMembers)
					r.Post("/members", d.handleInviteMember)
					r.Patch("/members/{memberId}", d.handleUpdateMember)
					r.Delete("/members/{memberId}", d.handleRemoveMember)
					r.Post("/leave", d.handleLeave)
					r.Get("/invitations", d.handleListInvitations)
					r.Post("/invitations", d.handleInviteMember)
					r.Delete("/invitations/{invitationId}", d.handleRevokeInvitation)
					r.Get("/share-links", d.handleListShareLinks)
					r.Post("/share-links", d.handleCreateShareLink)
					r.Delete("/share-links/{linkId}", d.handleDeleteShareLink)
				})
			})
		})
	})
}

func (d Deps) workspaceCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		if p == nil {
			webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "authentication required", nil)
			return
		}
		wsID := chi.URLParam(r, "workspaceId")
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
