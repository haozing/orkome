package workers

import (
	"context"
	"log/slog"
	"time"

	"orkome/internal/automation/app"
)

type TickWorker struct {
	Name  string
	Tick  func(ctx context.Context) (int, error)
	Log   *slog.Logger
	Every time.Duration
}

func (w *TickWorker) Run(ctx context.Context) error {
	if w.Every <= 0 {
		w.Every = time.Second
	}
	t := time.NewTicker(w.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if _, err := w.Tick(ctx); err != nil {
				w.Log.Error(w.Name, "err", err)
			}
		}
	}
}

func WakeupTick(svc *app.WakeupService, log *slog.Logger, every time.Duration) *TickWorker {
	return &TickWorker{Name: "wakeup_dispatch", Log: log, Every: every,
		Tick: svc.TickOnce}
}

func AutopilotCron(svc *app.AutopilotService, log *slog.Logger, every time.Duration) *TickWorker {
	return &TickWorker{Name: "autopilot_cron", Log: log, Every: every,
		Tick: svc.CronTickOnce}
}

func WebhookDelivery(svc *app.WebhookService, log *slog.Logger, every time.Duration) *TickWorker {
	return &TickWorker{Name: "automation_webhook_delivery", Log: log, Every: every,
		Tick: func(ctx context.Context) (int, error) {
			n := 0
			for i := 0; i < 20; i++ {
				ok, err := svc.ConsumeOnce(ctx)
				if err != nil {
					return n, err
				}
				if !ok {
					break
				}
				n++
			}
			return n, nil
		}}
}
