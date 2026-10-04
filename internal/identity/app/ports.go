package app

import (
	"context"
	"time"
)

type Repo interface {
	UpsertUserByEmail(ctx context.Context, email string) (User, error)
	GetUser(ctx context.Context, id string) (User, bool, error)

	GetUserByEmail(ctx context.Context, email string) (User, bool, error)
	UpdateUser(ctx context.Context, id string, name, lang, tz string) (User, error)

	CreateWorkspace(ctx context.Context, name, slug, ownerUserID string) (Workspace, Member, error)
	GetWorkspace(ctx context.Context, id string) (Workspace, bool, error)
	GetWorkspaceBySlug(ctx context.Context, slug string) (Workspace, bool, error)
	UpdateWorkspace(ctx context.Context, id, name, slug string) (Workspace, error)

	DeleteWorkspace(ctx context.Context, id string, teardown WorkspaceTeardown) error
	ListMyWorkspaces(ctx context.Context, userID string) ([]Workspace, error)
	GetMember(ctx context.Context, workspaceID, userID string) (Member, bool, error)
	GetMemberByID(ctx context.Context, memberID string) (Member, bool, error)
	ListMembers(ctx context.Context, workspaceID string) ([]Membership, error)
	UpdateMemberRole(ctx context.Context, memberID, role string) (Member, error)
	RemoveMember(ctx context.Context, memberID string) error
	CountOwners(ctx context.Context, workspaceID string) (int, error)
	AddMember(ctx context.Context, workspaceID, userID, role string) (Member, error)

	ListPins(ctx context.Context, workspaceID, userID string) ([]Pin, error)

	AddPin(ctx context.Context, workspaceID, userID, itemType, itemID string) (pin Pin, created bool, err error)

	ReorderPins(ctx context.Context, workspaceID, userID string, order []string) error

	RemovePin(ctx context.Context, workspaceID, userID, itemType, itemID string) error

	InsertCode(ctx context.Context, email, codeHash string, expiresAt time.Time) error
	LatestPendingCode(ctx context.Context, email string) (PendingCode, bool, error)
	ConsumeCode(ctx context.Context, id string) error
	IncCodeAttempts(ctx context.Context, id string) error

	CreateInvitation(ctx context.Context, workspaceID, email, role, createdBy string, expiresAt time.Time) (Invitation, error)
	HasPendingInvitation(ctx context.Context, workspaceID, email string) (bool, error)
	ListWorkspaceInvitations(ctx context.Context, workspaceID string) ([]Invitation, error)
	ListMyInvitations(ctx context.Context, email string) ([]Invitation, error)
	GetInvitation(ctx context.Context, id string) (Invitation, bool, error)
	SetInvitationStatus(ctx context.Context, id, status string) error
	DeleteInvitation(ctx context.Context, id string) error

	CreateShareLink(ctx context.Context, workspaceID, code, role, createdBy string, maxUses int, expiresAt time.Time) (ShareLink, error)
	ListShareLinks(ctx context.Context, workspaceID string) ([]ShareLink, error)
	GetShareLinkByCode(ctx context.Context, code string) (ShareLink, bool, error)
	DeleteShareLink(ctx context.Context, id string) error

	ConsumeShareUse(ctx context.Context, code string) (ShareLink, bool, error)

	InsertPAT(ctx context.Context, userID, name, tokenHash, prefix string, expiresAt *time.Time) (PAT, error)
	ListPATs(ctx context.Context, userID string) ([]PAT, error)
	RevokePAT(ctx context.Context, id, userID string) error
	RevokeAllPATs(ctx context.Context, userID string) error
	GetPATByHash(ctx context.Context, tokenHash string) (pat PAT, userID string, ok bool, err error)
	TouchPAT(ctx context.Context, id string) error
}

type EmailSender interface {
	SendVerificationCode(ctx context.Context, to, code string) error
	SendInvitation(ctx context.Context, to, workspaceName, inviterName string) error
}

type UserRevocation func(ctx context.Context, workspaceID, userID string) error

type WorkspaceTeardown func(ctx context.Context, workspaceID string) error

type Clock func() time.Time
