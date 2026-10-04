package workers

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/dispatchreason"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	workapp "orkome/internal/work/app"
)

func (e *env) createAgent(t *testing.T, wsID, name string) orchapp.Agent {
	a, err := e.agents.Create(ctx0(), orchapp.Agent{WorkspaceID: wsID, Name: name,
		RuntimeConfig: map[string]any{"provider": "fake", "model": "m"}})
	require.NoError(t, err)
	return *a
}

func (e *env) postComment(t *testing.T, ws, wi, authorID, content string) (string, string) {
	c, adm, err := e.comments.Create(ctx0(), workapp.NewComment{
		WorkItemID: wi, WorkspaceID: ws, AuthorType: "user", AuthorID: authorID, Content: content,
	})
	require.NoError(t, err)
	return c.ID, adm2result(adm)
}

func adm2result(a *workapp.AdmissionResult) string {
	if a == nil {
		return ""
	}
	return a.Result
}

func TestM4_CommentTrigger_GoldenFallback(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m4a@example.com")
	ws := e.createWS(t, user, "m4-a")
	wi := e.createWorkItem(t, ws, user, "trigger test")
	a := e.createAgent(t, ws, "echo")

	_, err := e.wRepo.UpdateWorkItem(ctx0(), ws, wi, workapp.WorkItemPatch{
		AssigneeType: strPtr("agent"), AssigneeID: strPtr(a.ID),
	}, 1)
	require.NoError(t, err)

	_, adm := e.postComment(t, ws, wi, user, "please start processing")
	assert.Equal(t, dispatchreason.Queued, adm, "7c fallback should enqueue")

	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	go d.Run(dctx)
	defer cancel()
	waitFor(t, func() bool {
		all, _ := e.repo.ListTasksByWorkItem(ctx0(), wi, false)
		for _, tk := range all {
			if tk.Status == "completed" {
				return true
			}
		}
		return false
	}, "comment triggers task completed")
}

func TestM4_CommentTrigger_MentionAndAll(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m4b@example.com")
	ws := e.createWS(t, user, "m4-b")
	wi := e.createWorkItem(t, ws, user, "M")
	a1 := e.createAgent(t, ws, "one")
	a2 := e.createAgent(t, ws, "two")
	_, err := e.wRepo.UpdateWorkItem(ctx0(), ws, wi, workapp.WorkItemPatch{
		AssigneeType: strPtr("agent"), AssigneeID: strPtr(a2.ID)}, 1)
	require.NoError(t, err)

	_, adm := e.postComment(t, ws, wi, user, "[@one](mention://agent/"+a1.ID+") come")
	assert.Equal(t, "queued", adm)
	tasks, _ := e.repo.ListTasksByWorkItem(ctx0(), wi, false)
	assert.Len(t, tasks, 1)
	assert.Equal(t, a1.ID, tasks[0].AgentID)
	assert.Equal(t, "3.explicit_mention", tasks[0].Context["trigger_reason"])

	wi2 := e.createWorkItem(t, ws, user, "M2")
	_, err = e.wRepo.UpdateWorkItem(ctx0(), ws, wi2, workapp.WorkItemPatch{
		AssigneeType: strPtr("agent"), AssigneeID: strPtr(a2.ID)}, 1)
	require.NoError(t, err)
	_, adm2 := e.postComment(t, ws, wi2, user, "[@all](mention://all/) broadcast")
	assert.Equal(t, "blocked", adm2)
	tasks2, _ := e.repo.ListTasksByWorkItem(ctx0(), wi2, false)
	assert.Empty(t, tasks2)
}

func TestM4_Protocol_Coalesce(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m4c@example.com")
	ws := e.createWS(t, user, "m4-c")
	wi := e.createWorkItem(t, ws, user, "C")
	a := e.createAgent(t, ws, "merger")
	_, _, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)
	_, err = e.wRepo.UpdateWorkItem(ctx0(), ws, wi, workapp.WorkItemPatch{
		AssigneeType: strPtr("agent"), AssigneeID: strPtr(a.ID)}, 1)
	require.NoError(t, err)

	c2, adm := e.postComment(t, ws, wi, user, "appended info")
	assert.Equal(t, dispatchreason.Coalesced, adm, "should merge into pending")

	tasks, _ := e.repo.ListActiveTasks(ctx0(), wi)
	require.Len(t, tasks, 1)
	ids, _ := tasks[0].Context["coalesced_comment_ids"].([]any)
	found := false
	for _, id := range ids {
		if id == c2 {
			found = true
		}
	}
	assert.True(t, found, "merge should append the comment ID (I10 write endorsement)")
}

func TestM4_Steering_InjectAndConsume(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m4d@example.com")
	ws := e.createWS(t, user, "m4-d")
	wi := e.createWorkItem(t, ws, user, "S")
	a := e.createAgent(t, ws, "steered")
	task, _, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)
	_, err = e.wRepo.UpdateWorkItem(ctx0(), ws, wi, workapp.WorkItemPatch{
		AssigneeType: strPtr("agent"), AssigneeID: strPtr(a.ID)}, 1)
	require.NoError(t, err)

	seen := ""
	e.engine.inner.SupplementClaimer = func(ctx context.Context, taskID string) (string, bool, func() error) {
		id, content, ok, err := e.repo.ClaimNextSupplement(ctx, taskID)
		if err != nil || !ok {
			return "", false, nil
		}
		return content, true, func() error { return e.repo.AckSupplementDelivered(ctx, id) }
	}

	e.SetReply(func(prompt string) (string, int, int, error) {
		seen = prompt
		if want := "[additional instruction] also add a conclusion section"; !containsStr(prompt, want) {
			return "missing additional instruction", 10, 5, nil
		}
		return "done per the additional instruction", 10, 5, nil
	})

	block := make(chan struct{})
	started := make(chan string, 1)

	d := &Dispatcher{
		Repo: e.repo, Claim: orchapp.NewClaimService(e.repo), Settle: e.settle,
		Engine: slowEngine{e: e.engine, repo: e.repo, started: started, block: block},
		Log:    logNoop(), Emit: e.bus, PollEvery: 20 * time.Millisecond, Semaphore: 1, Timeout: 10 * time.Second,
	}
	dctx, cancel := context.WithCancel(ctx0())
	go d.Run(dctx)
	defer cancel()

	<-started
	_, adm := e.postComment(t, ws, wi, user, "also add a conclusion section")
	assert.Equal(t, dispatchreason.Steered, adm, "comments while running should steer (internal archive 05 §4.5)")
	close(block)

	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, task.ID)
		return ok && tt.Status == "completed"
	}, "steering task completed")
	tt, _, _ := e.repo.GetTask(ctx0(), ws, task.ID)
	assert.Equal(t, "done per the additional instruction", tt.Result, "the model should see the injected supplement (seen=%q)", seen)

	var st string
	_ = e.pool.QueryRow(ctx0(), `SELECT status FROM task_supplement WHERE task_id=$1`, task.ID).Scan(&st)
	assert.Equal(t, "delivered", st)
}

type slowEngine struct {
	e       *fakeOrRealEngine
	repo    *orchpgrepo.Repo
	started chan string
	block   chan struct{}
}

func (s slowEngine) Name() string { return "slow" }
func (s slowEngine) Execute(ctx context.Context, task orchapp.Task, agent orchapp.Agent, secret string) (orchapp.EngineOutput, error) {
	s.started <- task.ID
	<-s.block
	return s.e.Execute(ctx, task, agent, secret)
}
func (s slowEngine) Resume(ctx context.Context, task orchapp.Task, agent orchapp.Agent, secret, targetID string, decision orchapp.ApprovalDecision) (orchapp.EngineOutput, error) {
	return s.e.Resume(ctx, task, agent, secret, targetID, decision)
}

func (s slowEngine) Generate(ctx context.Context, agent orchapp.Agent, secret, system, input string) (string, orchapp.UsageReport, error) {
	return s.e.Generate(ctx, agent, secret, system, input)
}

func TestM4_Steer_FallbackWhenNotRunning(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m4e@example.com")
	ws := e.createWS(t, user, "m4-e")
	wi := e.createWorkItem(t, ws, user, "F")
	a := e.createAgent(t, ws, "fb")
	_, _, err := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)
	_, err = e.wRepo.UpdateWorkItem(ctx0(), ws, wi, workapp.WorkItemPatch{
		AssigneeType: strPtr("agent"), AssigneeID: strPtr(a.ID)}, 1)
	require.NoError(t, err)

	_, adm := e.postComment(t, ws, wi, user, "comment while queued")
	assert.Equal(t, dispatchreason.Coalesced, adm, "no running should fall back to merging (internal archive 05 §2.12)")
}

func TestM4_Resume_PoisonConsistency(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m4f@example.com")
	ws := e.createWS(t, user, "m4-f")
	wi := e.createWorkItem(t, ws, user, "P")
	a := e.createAgent(t, ws, "poison")

	t1, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	_, ok, _ := e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, ok)
	_, err := e.repo.StartTask(ctx0(), t1.ID, "", "")
	require.NoError(t, err)
	require.NoError(t, e.repo.CompleteRun(ctx0(), t1.ID, "ok", "sess-1", "eino",
		orchapp.UsageReport{Provider: "fake", Model: "m"}))

	t2, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	_, ok, _ = e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, ok)
	_, err = e.repo.StartTask(ctx0(), t2.ID, "", "")
	require.NoError(t, err)
	_, err = e.repo.FailRun(ctx0(), t2.ID, "agent_error.context_overflow", "ctx too long")
	require.NoError(t, err)

	ref, ok, err := e.repo.GetLastSessionRef(ctx0(), wi, a.ID, "")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "sess-1", ref)

	t3, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	_, ok, _ = e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, ok)
	_, _ = e.repo.StartTask(ctx0(), t3.ID, "", "")
	require.NoError(t, e.repo.CompleteRun(ctx0(), t3.ID, "ok2", "sess-2", "eino",
		orchapp.UsageReport{Provider: "fake", Model: "m"}))
	ref2, ok2, _ := e.repo.GetLastSessionRef(ctx0(), wi, a.ID, "")
	require.True(t, ok2)
	assert.Equal(t, "sess-2", ref2)

	_, ok3, err := e.repo.GetLastSessionRef(ctx0(), wi, a.ID, "other-engine")
	require.NoError(t, err)
	assert.False(t, ok3, "fingerprint-incompatible rows produce no cursors (internal archive 05 §2.9 poison)")
	ref4, ok4, _ := e.repo.GetLastSessionRef(ctx0(), wi, a.ID, "")
	require.True(t, ok4)
	assert.Equal(t, "sess-2", ref4, "empty query tag = compat semantics (historical rows allowed)")
}

func TestM4_DelegatedRecovery(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m4g@example.com")
	ws := e.createWS(t, user, "m4-g")
	wi := e.createWorkItem(t, ws, user, "R")
	a := e.createAgent(t, ws, "recovered")
	t1, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	_, ok, _ := e.repo.ClaimNext(ctx0(), a.ID, 6)
	require.True(t, ok)
	_, _ = e.repo.StartTask(ctx0(), t1.ID, "", "")

	_, err := e.repo.FailRun(ctx0(), t1.ID, "agent_provider.auth", "bad key")
	require.NoError(t, err)

	trig := orchapp.NewTriggerService(e.repo, e.bus)
	rec, err := trig.RecoverDelegated(ctx0(), mustTask(t, e, ws, t1.ID), "upstream failed, please review")
	require.NoError(t, err)
	assert.True(t, rec)

	tasks, _ := e.repo.ListTasksByWorkItem(ctx0(), wi, false)
	var recovery *orchapp.Task
	for i := range tasks {
		if tasks[i].TriggerEvidenceKind == "delegated" {
			recovery = &tasks[i]
		}
	}
	require.NotNil(t, recovery, "recovery task should be created")
	assert.Equal(t, user, recovery.OriginatorUserID, "attribution inherits from the source task (I3)")

	for i := 0; i < 4; i++ {
		_, _ = e.repo.FailRun(ctx0(), recovery.ID, "agent_provider.auth", "x")
		_, _ = trig.RecoverDelegated(ctx0(), mustTask(t, e, ws, recovery.ID), "again")
	}
	all, _ := e.repo.ListTasksByWorkItem(ctx0(), wi, false)
	delegated := 0
	for _, tk := range all {
		if tk.TriggerEvidenceKind == "delegated" {
			delegated++
		}
	}
	assert.LessOrEqual(t, delegated, 3, "recovery cap 3 (internal archive 05 §2.11)")
}

func TestM4_Rerun(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m4h@example.com")
	ws := e.createWS(t, user, "m4-h")
	wi := e.createWorkItem(t, ws, user, "RR")
	a := e.createAgent(t, ws, "rerunee")
	_, _, _ = e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)

	trig := orchapp.NewTriggerService(e.repo, e.bus)
	nt, err := trig.Rerun(ctx0(), ws, wi, a.ID, user)
	require.NoError(t, err)
	assert.Equal(t, true, nt.Context["force_fresh_session"], "default fresh (internal archive 05 §2.13)")

	all, _ := e.repo.ListTasksByWorkItem(ctx0(), wi, false)
	live := 0
	for _, tk := range all {
		if tk.Status == "queued" || tk.Status == "dispatched" {
			live++
		}
	}
	assert.Equal(t, 1, live, "old pending should be cancelled, leaving only the new task")
}

func strPtr(s string) *string { return &s }

func firstTaskID(e *env, ws, wi string) string {
	tasks, _ := e.repo.ListActiveTasks(ctx0(), wi)
	if len(tasks) == 0 {
		return ""
	}
	return tasks[0].ID
}

func (e *env) newDispatcher() *Dispatcher {
	return &Dispatcher{
		Repo: e.repo, Claim: orchapp.NewClaimService(e.repo), Settle: e.settle,
		Engine: e.engine, Log: logNoop(), Emit: e.bus,
		PollEvery: 20 * time.Millisecond, Semaphore: 2, Timeout: 10 * time.Second,
	}
}

func mustTask(t *testing.T, e *env, ws, id string) orchapp.Task {
	tk, ok, err := e.repo.GetTask(ctx0(), ws, id)
	require.NoError(t, err)
	require.True(t, ok)
	return tk
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func logNoop() *slog.Logger { return slog.New(slog.DiscardHandler) }
