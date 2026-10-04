package engine

import (
	"context"
	"log/slog"

	"orkome/internal/channels/app"
	"orkome/internal/channels/wire"
)

type Runtime struct {
	supervisor *app.Supervisor
	router     *app.Router
	reconciler ReconcilerRunner
	senders    *app.SenderRegistry
	logger     *slog.Logger
}

type ReconcilerRunner interface {
	Run(ctx context.Context) error
}

type Config struct {
	Supervisor *app.Supervisor
	Router     *app.Router
	Reconciler ReconcilerRunner
	Senders    *app.SenderRegistry
	Logger     *slog.Logger
}

func New(cfg Config) *Runtime {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Supervisor != nil && cfg.Senders != nil {
		cfg.Supervisor.SetSenderRegistry(cfg.Senders)
	}
	return &Runtime{
		supervisor: cfg.Supervisor,
		router:     cfg.Router,
		reconciler: cfg.Reconciler,
		senders:    cfg.Senders,
		logger:     cfg.Logger,
	}
}

func (r *Runtime) HandleInbound(ctx context.Context, msg wire.InboundMessage) error {
	_, err := r.router.Handle(ctx, msg)
	return err
}

func (r *Runtime) Run(ctx context.Context) error {
	if r.router != nil {
		r.router.RecoverArmedDebounces(ctx)
	}
	errCh := make(chan error, 2)
	go func() {
		if r.supervisor != nil {
			r.supervisor.Run(ctx)
		}
		errCh <- nil
	}()
	go func() {
		if r.reconciler != nil {
			errCh <- r.reconciler.Run(ctx)
			return
		}
		errCh <- nil
	}()
	<-errCh
	return ctx.Err()
}

func (r *Runtime) Drain() {
	if r.supervisor != nil {
		r.supervisor.Wait()
	}
	if r.router != nil {
		r.router.Drain()
	}
}

func (r *Runtime) Outbound() *app.SenderRegistry { return r.senders }
