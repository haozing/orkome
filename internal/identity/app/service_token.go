package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"orkome/internal/contract/apierr"
	"orkome/internal/identity/domain"
	"orkome/internal/platform/webx"
)

type Cache interface {
	Get(ctx context.Context, key string) (string, bool)
	Set(ctx context.Context, key, val string, ttl time.Duration)
	Del(ctx context.Context, keys ...string)
}

type TokenService struct {
	repo  Repo
	cache Cache
	now   Clock

	agentBinding func(ctx context.Context, patID string) (string, bool)
}

func NewTokenService(repo Repo, cache Cache, now Clock) *TokenService {
	if now == nil {
		now = time.Now
	}
	if cache == nil {
		cache = NoopCache{}
	}
	return &TokenService{repo: repo, cache: cache, now: now}
}

func (s *TokenService) SetAgentBinding(fn func(ctx context.Context, patID string) (string, bool)) {
	s.agentBinding = fn
}

type NoopCache struct{}

func (NoopCache) Get(context.Context, string) (string, bool)         { return "", false }
func (NoopCache) Set(context.Context, string, string, time.Duration) {}
func (NoopCache) Del(context.Context, ...string)                     {}

type CreatePATResult struct {
	PAT       PAT
	Plaintext string
}

func (s *TokenService) Create(ctx context.Context, userID, name string, ttl *time.Duration) (*CreatePATResult, error) {
	if name == "" || len(name) > 100 {
		return nil, apierr.NewValidation("invalid token name")
	}
	var exp *time.Time
	if ttl != nil && *ttl > 0 {
		t := s.now().Add(*ttl)
		exp = &t
	}
	token, hash, prefix, err := domain.MintPAT()
	if err != nil {
		return nil, err
	}
	pat, err := s.repo.InsertPAT(ctx, userID, name, hash, prefix, exp)
	if err != nil {
		return nil, err
	}
	return &CreatePATResult{PAT: pat, Plaintext: token}, nil
}

func (s *TokenService) List(ctx context.Context, userID string) ([]PAT, error) {
	return s.repo.ListPATs(ctx, userID)
}

func (s *TokenService) Renew(ctx context.Context, oldToken string, name string) (*CreatePATResult, error) {
	if !domain.IsPATFormat(oldToken) {
		return nil, apierr.New(apierr.Unauthenticated, "invalid token")
	}
	oldHash := domain.HashPAT(oldToken)
	pat, userID, ok, err := s.repo.GetPATByHash(ctx, oldHash)
	if err != nil || !ok || pat.RevokedAt != nil {
		return nil, apierr.New(apierr.Unauthenticated, "invalid token")
	}
	res, err := s.Create(ctx, userID, name, nil)
	if err != nil {
		return nil, err
	}
	if err := s.repo.RevokePAT(ctx, pat.ID, userID); err != nil {
		return nil, err
	}
	s.dropCache(ctx, oldHash)
	return res, nil
}

func (s *TokenService) Revoke(ctx context.Context, id, userID string) error {
	if err := s.repo.RevokePAT(ctx, id, userID); err != nil {
		return err
	}
	return nil
}

func (s *TokenService) RevokeAll(ctx context.Context, userID string) error {
	return s.repo.RevokeAllPATs(ctx, userID)
}

func (s *TokenService) ResolvePAT(ctx context.Context, token string) (*webx.Principal, error) {
	if !domain.IsPATFormat(token) {
		return nil, errors.New("bad format")
	}
	hash := domain.HashPAT(token)
	if p, ok := s.cached(ctx, hash); ok {
		return p, nil
	}
	pat, userID, ok, err := s.repo.GetPATByHash(ctx, hash)
	if err != nil || !ok {
		return nil, errors.New("not found")
	}
	if pat.RevokedAt != nil {
		return nil, errors.New("revoked")
	}
	if pat.ExpiresAt != nil && s.now().After(*pat.ExpiresAt) {
		return nil, errors.New("expired")
	}
	user, ok, err := s.repo.GetUser(ctx, userID)
	if err != nil || !ok {
		return nil, errors.New("user gone")
	}
	p := &webx.Principal{
		UserID: user.ID, Email: user.Email, Name: user.DisplayName,
		PATID: pat.ID, Source: webx.SourcePAT,
	}
	if s.agentBinding != nil {
		if agentID, ok := s.agentBinding(ctx, pat.ID); ok {
			p.AgentID = agentID
		}
	}
	_ = s.repo.TouchPAT(ctx, pat.ID)
	s.putCache(ctx, hash, p, pat.ExpiresAt)
	return p, nil
}

const patCacheTTL = 10 * time.Minute

func (s *TokenService) patKey(hash string) string { return "pat:" + hash }

func (s *TokenService) cached(ctx context.Context, hash string) (*webx.Principal, bool) {
	val, ok := s.cache.Get(ctx, s.patKey(hash))
	if !ok || val == "" {
		return nil, false
	}
	parts := strings.Split(val, "|")
	if len(parts) != 5 {
		return nil, false
	}
	return &webx.Principal{
		UserID: parts[0], Email: parts[1], Name: parts[2], PATID: parts[3], Source: webx.SourcePAT,
	}, true
}

func (s *TokenService) putCache(ctx context.Context, hash string, p *webx.Principal, exp *time.Time) {
	ttl := patCacheTTL
	if exp != nil {
		if d := exp.Sub(s.now()); d < ttl {
			ttl = d
		}
	}
	if ttl <= 0 {
		return
	}
	val := p.UserID + "|" + p.Email + "|" + p.Name + "|" + p.PATID + "|pat"
	s.cache.Set(ctx, s.patKey(hash), val, ttl)
}

func (s *TokenService) dropCache(ctx context.Context, hashes ...string) {
	keys := make([]string, 0, len(hashes))
	for _, h := range hashes {
		keys = append(keys, s.patKey(h))
	}
	s.cache.Del(ctx, keys...)
}

func newSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
