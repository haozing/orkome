package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/channels/wire"
)

type scriptedChannel struct {
	kind     wire.Kind
	msgs     []wire.InboundMessage
	delivery func(msg wire.InboundMessage)
	caps     wire.Capability
}

func (c *scriptedChannel) Kind() wire.Kind                  { return c.kind }
func (c *scriptedChannel) Capabilities() wire.Capability    { return c.caps }
func (c *scriptedChannel) Disconnect(context.Context) error { return nil }
func (c *scriptedChannel) Send(_ context.Context, out wire.OutboundMessage) (wire.SendResult, error) {
	return wire.SendResult{MessageID: "plat-out-1"}, nil
}

func (c *scriptedChannel) Connect(ctx context.Context) error {
	if c.delivery != nil {
		for _, m := range c.msgs {
			c.delivery(m)
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestFakeChannelEndToEnd(t *testing.T) {
	repo := boundRepo()
	repo.mu.Lock()
	repo.bindings["inst-1|extu-9"] = "user-9"
	repo.mu.Unlock()
	convo := &fakeConvo{}
	runs := &fakeRuns{}
	issues := &fakeIssues{}
	invoke := &fakeInvoke{denied: map[string]bool{"user-9": true}}
	router := NewRouter(repo, convo, invoke, runs, issues, &fakeMembers{}, RouterConfig{})
	handler := func(ctx context.Context, m wire.InboundMessage) error {
		_, err := router.Handle(ctx, m)
		return err
	}

	registry := wire.NewRegistry()
	registry.Register("fake", func(cfg wire.Config) (wire.Channel, error) {
		require.NotNil(t, cfg.Handler, "engine injects the shared inbound callback")
		require.Equal(t, "inst-1", cfg.ID)
		return &scriptedChannel{
			kind: "fake",
			caps: wire.CapText,
			msgs: []wire.InboundMessage{
				p2pMsg("end-to-end first one"),
				func() wire.InboundMessage {
					m := p2pMsg("rejected user")
					m.Source.SenderID = "extu-9"
					return m
				}(),
			},
			delivery: func(m wire.InboundMessage) {

				_ = cfg.Handler(context.Background(), m)
			},
		}, nil
	})

	senders := NewSenderRegistry()
	sup := NewSupervisor(repo, newFakeLease(), registry, handler, fastSupervisorConfig())
	sup.SetSenderRegistry(senders)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); sup.Run(ctx); sup.Wait() }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runs.mu.Lock()
		n := len(runs.calls)
		runs.mu.Unlock()
		repo.mu.Lock()
		audits := len(repo.audits)
		repo.mu.Unlock()
		if n >= 1 && audits >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	require.Equal(t, 1, senders.Len())
	_, ok := senders.Get("inst-1")
	assert.True(t, ok)

	cancel()
	<-done

	runs.mu.Lock()
	require.Len(t, runs.calls, 1, "exactly one run end-to-end (rejected messages produce no run)")
	runs.mu.Unlock()
	require.Len(t, convo.appended, 1, "exactly one message persisted end-to-end (rejection precedes persistence)")

	require.Len(t, repo.audits, 1)
	assert.Equal(t, string(DropInvokeDenied), repo.audits[0].DropReason)
}
