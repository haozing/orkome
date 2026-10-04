package workers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
)

func newEval(t *testing.T, e *env, rate float64, quota int) *orchapp.EvalService {
	notif := notifpgrepo.New(e.db)
	ev := orchapp.NewEvalService(e.engine, e.repo, notif, nil, nil)
	ev.Rate = rate
	ev.DailyQuota = quota
	return ev
}

func TestB10_EvalSamplingDeterministic(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b10a@example.com")
	ws := e.createWS(t, user, "b10-a")
	wi := e.createWorkItem(t, ws, user, "sampling")
	a := e.createAgent(t, ws, "sampler")

	e.SetReply(func(string) (string, int, int, error) {
		return "the done conclusion is long enough, exceeding the twenty-character minimum.", 1, 1, nil
	})
	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	go d.Run(dctx)
	defer cancel()
	task, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, task.ID)
		return ok && tt.Status == "completed"
	}, "task completed")

	full := newEval(t, e, 1.0, 100)
	none := newEval(t, e, 0, 100)

	none.OnTaskCompleted(ctx0(), completedOf(t, e, ws, task.ID))
	var n int
	_ = e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM notification WHERE type='eval_report'`).Scan(&n)
	assert.Equal(t, 0, n)
	_ = e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM task_usage WHERE agent_label='eval'`).Scan(&n)
	assert.Equal(t, 0, n)
	_ = full
	_ = a
}

func TestB10_EvalReportEndToEnd(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b10b@example.com")
	ws := e.createWS(t, user, "b10-b")
	wi := e.createWorkItem(t, ws, user, "evaluation")
	a := e.createAgent(t, ws, "evaluated")

	e.SetReply(func(prompt string) (string, int, int, error) {
		if len(prompt) >= 6 && prompt[:6] == "You are in a conversation with the user" || len(prompt) >= 4 && prompt[:4] == "please complete the assignment" {
			return "this research conclusion covers three dimensions with actionable suggestions; the content is substantial.", 1, 1, nil
		}
		return `{"score":4,"comment":"good coverage, actionable"}`, 1, 1, nil
	})
	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	go d.Run(dctx)
	defer cancel()
	task, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, task.ID)
		return ok && tt.Status == "completed"
	}, "task completed")

	ev := newEval(t, e, 1.0, 100)
	ev.OnTaskCompleted(ctx0(), completedOf(t, e, ws, task.ID))

	var title, body string
	require.NoError(t, e.pool.QueryRow(ctx0(), `
SELECT title, body FROM notification WHERE type='eval_report' AND recipient_user_id=$1 AND dedup_key=$2`,
		user, "eval:"+task.ID).Scan(&title, &body))
	assert.Contains(t, title, "task quality evaluation")
	assert.Contains(t, body, "Rule checks", "rule section must exist")
	assert.Contains(t, body, "output length normal", "length rule hit")
	assert.Contains(t, body, "LLM score: 4/5", "LLM score section")
	assert.Contains(t, body, "good coverage", "LLM comment")

	used, err := e.repo.CountEvalToday(ctx0(), ws)
	require.NoError(t, err)
	assert.Equal(t, 1, used)

	ev.OnTaskCompleted(ctx0(), completedOf(t, e, ws, task.ID))
	var rows int
	_ = e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM notification WHERE type='eval_report' AND dedup_key=$1`, "eval:"+task.ID).Scan(&rows)
	assert.Equal(t, 1, rows, "dedup idempotent")
}

func TestB10_EvalQuotaSkip(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b10c@example.com")
	ws := e.createWS(t, user, "b10-c")
	wi := e.createWorkItem(t, ws, user, "quota")
	a := e.createAgent(t, ws, "quota-agent")

	require.NoError(t, e.repo.InsertAuxUsage(ctx0(), ws, orchapp.UsageReport{
		Provider: "eval", Model: "rules+llm", AgentLabel: "eval"}))
	used, _ := e.repo.CountEvalToday(ctx0(), ws)
	require.Equal(t, 1, used)

	e.SetReply(func(string) (string, int, int, error) {
		return "the conclusion is long enough, exceeding the twenty-character minimum.", 1, 1, nil
	})
	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	go d.Run(dctx)
	defer cancel()
	task, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, task.ID)
		return ok && tt.Status == "completed"
	}, "task completed")

	ev := newEval(t, e, 1.0, 1)
	ev.OnTaskCompleted(ctx0(), completedOf(t, e, ws, task.ID))

	var n int
	_ = e.pool.QueryRow(ctx0(), `SELECT count(*) FROM notification WHERE type='eval_report'`).Scan(&n)
	assert.Equal(t, 0, n, "quota full → skip (internal archive 09 §16.3)")
	used, _ = e.repo.CountEvalToday(ctx0(), ws)
	assert.Equal(t, 1, used, "skips do not increment the count")
}

func completedOf(t *testing.T, e *env, ws, id string) orchapp.Task {
	t.Helper()
	tt, ok, err := e.repo.GetTask(ctx0(), ws, id)
	require.NoError(t, err)
	require.True(t, ok)
	return tt
}
