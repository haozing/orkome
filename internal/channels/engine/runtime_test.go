package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/channels/wire"
)

type stubReconciler struct{ ran chan struct{} }

func (s *stubReconciler) Run(ctx context.Context) error {
	close(s.ran)
	<-ctx.Done()
	return ctx.Err()
}

type stubChannel struct{}

func (stubChannel) Kind() wire.Kind                   { return "fake" }
func (stubChannel) Connect(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
func (stubChannel) Disconnect(context.Context) error  { return nil }
func (stubChannel) Send(context.Context, wire.OutboundMessage) (wire.SendResult, error) {
	return wire.SendResult{MessageID: "x"}, nil
}
func (stubChannel) Capabilities() wire.Capability { return wire.CapText }

func TestRuntimeAssembly(t *testing.T) {
	registry := wire.NewRegistry()
	registry.Register("fake", func(wire.Config) (wire.Channel, error) { return stubChannel{}, nil })

	rec := &stubReconciler{ran: make(chan struct{})}
	rt := New(Config{
		Reconciler: rec,
		Senders:    nil,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(ctx) }()

	select {
	case <-rec.ran:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciler not driven")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit with ctx")
	}
	rt.Drain()
	require.NotNil(t, rt)
}

func TestRuntimeHandleInboundNilRouter(t *testing.T) {

	rt := New(Config{})
	assert.NotNil(t, rt)
}
