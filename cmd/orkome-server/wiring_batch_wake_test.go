package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

// originatorWaker 适配器（批次终态 → automation WakeAgentForBatch）行为：
// evidence 组装、无 agent 通道跳过、错误透传给调用方（BatchService 忽略，
// 但直调方如测试/面板可感知）。

type fakeWakePort struct {
	notifyCalls int
	wakeCalls   int
	ws, wi      string
	agentID     string
	taskID      string
	evidence    string
	err         error
}

func (f *fakeWakePort) NotifyBatchDone(context.Context, string, string, string) error {
	f.notifyCalls++
	return nil
}

func (f *fakeWakePort) WakeAgentForBatch(_ context.Context, ws, wi, agentID, taskID, evidence string) error {
	f.wakeCalls++
	f.ws, f.wi, f.agentID, f.taskID, f.evidence = ws, wi, agentID, taskID, evidence
	return f.err
}

func TestOriginatorWaker_BuildsEvidence(t *testing.T) {
	port := &fakeWakePort{}
	w := originatorWaker{wake: port}

	err := w.WakeBatchOriginator(context.Background(), "ws-1", "wi-1", "b-1", "task-origin", "ag-1",
		map[string]any{"batch_id": "b-1", "kind": "mcp_tool", "final_status": "completed",
			"total": 500, "done": 500, "succeeded": 497, "failed": 0, "skipped": 3})
	require.NoError(t, err)
	assert.Equal(t, 1, port.wakeCalls)
	assert.Equal(t, "ws-1", port.ws)
	assert.Equal(t, "wi-1", port.wi)
	assert.Equal(t, "ag-1", port.agentID)
	assert.Equal(t, "task-origin", port.taskID)
	assert.Contains(t, port.evidence, "Batch b-1 terminal")
	assert.Contains(t, port.evidence, `"total":500`)
	assert.Contains(t, port.evidence, "batch_status/batch_results")
	assert.Equal(t, 0, port.notifyCalls)
}

func TestOriginatorWaker_EmptyAgentSkips(t *testing.T) {
	// 面板/第三方通道：无创建者 agent → 不触 enqueuer（condition 扫描兜底）。
	port := &fakeWakePort{}
	w := originatorWaker{wake: port}

	err := w.WakeBatchOriginator(context.Background(), "ws-1", "wi-1", "b-1", "", "", nil)
	require.NoError(t, err)
	assert.Equal(t, 0, port.wakeCalls)
}

func TestOriginatorWaker_ErrorPropagates(t *testing.T) {
	port := &fakeWakePort{err: errors.New("db down")}
	w := originatorWaker{wake: port}

	err := w.WakeBatchOriginator(context.Background(), "ws-1", "wi-1", "b-1", "t", "ag-1", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

func TestWiringBatch_NilWakeKeepsWakerAbsent(t *testing.T) {
	// wake 未接线（部分测试装配）→ waker 整体缺席，不 panic。
	batches := orchapp.NewBatchService(nil, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bw := wiringBatch(log, nil, batches, nil, nil)
	require.NotNil(t, bw)
}
