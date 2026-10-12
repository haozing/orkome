package app

// FP-14 P1-1：agent unavailable 裸 error 分类回归。此前 GetAgent 不存在/
// 已归档一律 errors.New("agent unavailable") → writeErr 兜底 500 且服务端
// 无日志。现在：不存在 → 400 E_VALIDATION；已归档 → 409 E_REVISION_CONFLICT
//（请求合法而 agent 生命周期状态冲突，语义比 400 贴切；本仓 409 通用码）。

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

type enqueueAgentFakeRepo struct {
	Repo
	agent Agent
	found bool

	inserted int
}

func (f *enqueueAgentFakeRepo) GetAgent(context.Context, string, string) (Agent, bool, error) {
	return f.agent, f.found, nil
}

func (f *enqueueAgentFakeRepo) InsertChatTask(_ context.Context, t Task) (Task, error) {
	f.inserted++
	t.ID = "task-new"
	return t, nil
}

func assertAgentStateErr(t *testing.T, err error, wantCode string) {
	t.Helper()
	require.Error(t, err)
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, wantCode, ae.Code)
}

func TestEnqueueChat_AgentStateClassified(t *testing.T) {
	newRepo := func() *enqueueAgentFakeRepo {
		return &enqueueAgentFakeRepo{found: true, agent: Agent{ID: "ag-1", Archived: false}}
	}
	ctx := context.Background()

	// 不存在 → 400，不落任务
	repo := newRepo()
	repo.found = false
	_, err := NewEnqueueService(repo, &recorderEmitter{}).EnqueueChat(ctx, "ws-1", "sess-1", "ag-missing", "u-1", "hi")
	assertAgentStateErr(t, err, apierr.Validation)
	assert.Zero(t, repo.inserted, "agent 缺失时不得落任务")

	// 已归档 → 409，不落任务
	repo = newRepo()
	repo.agent.Archived = true
	_, err = NewEnqueueService(repo, &recorderEmitter{}).EnqueueChat(ctx, "ws-1", "sess-1", "ag-1", "u-1", "hi")
	assertAgentStateErr(t, err, apierr.Conflict)
	assert.Zero(t, repo.inserted, "agent 已归档时不得落任务")

	// 正常 agent → 任务落库（合法路径不回归）
	repo = newRepo()
	taskID, err := NewEnqueueService(repo, &recorderEmitter{}).EnqueueChat(ctx, "ws-1", "sess-1", "ag-1", "u-1", "hi")
	require.NoError(t, err)
	assert.Equal(t, "task-new", taskID)
	assert.Equal(t, 1, repo.inserted)
}

func TestEnqueueChatInTx_AgentStateClassified(t *testing.T) {
	newRepo := func() *enqueueAgentFakeRepo {
		return &enqueueAgentFakeRepo{found: true, agent: Agent{ID: "ag-1", Archived: false}}
	}
	ctx := context.Background()

	repo := newRepo()
	repo.found = false
	_, err := NewEnqueueService(repo, &recorderEmitter{}).EnqueueChatInTx(ctx, "ws-1", "sess-1", "ag-missing", "u-1", "hi")
	assertAgentStateErr(t, err, apierr.Validation)
	assert.Zero(t, repo.inserted)

	repo = newRepo()
	repo.agent.Archived = true
	_, err = NewEnqueueService(repo, &recorderEmitter{}).EnqueueChatInTx(ctx, "ws-1", "sess-1", "ag-1", "u-1", "hi")
	assertAgentStateErr(t, err, apierr.Conflict)
	assert.Zero(t, repo.inserted, "事务路径同样不得落任务（此前 500 且输入丢失）")

	repo = newRepo()
	taskID, err := NewEnqueueService(repo, &recorderEmitter{}).EnqueueChatInTx(ctx, "ws-1", "sess-1", "ag-1", "u-1", "hi")
	require.NoError(t, err)
	assert.Equal(t, "task-new", taskID)
	assert.Equal(t, 1, repo.inserted)
}
