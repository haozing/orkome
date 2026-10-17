package app

// visibility 枚举闸：非法值必须在服务层 422，不能穿透到库层撞迁移 1003
// 的 CHECK 约束裸 500（生产实证：POST /api/agents visibility="workspace"）。

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAgent_VisibilityGate(t *testing.T) {
	t.Run("非法 visibility 被拒且不落库", func(t *testing.T) {
		repo := newAgentAccessFakeRepo()
		svc := NewAgentService(repo, &recorderEmitter{})
		_, err := svc.Create(context.Background(), Agent{Name: "w", WorkspaceID: "ws", Visibility: "workspace"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "visibility must be one of")
		assert.Zero(t, repo.seq, "拒绝时不落库")
	})

	t.Run("三个合法值照常", func(t *testing.T) {
		svc := NewAgentService(newAgentAccessFakeRepo(), &recorderEmitter{})
		for i, v := range []string{"public", "private", "public_to"} {
			a, err := svc.Create(context.Background(), Agent{Name: "w" + string(rune('a'+i)), WorkspaceID: "ws", Visibility: v})
			require.NoError(t, err)
			assert.Equal(t, v, a.Visibility)
		}
	})

	t.Run("缺省回填 public", func(t *testing.T) {
		svc := NewAgentService(newAgentAccessFakeRepo(), &recorderEmitter{})
		a, err := svc.Create(context.Background(), Agent{Name: "w", WorkspaceID: "ws"})
		require.NoError(t, err)
		assert.Equal(t, "public", a.Visibility)
	})

	t.Run("Update 同样受闸", func(t *testing.T) {
		repo := newAgentAccessFakeRepo()
		svc := NewAgentService(repo, &recorderEmitter{})
		a, err := svc.Create(context.Background(), Agent{Name: "w", WorkspaceID: "ws"})
		require.NoError(t, err)
		upd := *a
		upd.Visibility = "workspace"
		_, err = svc.Update(context.Background(), upd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "visibility must be one of")
	})
}
