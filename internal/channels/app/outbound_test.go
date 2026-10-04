package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/channels/domain"
	"orkome/internal/channels/wire"
)

type recordingChannel struct {
	mu   sync.Mutex
	sent []wire.OutboundMessage
}

func (c *recordingChannel) Kind() wire.Kind                  { return "fake" }
func (c *recordingChannel) Connect(context.Context) error    { return nil }
func (c *recordingChannel) Disconnect(context.Context) error { return nil }
func (c *recordingChannel) Capabilities() wire.Capability    { return wire.CapText }
func (c *recordingChannel) Send(_ context.Context, out wire.OutboundMessage) (wire.SendResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, out)
	return wire.SendResult{MessageID: "plat-1"}, nil
}

type fakeTaskOutcomes struct {
	outputs map[string]string
}

func (f *fakeTaskOutcomes) TaskOutput(_ context.Context, taskID string) (string, bool, error) {
	out, ok := f.outputs[taskID]
	return out, ok, nil
}

func TestOutboundReplySourceDetermination(t *testing.T) {
	repo := newFakeRepo()
	ch := &recordingChannel{}
	senders := NewSenderRegistry()
	svc := NewOutboundService(repo, senders, fakeGuard{}, &fakeTaskOutcomes{
		outputs: map[string]string{"task-1": "results as above", "task-3": "eyJhbGciOiJIUzI1NiJ9.eyJzZWNyZXQiOiJsZWFrIn0.c2lnbmF0dXJlLWxlYWstY2hlY2s leak check"},
	})

	repo.mu.Lock()
	repo.taskStamps["task-1"] = TaskDelivery{
		TaskID: "task-1", BindingID: "route-1", InstallationID: "inst-1", Kind: "fake",
		ExternalChatID: "chat-1", ChatType: wire.ChatTypeP2P, RouteRevision: 1, ContextRevision: 1,
	}
	repo.taskStamps["task-3"] = TaskDelivery{
		TaskID: "task-3", BindingID: "route-1", InstallationID: "inst-1", Kind: "fake",
		ExternalChatID: "chat-1", ChatType: wire.ChatTypeP2P, RouteRevision: 1, ContextRevision: 1,
	}
	repo.mu.Unlock()

	res, err := svc.DeliverTaskReply(context.Background(), "task-noStamp")
	require.NoError(t, err)
	assert.False(t, res.Sent)
	assert.Equal(t, "not_channel_ingested", res.Reason)

	repo.mu.Lock()
	repo.taskStamps["task-2"] = TaskDelivery{
		TaskID: "task-2", BindingID: "route-1", InstallationID: "inst-1", Kind: "fake",
		ExternalChatID: "chat-1", ChatType: wire.ChatTypeP2P, RouteRevision: 1, ContextRevision: 1,
	}
	repo.mu.Unlock()
	res2, err := svc.DeliverTaskReply(context.Background(), "task-2")
	require.NoError(t, err)
	assert.False(t, res2.Sent)
	assert.Equal(t, "channel_not_connected", res2.Reason)

	senders.Register("inst-1", ch)
	res3, err := svc.DeliverTaskReply(context.Background(), "task-1")
	require.NoError(t, err)
	assert.True(t, res3.Sent)
	ch.mu.Lock()
	require.Len(t, ch.sent, 1)
	got := ch.sent[0]
	ch.mu.Unlock()
	assert.Equal(t, "chat-1", got.ChatID)
	assert.Equal(t, "results as above", got.Text)

	res4, err := svc.DeliverTaskReply(context.Background(), "task-3")
	require.NoError(t, err)
	assert.True(t, res4.Sent)
	ch.mu.Lock()
	sec := ch.sent[1].Text
	ch.mu.Unlock()
	assert.NotContains(t, sec, "eyJhbGciOiJIUzI1NiJ9.eyJzZWNyZXQiOiJsZWFrIn0.c2lnbmF0dXJlLWxlYWstY2hlY2s", "secrets redacted by redactx never leave the domain")

	repo.mu.Lock()
	repo.taskStamps["task-4"] = TaskDelivery{TaskID: "task-4", InstallationID: "inst-1", Kind: "fake", ExternalChatID: "chat-1", RouteRevision: 1}
	repo.mu.Unlock()
	res5, err := svc.DeliverTaskReply(context.Background(), "task-4")
	require.NoError(t, err)
	assert.False(t, res5.Sent)
	assert.Equal(t, "no_output", res5.Reason)

	repo.mu.Lock()
	assert.Len(t, repo.outbound, 2)
	assert.Len(t, repo.settled, 2)
	repo.mu.Unlock()
}

func TestOutboundBreaksLinkAdjacency(t *testing.T) {
	repo := newFakeRepo()
	ch := &recordingChannel{}
	senders := NewSenderRegistry()
	senders.Register("inst-1", ch)
	svc := NewOutboundService(repo, senders, fakeGuard{}, &fakeTaskOutcomes{
		outputs: map[string]string{"task-l": "user says: [click me](https://evil.example) claim the prize"},
	})
	repo.mu.Lock()
	repo.taskStamps["task-l"] = TaskDelivery{TaskID: "task-l", InstallationID: "inst-1", Kind: "fake", ExternalChatID: "chat-1", RouteRevision: 1}
	repo.mu.Unlock()

	_, err := svc.DeliverTaskReply(context.Background(), "task-l")
	require.NoError(t, err)
	ch.mu.Lock()
	text := ch.sent[0].Text
	ch.mu.Unlock()
	assert.NotContains(t, text, "[click me](", `  outbound text must not keep "](" adjacency`)
	assert.Contains(t, text, "[click me] (", `  split into "] ("`)
}

type manualTimer struct{ e any }

func (manualTimer) Stop() bool { return false }

func TestDebounceWindowFencing(t *testing.T) {

	type entry struct{ fn func() }
	var mu sync.Mutex
	var armed []*entry
	b := NewDebouncer(time.Hour)
	b.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
		mu.Lock()
		defer mu.Unlock()
		e := &entry{fn: fn}
		armed = append(armed, e)
		return manualTimer{e}
	}
	fire := func(i int) {
		mu.Lock()
		e := armed[i]
		mu.Unlock()
		e.fn()
	}

	key := domain.DebounceKey{SessionID: "s1", ContextRevision: 1}
	var calls []int
	b.Schedule(key, func() { calls = append(calls, 1) })
	b.Schedule(key, func() { calls = append(calls, 2) })
	require.Len(t, armed, 2)
	fire(0)
	assert.Empty(t, calls, "the replaced old timer must not execute flush")
	fire(1)
	assert.Equal(t, []int{2}, calls, "the latest arming's flush executes exactly once")

	b.Schedule(domain.DebounceKey{SessionID: "s1", ContextRevision: 2}, func() { calls = append(calls, 3) })
	b.Schedule(domain.DebounceKey{SessionID: "s2", ContextRevision: 1}, func() { calls = append(calls, 4) })
	b.FlushAll()
	assert.ElementsMatch(t, []int{3, 4}, calls[1:], "FlushAll drains all pending triggers (map order not guaranteed)")

	before := len(calls)
	b.Schedule(key, func() { calls = append(calls, 5) })
	assert.Equal(t, before+1, len(calls), "Schedule executes inline after terminal state")
}
