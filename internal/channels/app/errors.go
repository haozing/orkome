package app

import "errors"

var (
	ErrInstallationNotFound = errors.New("channels: installation not found")

	ErrDuplicateInbound = errors.New("channels: duplicate inbound message")

	ErrSenderUnbound = errors.New("channels: sender unbound")

	ErrSenderNotMember = errors.New("channels: sender not a workspace member")

	ErrInvokeDenied = errors.New("channels: invoke denied by permission gate")

	ErrLeaseNotAcquired = errors.New("channels: ws lease held elsewhere")

	ErrRouteConflict = errors.New("channels: route insert conflict")

	ErrBindingTokenInvalid = errors.New("channels: binding token invalid")
)

type Outcome string

const (
	OutcomeIngested        Outcome = "ingested"
	OutcomeDropped         Outcome = "dropped"
	OutcomeNeedsBinding    Outcome = "needs_binding"
	OutcomeInvokeDenied    Outcome = "invoke_denied"
	OutcomeChatStarted     Outcome = "chat_started"
	OutcomeContextRotated  Outcome = "context_rotated"
	OutcomeWorkItemCreated Outcome = "work_item_created"
	OutcomeWorkItemUsage   Outcome = "work_item_usage"
)

type DropReason string

const (
	DropInvalidEvent        DropReason = "invalid_event"
	DropRevokedInstallation DropReason = "revoked_installation"
	DropDuplicate           DropReason = "duplicate"
	DropNotAddressedInGroup DropReason = "not_addressed_in_group"
	DropUnboundUser         DropReason = "unbound_user"
	DropNonWorkspaceMember  DropReason = "non_workspace_member"
	DropInvokeDenied        DropReason = "invoke_denied"
)
