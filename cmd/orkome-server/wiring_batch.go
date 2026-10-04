package main

import (
	"context"
	"log/slog"
	"time"

	"orkome/internal/contract/events"
	orchworkers "orkome/internal/orchestration/adapters/workers"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
)

func wiringBatch(log *slog.Logger, bus *eventbus.Bus, batches *orchapp.BatchService,
	wake WakeupBatchPort,
) *orchworkers.BatchWorker {
	batches.SetBatchPorts(batchDoneAdapter{wake: wake}, batchEmitter{bus: bus})
	return &orchworkers.BatchWorker{
		Svc: batches, Registry: map[string]orchapp.BatchProcessor{}, Log: log,
		Every: 2e9, WorkerID: "batch-1",
	}
}

type WakeupBatchPort interface {
	NotifyBatchDone(ctx context.Context, ws, workItemID, batchID string) error
}

type batchDoneAdapter struct{ wake WakeupBatchPort }

func (a batchDoneAdapter) NotifyBatchDone(ctx context.Context, ws, wi, batchID string) error {
	return a.wake.NotifyBatchDone(ctx, ws, wi, batchID)
}

type batchEmitter struct{ bus *eventbus.Bus }

func (e batchEmitter) Emit(ctx context.Context, event string, payload map[string]any) {
	e.bus.Emit(ctx, events.Envelope{Event: event, Payload: payload, OccurredAt: time.Now().UTC()})
}
