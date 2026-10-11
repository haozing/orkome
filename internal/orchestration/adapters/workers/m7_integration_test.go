package workers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	autoppgrepo "orkome/internal/automation/adapters/pgrepo"
	autopapp "orkome/internal/automation/app"
	autodomain "orkome/internal/automation/domain"
	orchapp "orkome/internal/orchestration/app"
	workapp "orkome/internal/work/app"
)

type m7env struct {
	*env
	wu      *autopapp.WakeupService
	auto    *autopapp.AutopilotService
	wh      *autopapp.WebhookService
	arepo   *autoppgrepo.Repo
	enqPort wakeupEnq
}

type wakeupEnq struct {
	e *env
}

func (w wakeupEnq) EnqueueForWakeup(ctx context.Context, ws, wi, agentID, userID, evidence string) (string, bool, error) {

	if wi != "" {
		merged, err := w.e.repo.MergeIntoPending(ctx, ws, wi, agentID, "")
		if err != nil {
			return "", false, err
		}
		if merged {
			return "", true, nil
		}
	}
	t, err := w.e.repo.InsertTask(ctx, orchapp.Task{
		WorkspaceID: ws, AgentID: agentID, WorkItemID: wi, Status: "queued", MaxAttempts: 2,
		Context:          map[string]any{"trigger": "wakeup", "wakeup_note": evidence, "work_item_title": ""},
		OriginatorUserID: orZero(userID), AccountableUserID: orZero(userID),
		OriginatorSource: "trigger_owner", TriggerEvidenceKind: "rule",
	})
	if err != nil {
		return "", false, err
	}
	return t.ID, false, nil
}

type factsPort struct{ e *env }

func (f factsPort) WorkItemFacts(ctx context.Context, ws, wi string) (string, int, error) {
	w, ok, err := f.e.wRepo.GetWorkItem(ctx, ws, wi)
	return w.StatusKey, w.Priority, errWithNotFound(ok, err)
}

func (f factsPort) ChildStageFacts(context.Context, string, string) ([]autodomain.ChildStageFact, error) {
	return nil, nil
}

func (f factsPort) WakeupTargetAgent(context.Context, string, string) (string, error) {
	return "", nil
}

type creatorPort struct{ e *env }

func (c creatorPort) CreateForAutopilot(ctx context.Context, ws, title, desc, agentID, creator, origin string) (string, error) {
	wi, err := c.e.wRepo.CreateWorkItem(ctx, workapp.NewWorkItem{
		WorkspaceID: ws, Title: title, Description: desc, StatusKey: "todo",
		AssigneeType: "agent", AssigneeID: agentID, CreatorType: "user", CreatorID: creator,
		OriginType: origin,
	})
	return wi.ID, err
}

func orZero(s string) string {
	if s == "" {
		return "00000000-0000-0000-0000-000000000000"
	}
	return s
}

func errWithNotFound(ok bool, err error) error {
	if !ok && err == nil {
		return nil
	}
	return err
}

func newM7(t *testing.T) *m7env {
	e := newEnv(t)
	arepo := autoppgrepo.New(e.db)
	enqP := wakeupEnq{e: e}
	wu := autopapp.NewWakeupService(arepo, enqP, factsPort{e: e})
	auto := autopapp.NewAutopilotService(arepo, creatorPort{e: e}, enqP)
	wh := autopapp.NewWebhookService(arepo, auto)
	return &m7env{env: e, wu: wu, auto: auto, wh: wh, arepo: arepo, enqPort: enqP}
}

func TestM7_CronAutopilot_FullChain(t *testing.T) {
	m := newM7(t)
	e := m.env
	user := e.createUser(t, "m7a@example.com")
	ws := e.createWS(t, user, "m7-a")
	leader := e.createAgent(t, ws, "leader")

	ap, trig, err := m.auto.Create(ctx0(), autopapp.Autopilot{
		WorkspaceID: ws, Name: "daily briefing", ExecMode: "create_work_item",
		Template:      map[string]any{"title": "brief {{date}}", "description": "generate the daily briefing"},
		TargetAgentID: leader.ID, CreatedBy: user,
	}, "*/5 * * * *", "UTC")
	require.NoError(t, err)
	require.NotEmpty(t, ap.ID)
	require.NotNil(t, trig.NextRunAt)

	_, err = e.pool.Exec(ctx0(),
		`UPDATE autopilot_trigger SET next_run_at = now() - interval '1 minute' WHERE id=$1`, trig.ID)
	require.NoError(t, err)

	n, err := m.auto.CronTickOnce(ctx0())
	require.NoError(t, err)
	require.Equal(t, 1, n, "should trigger once")

	var wiTitle string
	var wiID string
	err = e.pool.QueryRow(ctx0(),
		`SELECT id::text, title FROM work_item WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT 1`, ws).
		Scan(&wiID, &wiTitle)
	require.NoError(t, err)
	assert.Contains(t, wiTitle, "brief 20", "{{date}} should be interpolated (with year prefix)")

	tasks, err := e.repo.ListTasksByWorkItem(ctx0(), wiID, false)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, leader.ID, tasks[0].AgentID)

	var runStatus string
	var runTask string
	err = e.pool.QueryRow(ctx0(),
		`SELECT status, coalesce(task_id::text,'') FROM autopilot_run WHERE autopilot_id=$1 ORDER BY planned_at DESC LIMIT 1`,
		ap.ID).Scan(&runStatus, &runTask)
	require.NoError(t, err)
	assert.Equal(t, "running", runStatus)
	assert.Equal(t, tasks[0].ID, runTask)

	m.auto.BackfillFromTask(ctx0(), tasks[0].ID, "completed")
	err = e.pool.QueryRow(ctx0(),
		`SELECT status FROM autopilot_run WHERE task_id=$1`, tasks[0].ID).Scan(&runStatus)
	require.NoError(t, err)
	assert.Equal(t, "completed", runStatus, "run status driven by task terminal state (internal archive 05 §9)")

	var next time.Time
	_ = e.pool.QueryRow(ctx0(), `SELECT next_run_at FROM autopilot_trigger WHERE id=$1`, trig.ID).Scan(&next)
	assert.True(t, next.After(time.Now().Add(-time.Minute)))
}

func TestM7_Webhook_VerifyDedupeDeliver(t *testing.T) {
	m := newM7(t)
	e := m.env
	user := e.createUser(t, "m7b@example.com")
	ws := e.createWS(t, user, "m7-b")
	agent := e.createAgent(t, ws, "hooked")

	ap, _, err := m.auto.Create(ctx0(), autopapp.Autopilot{
		WorkspaceID: ws, Name: "hook stream", ExecMode: "task_only",
		Template: map[string]any{}, TargetAgentID: agent.ID, CreatedBy: user,
	}, "", "UTC")
	require.NoError(t, err)

	trig, err := m.arepo.CreateTrigger(ctx0(), autopapp.Trigger{
		AutopilotID: ap.ID, Kind: "webhook", WebhookToken: "tok_m7", SigningSecret: "sec_m7", Enabled: true,
	})
	require.NoError(t, err)

	body := []byte(`{"event":"push","ref":"main"}`)
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte("sec_m7"))
	mac.Write([]byte(itoa(ts) + "." + string(body)))
	sig := hex.EncodeToString(mac.Sum(nil))

	code, err := m.wh.Ingest(ctx0(), "tok_m7", "deadbeef", itoa(ts), body)
	assert.Equal(t, 403, code)

	code, err = m.wh.Ingest(ctx0(), "tok_m7", sig, itoa(ts), body)
	require.NoError(t, err)
	assert.Equal(t, 202, code)

	code, err = m.wh.Ingest(ctx0(), "tok_m7", sig, itoa(ts), body)
	require.NoError(t, err)
	assert.Equal(t, 202, code)
	var cnt int
	_ = e.pool.QueryRow(ctx0(), `SELECT count(*) FROM automation_webhook_delivery WHERE trigger_id=$1`, trig.ID).Scan(&cnt)
	assert.Equal(t, 1, cnt, "dedup key should be idempotent")

	code, _ = m.wh.Ingest(ctx0(), "tok_m7", sig, itoa(time.Now().Add(-time.Hour).Unix()), body)
	assert.Equal(t, 403, code)

	ok, err := m.wh.ConsumeOnce(ctx0())
	require.NoError(t, err)
	assert.True(t, ok)
	var dstatus string
	waitFor(t, func() bool {
		_ = e.pool.QueryRow(ctx0(),
			`SELECT status FROM automation_webhook_delivery WHERE trigger_id=$1`, trig.ID).Scan(&dstatus)
		return dstatus == "delivered"
	}, "webhook delivery should reach delivered")
	var runs int
	_ = e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM autopilot_run WHERE autopilot_id=$1 AND source='webhook'`, ap.ID).Scan(&runs)
	assert.Equal(t, 1, runs, "webhook should trigger a run")
}

func TestM7_Wakeup_DispatchAndJoin(t *testing.T) {
	m := newM7(t)
	e := m.env
	user := e.createUser(t, "m7c@example.com")
	ws := e.createWS(t, user, "m7-c")
	wi := e.createWorkItem(t, ws, user, "wakeup")
	agent := e.createAgent(t, ws, "sleeper")

	w, err := m.wu.Create(ctx0(), autopapp.Wakeup{
		WorkspaceID: ws, WorkItemID: wi, AgentID: agent.ID, Kind: "time",
		Instruction: "check progress", IntervalSeconds: 3600, CreatedBy: user,
	})
	require.NoError(t, err)
	require.NotNil(t, w.NextFireAt)

	_, err = e.pool.Exec(ctx0(),
		`UPDATE work_item_wakeup SET next_fire_at = now() - interval '1 minute' WHERE id=$1`, w.ID)
	require.NoError(t, err)

	n, err := m.wu.TickOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "should dispatch once")

	tasks, _ := e.repo.ListTasksByWorkItem(ctx0(), wi, false)
	require.Len(t, tasks, 1, "exactly one task")

	_, err = e.pool.Exec(ctx0(),
		`UPDATE work_item_wakeup SET next_fire_at = now() - interval '1 minute' WHERE id=$1`, w.ID)
	require.NoError(t, err)
	n2, err := m.wu.TickOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 1, n2, "confluence also counts as one fire (internal archive 05 §8.1)")
	tasks2, _ := e.repo.ListTasksByWorkItem(ctx0(), wi, false)
	assert.Len(t, tasks2, 1, "confluence must not create new tasks (merged into pending)")

	var fc int
	_ = e.pool.QueryRow(ctx0(), `SELECT fire_count FROM work_item_wakeup WHERE id=$1`, w.ID).Scan(&fc)
	assert.Equal(t, 2, fc)
}

func TestM7_ConditionWakeup_FiresOnBecomingTrue(t *testing.T) {
	m := newM7(t)
	e := m.env
	user := e.createUser(t, "m7d@example.com")
	ws := e.createWS(t, user, "m7-d")
	wi := e.createWorkItem(t, ws, user, "conditional wakeup")
	agent := e.createAgent(t, ws, "watcher")

	_, err := m.wu.Create(ctx0(), autopapp.Wakeup{
		WorkspaceID: ws, WorkItemID: wi, AgentID: agent.ID, Kind: "condition",
		Instruction: "summary after done", CreatedBy: user,
		Condition: map[string]any{"field": "status", "op": "eq", "value": "done"},
	})
	require.NoError(t, err)

	n, err := m.wu.TickOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	tasks, _ := e.repo.ListTasksByWorkItem(ctx0(), wi, false)
	assert.Empty(t, tasks, "false predicate must not trigger")

	st := "done"
	_, err = e.wRepo.UpdateWorkItem(ctx0(), ws, wi, workapp.WorkItemPatch{StatusKey: &st}, 1)
	require.NoError(t, err)

	n2, err := m.wu.TickOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 1, n2, "turning real should trigger")
	tasks2, _ := e.repo.ListTasksByWorkItem(ctx0(), wi, false)
	assert.Len(t, tasks2, 1)

	n3, err := m.wu.TickOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 0, n3, "staying true does not re-trigger (edge detection)")
}

func itoa(i int64) string {
	b, _ := json.Marshal(i)
	return string(b)
}
