package workers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
	"orkome/internal/contract/failuretax"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/outboxx"
)

func TestB3_TerminalEventInOutboxMarkedPublished(t *testing.T) {
	e := newEnv(t)
	recorder := outboxx.NewRecorder(e.db)
	e.settle.SetOutbox(recorder, e.db.Within)
	e.enq.SetOutbox(recorder, e.db.Within)

	user := e.createUser(t, "b3a@example.com")
	ws := e.createWS(t, user, "b3-a")
	wi := e.createWorkItem(t, ws, user, "outbox fast path")
	a := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})
	task, _, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)

	var queued int
	require.NoError(t, e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM event_outbox WHERE event_type='task.queued' AND workspace_id=$1`, ws).Scan(&queued))
	assert.Equal(t, 1, queued, "enqueue event should land in outbox in the same transaction")
	var publishedNil int
	_ = e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM event_outbox WHERE workspace_id=$1 AND published_at IS NULL`, ws).Scan(&publishedNil)
	assert.Equal(t, 0, publishedNil, "after fast path success, all marked published")

	e.SetReply(func(string) (string, int, int, error) { return "conclusion。", 1, 1, nil })
	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	ddone := make(chan struct{})
	go func() { defer close(ddone); d.Run(dctx) }()
	defer func() { cancel(); <-ddone }()
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, task.ID)
		return ok && tt.Status == "completed"
	}, "task should reach completed")

	var completed int
	require.NoError(t, e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM event_outbox WHERE event_type='task.completed' AND workspace_id=$1`, ws).Scan(&completed))
	assert.Equal(t, 1, completed, "terminal events should land in outbox in the same transaction")
	_ = e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM event_outbox WHERE workspace_id=$1 AND published_at IS NULL`, ws).Scan(&publishedNil)
	assert.Equal(t, 0, publishedNil, "still no unpublished rows after terminal state")
}

func TestB3_OutboxCrashReplayExactlyOnce(t *testing.T) {
	e := newEnv(t)
	recorder := outboxx.NewRecorder(e.db)
	user := e.createUser(t, "b3b@example.com")
	ws := e.createWS(t, user, "b3-b")
	wi := e.createWorkItem(t, ws, user, "crash replay")
	a := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})
	task, _, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)

	env := events.Envelope{EventID: "b3-crash-1", Event: events.TaskFailed, WorkspaceID: ws,
		Payload: orchapp.TaskPayload{ID: task.ID, Status: "failed"}}
	_, ok, err := e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, ok)
	require.NoError(t, err)
	require.NoError(t, e.db.Within(ctx0(), func(ctx context.Context) error {
		if _, err := e.repo.StartTask(ctx, task.ID, "", ""); err != nil {
			return err
		}
		if _, err := e.repo.FailRun(ctx, task.ID, failuretax.Timeout, "crash before emit"); err != nil {
			return err
		}
		return recorder.Append(ctx, env)
	}))

	var pending int
	require.NoError(t, e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM event_outbox WHERE id='b3-crash-1' AND published_at IS NULL`).Scan(&pending))
	require.Equal(t, 1, pending)

	var got []events.Envelope
	relay := outboxx.NewRelay(e.db, func(_ context.Context, ev events.Envelope) error {
		got = append(got, ev)
		return nil
	}, logNoop())
	relay.DecodePayload = func(event string, raw []byte) any {
		var p orchapp.TaskPayload
		if event == events.TaskFailed {
			_ = json.Unmarshal(raw, &p)
			return p
		}
		return nil
	}
	n, err := relay.ReplayOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "crashed rows should be redelivered once")
	require.Len(t, got, 1)
	assert.Equal(t, "b3-crash-1", got[0].EventID)
	tp, ok := got[0].Payload.(orchapp.TaskPayload)
	require.True(t, ok, "replay payloads should restore strong types")
	assert.Equal(t, task.ID, tp.ID)

	n, err = relay.ReplayOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 0, n, "published rows not delivered twice")
}

func TestB3_OutboxRetryOnHandlerFailure(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b3c@example.com")
	ws := e.createWS(t, user, "b3-c")

	_, err := e.pool.Exec(ctx0(), `
INSERT INTO event_outbox (id, workspace_id, event_type, payload)
VALUES ('b3-retry-1', $1, 'task.cancelled', '{}')`, ws)
	require.NoError(t, err)

	fail := true
	relay := outboxx.NewRelay(e.db, func(context.Context, events.Envelope) error {
		if fail {
			return errSimulated
		}
		return nil
	}, logNoop())
	_, _ = relay.ReplayOnce(ctx0())
	var published bool
	_ = e.pool.QueryRow(ctx0(),
		`SELECT published_at IS NOT NULL FROM event_outbox WHERE id='b3-retry-1'`).Scan(&published)
	assert.False(t, published, "handler failure marks nothing published")

	n, err := relay.ReplayOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 0, n, "no retry inside the backoff window")

	_, _ = e.pool.Exec(ctx0(), `UPDATE event_outbox SET next_retry_at=now() WHERE id='b3-retry-1'`)
	fail = false
	n, err = relay.ReplayOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_ = e.pool.QueryRow(ctx0(),
		`SELECT published_at IS NOT NULL FROM event_outbox WHERE id='b3-retry-1'`).Scan(&published)
	assert.True(t, published)
}

var errSimulated = &simErr{}

type simErr struct{}

func (*simErr) Error() string { return "simulated consumer failure" }
