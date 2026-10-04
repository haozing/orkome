package main

import (
	"context"
	"log/slog"
	"time"

	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/pg"
	webhookhttp "orkome/internal/webhooks/adapters/http"
	webhookpgrepo "orkome/internal/webhooks/adapters/pgrepo"
	webhookworkers "orkome/internal/webhooks/adapters/workers"
	"orkome/internal/webhooks/app"
)

func wiringWebhooks(log *slog.Logger, db *pg.DB, bus *eventbus.Bus, reg *Workers,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
	roleOf func(ctx context.Context, workspaceID, userID string) (string, error),
	egress app.EgressGuard) webhookhttp.Deps {
	repo := &webhookpgrepo.Repo{DB: db}
	svc := app.New(repo, egress)

	bus.On(eventbus.PhaseProject, svc.HandleEvent)

	if reg != nil {
		reg.Add(&webhookworkers.DeliveryWorker{Service: svc, Log: log, Every: 5 * time.Second, Batch: 20})
	}
	return webhookhttp.Deps{Service: svc, MemberCheck: memberCheck, RoleOf: roleOf}
}
