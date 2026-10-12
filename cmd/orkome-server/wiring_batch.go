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
	e.bus.Emit(ctx, events.Envelope{
		Event: event,
		// FP-20 F3：batch 帧此前不带 WorkspaceID，被 wiring_ws 的广播门禁
		// （空 WorkspaceID 静默 return）整族丢弃——batch.created/progress/
		// completed/failed 四类帧全灭。工作区 ID 由 app 层从 batch 实体放进
		// payload（orchapp/batch.go 三个 Emit 位点）。
		WorkspaceID: payloadString(payload, "workspace_id"),
		Payload:     payload,
		OccurredAt:  time.Now().UTC(),
	})
}

func payloadString(payload map[string]any, key string) string {
	if v, ok := payload[key].(string); ok {
		return v
	}
	return ""
}
