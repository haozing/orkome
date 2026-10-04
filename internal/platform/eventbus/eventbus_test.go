package eventbus

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"orkome/internal/contract/events"
)

type fakeMetrics struct {
	calls []struct {
		name  string
		attrs map[string]string
	}
}

func (f *fakeMetrics) IncrAttrs(name string, attrs map[string]string) {
	f.calls = append(f.calls, struct {
		name  string
		attrs map[string]string
	}{name, attrs})
}

func TestEmitFastPathCounted(t *testing.T) {
	m := &fakeMetrics{}
	b := New(slog.Default())
	b.Metrics = m

	var got []string
	b.On(PhaseCascade, func(_ context.Context, e events.Envelope) { got = append(got, e.Event) })
	b.Emit(context.Background(), events.Envelope{Event: events.TaskQueued, WorkspaceID: "ws1"})
	b.Emit(context.Background(), events.Envelope{Event: events.TaskQueued, WorkspaceID: "ws1"})

	assert.Equal(t, []string{events.TaskQueued, events.TaskQueued}, got)
	assert.Len(t, m.calls, 2, "each Emit counted once (fast path counting, internal archive 07 §5)")
	assert.Equal(t, "event_emitted", m.calls[0].name)
	assert.Equal(t, events.TaskQueued, m.calls[0].attrs["event"], "event names as the property dimension")
}

func TestEmitNilMetricsNoop(t *testing.T) {
	b := New(slog.Default())
	called := false
	b.On(PhaseProject, func(context.Context, events.Envelope) { called = true })
	b.Emit(context.Background(), events.Envelope{Event: events.TaskFailed})
	assert.True(t, called)
}
