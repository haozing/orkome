package workers

import (
	"context"
	"log/slog"
	"time"

	"orkome/internal/orchestration/app"
)

type BatchWorker struct {
	Svc      *app.BatchService
	Registry map[string]app.BatchProcessor
	Log      *slog.Logger
	Every    time.Duration
	WorkerID string
	Ticks    int
}

func (w *BatchWorker) Run(ctx context.Context) error {
	if w.Every <= 0 {
		w.Every = 2 * time.Second
	}
	if w.Ticks <= 0 {
		w.Ticks = 4
	}
	ticker := time.NewTicker(w.Every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.RunOnce(ctx)
		}
	}
}

func (w *BatchWorker) RunOnce(ctx context.Context) {
	if n, err := w.Svc.SweepExpiredLeases(ctx); err == nil && n > 0 {
		w.Log.Info("batch: leases reclaimed", "n", n)
	}
	batches, err := w.Svc.ListActive(ctx)
	if err != nil {
		w.Log.Error("batch list", "err", err)
		return
	}
	for _, b := range batches {
		proc, ok := w.Registry[b.Kind]
		if !ok {
			continue
		}
		if n, err := w.Svc.RunWorker(ctx, b, proc, w.WorkerID, w.Ticks); err != nil {
			w.Log.Error("batch run", "batch", b.ID, "err", err)
		} else if n > 0 {
			w.Log.Info("batch processed", "batch", b.ID, "items", n)
		}
	}
}
