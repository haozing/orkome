package app

// 助理接入（v4 §4.1/§6 第一刀）：CreateAgent API 的 runtime_kind 闸门。
// external 拒绝（退役第一刀提前到 W1）；assistant_proxy 不暴露给 API
//（内部行，惰性创建于 repo 层）。

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAgent_RuntimeKindGate(t *testing.T) {
	newSvc := func() (*AgentService, *agentAccessFakeRepo) {
		repo := newAgentAccessFakeRepo()
		return NewAgentService(repo, &recorderEmitter{}), repo
	}

	t.Run("external 被拒（退役第一刀）", func(t *testing.T) {
		svc, repo := newSvc()
		_, err := svc.Create(context.Background(), Agent{Name: "w", WorkspaceID: "ws", RuntimeKind: "external"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "retired")
		assert.Zero(t, repo.seq, "拒绝时不落库")
	})

	t.Run("assistant_proxy 不暴露给 API（内部行）", func(t *testing.T) {
		svc, repo := newSvc()
		_, err := svc.Create(context.Background(), Agent{Name: "w", WorkspaceID: "ws", RuntimeKind: "assistant_proxy"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "managed internally")
		assert.Zero(t, repo.seq)
	})

	t.Run("engine / 缺省照常", func(t *testing.T) {
		svc, repo := newSvc()
		a, err := svc.Create(context.Background(), Agent{Name: "w1", WorkspaceID: "ws"})
		require.NoError(t, err)
		assert.Equal(t, "engine", a.RuntimeKind, "缺省回填 engine")
		a2, err := svc.Create(context.Background(), Agent{Name: "w2", WorkspaceID: "ws", RuntimeKind: "engine"})
		require.NoError(t, err)
		assert.Equal(t, "engine", a2.RuntimeKind)
		assert.EqualValues(t, 2, repo.seq)
	})

	t.Run("未知 kind 拒绝文案更新", func(t *testing.T) {
		svc, _ := newSvc()
		_, err := svc.Create(context.Background(), Agent{Name: "w", WorkspaceID: "ws", RuntimeKind: "alien"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "engine or assistant_proxy")
	})
}
