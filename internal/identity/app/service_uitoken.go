package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"orkome/internal/contract/apierr"
	"orkome/internal/identity/domain"
)

// UITokenTTL bounds a plugin-embedding ticket's life.
const UITokenTTL = 60 * time.Second

// UITokenClaims is the payload of a plugin-embedding ticket. The JWT field
// names (jti/sub/wsid/plugin) match the tokens the retired orkome webx
// signed, so tickets minted before the ploykit migration still verify.
// WorkItemID is the optional mint-time binding (FP-15 P1-2): the platform
// verified the work item lives in the minting workspace before signing it
// into the ticket; pack pages compare it against their URL parameter instead
// of trusting the raw {workItemId}. Empty = unbound ticket.
type UITokenClaims struct {
	Jti         string
	Sub         string
	WorkspaceID string
	Plugin      string
	WorkItemID  string
}

// WorkItemLocator answers "does this work item exist in this workspace" for
// the optional mint-time work_item_id binding. The composition root adapts
// the work domain's existing workspace-scoped lookup onto it — identity never
// imports the work package.
type WorkItemLocator interface {
	WorkItemInWorkspace(ctx context.Context, workspaceID, workItemID string) (bool, error)
}

// WorkItemLocatorFunc adapts a plain function to WorkItemLocator.
type WorkItemLocatorFunc func(ctx context.Context, workspaceID, workItemID string) (bool, error)

// WorkItemInWorkspace implements WorkItemLocator.
func (f WorkItemLocatorFunc) WorkItemInWorkspace(ctx context.Context, workspaceID, workItemID string) (bool, error) {
	return f(ctx, workspaceID, workItemID)
}

type UITokenRepo interface {
	InsertUIToken(ctx context.Context, jti, ws, user, plugin string, expiresAt time.Time) error
	ConsumeUIToken(ctx context.Context, jti string, now time.Time) (ws, user, plugin string, ok bool, err error)
}

type UITokenService struct {
	repo      UITokenRepo
	secret    []byte
	now       Clock
	workItems WorkItemLocator
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

// WithWorkItemLocator enables the optional mint-time work_item_id binding
// (FP-15 P1-2). Without it, mint requests carrying work_item_id are rejected.
func (s *UITokenService) WithWorkItemLocator(l WorkItemLocator) *UITokenService {
	s.workItems = l
	return s
}

// Mint signs a one-shot plugin ticket. workItemID is optional: empty leaves
// the ticket workspace-bound only; non-empty binds the ticket to that work
// item after the platform verified it lives in ws (cross-workspace or
// unknown ids are rejected here, so the signed claim is the ownership proof).
func (s *UITokenService) Mint(ctx context.Context, ws, user, plugin, workItemID string) (string, error) {
	if ws == "" || user == "" || plugin == "" {
		return "", apierr.NewValidation("workspace, user and plugin required")
	}
	if workItemID != "" {
		if !domain.UUIDOK(workItemID) {
			return "", apierr.NewValidation("work_item_id must be a UUID",
				apierr.FieldError{Field: "work_item_id", Problem: "must be a uuid"})
		}
		if s.workItems == nil {
			return "", apierr.New(apierr.Internal, "work item binding not configured")
		}
		ok, err := s.workItems.WorkItemInWorkspace(ctx, ws, workItemID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", apierr.NewValidation("work_item_id does not exist in the minting workspace",
				apierr.FieldError{Field: "work_item_id", Problem: "not found in workspace"})
		}
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
	mc := jwt.MapClaims{
		"jti": jti, "sub": user, "wsid": ws, "plugin": plugin,
		"iat": now.Unix(), "exp": now.Add(UITokenTTL).Unix(),
	}
	if workItemID != "" {
		mc["wiid"] = workItemID
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, mc)
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
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(),
		// exp 校验与 DB 消费检查同钟（s.now）：固定时钟的测试夹具才能自洽
		//（真实时钟会让写死 uitNow 的夹具在第二天全部"过期"）。
		jwt.WithTimeFunc(s.now))
	if err != nil || !parsed.Valid {
		return nil, errors.New("invalid ui token")
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid ui token claims")
	}
	str := func(k string) string { v, _ := mc[k].(string); return v }
	c := &UITokenClaims{Jti: str("jti"), Sub: str("sub"), WorkspaceID: str("wsid"),
		Plugin: str("plugin"), WorkItemID: str("wiid")}
	if c.Jti == "" || c.Sub == "" || c.WorkspaceID == "" || c.Plugin == "" {
		return nil, errors.New("incomplete ui token claims")
	}
	return c, nil
}
