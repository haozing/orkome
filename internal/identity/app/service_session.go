package app

import (
	"context"
	"time"

	"orkome/internal/contract/apierr"
	"orkome/internal/identity/domain"
	"orkome/internal/platform/webx"
)

type SessionConfig struct {
	AllowSignup    bool
	AllowedEmails  []string
	AllowedDomains []string
	DevCode        string
}

type SessionService struct {
	repo  Repo
	email EmailSender
	auth  *webx.AuthConfig
	cfg   SessionConfig
	now   Clock
}

func NewSessionService(repo Repo, email EmailSender, auth *webx.AuthConfig, cfg SessionConfig, now Clock) *SessionService {
	if now == nil {
		now = time.Now
	}
	return &SessionService{repo: repo, email: email, auth: auth, cfg: cfg, now: now}
}

func (s *SessionService) SendCode(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	if !domain.EmailOK(email) {
		return apierr.NewValidation("invalid email", fieldErr("email", "malformed"))
	}
	if !domain.SignupAllowed(email, s.cfg.AllowedEmails, s.cfg.AllowedDomains) {
		return apierr.New(apierr.Forbidden, "signup not allowed for this email")
	}
	if latest, ok, err := s.repo.LatestPendingCode(ctx, email); err == nil && ok {
		if s.now().Sub(latest.CreatedAt) < domain.ResendCooldown {
			return apierr.New(apierr.RateLimited, "code already sent, retry later")
		}
	}
	code, err := domain.IssueCode()
	if err != nil {
		return err
	}
	if err := s.repo.InsertCode(ctx, email, domain.HashCode(code), domain.CodeExpiry(s.now())); err != nil {
		return err
	}
	return s.email.SendVerificationCode(ctx, email, code)
}

type VerifyResult struct {
	Token   string
	Expires time.Time
	User    User
}

func (s *SessionService) VerifyCode(ctx context.Context, email, code string) (*VerifyResult, error) {
	email = normalizeEmail(email)
	if !domain.EmailOK(email) || !domain.CodeOK(code) && code != s.cfg.DevCode {
		return nil, apierr.NewValidation("invalid input")
	}
	effective := code
	if s.cfg.DevCode != "" && code == s.cfg.DevCode {
		effective = ""
	}

	if !s.cfg.AllowSignup && !domain.EmailWhitelisted(email, s.cfg.AllowedEmails, s.cfg.AllowedDomains) {
		_, exists, err := s.repo.GetUserByEmail(ctx, email)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, apierr.New(apierr.Forbidden, "signup disabled")
		}
	}
	if effective != "" {
		latest, ok, err := s.repo.LatestPendingCode(ctx, email)
		if err != nil {
			return nil, err
		}
		if !ok || s.now().After(latest.ExpiresAt) {
			return nil, apierr.New(apierr.Unauthenticated, "code expired or not found")
		}
		if latest.Attempts >= domain.CodeMaxTries {
			return nil, apierr.New(apierr.Unauthenticated, "too many attempts")
		}
		if latest.CodeHash != domain.HashCode(effective) {
			_ = s.repo.IncCodeAttempts(ctx, latest.ID)
			return nil, apierr.New(apierr.Unauthenticated, "code mismatch")
		}
		if err := s.repo.ConsumeCode(ctx, latest.ID); err != nil {
			return nil, err
		}
	}
	user, err := s.repo.UpsertUserByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	token, exp, err := s.auth.SignSession(webx.SessionClaims{
		Sub: user.ID, Email: user.Email, Name: user.DisplayName, Sid: newSessionID(),
	}, s.now())
	if err != nil {
		return nil, err
	}
	return &VerifyResult{Token: token, Expires: exp, User: user}, nil
}

func (s *SessionService) Me(ctx context.Context, p *webx.Principal) (*User, []Workspace, error) {
	user, ok, err := s.repo.GetUser(ctx, p.UserID)
	if err != nil || !ok {
		return nil, nil, apierr.New(apierr.Unauthenticated, "user not found")
	}
	wss, err := s.repo.ListMyWorkspaces(ctx, p.UserID)
	return &user, wss, err
}

func (s *SessionService) UpdateMe(ctx context.Context, p *webx.Principal, name, lang, tz string) (*User, error) {
	if name != "" && len(name) > 100 {
		return nil, apierr.NewValidation("display name too long")
	}
	u, err := s.repo.UpdateUser(ctx, p.UserID, name, lang, tz)
	return &u, err
}

func (s *SessionService) Reissue(ctx context.Context, p *webx.Principal) (string, time.Time, error) {
	user, ok, err := s.repo.GetUser(ctx, p.UserID)
	if err != nil || !ok {
		return "", time.Time{}, apierr.New(apierr.Unauthenticated, "user not found")
	}
	return s.auth.SignSession(webx.SessionClaims{
		Sub: user.ID, Email: user.Email, Name: user.DisplayName, Sid: newSessionID(),
	}, s.now())
}

func fieldErr(f, problem string) apierr.FieldError {
	return apierr.FieldError{Field: f, Problem: problem}
}

func normalizeEmail(e string) string {

	return trimLower(e)
}
