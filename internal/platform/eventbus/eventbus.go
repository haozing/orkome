package eventbus

import (
	"context"
	"log/slog"
	"sync"

	"orkome/internal/contract/events"
)

type Phase int

const (
	PhaseCascade Phase = iota
	PhaseProject
	PhaseBroadcast
)

type Handler func(ctx context.Context, e events.Envelope)

type subscription struct {
	phase   Phase
	handler Handler
}

type Bus struct {
	mu   sync.RWMutex
	subs []subscription
	log  *slog.Logger

	Metrics interface {
		IncrAttrs(name string, attrs map[string]string)
	}
}

func New(log *slog.Logger) *Bus {
	return &Bus{log: log}
}

func (b *Bus) On(phase Phase, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = append(b.subs, subscription{phase: phase, handler: h})
}

func (b *Bus) Emit(ctx context.Context, e events.Envelope) {
	if b.Metrics != nil {
		b.Metrics.IncrAttrs("event_emitted", map[string]string{"event": e.Event})
	}
	b.mu.RLock()
	subs := make([]subscription, len(b.subs))
	copy(subs, b.subs)
	b.mu.RUnlock()

	for _, phase := range []Phase{PhaseCascade, PhaseProject, PhaseBroadcast} {
		for _, s := range subs {
			if s.phase != phase {
				continue
			}
			b.run(ctx, e, s)
		}
	}
}

func (b *Bus) OnBroadcast(fn func(event string, payload any)) {
	b.On(PhaseBroadcast, func(_ context.Context, e events.Envelope) {
		fn(e.Event, e.Payload)
	})
}

func (b *Bus) run(ctx context.Context, e events.Envelope, s subscription) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("eventbus handler panic", "event", e.Event, "panic", r)
		}
	}()
	s.handler(ctx, e)
}
