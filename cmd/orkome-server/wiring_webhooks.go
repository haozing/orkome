package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/haozing/ploykit/authz"
	"github.com/haozing/ploykit/platform/pg"
	"github.com/haozing/ploykit/platform/sealx"
	webhookhttp "github.com/haozing/ploykit/webhooks/adapters/http"
	webhookpgrepo "github.com/haozing/ploykit/webhooks/adapters/pgrepo"
	webhookworkers "github.com/haozing/ploykit/webhooks/adapters/workers"
	webhookapp "github.com/haozing/ploykit/webhooks/app"
	"orkome/internal/contract/events"
	"orkome/internal/platform/eventbus"
)

// wiringWebhooks builds the framework webhooks domain (ploykit/webhooks):
// webhook_subscription + webhook_delivery tables (framework migrations
// 011/020), EventCatalog seeded with the orkome event vocabulary, and the
// framework delivery worker registered into the workers registry under its
// framework name "webhook_delivery".
//
// egressGuardOf/roleOf-style guards from the old in-tree implementation are
// dropped on purpose: the framework service builds its own egress-guarded
// HTTP client (envAllowsPrivateTarget) and enforces roles through the
// authz.Authorizer (webhooks:read / webhooks:manage).
func wiringWebhooks(
	log *slog.Logger,
	db *pg.DB,
	bus *eventbus.Bus,
	reg *Workers,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
	roleOf func(ctx context.Context, workspaceID, userID string) (string, error),
	az *authz.Authorizer,
	cfg *Config,
) webhookhttp.Deps {
	catalog := orkomeEventCatalog()
	svc := webhookapp.NewWebhookService(webhookpgrepo.New(db.Pool()), catalog, log, time.Now)
	// Webhook signing secrets are sealed at rest (framework refuses plaintext).
	if secrets, err := sealx.NewSecrets(sealx.DeriveKey(cfg.JWTSecret, "webhook")); err == nil {
		svc = svc.WithSecrets(secrets)
	} else {
		log.Error("webhook secrets sealing disabled", "err", err)
	}

	// Fan bus events out to active subscriptions. The catalog gate keeps
	// internal plumbing (notification:*, chat:session_deleted, ...) from ever
	// reaching the delivery table; Emit re-checks membership on the service
	// side as well.
	bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		if e.WorkspaceID == "" || e.EventID == "" || !catalog.Has(e.Event) {
			return
		}
		_, _ = svc.Emit(ctx, e.WorkspaceID, webhookapp.OutboundEvent{
			ID:      e.EventID,
			Type:    e.Event,
			Payload: webhookPayloadMap(e.Payload),
		})
	})

	if reg != nil {
		reg.Add(&webhookworkers.DeliveryWorker{Service: svc, Every: 5 * time.Second, Batch: 20})
	}

	// Framework MemberLookup = membership + role in one call; compose it from
	// the two product lookups main.go already owns.
	lookup := func(ctx context.Context, wsID, userID string) (string, bool, error) {
		ok, err := memberCheck(ctx, wsID, userID)
		if err != nil || !ok {
			return "", false, err
		}
		role, err := roleOf(ctx, wsID, userID)
		if err != nil {
			return "", false, err
		}
		return role, true, nil
	}

	return webhookhttp.Deps{
		Service:     svc,
		MemberCheck: webhookhttp.MemberLookup(lookup),
		Log:         log,
		Authz:       az,
	}
}

// orkomeEventCatalog registers the orkome Envelope event vocabulary
// (internal/contract/events) as subscribable webhook event types. Only the
// dot-namespaced product events are exposed; colon-namespaced events
// (notification:*, chat:session_deleted, work_item:updated_by_agent) are
// internal cross-domain plumbing and stay off the catalog.
func orkomeEventCatalog() *webhookapp.EventCatalog {
	c := webhookapp.NewEventCatalog()
	for _, e := range []struct{ Type, Desc string }{
		// workspace & membership
		{events.WorkspaceCreated, "workspace created"},
		{events.WorkspaceUpdated, "workspace updated"},
		{events.WorkspaceDeleted, "workspace deleted"},
		{events.MemberAdded, "workspace member added"},
		{events.MemberUpdated, "workspace member role updated"},
		{events.MemberRemoved, "workspace member removed"},
		{events.InvitationCreated, "workspace invitation created"},
		{events.InvitationAccepted, "workspace invitation accepted"},
		{events.InvitationDeclined, "workspace invitation declined"},

		// work items, comments, catalog
		{events.WorkItemCreated, "work item created"},
		{events.WorkItemUpdated, "work item updated"},
		{events.WorkItemDeleted, "work item deleted"},
		{events.CommentCreated, "comment created"},
		{events.CommentUpdated, "comment updated"},
		{events.CommentDeleted, "comment deleted"},
		{events.CommentResolved, "comment resolved"},
		{events.LabelCreated, "label created"},
		{events.ProjectCreated, "project created"},

		// orchestration: tasks & batches
		{events.TaskQueued, "agent task queued"},
		{events.TaskDispatch, "agent task dispatched"},
		{events.TaskRunning, "agent task started running"},
		{events.TaskCompleted, "agent task completed"},
		{events.TaskFailed, "agent task failed"},
		{events.TaskCancelled, "agent task cancelled"},
		{events.TaskAwaiting, "agent task awaiting input"},
		{events.TaskResumed, "agent task resumed"},
		{events.TaskMessage, "agent task message appended"},
		{events.BatchCreated, "batch created"},
		{events.BatchProgress, "batch progress updated"},
		{events.BatchCompleted, "batch completed"},
		{events.BatchFailed, "batch failed"},

		// conversation
		{events.ChatDone, "chat turn finished"},
	} {
		c.Register(e.Type, e.Desc)
	}
	return c
}

// webhookPayloadMap normalizes an Envelope payload (typed struct, map, or nil) into
// the map[string]any shape the framework OutboundEvent carries; values that
// are not JSON objects (or nil) marshal to a JSON null payload.
func webhookPayloadMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}
