package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"orkome/internal/channels/domain"
	"orkome/internal/channels/wire"
)

const BindingTokenTTL = 15 * time.Minute

type Router struct {
	repo      Repo
	sessions  *ChannelSession
	invoke    InvokePolicy
	runs      ChatRunEnqueuer
	issues    WorkItemCreator
	members   MembershipReader
	debouncer *Debouncer
	logger    *slog.Logger
}

type RouterConfig struct {
	DebounceWindow time.Duration
	Logger         *slog.Logger
}

func NewRouter(
	repo Repo,
	convo ConversationRouter,
	invoke InvokePolicy,
	runs ChatRunEnqueuer,
	issues WorkItemCreator,
	members MembershipReader,
	cfg RouterConfig,
) *Router {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	r := &Router{
		repo:     repo,
		sessions: NewChannelSession(repo, convo),
		invoke:   invoke,
		runs:     runs,
		issues:   issues,
		members:  members,
		logger:   cfg.Logger,
	}
	if cfg.DebounceWindow > 0 {
		r.debouncer = NewDebouncer(cfg.DebounceWindow)
	}
	return r
}

func (r *Router) Drain() {
	if r.debouncer != nil {
		r.debouncer.FlushAll()
	}
}

type Result struct {
	Outcome         Outcome
	DropReason      DropReason
	InstallationID  string
	WorkspaceID     string
	Sender          string
	UserID          string
	SessionID       string
	RouteRevision   int64
	ContextRevision int64
	WorkItemID      string
	BindingToken    string
}

func (r *Router) Handle(ctx context.Context, msg wire.InboundMessage) (Result, error) {
	if msg.CommandText == "" {
		msg.CommandText = msg.Text
	}

	inst, err := r.repo.ResolveInstallation(ctx, msg.Source.Kind, msg.Source.RoutingKey)
	if err != nil {
		if errors.Is(err, ErrInstallationNotFound) {
			return r.drop(ctx, msg, "", DropInvalidEvent), nil
		}
		return Result{}, err
	}
	if inst.Status != "active" {
		return r.drop(ctx, msg, inst.ID, DropRevokedInstallation), nil
	}

	claimToken := ""
	claimed := false
	if msg.MessageID != "" {
		claimToken, err = r.repo.ClaimInbound(ctx, inst.ID, msg.MessageID)
		if err != nil {
			if errors.Is(err, ErrDuplicateInbound) {
				return r.drop(ctx, msg, inst.ID, DropDuplicate), nil
			}
			return Result{}, err
		}
		claimed = true
	}

	res, finalize, err := r.processClaimed(ctx, inst, msg, claimToken)
	if claimed && err == nil && finalize {

		mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		_ = r.repo.MarkInboundProcessed(mctx, inst.ID, msg.MessageID, claimToken)
		cancel()
	}
	if claimed && err != nil && msg.MessageID != "" {

		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		_ = r.repo.ReleaseInbound(rctx, inst.ID, msg.MessageID, claimToken)
		cancel()
	}
	return res, err
}

func (r *Router) processClaimed(ctx context.Context, inst Installation, msg wire.InboundMessage, claimToken string) (Result, bool, error) {

	if msg.Source.ChatType == wire.ChatTypeGroup && !msg.AddressedToBot {
		return r.drop(ctx, msg, inst.ID, DropNotAddressedInGroup), true, nil
	}

	userID, err := r.repo.ResolveSender(ctx, inst.ID, msg.Source.SenderID)
	if err != nil {
		switch {
		case errors.Is(err, ErrSenderUnbound):
			res := r.drop(ctx, msg, inst.ID, DropUnboundUser)
			res.Outcome = OutcomeNeedsBinding

			if token, terr := r.repo.IssueBindingToken(ctx, inst.ID, msg.Source.SenderID, BindingTokenTTL); terr == nil {
				res.BindingToken = token
			}
			return res, true, nil
		case errors.Is(err, ErrSenderNotMember):
			return r.drop(ctx, msg, inst.ID, DropNonWorkspaceMember), true, nil
		default:
			return Result{}, false, err
		}
	}
	if r.members != nil {
		member, err := r.members.IsWorkspaceMember(ctx, inst.WorkspaceID, userID)
		if err != nil {
			return Result{}, false, err
		}
		if !member {
			return r.drop(ctx, msg, inst.ID, DropNonWorkspaceMember), true, nil
		}
	}

	if r.invoke == nil {
		return r.drop(ctx, msg, inst.ID, DropInvokeDenied), true, nil
	}
	allowed, err := r.invoke.MemberMayInvokeAgent(ctx, inst.AgentID, userID)
	if err != nil {

		return Result{}, false, err
	}
	if !allowed {
		res := Result{
			Outcome: OutcomeInvokeDenied, DropReason: DropInvokeDenied,
			InstallationID: inst.ID, WorkspaceID: inst.WorkspaceID,
			Sender: msg.Source.SenderID, UserID: userID,
		}
		_ = r.repo.RecordDrop(ctx, DropAudit{
			InstallationID: inst.ID, Kind: msg.Source.Kind,
			ExternalChatID: msg.Source.ChatID, EventType: eventTypeOf(msg),
			ExternalEventID: msg.EventID, ExternalMessageID: msg.MessageID,
			DropReason: string(DropInvokeDenied),
		})
		return res, true, nil
	}

	creator := userID
	if msg.Source.ChatType == wire.ChatTypeGroup {
		creator = inst.InstallerUserID
	}
	control := domain.ParseControlCommand(msg.CommandText)
	workItemCmd, isWorkItemCmd := domain.ParseWorkItemCommand(msg.CommandText)
	hasMedia := msg.Type != wire.MsgTypeText || len(msg.MediaRefs) > 0

	var route Route
	if control.Kind == domain.ControlCommandNewChat {

		route, err = r.sessions.Rotate(ctx, inst, msg, creator, control.Body)
		if err != nil {
			return Result{}, false, err
		}
	} else {
		route, err = r.sessions.Ensure(ctx, inst, msg, creator)
		if err != nil {
			return Result{}, false, err
		}
	}

	_, err = r.sessions.Append(ctx, route, msg, userID, inst.ID, claimToken, hasMedia)
	if err != nil {
		return Result{}, false, err
	}

	_ = r.repo.TouchRouteMessage(ctx, inst.ID, msg.Source.ChatID, msg.MessageID, msg.Source.ThreadID)

	res := Result{
		Outcome:         OutcomeIngested,
		InstallationID:  inst.ID,
		WorkspaceID:     inst.WorkspaceID,
		Sender:          msg.Source.SenderID,
		UserID:          userID,
		SessionID:       route.ChatSessionID,
		RouteRevision:   route.Generation,
		ContextRevision: route.ContextRevision,
	}

	switch {
	case control.Kind == domain.ControlCommandNewChat:
		res.Outcome = OutcomeChatStarted
		return res, false, nil
	case control.Kind == domain.ControlCommandFreshSession:

		rev, err := r.sessions.NextContextGeneration(ctx, route.ChatSessionID, userID)
		if err != nil {
			return Result{}, false, err
		}
		res.ContextRevision = rev
		res.Outcome = OutcomeContextRotated

		if control.Body != "" {
			r.scheduleRun(inst, route, userID, rev, hasMedia, msg)
		}
		return res, false, nil
	case isWorkItemCmd:
		return r.handleWorkItemCommand(ctx, inst, route, msg, userID, workItemCmd, hasMedia, res)
	}

	r.scheduleRun(inst, route, userID, route.ContextRevision, hasMedia, msg)
	return res, false, nil
}

func (r *Router) handleWorkItemCommand(ctx context.Context, inst Installation, route Route, msg wire.InboundMessage, userID string, cmd domain.WorkItemCommand, hasMedia bool, res Result) (Result, bool, error) {
	if cmd.Title == "" {
		res.Outcome = OutcomeWorkItemUsage
		return res, false, nil
	}
	if r.issues == nil {
		r.logger.Error("channels router: work item creator port not wired", "installation_id", inst.ID)
		return Result{}, false, errors.New("channels: work item creator port not wired")
	}
	workItemID, err := r.issues.CreateFromChannel(ctx, CreateWorkItemParams{
		WorkspaceID:   inst.WorkspaceID,
		Title:         cmd.Title,
		Description:   cmd.Description,
		CreatorUserID: userID,
		AgentID:       inst.AgentID,
		MediaPending:  hasMedia,
	})
	if err != nil {
		return Result{}, false, err
	}
	res.Outcome = OutcomeWorkItemCreated
	res.WorkItemID = workItemID
	return res, false, nil
}

func (r *Router) scheduleRun(inst Installation, route Route, userID string, contextRevision int64, mediaPending bool, msg wire.InboundMessage) {
	snap := ArmedPendingContext{
		SessionID:         route.ChatSessionID,
		ContextRevision:   contextRevision,
		WorkspaceID:       inst.WorkspaceID,
		AgentID:           inst.AgentID,
		UserID:            userID,
		MediaPending:      mediaPending,
		BindingID:         route.ID,
		InstallationID:    inst.ID,
		Kind:              inst.Kind,
		ExternalChatID:    route.ExternalChatID,
		ChatType:          route.ChatType,
		ExternalMessageID: msg.MessageID,
		ExternalThreadID:  msg.Source.ThreadID,
		RouteRevision:     route.Generation,
	}
	if r.debouncer == nil {
		r.flushArmedRun(snap)
		return
	}

	actx, cancel := context.WithTimeout(context.Background(), runArmTimeout)
	token, err := r.repo.ArmPendingRun(actx, snap)
	cancel()
	if err != nil {

		r.logger.Error("channels router: arm pending run failed",
			"session_id", snap.SessionID, "revision", snap.ContextRevision, "error", err)
	}
	snap.Token = token
	key := domain.DebounceKey{SessionID: route.ChatSessionID, ContextRevision: contextRevision}
	r.debouncer.Schedule(key, func() { r.flushArmedRun(snap) })
}

func (r *Router) RecoverArmedDebounces(ctx context.Context) {
	armed, err := r.repo.ListArmedPendingContexts(ctx)
	if err != nil {
		r.logger.Error("channels router: list armed pending contexts failed", "error", err)
		return
	}
	for _, snap := range armed {
		flush := func() { r.flushArmedRun(snap) }
		if r.debouncer == nil {
			flush()
			continue
		}
		key := domain.DebounceKey{SessionID: snap.SessionID, ContextRevision: snap.ContextRevision}
		r.debouncer.ScheduleIfAbsent(key, flush)
	}
	if len(armed) > 0 {
		r.logger.Info("channels router: recovered armed debounce windows",
			"count", len(armed))
	}
}

func (r *Router) flushArmedRun(snap ArmedPendingContext) {
	ctx, cancel := context.WithTimeout(context.Background(), runFlushTimeout)
	defer cancel()
	taskID, err := r.runs.EnqueueChannelChatRun(ctx, EnqueueChatRunParams{
		WorkspaceID:     snap.WorkspaceID,
		SessionID:       snap.SessionID,
		AgentID:         snap.AgentID,
		UserID:          snap.UserID,
		ContextRevision: snap.ContextRevision,
		MediaPending:    snap.MediaPending,
	})
	if err != nil {
		r.logger.Error("channels router: enqueue channel chat run failed",
			"session_id", snap.SessionID, "error", err)
		return
	}

	_ = r.repo.InsertTaskDelivery(ctx, TaskDelivery{
		TaskID:            taskID,
		BindingID:         snap.BindingID,
		InstallationID:    snap.InstallationID,
		Kind:              snap.Kind,
		ExternalChatID:    snap.ExternalChatID,
		ChatType:          snap.ChatType,
		ExternalMessageID: snap.ExternalMessageID,
		ExternalThreadID:  snap.ExternalThreadID,
		RouteRevision:     snap.RouteRevision,
		ContextRevision:   snap.ContextRevision,
		MediaPending:      snap.MediaPending,
	})
	if snap.Token != "" {
		_ = r.repo.ClearPendingRun(ctx, snap.SessionID, snap.ContextRevision, snap.Token)
	}
}

const (
	runFlushTimeout = 10 * time.Second
	runArmTimeout   = 3 * time.Second
)

func (r *Router) drop(ctx context.Context, msg wire.InboundMessage, instID string, reason DropReason) Result {
	_ = r.repo.RecordDrop(ctx, DropAudit{
		InstallationID:    instID,
		Kind:              msg.Source.Kind,
		ExternalChatID:    msg.Source.ChatID,
		EventType:         eventTypeOf(msg),
		ExternalEventID:   msg.EventID,
		ExternalMessageID: msg.MessageID,
		DropReason:        string(reason),
	})
	return Result{Outcome: OutcomeDropped, DropReason: reason, InstallationID: instID, Sender: msg.Source.SenderID}
}

func eventTypeOf(msg wire.InboundMessage) string {
	if msg.EventID != "" {
		return "event"
	}
	return "message"
}
