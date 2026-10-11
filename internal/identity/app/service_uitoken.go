package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"orkome/internal/contract/apierr"
)

// UITokenTTL bounds a plugin-embedding ticket's life.
const UITokenTTL = 60 * time.Second

// UITokenClaims is the payload of a plugin-embedding ticket. The JWT field
// names (jti/sub/wsid/plugin) match the tokens the retired orkome webx
// signed, so tickets minted before the ploykit migration still verify.
type UITokenClaims struct {
	Jti         string
	Sub         string
	WorkspaceID string
	Plugin      string
}

type UITokenRepo interface {
	InsertUIToken(ctx context.Context, jti, ws, user, plugin string, expiresAt time.Time) error
	ConsumeUIToken(ctx context.Context, jti string, now time.Time) (ws, user, plugin string, ok bool, err error)
}

type UITokenService struct {
	repo   UITokenRepo
	secret []byte
	now    Clock
}

// NewUITokenService signs single-use plugin tickets with an HS256 secret the
// product owns (any stable secret works; derive one from the product's JWT
// secret in the composition root).
func NewUITokenService(repo UITokenRepo, secret []byte, now Clock) *UITokenService {
	if now == nil {
		now = time.Now
	}
	return &UITokenService{repo: repo, secret: secret, now: now}
}

func (s *UITokenService) Mint(ctx context.Context, ws, user, plugin string) (string, error) {
	if ws == "" || user == "" || plugin == "" {
		return "", apierr.NewValidation("workspace, user and plugin required")
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	jti := "uit_" + hex.EncodeToString(b)
	now := s.now().UTC()
	if err := s.repo.InsertUIToken(ctx, jti, ws, user, plugin, now.Add(UITokenTTL)); err != nil {
		return "", err
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"jti": jti, "sub": user, "wsid": ws, "plugin": plugin,
		"iat": now.Unix(), "exp": now.Add(UITokenTTL).Unix(),
	})
	return tok.SignedString(s.secret)
}

func (s *UITokenService) Consume(ctx context.Context, token string) (*UITokenClaims, error) {
	claims, err := s.verifyUIToken(token)
	if err != nil {
		return nil, apierr.New(apierr.Unauthenticated, "invalid ui token")
	}
	ws, user, plugin, ok, err := s.repo.ConsumeUIToken(ctx, claims.Jti, s.now().UTC())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.Unauthenticated, "ui token already consumed or expired")
	}
	if ws != claims.WorkspaceID || user != claims.Sub || plugin != claims.Plugin {
		return nil, apierr.New(apierr.Unauthenticated, "ui token claims mismatch")
	}
	return claims, nil
}

func (s *UITokenService) verifyUIToken(token string) (*UITokenClaims, error) {
	parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return s.secret, nil },
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
