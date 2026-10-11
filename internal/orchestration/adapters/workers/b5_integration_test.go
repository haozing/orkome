package workers

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fwwswire "github.com/haozing/ploykit/platform/wswire"
	"orkome/internal/contract/events"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
)

func TestB5_ChatDoneInlineFrame(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b5@example.com")
	ws := e.createWS(t, user, "b5-w")
	a := e.createAgent(t, ws, "b5-chatter")
	sess, err := e.conv.CreateSession(ctx0(), ws, a.ID, user, "inline frame")
	require.NoError(t, err)

	var mu sync.Mutex
	var seen []events.Envelope
	e.bus.On(eventbus.PhaseBroadcast, func(_ context.Context, ev events.Envelope) {
		mu.Lock()
		seen = append(seen, ev)
		mu.Unlock()
	})

	e.SetReply(func(string) (string, int, int, error) { return "Inline reply.", 1, 1, nil })
	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	ddone := make(chan struct{})
	go func() { defer close(ddone); d.Run(dctx) }()
	defer func() { cancel(); <-ddone }()

	_, taskID, err := e.conv.SendMessage(ctx0(), ws, sess.ID, user, "ask")
	require.NoError(t, err)

	waitFor(t, func() bool {
		ms, _ := e.listMessages(sess.ID)
		for _, m := range ms {
			if m.Role == "assistant" && m.Content == "Inline reply." {
				return true
			}
		}
		return false
	}, "assistant rows should be persisted")

	var done *orchapp.ChatDoneFrame
	mu.Lock()
	for i := range seen {
		if seen[i].Event == events.ChatDone {
			p, ok := seen[i].Payload.(orchapp.ChatDoneFrame)
			if ok && done == nil {
				cp := p
				done = &cp
			} else if ok {
				t.Fatal("chat:done should emit exactly one")
			}
		}
	}
	mu.Unlock()
	require.NotNil(t, done, "a chat:done event should be emitted")
	assert.Equal(t, sess.ID, done.SessionID)
	assert.Equal(t, "Inline reply.", done.Content)
	assert.Equal(t, "assistant", done.Role)
	assert.Equal(t, "message", done.Kind)

	assert.Equal(t, "chat:done", fwwswire.WSName(events.ChatDone))
	_ = taskID
}
