package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/outboxx"
	workhttp "orkome/internal/work/adapters/http"
	"orkome/internal/work/adapters/pgrepo"
	workworkers "orkome/internal/work/adapters/workers"
	"orkome/internal/work/app"
)

func wiringWork(log *slog.Logger, db *pg.DB, bus *eventbus.Bus, reg *Workers, memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error), jwtSecret string) workhttp.Deps {
	repo := &pgrepo.Repo{DB: db}

	orchRepo := orchpgrepo.New(db)
	bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		var wiID, actorType, actorID, kind string
		var payload any
		switch e.Event {
		case events.WorkItemCreated, events.WorkItemUpdated, events.WorkItemDeleted:
			p, _ := e.Payload.(events.WorkItemPayload)
			wiID, actorType, actorID, kind = p.ID, "user", e.ActorUserID, e.Event
			payload = p
		case events.TaskCompleted, events.TaskFailed:
			p, _ := e.Payload.(orchapp.TaskPayload)
			taskID, wsID := p.ID, e.WorkspaceID
			if taskID == "" || wsID == "" {
				return
			}
			t, ok, err := orchRepo.GetTask(ctx, wsID, taskID)
			if err != nil || !ok || t.WorkItemID == "" {
				return
			}
			outcome := "completed"
			if e.Event == events.TaskFailed {
				outcome = "failed"
			}
			agentName := ""
			if a, ok, err := orchRepo.GetAgent(ctx, wsID, t.AgentID); err == nil && ok {
				agentName = a.Name
			}
			ap := map[string]any{
				"task_id": t.ID, "outcome": outcome, "summary": t.Summary,
				"agent_name": agentName, "attempt": t.Attempt,
			}
			if outcome == "failed" {
				ap["failure_reason"] = t.FailureReason
			}
			eventID := "task_settled:" + t.ID
			_ = repo.AppendActivity(ctx, eventID, wsID, t.WorkItemID, "agent", t.AgentID, "task_settled", ap)
			return
		default:
			return
		}
		eventID := e.Event + ":" + wiID + ":" + e.OccurredAt.UTC().Format("20060102T150405.000000000")
		_ = repo.AppendActivity(ctx, eventID, e.WorkspaceID, wiID, actorType, actorID, kind, payloadMap(payload))
	})

	sourceCtx := app.NewSourceContextService(repo, bus, nil)
	sourceCtx.Sign = func(msg string) string {
		mac := hmac.New(sha256.New, []byte(jwtSecret))
		mac.Write([]byte(msg))
		return hex.EncodeToString(mac.Sum(nil))
	}

	if reg != nil {
		reg.Add(&workworkers.SearchIndexCatchUp{Repo: repo, Every: 10 * time.Minute, Grace: 24 * time.Hour, BatchLimit: 500, Log: log})
		reg.Add(&workworkers.PruneMarkSweeper{Repo: repo, Every: time.Hour, Retention: 7 * 24 * time.Hour, BatchLimit: 500, Log: log})
	}

	wiSvc := app.NewWorkItemService(repo, bus, nil)
	cmSvc := app.NewCommentService(repo, bus, nil)

	rec := outboxx.NewRecorder(db)
	wiSvc.SetOutbox(rec, db.Within)
	cmSvc.SetOutbox(rec, db.Within)
	sourceCtx.SetOutbox(rec, db.Within)
	deps := workhttp.Deps{
		WorkItems: wiSvc,
		Comments:  cmSvc,
		SourceCtx: sourceCtx,
		Catalog: func() *app.CatalogService {
			c := app.NewCatalogService(repo, bus)
			c.SetOutbox(rec, db.Within)
			return c
		}(),
		Views:       app.NewViewService(repo),
		Search:      app.NewSearchService(repo),
		Timeline:    repo.ListTimeline,
		Subscribers: repo.ListSubscribers,
		MemberCheck: memberCheck,

		ClientUsage: app.NewClientUsageService(repo),
	}
	return deps
}

func retrySourceContext(db *pg.DB, sc *app.SourceContextService) func(ctx context.Context, workspaceID, taskID, userID string) (any, error) {
	return func(ctx context.Context, ws, taskID, userID string) (any, error) {
		var digest string
		err := db.Tx(ctx).QueryRow(ctx,
			`SELECT coalesce(context->>'source_context_digest','') FROM agent_task_queue WHERE workspace_id=$1 AND id=$2`,
			ws, taskID).Scan(&digest)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && digest == "") {
			return nil, apierr.New(apierr.NotFound, "task carries no source context")
		}
		if err != nil {
			return nil, err
		}
		return sc.CreateFromDigest(ctx, ws, digest, userID, "")
	}
}

func payloadMap(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return map[string]any{"data": v}
}
