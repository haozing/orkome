package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 批次终态唤醒创建者 agent（batch v2.0 设计 §2.2）：绕过条件规则直接建
// 收割任务；建任务机制 = wakeup 同款 TaskEnqueuer（MergeIntoPending 去重
// 在实现方）。

func TestWakeAgentForBatch_EnqueuesHarvestTask(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, &fakeFacts{})

	err := svc.WakeAgentForBatch(context.Background(), "ws-1", "wi-1", "ag-1", "task-origin",
		`Batch b-1 terminal. Summary: {"total":500,"succeeded":497}.`)
	require.NoError(t, err)
	assert.Equal(t, 1, enq.calls)
	require.Len(t, enq.agents, 1)
	assert.Equal(t, "ag-1", enq.agents[0])
}

func TestWakeAgentForBatch_EmptyAgentIsNoop(t *testing.T) {
	// 面板/第三方通道：无创建者 agent → 跳过（走现有 condition 扫描），
	// 不算错误、不触 enqueuer。
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(newFakeRepo(), enq, &fakeFacts{})

	err := svc.WakeAgentForBatch(context.Background(), "ws-1", "wi-1", "", "task-origin", "evidence")
	require.NoError(t, err)
	assert.Equal(t, 0, enq.calls)
}

func TestWakeAgentForBatch_EnqueuerErrorPropagates(t *testing.T) {
	enq := &fakeEnqueuer{err: errors.New("insert failed")}
	svc := newTestWakeupService(newFakeRepo(), enq, &fakeFacts{})

	err := svc.WakeAgentForBatch(context.Background(), "ws-1", "wi-1", "ag-1", "", "evidence")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert failed")
}

func TestWakeAgentForBatch_EvidenceCapped(t *testing.T) {
	// 巨幅 evidence（如 500 条对账原文误传入）被 CapEvidence 截断，不灌爆
	// 任务上下文。
	var got string
	repo := newFakeRepo()
	enq := &evidenceSpyEnqueuer{fakeEnqueuer: fakeEnqueuer{}, capture: &got}
	svc := &WakeupService{repo: repo, enq: enq, facts: fakeFacts{}, now: func() time.Time { return testNow }}

	big := strings.Repeat("x", 100_000)
	err := svc.WakeAgentForBatch(context.Background(), "ws-1", "wi-1", "ag-1", "", big)
	require.NoError(t, err)
	assert.Less(t, len(got), 100_000)
	assert.Contains(t, got, "evidence truncated")
}

// evidenceSpyEnqueuer 捕获 evidence 串（fakeEnqueuer 不落 evidence），
// 复用 fakeEnqueuer 的其余行为。
type evidenceSpyEnqueuer struct {
	fakeEnqueuer
	capture *string
}

func (e *evidenceSpyEnqueuer) EnqueueForWakeup(_ context.Context, _, _, _, _, evidence string) (string, bool, error) {
	*e.capture = evidence
	return "task-cap", false, nil
}
