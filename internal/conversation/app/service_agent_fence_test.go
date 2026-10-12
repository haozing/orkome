package app

// FP-14 P1-1/P1-2/P1-3 回归：
//   - CreateSession 前置 agent 存在性 + 归档校验（不存在→400、已归档→409；
//     此前 FK 23503 裸 500 / 归档 agent 可建会话后首条消息必 500 丢输入）
//   - POST /read 归属 fence（creator 口径，与 mustOwnSession 一致；此前任意
//     UUID 一律 200，跨工作区可标他人会话已读）

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

func TestCreateSession_AgentStateGate(t *testing.T) {
	ctx := context.Background()

	// 不存在 → 400 E_VALIDATION，且不落会话
	repo := newFakeChatRepo()
	_, err := NewChatService(repo, nil).CreateSession(ctx, "ws-1", "ag-missing", "u-1", "")
	require.Error(t, err)
	var coder interface{ Code() string }
	require.ErrorAs(t, err, &coder)
	assert.Equal(t, "E_VALIDATION", coder.Code())
	assert.Empty(t, repo.sessions, "agent 缺失时不得建会话")

	// 已归档 → 409 Conflict，且不落会话
	repo = newFakeChatRepo()
	repo.agentStates["ws-1/ag-archived"] = true
	_, err = NewChatService(repo, nil).CreateSession(ctx, "ws-1", "ag-archived", "u-1", "")
	require.Error(t, err)
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Conflict, ae.Code)
	assert.Empty(t, repo.sessions, "已归档 agent 不得建会话（堵住 P1-1 的必然踩雷链）")

	// 正常 agent → 201 语义（合法路径不回归）
	repo = newFakeChatRepo()
	repo.agentStates["ws-1/ag-1"] = false
	sess, err := NewChatService(repo, nil).CreateSession(ctx, "ws-1", "ag-1", "u-1", "hello")
	require.NoError(t, err)
	assert.Equal(t, "sess1", sess.ID)
}

func TestMarkRead_OwnershipFence(t *testing.T) {
	ctx := context.Background()
	newFixture := func() (*fakeChatRepo, *ChatService) {
		repo := newFakeChatRepo()
		repo.agentStates["ws-1/ag-1"] = false
		svc := NewChatService(repo, nil)
		_, err := svc.CreateSession(ctx, "ws-1", "ag-1", "u-creator", "s")
		require.NoError(t, err)
		return repo, svc
	}

	// creator 本人 → 200 语义
	repo, svc := newFixture()
	require.NoError(t, svc.MarkRead(ctx, "ws-1", "sess1", "u-creator"))
	assert.Equal(t, []string{"ws-1/sess1/u-creator"}, repo.readMarks, "repo 调用带 ws+creator 口径")

	// 同工作区非 creator → 404（严格者口径：creator 单主模型）
	_, svc = newFixture()
	err := svc.MarkRead(ctx, "ws-1", "sess1", "u-other-member")
	require.Error(t, err)
	var coder interface{ Code() string }
	require.ErrorAs(t, err, &coder)
	assert.Equal(t, "E_NOT_FOUND", coder.Code())

	// 随机不存在的会话 → 404（此前任意 UUID 一律 200）
	_, svc = newFixture()
	err = svc.MarkRead(ctx, "ws-1", "0b6fbf6a-7b3d-4a5e-9d1f-2c3d4e5f6a7b", "u-anyone")
	require.Error(t, err)
	require.ErrorAs(t, err, &coder)
	assert.Equal(t, "E_NOT_FOUND", coder.Code())

	// 跨工作区（会话不在该 ws 下）→ 404
	_, svc = newFixture()
	err = svc.MarkRead(ctx, "ws-other", "sess1", "u-creator")
	require.Error(t, err)
	require.ErrorAs(t, err, &coder)
	assert.Equal(t, "E_NOT_FOUND", coder.Code())
}
