package app

// 助理接入（v4 §4）单测：claim 防重/代理行解析/结算推进/超时挂起交还。
// fake repo 风格参照 service_settle_notify_test.go（嵌入 Repo 接口零值 +
// 按需覆写）。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	"orkome/internal/orchestration/domain"
)

type assistantFakeRepo struct {
	Repo
	proxyID       string
	proxyCreated  int
	claim         func(in AssistantClaim) (Task, bool, error)
	getTask       Task
	getTaskOK     bool
	running       []Task
	wiStatus      map[string]string // workItemID → status_key
	appendMsg     int
	completeRun   int
	failRun       int
	stalledRows   []StalledAssistantTask
	stalledCalled int
}

func (f *assistantFakeRepo) EnsureAssistantProxyAgent(ctx context.Context, ws, userID string) (string, error) {
	f.proxyCreated++
	if f.proxyID == "" {
		f.proxyID = "proxy-1"
	}
	return f.proxyID, nil
}

func (f *assistantFakeRepo) GetAssistantProxyAgent(ctx context.Context, ws, userID string) (string, bool, error) {
	if f.proxyID == "" {
		return "", false, nil
	}
	return f.proxyID, true, nil
}

func (f *assistantFakeRepo) ClaimAssistantTask(ctx context.Context, in AssistantClaim) (Task, bool, error) {
	if f.claim != nil {
		return f.claim(in)
	}
	t := Task{ID: "t-1", WorkspaceID: in.WorkspaceID, AgentID: in.AgentID,
		WorkItemID: in.WorkItemID, Status: "running", MaxAttempts: 1,
		OriginatorSource: domain.SrcAssistantClaim, OriginatorUserID: in.UserID}
	f.getTask, f.getTaskOK = t, true
	return t, true, nil
}

func (f *assistantFakeRepo) GetTask(ctx context.Context, ws, id string) (Task, bool, error) {
	if f.getTask.ID == id && f.getTaskOK {
		return f.getTask, true, nil
	}
	return Task{}, false, nil
}

func (f *assistantFakeRepo) ListRunningByAgent(ctx context.Context, ws, agentID string) ([]Task, error) {
	return f.running, nil
}

func (f *assistantFakeRepo) GetTaskWorkItemStatus(ctx context.Context, ws, wi string) (string, bool, error) {
	k, ok := f.wiStatus[wi]
	return k, ok, nil
}

func (f *assistantFakeRepo) AppendTaskMessage(ctx context.Context, ws, taskID, kind, callID, content string, truncated bool) (int64, error) {
	f.appendMsg++
	return 1, nil
}

func (f *assistantFakeRepo) CompleteRun(ctx context.Context, id, result, sessionRef, engineTag, summary string, u UsageReport) error {
	f.completeRun++
	return nil
}

func (f *assistantFakeRepo) FailRun(ctx context.Context, id, reason, errMsg, summary string) (bool, error) {
	f.failRun++
	return false, nil // max_attempts=1 → 不排重试
}

func (f *assistantFakeRepo) LastTaskText(context.Context, string) (string, error) { return "", nil }

func (f *assistantFakeRepo) StallIdleAssistantTasks(ctx context.Context, hours int) ([]StalledAssistantTask, error) {
	f.stalledCalled++
	return f.stalledRows, nil
}

type assistantFakeStatus struct {
	updated    []string // "wi:statusKey" 记录
	done       string
	todo       string
	terminalOf func(key string) bool

	UpdateStatusByAgentErr error // 非 nil 时模拟推进失败（revision 冲突降级路径）
}

func (a *assistantFakeStatus) UpdateStatusByAgent(ctx context.Context, ws, wi, agentID, statusKey string) error {
	a.updated = append(a.updated, wi+":"+statusKey)
	return a.UpdateStatusByAgentErr
}

func (a *assistantFakeStatus) DoneStatusKey(ctx context.Context, ws string) (string, error) {
	return a.done, nil
}

func (a *assistantFakeStatus) TodoStatusKey(ctx context.Context, ws string) (string, error) {
	return a.todo, nil
}

func (a *assistantFakeStatus) IsTerminalStatus(ctx context.Context, ws, key string) (bool, error) {
	if a.terminalOf != nil {
		return a.terminalOf(key), nil
	}
	return false, nil
}

type assistantFakeNotifier struct{ notices int }

func (a *assistantFakeNotifier) WriteStalledNotice(ctx context.Context, ws, userID, wi, body string) error {
	a.notices++
	return nil
}

func newAssistantSvc(repo Repo, emit *recorderEmitter) (*AssistantTaskService, *assistantFakeStatus, *assistantFakeNotifier) {
	svc := NewAssistantTaskService(repo, emit)
	st := &assistantFakeStatus{done: "done", todo: "todo"}
	nt := &assistantFakeNotifier{}
	svc.SetStatusUpdater(st)
	svc.SetNotifier(nt)
	return svc, st, nt
}

func TestAssistant_ClaimWorkItem(t *testing.T) {
	repo := &assistantFakeRepo{}
	emit := &recorderEmitter{}
	svc, _, _ := newAssistantSvc(repo, emit)

	t.Run("认领成功：proxy 行惰性创建 + 任务 running + 事件携带来源", func(t *testing.T) {
		_, err := svc.ClaimWorkItem(context.Background(), "ws-1", "u-1", "wi-1")
		require.NoError(t, err)
		assert.Equal(t, 1, repo.proxyCreated, "proxy 行幂等创建恰好一次")
		running := 0
		for _, e := range emit.emitted {
			if e.Event == events.TaskRunning {
				running++
			}
		}
		assert.Equal(t, 1, running, "claim 直达 running（无 dispatched 中间态）")
	})

	t.Run("防重：repo 报冲突 → 409", func(t *testing.T) {
		dup := &assistantFakeRepo{claim: func(AssistantClaim) (Task, bool, error) {
			return Task{}, false, nil
		}}
		svc2, _, _ := newAssistantSvc(dup, &recorderEmitter{})
		_, err := svc2.ClaimWorkItem(context.Background(), "ws-1", "u-1", "wi-1")
		require.Error(t, err)
		var ae *apierr.Error
		require.True(t, errors.As(err, &ae))
		assert.Equal(t, apierr.Conflict, ae.Code)
	})

	t.Run("抢别人的单：repo 报 Forbidden 原样透出", func(t *testing.T) {
		forb := &assistantFakeRepo{claim: func(AssistantClaim) (Task, bool, error) {
			return Task{}, false, apierr.New(apierr.Forbidden, "work item is not assigned to you")
		}}
		svc2, _, _ := newAssistantSvc(forb, &recorderEmitter{})
		_, err := svc2.ClaimWorkItem(context.Background(), "ws-1", "u-1", "wi-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not assigned to you")
	})
}

func TestAssistant_CompleteWorkItem_AdvanceAndGuards(t *testing.T) {
	newCompleted := func() (*assistantFakeRepo, *recorderEmitter, *AssistantTaskService, *assistantFakeStatus) {
		repo := &assistantFakeRepo{getTask: Task{ID: "t-1", WorkspaceID: "ws-1", AgentID: "proxy-1",
			WorkItemID: "wi-1", Status: "running", OriginatorSource: domain.SrcAssistantClaim}, getTaskOK: true,
			wiStatus: map[string]string{"wi-1": "in_progress"}}
		emit := &recorderEmitter{}
		svc, st, _ := newAssistantSvc(repo, emit)
		svc.SetSettlement(nil) // 单测不接 outcome 层
		return repo, emit, svc, st
	}

	t.Run("结算 + 推进到 done 类", func(t *testing.T) {
		repo, _, svc, st := newCompleted()
		require.NoError(t, svc.CompleteWorkItem(context.Background(), "ws-1", "u-1", "t-1", "干完了"))
		assert.Equal(t, 1, repo.completeRun)
		assert.Equal(t, 1, repo.appendMsg, "result 落时间线")
		assert.Equal(t, []string{"wi-1:done"}, st.updated, "推进到 done status_key")
	})

	t.Run("工单已终态 → 只结算不推进", func(t *testing.T) {
		repo, _, svc, st := newCompleted()
		repo.wiStatus["wi-1"] = "done"
		st.terminalOf = func(key string) bool { return key == "done" || key == "cancelled" }
		require.NoError(t, svc.CompleteWorkItem(context.Background(), "ws-1", "u-1", "t-1", "补充说明"))
		assert.Empty(t, st.updated, "终态不越权覆盖")
		assert.Equal(t, 1, repo.completeRun, "结算照常")
	})

	t.Run("revision 冲突 → 结算成功、推进降级放弃", func(t *testing.T) {
		repo, _, svc, st := newCompleted()
		st.UpdateStatusByAgentErr = apierr.New(apierr.Conflict, "revision conflict")
		require.NoError(t, svc.CompleteWorkItem(context.Background(), "ws-1", "u-1", "t-1", "result"))
		assert.Equal(t, 1, repo.completeRun, "结算已成功")
		assert.Len(t, st.updated, 1, "推进尝试了一次后放弃（不重试）")
	})

	t.Run("别人的任务 / 非 running → guard 403/409", func(t *testing.T) {
		repo, _, svc, _ := newCompleted()
		err := svc.CompleteWorkItem(context.Background(), "ws-1", "u-1", "t-other", "x")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		repo.getTask.Status = "completed"
		err = svc.CompleteWorkItem(context.Background(), "ws-1", "u-1", "t-1", "x")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not running")
	})
}

func TestAssistant_FailWorkItem_NoRetry(t *testing.T) {
	repo := &assistantFakeRepo{getTask: Task{ID: "t-1", WorkspaceID: "ws-1", AgentID: "proxy-1",
		WorkItemID: "wi-1", Status: "running", MaxAttempts: 1, Attempt: 1}, getTaskOK: true}
	svc, _, _ := newAssistantSvc(repo, &recorderEmitter{})
	require.NoError(t, svc.FailWorkItem(context.Background(), "ws-1", "u-1", "t-1", "blocked", "缺材料"))
	assert.Equal(t, 1, repo.failRun)
}

func TestAssistant_StallExpired_HandBack(t *testing.T) {
	t.Run("挂起 → 通知 + 工单回待处理；终态工单不动", func(t *testing.T) {
		repo := &assistantFakeRepo{stalledRows: []StalledAssistantTask{
			{ID: "t-1", WorkspaceID: "ws-1", AgentID: "proxy-1", WorkItemID: "wi-1", OriginatorUserID: "u-1"},
			{ID: "t-2", WorkspaceID: "ws-1", AgentID: "proxy-1", WorkItemID: "wi-2", OriginatorUserID: "u-1"},
		}, wiStatus: map[string]string{"wi-1": "in_progress", "wi-2": "cancelled"}}
		svc, st, nt := newAssistantSvc(repo, &recorderEmitter{})
		st.terminalOf = func(key string) bool { return key == "cancelled" || key == "done" }
		n, err := svc.StallExpired(context.Background(), 4)
		require.NoError(t, err)
		assert.Equal(t, 2, n)
		assert.Equal(t, 2, nt.notices, "每个挂起行一条交还通知")
		assert.Equal(t, []string{"wi-1:todo"}, st.updated, "非终态回待处理；终态 wi-2 不动")
	})

	t.Run("已待处理的工单不重复推", func(t *testing.T) {
		repo := &assistantFakeRepo{stalledRows: []StalledAssistantTask{
			{ID: "t-1", WorkspaceID: "ws-1", AgentID: "proxy-1", WorkItemID: "wi-1", OriginatorUserID: "u-1"},
		}, wiStatus: map[string]string{"wi-1": "todo"}}
		svc, st, _ := newAssistantSvc(repo, &recorderEmitter{})
		_, err := svc.StallExpired(context.Background(), 4)
		require.NoError(t, err)
		assert.Empty(t, st.updated, "todo==cur 不动")
	})
}

func TestAssistant_ListRunningTasks_NoProxyRow(t *testing.T) {
	repo := &assistantFakeRepo{} // proxyID 空 → 未开过接单
	svc, _, _ := newAssistantSvc(repo, &recorderEmitter{})
	ts, err := svc.ListRunningTasks(context.Background(), "ws-1", "u-1")
	require.NoError(t, err)
	assert.Empty(t, ts, "无代理行 = 无在跑任务，不建行")
}

func TestAssistant_SrcAssistantClaimIs(t *testing.T) {
	assert.True(t, SrcAssistantClaimIs("assistant_claim"))
	assert.False(t, SrcAssistantClaimIs("direct_human"))
	assert.True(t, SrcAssistantClaimIs(domain.SrcAssistantClaim))
	_ = strings.TrimSpace
}
