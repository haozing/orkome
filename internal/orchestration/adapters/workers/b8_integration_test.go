package workers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

func TestB8_SessionContinuityChain(t *testing.T) {
	e := newEnv(t)
	e.engine.inner.WorkItemSession = func(ctx context.Context, wi, agentID string) ([]orchapp.SessionTurn, error) {
		return e.repo.SessionTranscript(ctx, wi, agentID, 5)
	}
	user := e.createUser(t, "b8@example.com")
	ws := e.createWS(t, user, "b8-w")
	wi := e.createWorkItem(t, ws, user, "session continues")
	a := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})

	var lastPrompt string
	e.SetReply(func(prompt string) (string, int, int, error) {
		lastPrompt = prompt
		return "first-round conclusion: baseline established.", 1, 1, nil
	})
	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	ddone := make(chan struct{})
	go func() { defer close(ddone); d.Run(dctx) }()
	defer func() { cancel(); <-ddone }()

	t1, adm, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)
	require.Equal(t, "queued", adm)
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, t1.ID)
		return ok && tt.Status == "completed"
	}, "task one should be done")
	var ref1 string
	require.NoError(t, e.pool.QueryRow(ctx0(),
		`SELECT session_ref_out FROM agent_task_queue WHERE id=$1`, t1.ID).Scan(&ref1))
	assert.True(t, len(ref1) > len("session:"), "should produce a session cursor, got %q", ref1)
	assert.Contains(t, ref1, "session:")

	t2, adm, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)
	require.Equal(t, "queued", adm)
	assert.Equal(t, ref1, t2.SessionRefIn, "cursor chain: session_ref_in = previous terminal session_ref_out")
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, t2.ID)
		return ok && tt.Status == "completed"
	}, "task two should be done")
	assert.Contains(t, lastPrompt, "first-round conclusion", "continuation prompts should inject past conclusions")
	assert.Contains(t, lastPrompt, "keep context continuity", "session continuation marker")
	var ref2 string
	_ = e.pool.QueryRow(ctx0(),
		`SELECT session_ref_out FROM agent_task_queue WHERE id=$1`, t2.ID).Scan(&ref2)
	assert.NotEqual(t, ref1, ref2, "cursor advances on each success")
	assert.Contains(t, ref2, "session:")

	e.SetReply(func(string) (string, int, int, error) {
		return "", 0, 0, ctxOverflowErr{}
	})
	t3, _, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, t3.ID)
		return ok && tt.Status == "failed"
	}, "poison task should fail")
	var ref3 *string
	_ = e.pool.QueryRow(ctx0(),
		`SELECT session_ref_out FROM agent_task_queue WHERE id=$1`, t3.ID).Scan(&ref3)
	assert.Nil(t, ref3, "poison failure produces no cursor (11.2)")

	e.SetReply(func(prompt string) (string, int, int, error) {
		lastPrompt = prompt
		return "fourth-round conclusion.", 1, 1, nil
	})
	t4, _, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)
	assert.Equal(t, ref2, t4.SessionRefIn, "cursor parsing skips poison-failed rows (internal archive 05 §2.9 predicate)")
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, t4.ID)
		return ok && tt.Status == "completed"
	}, "task four should be done")
	assert.Contains(t, lastPrompt, "first-round conclusion", "history contains task one's conclusion")
	assert.NotContains(t, lastPrompt, "fourth-round conclusion", "excludes itself")
}

func TestB8_SessionAgentScoped(t *testing.T) {
	e := newEnv(t)
	e.engine.inner.WorkItemSession = func(ctx context.Context, wi, agentID string) ([]orchapp.SessionTurn, error) {
		return e.repo.SessionTranscript(ctx, wi, agentID, 5)
	}
	user := e.createUser(t, "b8b@example.com")
	ws := e.createWS(t, user, "b8-b")
	wi := e.createWorkItem(t, ws, user, "multi-agent session isolation")
	a1 := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})
	a2 := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})

	e.SetReply(func(string) (string, int, int, error) { return "a1 private conclusion", 1, 1, nil })
	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	ddone := make(chan struct{})
	go func() { defer close(ddone); d.Run(dctx) }()
	defer func() { cancel(); <-ddone }()

	t1, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a1.ID, user)
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, t1.ID)
		return ok && tt.Status == "completed"
	}, "a1 task completed")

	var lastPrompt string
	e.SetReply(func(prompt string) (string, int, int, error) {
		lastPrompt = prompt
		return "a2 conclusion", 1, 1, nil
	})
	t2, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a2.ID, user)
	assert.Empty(t, t2.SessionRefIn, "different agents do not share session pointers")
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, t2.ID)
		return ok && tt.Status == "completed"
	}, "a2 task completed")
	assert.NotContains(t, lastPrompt, "a1 private conclusion", "a2 prompt must not inject a1's session")
}

func TestB8_ForceFreshSession(t *testing.T) {
	e := newEnv(t)
	e.engine.inner.WorkItemSession = func(ctx context.Context, wi, agentID string) ([]orchapp.SessionTurn, error) {
		return e.repo.SessionTranscript(ctx, wi, agentID, 5)
	}
	user := e.createUser(t, "b8c@example.com")
	ws := e.createWS(t, user, "b8-c")
	wi := e.createWorkItem(t, ws, user, "manual rerun")
	a := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})

	e.SetReply(func(string) (string, int, int, error) { return "old session conclusion", 1, 1, nil })
	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	ddone := make(chan struct{})
	go func() { defer close(ddone); d.Run(dctx) }()
	defer func() { cancel(); <-ddone }()
	t1, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, t1.ID)
		return ok && tt.Status == "completed"
	}, "first task completed")

	var lastPrompt string
	e.SetReply(func(prompt string) (string, int, int, error) {
		lastPrompt = prompt
		return "brand-new conclusion", 1, 1, nil
	})
	ref, ok, _ := e.repo.GetLastSessionRef(ctx0(), wi, a.ID, "")
	require.True(t, ok)
	fresh, err := e.repo.InsertTask(ctx0(), orchapp.Task{
		WorkspaceID: ws, AgentID: a.ID, WorkItemID: wi, Status: "queued", MaxAttempts: 1,
		Context:          map[string]any{"trigger": "rerun", "force_fresh_session": true},
		OriginatorUserID: user, SessionRefIn: ref,
	})
	require.NoError(t, err)
	waitFor(t, func() bool {
		tt, okk, _ := e.repo.GetTask(ctx0(), ws, fresh.ID)
		return okk && tt.Status == "completed"
	}, "fresh task completed")
	assert.NotContains(t, lastPrompt, "old session conclusion", "force_fresh_session does not inject history (internal archive 09 §11.1)")
}

type ctxOverflowErr struct{}

func (ctxOverflowErr) Error() string { return "model error: context length exceeded (token limit)" }
