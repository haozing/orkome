package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/orchestration/domain"
)

// 批次终态 → 创建者 agent 唤醒注入点（batch v2.0 设计 §2.2）的 app 级
// 行为测试：用 fake repo + fake processor 驱动 RunWorker 走完最后一条
// item，验证 waker 被调用（终态参数 + 四态计数摘要）与各跳过分支。

type fakeBatchRepo struct {
	BatchRepo
	b           Batch
	items       []BatchItem
	claimedAll  bool
	finished    []string // itemID:status
	completedFn string   // final status passed to CompleteIfAllDone
}

func (r *fakeBatchRepo) GetBatch(_ context.Context, _ string, _ string) (Batch, bool, error) {
	return r.b, true, nil
}

func (r *fakeBatchRepo) ClaimItems(_ context.Context, _ string, _ string, limit int) ([]BatchItem, error) {
	if r.claimedAll {
		return nil, nil
	}
	r.claimedAll = true
	out := make([]BatchItem, 0, limit)
	for _, it := range r.items {
		if len(out) >= limit {
			break
		}
		out = append(out, it)
	}
	return out, nil
}

func (r *fakeBatchRepo) FinishItem(_ context.Context, itemID, status string, _ map[string]any, _ string) (bool, error) {
	r.finished = append(r.finished, itemID+":"+status)
	// 假设单批全部收口 → 返回 done
	return true, nil
}

func (r *fakeBatchRepo) CompleteIfAllDone(_ context.Context, _ string, final string) (bool, error) {
	r.completedFn = final
	return true, nil
}

func (r *fakeBatchRepo) GetBatchCounts(context.Context, string) (int, int, int, int, error) {
	return 0, 0, 0, len(r.finished), nil
}

type okProcessor struct{}

func (okProcessor) Kind() string { return "test" }

func (okProcessor) Process(context.Context, map[string]any, map[string]any) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}

type fakeWaker struct {
	calls   int
	ws, wi  string
	batchID string
	taskID  string
	agentID string
	summary map[string]any
	err     error
}

func (w *fakeWaker) WakeBatchOriginator(_ context.Context, ws, wi, batchID, taskID, agentID string, summary map[string]any) error {
	w.calls++
	w.ws, w.wi, w.batchID, w.taskID, w.agentID, w.summary = ws, wi, batchID, taskID, agentID, summary
	return w.err
}

func terminalBatch() (Batch, []BatchItem) {
	b := Batch{ID: "b-9", WorkspaceID: "ws-1", WorkItemID: "wi-1", Kind: "mcp_tool",
		Status: domain.BatchRunning, Total: 2, Concurrency: 4, ChunkSize: 100,
		OriginatorTaskID: "task-origin", OriginatorAgentID: "ag-1"}
	items := []BatchItem{
		{ID: "i-1", BatchID: "b-9", Seq: 1, PayloadRef: map[string]any{"x": 1}, MaxAttempts: 3},
		{ID: "i-2", BatchID: "b-9", Seq: 2, PayloadRef: map[string]any{"x": 2}, MaxAttempts: 3},
	}
	return b, items
}

func TestRunWorker_WakesOriginatorOnTerminal(t *testing.T) {
	b, items := terminalBatch()
	repo := &fakeBatchRepo{b: b, items: items}
	svc := NewBatchService(repo, nil)
	waker := &fakeWaker{}
	svc.SetOriginatorWaker(waker)

	n, err := svc.RunWorker(context.Background(), b, okProcessor{}, "w-1", 10)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.GreaterOrEqual(t, waker.calls, 1, "woken per CompleteIfAllDone win (fake repo wins per item)")
	assert.Equal(t, "ws-1", waker.ws)
	assert.Equal(t, "wi-1", waker.wi)
	assert.Equal(t, "b-9", waker.batchID)
	assert.Equal(t, "task-origin", waker.taskID)
	assert.Equal(t, "ag-1", waker.agentID)
	require.NotNil(t, waker.summary)
	assert.Equal(t, "b-9", waker.summary["batch_id"])
	assert.Equal(t, "mcp_tool", waker.summary["kind"])
	assert.Equal(t, domain.BatchCompleted, waker.summary["final_status"])
	assert.Equal(t, 2, waker.summary["total"])
	assert.Equal(t, domain.BatchCompleted, repo.completedFn)
}

func TestRunWorker_NoWakerIsNoop(t *testing.T) {
	// 未接线（nil）= 跳过唤醒，行为与接线前一致。
	b, items := terminalBatch()
	repo := &fakeBatchRepo{b: b, items: items}
	svc := NewBatchService(repo, nil)

	n, err := svc.RunWorker(context.Background(), b, okProcessor{}, "w-1", 10)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, domain.BatchCompleted, repo.completedFn) // 终态转移照常
}

func TestRunWorker_WakeErrorDoesNotBlock(t *testing.T) {
	// 唤醒失败不阻断 item 循环、不影响终态转移。
	b, items := terminalBatch()
	repo := &fakeBatchRepo{b: b, items: items}
	svc := NewBatchService(repo, nil)
	svc.SetOriginatorWaker(&fakeWaker{err: errors.New("automation down")})

	n, err := svc.RunWorker(context.Background(), b, okProcessor{}, "w-1", 10)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, domain.BatchCompleted, repo.completedFn)
}
