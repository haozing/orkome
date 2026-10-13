package app

// FP-10 P1-2：任务完成通知双发/三发治理。complete() 已在同一事务内经 outbox
// 落一条 TaskCompleted（唯一投影源），Settle/Approve 不再重复 Emit（新
// EventID 绕过投影 dedup → 订阅者双通知），Settle 的起源者直写第三条下线。
// 回归锁定：同任务完成只产 1 条 TaskCompleted 事件、0 次直写；
// 起源者自通知由投影侧 ActorUserID 过滤（notification 域测试锁定）。

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
)

type settleNotifyFakeRepo struct {
	Repo
	completeCalled bool
}

func (f *settleNotifyFakeRepo) LastTaskText(context.Context, string) (string, error) { return "", nil }

func (f *settleNotifyFakeRepo) CompleteRun(context.Context, string, string, string, string, string, UsageReport) error {
	f.completeCalled = true
	return nil
}

func (f *settleNotifyFakeRepo) SettlePendingSupplements(context.Context, string) error { return nil }

func (f *settleNotifyFakeRepo) FailRun(context.Context, string, string, string, string) (bool, error) {
	return false, nil
}

func (f *settleNotifyFakeRepo) PromoteDeferred(context.Context) ([]Task, error) { return nil, nil }

type directWriteRecorder struct{ calls int }

func (d *directWriteRecorder) WriteTaskCompleted(context.Context, string, string, string, string, string) error {
	d.calls++
	return nil
}

func completedTask() Task {
	return Task{
		ID: "task-1", WorkspaceID: "ws-1", AgentID: "ag-1", WorkItemID: "wi-1",
		OriginatorUserID: "u-originator", Status: "running",
	}
}

func TestSettle_TaskCompletedSingleEmit(t *testing.T) {
	repo := &settleNotifyFakeRepo{}
	emit := &recorderEmitter{}
	direct := &directWriteRecorder{}
	settle := NewSettlementService(repo, emit, direct, nil, nil)
	settle.SetRuntimeInstance("i-1")

	agent := Agent{ID: "ag-1", WorkspaceID: "ws-1", Name: "worker"}
	require.NoError(t, settle.Settle(context.Background(), completedTask(), agent,
		EngineOutput{Output: "done"}, nil))

	completed := 0
	for _, e := range emit.emitted {
		if e.Event == events.TaskCompleted {
			completed++
		}
	}
	assert.Equal(t, 1, completed, "同任务完成只允许一条 TaskCompleted（outbox 单投影源）")
	assert.Zero(t, direct.calls, "起源者直写第三条已下线（FP-10 P1-2）")
	assert.True(t, repo.completeCalled)
}

type approveFakeRepo struct {
	Repo
	task Task
}

func (f *approveFakeRepo) GetTask(context.Context, string, string) (Task, bool, error) {
	return f.task, true, nil
}

func (f *approveFakeRepo) GetAgent(context.Context, string, string) (Agent, bool, error) {
	return Agent{ID: "ag-1", WorkspaceID: "ws-1", Name: "worker", RuntimeConfig: map[string]any{}}, true, nil
}

func (f *approveFakeRepo) ClearAwaiting(context.Context, string) error { return nil }

func (f *approveFakeRepo) LastTaskText(context.Context, string) (string, error) { return "", nil }

func (f *approveFakeRepo) CompleteRun(context.Context, string, string, string, string, string, UsageReport) error {
	return nil
}

func (f *approveFakeRepo) SettlePendingSupplements(context.Context, string) error { return nil }

func (f *approveFakeRepo) PromoteDeferred(context.Context) ([]Task, error) { return nil, nil }

// approveFakeEngine.Resume 回放固定成功输出（summaryFakeEngine 的 Resume 恒报错，不适用）。
type approveFakeEngine struct{ out string }

func (e *approveFakeEngine) Name() string { return "fake" }

func (e *approveFakeEngine) Execute(context.Context, Task, Agent, string) (EngineOutput, error) {
	return EngineOutput{}, nil
}

func (e *approveFakeEngine) Resume(context.Context, Task, Agent, string, string, ApprovalDecision) (EngineOutput, error) {
	return EngineOutput{Output: e.out}, nil
}

func (e *approveFakeEngine) Generate(context.Context, Agent, string, string, string) (string, UsageReport, error) {
	return "", UsageReport{}, nil
}

func TestApprove_TaskCompletedSingleEmit(t *testing.T) {
	task := completedTask()
	task.Context = map[string]any{"interrupt": map[string]any{"target_id": "tgt-1"}}
	repo := &approveFakeRepo{task: task}
	emit := &recorderEmitter{}
	settle := NewSettlementService(repo, emit, nil, nil, nil)

	settle.SetApprovalDeps(nil, &approveFakeEngine{out: "resumed result"}, func(context.Context, string, string) (string, error) {
		return "", nil
	})

	require.NoError(t, settle.Approve(context.Background(), "ws-1", "task-1", "u-approver", true, ""))

	completed := 0
	for _, e := range emit.emitted {
		if e.Event == events.TaskCompleted {
			completed++
			assert.Equal(t, "u-approver", e.ActorUserID, "actor 透传，投影侧据此过滤自通知")
		}
	}
	assert.Equal(t, 1, completed, "Approve 路径同样只允许一条 TaskCompleted")
}

// ---- 工单产出（"需求→产出"一等公民）----
// completed 结算：outcome 后写覆盖（每次完成都写最新结论）；
// fail 结算：不写（失败没有产出）；
// outcome 落库后不再写兜底 agent 评论（同一内容不落两处，评论流回归交流面）。

type fakeOutcomeWriter struct {
	calls   int
	lastWS  string
	lastWI  string
	lastTask string
	lastBody string
	failOn  int // 第 N 次调用返回错误（0 = 永不）
}

func (f *fakeOutcomeWriter) WriteOutcome(_ context.Context, ws, wi, task, summary string) error {
	f.calls++
	if f.failOn > 0 && f.calls == f.failOn {
		return context.DeadlineExceeded
	}
	f.lastWS, f.lastWI, f.lastTask, f.lastBody = ws, wi, task, summary
	return nil
}

type commentRecorder struct {
	writes int
	has    bool
}

func (c *commentRecorder) WriteAgentComment(context.Context, string, string, string, string) error {
	c.writes++
	return nil
}
func (c *commentRecorder) HasAgentComment(context.Context, string) (bool, error) { return c.has, nil }

func TestSettle_CompletedWritesOutcome_LatestWins(t *testing.T) {
	repo := &settleNotifyFakeRepo{}
	ow := &fakeOutcomeWriter{}
	settle := NewSettlementService(repo, &recorderEmitter{}, nil, nil, nil)
	settle.SetRuntimeInstance("i-1")
	settle.SetOutcomeWriter(ow)

	agent := Agent{ID: "ag-1", WorkspaceID: "ws-1", Name: "worker"}
	require.NoError(t, settle.Settle(context.Background(), completedTask(), agent,
		EngineOutput{Output: "final conclusion v1"}, nil))
	require.NoError(t, settle.Settle(context.Background(), completedTask(), agent,
		EngineOutput{Output: "final conclusion v2"}, nil))

	assert.Equal(t, 2, ow.calls, "后写覆盖：每次 completed 都写")
	assert.Equal(t, "ws-1", ow.lastWS)
	assert.Equal(t, "wi-1", ow.lastWI)
	assert.Equal(t, "task-1", ow.lastTask)
	assert.Equal(t, "final conclusion v2", ow.lastBody, "后写覆盖：最终展示最新结论")
}

func TestSettle_FailedDoesNotWriteOutcome(t *testing.T) {
	repo := &settleNotifyFakeRepo{}
	ow := &fakeOutcomeWriter{}
	settle := NewSettlementService(repo, &recorderEmitter{}, nil, nil, nil)
	settle.SetRuntimeInstance("i-1")
	settle.SetOutcomeWriter(ow)

	agent := Agent{ID: "ag-1", WorkspaceID: "ws-1", Name: "worker"}
	err := &EngineError{Reason: "agent_error"}
	require.NoError(t, settle.Settle(context.Background(), completedTask(), agent,
		EngineOutput{}, err))

	assert.Equal(t, 0, ow.calls, "失败没有产出：fail 结算不写 outcome")
}

func TestSettle_OutcomeIsSoleChannel_NoFallbackComment(t *testing.T) {
	// 裁决（2026-10-12）：outcome 是产出的唯一正式面；结算不再写任何兜底
	// 评论——评论流只承载交流（人 + agent 按需 post_comment）。
	repo := &settleNotifyFakeRepo{}
	comments := &commentRecorder{}
	settle := NewSettlementService(repo, &recorderEmitter{}, nil, comments, nil)
	settle.SetRuntimeInstance("i-1")

	agent := Agent{ID: "ag-1", WorkspaceID: "ws-1", Name: "worker"}
	require.NoError(t, settle.Settle(context.Background(), completedTask(), agent,
		EngineOutput{Output: "the deliverable"}, nil))

	assert.Equal(t, 0, comments.writes, "结算不写评论：汇报 tab 是唯一汇报面")
}

// ---- 派发式小队自动回环（leader wake）----

type fakeSquadWake struct{ calls int; last Task }

func (f *fakeSquadWake) WakeLeaderIfSquadDone(_ context.Context, t Task) error {
	f.calls++
	f.last = t
	return nil
}

func memberDispatchTask() Task {
	return Task{
		ID: "member-task-1", WorkspaceID: "ws-1", AgentID: "ag-member", WorkItemID: "wi-1",
		Status: "running", MaxAttempts: 2,
		Context: map[string]any{"trigger": "leader_dispatch", "leader_task_id": "leader-task-0", "squad_id": "sq-1"},
	}
}

func TestSettle_MemberCompleted_TriggersSquadWake(t *testing.T) {
	repo := &settleNotifyFakeRepo{}
	wake := &fakeSquadWake{}
	settle := NewSettlementService(repo, &recorderEmitter{}, nil, nil, nil)
	settle.SetRuntimeInstance("i-1")
	settle.SetSquadWake(wake)

	agent := Agent{ID: "ag-member", WorkspaceID: "ws-1", Name: "member"}
	require.NoError(t, settle.Settle(context.Background(), memberDispatchTask(), agent,
		EngineOutput{Output: "member deliverable"}, nil))

	assert.Equal(t, 1, wake.calls, "leader_dispatch 成员任务终态必须触发 wake")
	assert.Equal(t, "leader_dispatch", wake.last.Context["trigger"])
}

func TestSettle_CommentTriggerTask_DoesNotWake(t *testing.T) {
	repo := &settleNotifyFakeRepo{}
	wake := &fakeSquadWake{}
	settle := NewSettlementService(repo, &recorderEmitter{}, nil, nil, nil)
	settle.SetRuntimeInstance("i-1")
	settle.SetSquadWake(wake)

	tk := memberDispatchTask()
	tk.Context = map[string]any{"trigger": "comment"}
	agent := Agent{ID: "ag-member", WorkspaceID: "ws-1", Name: "member"}
	require.NoError(t, settle.Settle(context.Background(), tk, agent,
		EngineOutput{Output: "x"}, nil))

	assert.Equal(t, 0, wake.calls, "非 leader_dispatch 任务不触发 wake")
}
