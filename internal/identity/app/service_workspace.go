package app

import (
	"context"
	"strings"
	"time"

	"orkome/internal/contract/apierr"
	"orkome/internal/identity/domain"
	"orkome/internal/platform/webx"
)

type WorkspaceService struct {
	repo   Repo
	email  EmailSender
	tokens *TokenService
	now    Clock

	UserRevocation UserRevocation

	WorkspaceTeardown WorkspaceTeardown
}

func NewWorkspaceService(repo Repo, email EmailSender, tokens *TokenService, now Clock) *WorkspaceService {
	if now == nil {
		now = time.Now
	}
	return &WorkspaceService{repo: repo, email: email, tokens: tokens, now: now}
}

func (s *WorkspaceService) Create(ctx context.Context, userID, name, slug string) (*Workspace, error) {
	name = strings.TrimSpace(name)
	slug = strings.ToLower(strings.TrimSpace(slug))
	if name == "" || len(name) > 100 {
		return nil, apierr.NewValidation("invalid name", fieldErr("name", "1-100 chars"))
	}
	if !domain.SlugOK(slug) || domain.SlugReserved(slug) {
		return nil, apierr.NewValidation("invalid slug", fieldErr("slug", "lowercase alnum/dash, 4-40, not reserved"))
	}
	if _, exists, _ := s.repo.GetWorkspaceBySlug(ctx, slug); exists {
		return nil, apierr.NewValidation("slug taken", fieldErr("slug", "already in use"))
	}
	ws, _, err := s.repo.CreateWorkspace(ctx, name, slug, userID)
	if err != nil {
		return nil, err
	}
	return &ws, nil
}

func (s *WorkspaceService) Get(ctx context.Context, wsID, userID string) (*Workspace, error) {
	ws, ok, err := s.repo.GetWorkspace(ctx, wsID)
	if err != nil || !ok {
		return nil, apierr.New(apierr.NotFound, "workspace not found")
	}
	if _, ok, _ := s.repo.GetMember(ctx, wsID, userID); !ok {
		return nil, apierr.New(apierr.NotFound, "workspace not found")
	}
	return &ws, nil
}

func (s *WorkspaceService) Update(ctx context.Context, wsID, actorID, name, slug string) (*Workspace, error) {
	if err := s.requireRole(ctx, wsID, actorID, domain.CanManageMembers); err != nil {
		return nil, err
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug != "" {
		if !domain.SlugOK(slug) || domain.SlugReserved(slug) {
			return nil, apierr.NewValidation("invalid slug")
		}
		if other, ok, _ := s.repo.GetWorkspaceBySlug(ctx, slug); ok && other.ID != wsID {
			return nil, apierr.NewValidation("slug taken")
		}
	}
	ws, err := s.repo.UpdateWorkspace(ctx, wsID, strings.TrimSpace(name), slug)
	return &ws, err
}

func (s *WorkspaceService) Delete(ctx context.Context, wsID, actorID string) error {
	if err := s.requireRole(ctx, wsID, actorID, func(r string) bool { return r == domain.RoleOwner }); err != nil {
		return err
	}
	return s.repo.DeleteWorkspace(ctx, wsID, s.WorkspaceTeardown)
}

func (s *WorkspaceService) ListMembers(ctx context.Context, wsID, actorID string) ([]Membership, error) {
	if err := s.requireMember(ctx, wsID, actorID); err != nil {
		return nil, err
	}
	return s.repo.ListMembers(ctx, wsID)
}

func (s *WorkspaceService) InviteMember(ctx context.Context, wsID, actorID, email, role string) (*Invitation, error) {
	if err := s.requireRole(ctx, wsID, actorID, domain.CanManageMembers); err != nil {
		return nil, err
	}
	email = trimLower(email)
	if !domain.EmailOK(email) {
		return nil, apierr.NewValidation("invalid email")
	}
	if !domain.AssignableRole(role) {
		return nil, apierr.NewValidation("role must be admin|member")
	}
	if dup, _ := s.repo.HasPendingInvitation(ctx, wsID, email); dup {
		return nil, apierr.NewValidation("invitation already pending")
	}
	inv, err := s.repo.CreateInvitation(ctx, wsID, email, role, actorID, domain.InviteExpiry(s.now()))
	if err != nil {
		return nil, err
	}
	if ws, ok, _ := s.repo.GetWorkspace(ctx, wsID); ok {
		actor, _, _ := s.repo.GetUser(ctx, actorID)
		if err := s.email.SendInvitation(ctx, email, ws.Name, actor.DisplayName); err != nil {
			return &inv, apierr.New(apierr.Internal, "invitation created but mail failed")
		}
	}
	return &inv, nil
}

func (s *WorkspaceService) ListWorkspaceInvitations(ctx context.Context, wsID, actorID string) ([]Invitation, error) {
	if err := s.requireRole(ctx, wsID, actorID, domain.CanManageMembers); err != nil {
		return nil, err
	}
	return s.repo.ListWorkspaceInvitations(ctx, wsID)
}

func (s *WorkspaceService) ListMyInvitations(ctx context.Context, p *webx.Principal) ([]Invitation, error) {
	return s.repo.ListMyInvitations(ctx, trimLower(p.Email))
}

func (s *WorkspaceService) AcceptInvitation(ctx context.Context, invID string, p *webx.Principal) (*Member, error) {
	inv, ok, err := s.repo.GetInvitation(ctx, invID)
	if err != nil || !ok || inv.Status != "pending" || s.now().After(inv.ExpiresAt) {
		return nil, apierr.New(apierr.NotFound, "invitation not available")
	}
	if inv.Email != trimLower(p.Email) {
		return nil, apierr.New(apierr.Forbidden, "invitation email mismatch")
	}
	if _, exists, _ := s.repo.GetMember(ctx, inv.WorkspaceID, p.UserID); exists {
		_ = s.repo.SetInvitationStatus(ctx, invID, "accepted")
		return nil, apierr.NewValidation("already a member")
	}
	m, err := s.repo.AddMember(ctx, inv.WorkspaceID, p.UserID, inv.Role)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SetInvitationStatus(ctx, invID, "accepted"); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *WorkspaceService) DeclineInvitation(ctx context.Context, invID string, p *webx.Principal) error {
	inv, ok, err := s.repo.GetInvitation(ctx, invID)
	if err != nil || !ok {
		return apierr.New(apierr.NotFound, "invitation not found")
	}
	if inv.Email != trimLower(p.Email) {
		return apierr.New(apierr.Forbidden, "invitation email mismatch")
	}
	return s.repo.SetInvitationStatus(ctx, invID, "declined")
}

func (s *WorkspaceService) RevokeInvitation(ctx context.Context, wsID, invID, actorID string) error {
	if err := s.requireRole(ctx, wsID, actorID, domain.CanManageMembers); err != nil {
		return err
	}
	return s.repo.DeleteInvitation(ctx, invID)
}

func (s *WorkspaceService) UpdateMemberRole(ctx context.Context, wsID, memberID, newRole, actorID string) (*Member, error) {
	actor, ok, err := s.repo.GetMember(ctx, wsID, actorID)
	if err != nil || !ok || !domain.CanManageMembers(actor.Role) {
		return nil, apierr.New(apierr.Forbidden, "admin required")
	}
	target, ok, err := s.repo.GetMemberByID(ctx, memberID)
	if err != nil || !ok || target.WorkspaceID != wsID {
		return nil, apierr.New(apierr.NotFound, "member not found")
	}
	owners, _ := s.repo.CountOwners(ctx, wsID)
	action := domain.ActionDemote
	if newRole == domain.RoleOwner {
		action = domain.ActionPromote
	}
	if domain.LastOwnerProtected(target.Role, owners <= 1, action) {
		return nil, apierr.NewValidation("cannot demote/remove the last owner")
	}
	if newRole == domain.RoleOwner && actor.Role != domain.RoleOwner {
		return nil, apierr.New(apierr.Forbidden, "only owner can transfer ownership")
	}
	m, err := s.repo.UpdateMemberRole(ctx, memberID, newRole)
	return &m, err
}

func (s *WorkspaceService) RemoveMember(ctx context.Context, wsID, memberID, actorID string) error {
	if err := s.requireRole(ctx, wsID, actorID, domain.CanManageMembers); err != nil {
		return err
	}
	target, ok, err := s.repo.GetMemberByID(ctx, memberID)
	if err != nil || !ok || target.WorkspaceID != wsID {
		return apierr.New(apierr.NotFound, "member not found")
	}
	owners, _ := s.repo.CountOwners(ctx, wsID)
	if domain.LastOwnerProtected(target.Role, owners <= 1, domain.ActionRemove) {
		return apierr.NewValidation("cannot remove the last owner")
	}
	if err := s.repo.RemoveMember(ctx, memberID); err != nil {
		return err
	}
	if err := s.tokens.RevokeAll(ctx, target.UserID); err != nil {
		return err
	}
	if s.UserRevocation != nil {
		_ = s.UserRevocation(ctx, wsID, target.UserID)
	}
	return nil
}

func (s *WorkspaceService) Leave(ctx context.Context, wsID, userID string) error {
	m, ok, err := s.repo.GetMember(ctx, wsID, userID)
	if err != nil || !ok {
		return apierr.New(apierr.NotFound, "not a member")
	}
	owners, _ := s.repo.CountOwners(ctx, wsID)
	if !domain.CanLeave(m.Role, owners <= 1) {
		return apierr.NewValidation("the last owner cannot leave")
	}
	return s.repo.RemoveMember(ctx, m.ID)
}

func (s *WorkspaceService) CreateShareLink(ctx context.Context, wsID, actorID, role string, maxUses int, ttl time.Duration) (*ShareLink, error) {
	if err := s.requireRole(ctx, wsID, actorID, domain.CanManageMembers); err != nil {
		return nil, err
	}
	if !domain.AssignableRole(role) {
		return nil, apierr.NewValidation("role must be admin|member")
	}
	if maxUses < 0 || maxUses > 100000 {
		return nil, apierr.NewValidation("invalid max_uses")
	}
	if ttl <= 0 || ttl > 30*24*time.Hour {
		ttl = 7 * 24 * time.Hour
	}
	code, err := domain.MintShareCode()
	if err != nil {
		return nil, err
	}
	sl, err := s.repo.CreateShareLink(ctx, wsID, code, role, actorID, maxUses, s.now().Add(ttl))
	return &sl, err
}

func (s *WorkspaceService) ListShareLinks(ctx context.Context, wsID, actorID string) ([]ShareLink, error) {
	if err := s.requireRole(ctx, wsID, actorID, domain.CanManageMembers); err != nil {
		return nil, err
	}
	return s.repo.ListShareLinks(ctx, wsID)
}

func (s *WorkspaceService) DeleteShareLink(ctx context.Context, wsID, linkID, actorID string) error {
	if err := s.requireRole(ctx, wsID, actorID, domain.CanManageMembers); err != nil {
		return err
	}
	return s.repo.DeleteShareLink(ctx, linkID)
}

func (s *WorkspaceService) PeekShareLink(ctx context.Context, code string) (*ShareLink, error) {
	if !domain.ShareCodeOK(code) {
		return nil, apierr.New(apierr.NotFound, "share link not found")
	}
	sl, ok, err := s.repo.GetShareLinkByCode(ctx, code)
	if err != nil || !ok || s.now().After(sl.ExpiresAt) {
		return nil, apierr.New(apierr.NotFound, "share link not found")
	}
	sl.Code = code
	return &sl, nil
}

func (s *WorkspaceService) JoinByCode(ctx context.Context, code string, p *webx.Principal) (*Member, error) {
	if !domain.ShareCodeOK(code) {
		return nil, apierr.New(apierr.NotFound, "share link not available")
	}
	sl, ok, err := s.repo.GetShareLinkByCode(ctx, code)
	if err != nil || !ok || s.now().After(sl.ExpiresAt) {
		return nil, apierr.New(apierr.NotFound, "share link not available")
	}
	if _, exists, _ := s.repo.GetMember(ctx, sl.WorkspaceID, p.UserID); exists {
		return nil, apierr.NewValidation("already a member")
	}
	claimed, consumed, err := s.repo.ConsumeShareUse(ctx, code)
	if err != nil || !consumed {
		return nil, apierr.New(apierr.NotFound, "share link not available")
	}
	m, err := s.repo.AddMember(ctx, claimed.WorkspaceID, p.UserID, claimed.Role)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *WorkspaceService) requireMember(ctx context.Context, wsID, userID string) error {
	_, ok, err := s.repo.GetMember(ctx, wsID, userID)
	if err != nil || !ok {
		return apierr.New(apierr.NotFound, "workspace not found")
	}
	return nil
}

func (s *WorkspaceService) requireRole(ctx context.Context, wsID, userID string, can func(string) bool) error {
	m, ok, err := s.repo.GetMember(ctx, wsID, userID)
	if err != nil || !ok || !can(m.Role) {
		return apierr.New(apierr.Forbidden, "insufficient role")
	}
	return nil
}

func trimLower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
