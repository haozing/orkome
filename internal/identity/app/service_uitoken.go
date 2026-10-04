package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
)

const UITokenTTL = 60 * time.Second

type UITokenClaims = webx.UITokenClaims

type UITokenRepo interface {
	InsertUIToken(ctx context.Context, jti, ws, user, plugin string, expiresAt time.Time) error
	ConsumeUIToken(ctx context.Context, jti string, now time.Time) (ws, user, plugin string, ok bool, err error)
}

type UITokenService struct {
	repo UITokenRepo
	auth *webx.AuthConfig
	now  Clock
}

func NewUITokenService(repo UITokenRepo, auth *webx.AuthConfig, now Clock) *UITokenService {
	if now == nil {
		now = time.Now
	}
	return &UITokenService{repo: repo, auth: auth, now: now}
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
	return s.auth.SignUIToken(UITokenClaims{Jti: jti, Sub: user, WorkspaceID: ws, Plugin: plugin}, now, UITokenTTL)
}

func (s *UITokenService) Consume(ctx context.Context, token string) (*UITokenClaims, error) {
	claims, err := s.auth.VerifyUIToken(token)
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
