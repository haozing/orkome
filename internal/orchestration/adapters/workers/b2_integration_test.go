package workers

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/failuretax"
	"orkome/internal/orchestration/adapters/engine"
	orchapp "orkome/internal/orchestration/app"
)

func TestB2_TaskMessageSummaryRows(t *testing.T) {
	e := newEnv(t)
	e.upgradeEngine()
	e.engine.inner.TaskMessages = e.repo

	user := e.createUser(t, "b2msg@example.com")
	ws := e.createWS(t, user, "b2-msg")
	wi := e.createWorkItem(t, ws, user, "approval flow")
	a := e.createAgent(t, ws, "b2-approver")
	task, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)

	e.setToolsScript(func(hist []*schema.Message) (engine.FakeStep, schema.TokenUsage, error) {
		for _, h := range hist {
			if h.Role == schema.Tool {
				return engine.FakeStep{Text: "Final conclusion after the approval."}, schema.TokenUsage{PromptTokens: 5, CompletionTokens: 5}, nil
			}
		}
		return engine.FakeStep{ToolName: "request_approval",
				ToolArgs: `{"action":"delete_work_item","detail":"clean up stale items"}`},
			schema.TokenUsage{PromptTokens: 5, CompletionTokens: 5}, nil
	})

	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	ddone := make(chan struct{})
	go func() { defer close(ddone); d.Run(dctx) }()
	defer func() { cancel(); <-ddone }()

	waitFor(t, func() bool {
		var since *time.Time
		_ = e.pool.QueryRow(ctx0(),
			`SELECT awaiting_input_since FROM agent_task_queue WHERE id=$1`, task.ID).Scan(&since)
		return since != nil
	}, "task should be parked")

	msgs, err := e.repo.ListTaskMessages(ctx0(), ws, task.ID, orchapp.TaskMessagesQuery{Limit: 200})
	require.NoError(t, err)
	kinds := map[string]bool{}
	for _, m := range msgs {
		kinds[m.Kind] = true
	}
	assert.True(t, kinds["tool_use"], "tool_use row should land (call_id paired), got %+v", msgs)
	assert.True(t, kinds["approval"], "approval row should land (internal archive 09 §10 interrupt payload)")

	require.NoError(t, e.settle.Approve(ctx0(), ws, task.ID, user, true, "ok"))

	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, task.ID)
		return ok && tt.Status == "completed"
	}, "should reach completed after recovery")

	msgs, err = e.repo.ListTaskMessages(ctx0(), ws, task.ID, orchapp.TaskMessagesQuery{Limit: 200})
	require.NoError(t, err)
	kinds = map[string]bool{}
	for _, m := range msgs {
		kinds[m.Kind] = true
	}
	assert.True(t, kinds["tool_result"], "recovery segment should land a tool_result row")
	assert.True(t, kinds["text"], "final draft should land a text row")

	var last int64
	for i, m := range msgs {
		require.EqualValues(t, i+1, m.Seq, "seq should be monotonically continuous within a task")
		assert.GreaterOrEqual(t, m.Seq, last)
		last = m.Seq
	}

	var callID string
	for _, m := range msgs {
		if m.Kind == "tool_use" {
			callID = m.CallID
		}
	}
	assert.NotEmpty(t, callID, "tool_use rows should carry call_id")
	paired := false
	for _, m := range msgs {
		if m.Kind == "tool_result" && m.CallID == callID {
			paired = true
		}
	}
	assert.True(t, paired, "tool_result should pair with tool_use by call_id")
}

func TestB2_LivenessSweeper(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b2lv@example.com")
	ws := e.createWS(t, user, "b2-lv")
	wi := e.createWorkItem(t, ws, user, "sweep")
	a := e.createAgent(t, ws, "b2-sweep")
	tDead, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)

	_, err := e.pool.Exec(ctx0(), `
INSERT INTO agent_runtime (instance_id, engine, last_seen_at)
VALUES ('dead-inst', 'eino', now() - interval '10 minutes'),
       ('live-inst', 'eino', now())`)
	require.NoError(t, err)

	_, ok, _ := e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, ok)
	_, err = e.repo.StartTask(ctx0(), tDead.ID, "dead-inst", "")
	require.NoError(t, err)

	wi2 := e.createWorkItem(t, ws, user, "parked exemption")
	a2 := e.createAgent(t, ws, "b2-parked")
	tPark, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi2, a2.ID, user)
	_, ok, _ = e.repo.ClaimNext(ctx0(), a2.ID, 6)
	require.True(t, ok)
	_, err = e.repo.StartTask(ctx0(), tPark.ID, "dead-inst", "")
	require.NoError(t, err)
	require.NoError(t, e.repo.ParkTask(ctx0(), tPark.ID, orchapp.InterruptInfo{
		TargetID: "x", Kind: "approval", Payload: "{}"}))

	wi3 := e.createWorkItem(t, ws, user, "live replica")
	a3 := e.createAgent(t, ws, "b2-live")
	tLive, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi3, a3.ID, user)
	_, ok, _ = e.repo.ClaimNext(ctx0(), a3.ID, 6)
	require.True(t, ok)
	_, err = e.repo.StartTask(ctx0(), tLive.ID, "live-inst", "")
	require.NoError(t, err)

	s := &LivenessSweeper{Repo: e.repo, Settle: e.settle, Log: logNoop()}
	n, err := s.RunOnce(ctx0())
	require.NoError(t, err)
	assert.EqualValues(t, 1, n, "only dead-replica running tasks death-verdicted")

	dead, _, _ := e.repo.GetTask(ctx0(), ws, tDead.ID)
	assert.Equal(t, "failed", dead.Status)
	assert.Equal(t, failuretax.RuntimeRecovery, dead.FailureReason)
	var queuedRetry int
	_ = e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM agent_task_queue WHERE retry_of_task_id=$1 AND status='queued'`, tDead.ID).Scan(&queuedRetry)
	assert.Equal(t, 1, queuedRetry, "runtime_recovery retryable → subtask enqueued")

	park, _, _ := e.repo.GetTask(ctx0(), ws, tPark.ID)
	assert.Equal(t, "running", park.Status, "parked tasks exempt from heartbeat verdicts")
	live, _, _ := e.repo.GetTask(ctx0(), ws, tLive.ID)
	assert.Equal(t, "running", live.Status, "live replica tasks not death-verdicted")
}

func TestB2_DispatcherRegistersAndAttributes(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b2hb@example.com")
	ws := e.createWS(t, user, "b2-hb")
	wi := e.createWorkItem(t, ws, user, "heartbeat")
	a := e.createAgent(t, ws, "b2-hb")

	e.setToolsScript(func(hist []*schema.Message) (engine.FakeStep, schema.TokenUsage, error) {
		time.Sleep(300 * time.Millisecond)
		return engine.FakeStep{Text: "slow conclusion."}, schema.TokenUsage{}, nil
	})
	_, _, _ = e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)

	const inst = "b2-inst-1"
	d := e.newDispatcher()
	d.InstanceID = inst
	d.HeartbeatEvery = 50 * time.Millisecond
	dctx, cancel := context.WithCancel(ctx0())
	ddone := make(chan struct{})
	go func() { defer close(ddone); d.Run(dctx) }()
	defer func() { cancel(); <-ddone }()

	waitFor(t, func() bool {
		var n int
		_ = e.pool.QueryRow(ctx0(),
			`SELECT count(*) FROM agent_runtime WHERE instance_id=$1`, inst).Scan(&n)
		return n == 1
	}, "replicas should register (agent_runtime row)")

	waitFor(t, func() bool {
		var rt string
		var st string
		_ = e.pool.QueryRow(ctx0(),
			`SELECT runtime_id, status FROM agent_task_queue WHERE work_item_id=$1`, wi).Scan(&rt, &st)
		return rt == inst && st == "running"
	}, "running tasks should belong to the claiming replica (runtime_id)")

	var seen time.Time
	_ = e.pool.QueryRow(ctx0(),
		`SELECT last_seen_at FROM agent_runtime WHERE instance_id=$1`, inst).Scan(&seen)
	waitFor(t, func() bool {
		var again time.Time
		_ = e.pool.QueryRow(ctx0(),
			`SELECT last_seen_at FROM agent_runtime WHERE instance_id=$1`, inst).Scan(&again)
		return again.After(seen)
	}, "heartbeat should advance last_seen_at")
}

func TestB2_SweeperGraceWindow(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b2gr@example.com")
	ws := e.createWS(t, user, "b2-grace")
	wi := e.createWorkItem(t, ws, user, "grace")
	a := e.createAgent(t, ws, "b2-grace")
	t1, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	_, err := e.pool.Exec(ctx0(), `
INSERT INTO agent_runtime (instance_id, engine, last_seen_at)
VALUES ('grace-inst', 'eino', now() - interval '100 seconds')`)
	require.NoError(t, err)
	_, ok, _ := e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, ok)
	_, err = e.repo.StartTask(ctx0(), t1.ID, "grace-inst", "")
	require.NoError(t, err)

	s := &LivenessSweeper{Repo: e.repo, Log: logNoop(), StaleSeconds: 150}
	n, err := s.RunOnce(ctx0())
	require.NoError(t, err)
	assert.EqualValues(t, 0, n, "no death verdict inside the grace window")
	tk, _, _ := e.repo.GetTask(ctx0(), ws, t1.ID)
	assert.Equal(t, "running", tk.Status)
}
