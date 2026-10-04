package webx

import (
	"crypto/sha256"
	"net/http"

	"github.com/gorilla/csrf"
)

type CSRFConfig struct {
	Key []byte
}

func DeriveCSRFKey(jwtSecret []byte) []byte {
	prefix := []byte("orkome-csrf-v1:")
	buf := make([]byte, 0, len(prefix)+len(jwtSecret))
	buf = append(buf, prefix...)
	buf = append(buf, jwtSecret...)
	h := sha256.Sum256(buf)
	return h[:]
}

func CSRFConditional(cfg *CSRFConfig, secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		var inner http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
		protected := csrf.Protect(cfg.Key,

			csrf.CookieName("orkome_csrf"),

			csrf.Path("/"),
			csrf.Secure(secure), csrf.HttpOnly(true), csrf.SameSite(csrf.SameSiteLaxMode),
		)(inner)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p := PrincipalFrom(r.Context()); p != nil && p.Source == SourcePAT {
				next.ServeHTTP(w, r)
				return
			}
			if !secure {
				r = csrf.PlaintextHTTPRequest(r)
			}
			protected.ServeHTTP(w, r)
		})
	}
}

func CSRFMaskedToken(r *http.Request) string { return csrf.Token(r) }
