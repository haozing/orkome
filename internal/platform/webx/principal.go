package webx

import (
	"context"
	"net/http"
)

type Source string

const (
	SourceSession Source = "session"
	SourcePAT     Source = "pat"
)

type Principal struct {
	UserID      string
	Email       string
	Name        string
	WorkspaceID string
	Role        string
	PATID       string
	Source      Source

	AgentID string
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if PrincipalFrom(r.Context()) == nil {
			WriteError(w, http.StatusUnauthorized, "E_UNAUTHENTICATED", "authentication required", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireHuman(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p := PrincipalFrom(r.Context()); p == nil || p.Source != SourceSession {
			WriteError(w, http.StatusForbidden, "E_FORBIDDEN", "human session required", nil)
			return
		}
		next(w, r)
	}
}
