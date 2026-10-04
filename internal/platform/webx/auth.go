package webx

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AuthConfig struct {
	Secret       []byte
	SessionTTL   time.Duration
	CookieDomain string
	Secure       bool
}

type PATLookup interface {
	ResolvePAT(ctx context.Context, token string) (*Principal, error)
}

type SessionClaims struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Sid   string `json:"sid"`
	Exp   time.Time
}

func (a *AuthConfig) SignSession(claims SessionClaims, issued time.Time) (string, time.Time, error) {
	exp := issued.Add(a.SessionTTL)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": claims.Sub, "email": claims.Email, "name": claims.Name,
		"sid": claims.Sid, "iat": issued.Unix(), "exp": exp.Unix(),
	})
	s, err := tok.SignedString(a.Secret)
	return s, exp, err
}

func (a *AuthConfig) VerifySession(token string) (*SessionClaims, error) {
	parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return a.Secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		return nil, errors.New("invalid session token")
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid claims")
	}
	str := func(k string) string { v, _ := mc[k].(string); return v }
	if str("sub") == "" {
		return nil, errors.New("missing sub")
	}
	claims := &SessionClaims{
		Sub: str("sub"), Email: str("email"), Name: str("name"), Sid: str("sid"),
	}
	if expNum, ok := mc["exp"].(float64); ok {
		claims.Exp = time.Unix(int64(expNum), 0)
	}
	return claims, nil
}

type UITokenClaims struct {
	Jti         string
	Sub         string
	WorkspaceID string
	Plugin      string
}

func (a *AuthConfig) SignUIToken(claims UITokenClaims, issued time.Time, ttl time.Duration) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"jti": claims.Jti, "sub": claims.Sub, "wsid": claims.WorkspaceID, "plugin": claims.Plugin,
		"iat": issued.Unix(), "exp": issued.Add(ttl).Unix(),
	})
	return tok.SignedString(a.Secret)
}

func (a *AuthConfig) VerifyUIToken(token string) (*UITokenClaims, error) {
	parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return a.Secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid {
		return nil, errors.New("invalid ui token")
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid ui token claims")
	}
	str := func(k string) string { v, _ := mc[k].(string); return v }
	c := &UITokenClaims{Jti: str("jti"), Sub: str("sub"), WorkspaceID: str("wsid"), Plugin: str("plugin")}
	if c.Jti == "" || c.Sub == "" || c.WorkspaceID == "" || c.Plugin == "" {
		return nil, errors.New("incomplete ui token claims")
	}
	return c, nil
}

func (a *AuthConfig) SetSessionCookie(w http.ResponseWriter, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: "orkome_auth", Value: token, Path: "/",
		Domain: a.CookieDomain, Expires: exp, HttpOnly: true,
		Secure: a.Secure, SameSite: http.SameSiteLaxMode,
	})
}

func (a *AuthConfig) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: "orkome_auth", Value: "", Path: "/", Domain: a.CookieDomain,
		MaxAge: -1, HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode,
	})
}

func (a *AuthConfig) ShouldRenew(exp time.Time, now time.Time) bool {
	remaining := exp.Sub(now)
	return remaining < a.SessionTTL/2
}

const bearerPrefix = "Bearer "

func Authenticate(cfg *AuthConfig, pat PATLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, bearerPrefix) {
				token := strings.TrimPrefix(h, bearerPrefix)
				if !strings.HasPrefix(token, "ork_") {
					WriteError(w, http.StatusUnauthorized, "E_UNAUTHENTICATED", "malformed token", nil)
					return
				}
				p, err := pat.ResolvePAT(ctx, token)
				if err != nil || p == nil {
					WriteError(w, http.StatusUnauthorized, "E_UNAUTHENTICATED", "invalid token", nil)
					return
				}
				next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, p)))
				return
			}

			ck, err := r.Cookie("orkome_auth")
			if err != nil || ck.Value == "" {
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			claims, err := cfg.VerifySession(ck.Value)
			if err != nil {
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if cfg.ShouldRenew(claims.Exp, time.Now()) {
				tok, exp, err := cfg.SignSession(*claims, time.Now())
				if err == nil {
					cfg.SetSessionCookie(w, tok, exp)
				}
			}
			p := &Principal{UserID: claims.Sub, Email: claims.Email, Name: claims.Name, Source: SourceSession}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, p)))
		})
	}
}
