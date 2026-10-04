package app

import (
	"context"
	"encoding/json"
	"time"

	"orkome/internal/channels/wire"
)

type Installation struct {
	ID              string
	WorkspaceID     string
	AgentID         string
	InstallerUserID string
	Kind            wire.Kind
	Name            string
	Status          string
	Fingerprint     string
	Config          json.RawMessage
}

type Route struct {
	ID              string
	InstallationID  string
	WorkspaceID     string
	ExternalChatID  string
	ChatType        wire.ChatType
	ChatSessionID   string
	Generation      int64
	ContextRevision int64
	LastMessageID   string
	LastThreadID    string
	Title           string
	TitleSource     string
}

type DropAudit struct {
	InstallationID    string
	Kind              wire.Kind
	ExternalChatID    string
	EventType         string
	ExternalEventID   string
	ExternalMessageID string
	DropReason        string
}

type MediaIntent struct {
	StorageKey     string
	WorkspaceID    string
	ChatMessageID  string
	StorageURL     string
	InstallationID string
	State          string
	Attempt        int
	TombstonePass  int
	NextAttemptAt  time.Time
	CreatedAt      time.Time
}

type ArmedPendingContext struct {
	SessionID       string
	ContextRevision int64
	Token           string

	WorkspaceID       string
	AgentID           string
	UserID            string
	MediaPending      bool
	BindingID         string
	InstallationID    string
	Kind              wire.Kind
	ExternalChatID    string
	ChatType          wire.ChatType
	ExternalMessageID string
	ExternalThreadID  string
	RouteRevision     int64
}

type TaskDelivery struct {
	TaskID            string
	BindingID         string
	InstallationID    string
	Kind              wire.Kind
	ExternalChatID    string
	ChatType          wire.ChatType
	ExternalMessageID string
	ExternalThreadID  string
	RouteRevision     int64
	ContextRevision   int64
	MediaPending      bool
}

type OutboundRecord struct {
	InstallationID    string
	Kind              wire.Kind
	ExternalMessageID string
	BindingID         string
	RouteRevision     int64
	TaskID            string
	OutboundKind      string
}

type CardMessage struct {
	ChatSessionID         string
	TaskID                string
	Kind                  wire.Kind
	ExternalChatID        string
	ExternalCardMessageID string
	Status                string
}

type Repo interface {
	ResolveInstallation(ctx context.Context, kind wire.Kind, routingKey string) (Installation, error)
	ListActiveInstallations(ctx context.Context) ([]Installation, error)

	ClaimInbound(ctx context.Context, installationID, externalMessageID string) (claimToken string, err error)
	MarkInboundProcessed(ctx context.Context, installationID, externalMessageID, claimToken string) error
	ReleaseInbound(ctx context.Context, installationID, externalMessageID, claimToken string) error
	CleanStaleInbound(ctx context.Context, olderThan time.Time) (int64, error)

	RecordDrop(ctx context.Context, rec DropAudit) error

	ResolveSender(ctx context.Context, installationID, externalUserID string) (userID string, err error)
	IssueBindingToken(ctx context.Context, installationID, externalUserID string, ttl time.Duration) (token string, err error)
	ConsumeBindingToken(ctx context.Context, workspaceID, token, memberUserID string) error

	GetRoute(ctx context.Context, installationID, externalChatID string) (Route, bool, error)
	InsertRoute(ctx context.Context, r Route) (Route, error)
	RotateRoute(ctx context.Context, installationID, externalChatID, newChatSessionID string) (Route, error)
	TouchRouteMessage(ctx context.Context, installationID, externalChatID, externalMessageID, externalThreadID string) error

	CurrentGeneration(ctx context.Context, chatSessionID string) (int64, error)
	NextGeneration(ctx context.Context, chatSessionID, initiatorUserID string) (int64, error)

	ArmPendingRun(ctx context.Context, snap ArmedPendingContext) (token string, err error)
	ClearPendingRun(ctx context.Context, chatSessionID string, revision int64, token string) error
	ListArmedPendingContexts(ctx context.Context) ([]ArmedPendingContext, error)

	InsertMediaIntents(ctx context.Context, intents []MediaIntent) error
	BindMediaToMessage(ctx context.Context, chatMessageID string, storageKeys []string) (bound int, err error)
	ListDueMedia(ctx context.Context, now time.Time, limit int) ([]MediaIntent, error)
	ClaimMediaIntent(ctx context.Context, storageKey, leaseToken string, expiresAt time.Time) (bool, error)
	SettleMediaIntent(ctx context.Context, storageKey, leaseToken string) error
	MarkMediaTombstoned(ctx context.Context, storageKey, leaseToken string, nextPass int, nextAttemptAt time.Time) error
	ReleaseMediaIntent(ctx context.Context, storageKey, leaseToken string, attempt int, nextAttemptAt time.Time, lastErr string) error

	InsertTaskDelivery(ctx context.Context, d TaskDelivery) error
	GetTaskDelivery(ctx context.Context, taskID string) (TaskDelivery, bool, error)
	RecordOutboundMessage(ctx context.Context, rec OutboundRecord) error
	SettleReplyDelivery(ctx context.Context, turnID, taskID, bindingID, installationID string, kind wire.Kind, externalChatID, externalMessageID, reason string) error

	BeginReplyStreaming(ctx context.Context, turnID, taskID, bindingID, installationID string, kind wire.Kind, externalChatID string) (opened bool, err error)

	RecordReplyPlaceholder(ctx context.Context, turnID, externalMessageID string) error

	MarkReplyTerminal(ctx context.Context, turnID, externalMessageID string) (advanced bool, err error)

	InsertCardMessage(ctx context.Context, rec CardMessage) error

	FinishCardMessage(ctx context.Context, taskID string) error

	GetInstallation(ctx context.Context, installationID string) (Installation, error)

	UpdateRouteTitle(ctx context.Context, installationID, externalChatID, title, source string) (updated bool, err error)
}

type LeaseMetrics interface {
	Incr(name string)
	Gauge(name string, v float64)
}

type LeaseStore interface {
	TryAcquire(ctx context.Context, installationID, token string, ttl time.Duration) error

	Renew(ctx context.Context, installationID, token string, ttl time.Duration) error

	Release(ctx context.Context, installationID, token string) error

	ListHeld(ctx context.Context, ids []string) (map[string]struct{}, error)
}

type EnsureSessionParams struct {
	WorkspaceID    string
	AgentID        string
	CreatorUserID  string
	Title          string
	ChatType       wire.ChatType
	ExternalChatID string
}

type AppendParams struct {
	SessionID         string
	UserID            string
	Content           string
	Kind              string
	ContextRevision   int64
	MediaPending      bool
	ExternalMessageID string
}

type AppendResult struct {
	MessageID string
	Seq       int64
}

type ConversationRouter interface {
	EnsureChannelSession(ctx context.Context, p EnsureSessionParams) (sessionID string, err error)

	AppendChannelMessage(ctx context.Context, p AppendParams, markInTx func(context.Context) error) (AppendResult, error)
}

type InvokePolicy interface {
	MemberMayInvokeAgent(ctx context.Context, agentID, userID string) (bool, error)
}

type EnqueueChatRunParams struct {
	WorkspaceID     string
	SessionID       string
	AgentID         string
	UserID          string
	ContextRevision int64
	MediaPending    bool
}

type ChatRunEnqueuer interface {
	EnqueueChannelChatRun(ctx context.Context, p EnqueueChatRunParams) (taskID string, err error)
}

type CreateWorkItemParams struct {
	WorkspaceID   string
	Title         string
	Description   string
	CreatorUserID string
	AgentID       string
	MediaPending  bool
}

type WorkItemCreator interface {
	CreateFromChannel(ctx context.Context, p CreateWorkItemParams) (workItemID string, err error)
}

type MembershipReader interface {
	IsWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error)
}

type OutboundTextGuard interface {
	BreakLinks(s string) string
}

type TaskOutcomeReader interface {
	TaskOutput(ctx context.Context, taskID string) (output string, found bool, err error)
}

type ObjectDeleter interface {
	Delete(ctx context.Context, storageKey string) error
}

type AttachmentReferenceChecker interface {
	ExistsByStorageKey(ctx context.Context, workspaceID, storageKey string) (bool, error)
}

type MediaMaterializedRef struct {
	Kind       string
	StorageKey string
	StorageURL string
	Filename   string
}

type MessageRewriter interface {
	RewriteMediaReady(ctx context.Context, chatMessageID string, refs []MediaMaterializedRef) error
}
