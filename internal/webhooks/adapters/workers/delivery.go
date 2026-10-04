package workers

import (
	"context"
	"log/slog"
	"time"

	"orkome/internal/webhooks/app"
)

type DeliveryWorker struct {
	Service *app.Service
	Log     *slog.Logger

	Every time.Duration

	Batch int
}

func (w *DeliveryWorker) Name() string { return "event_delivery" }

func (w *DeliveryWorker) Run(ctx context.Context) error {
	if w.Every <= 0 {
		w.Every = 5 * time.Second
	}
	ticker := time.NewTicker(w.Every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := w.RunOnce(ctx); err != nil {
				w.Log.Error("webhook delivery sweep", "err", err)
			}
		}
	}
}

func (w *DeliveryWorker) RunOnce(ctx context.Context) (int, error) {
	batch, err := w.Service.ClaimBatch(ctx, w.Batch)
	if err != nil {
		return 0, err
	}
	for _, item := range batch {
		sub, ok, err := w.Service.GetSubscriptionForDelivery(ctx, item.SubscriptionID)
		if err != nil || !ok {
			_ = w.Service.DeadUndeliverable(ctx, item.ID)
			continue
		}
		if err := w.Service.Deliver(ctx, item, sub); err != nil {
			w.Log.Error("webhook deliver", "delivery", item.ID, "err", err)
		}
	}
	return len(batch), nil
}
