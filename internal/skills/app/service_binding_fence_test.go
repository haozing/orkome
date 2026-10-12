package app

// FP-08 P1-4（IDOR）：agent-skill 绑定面五个方法此前收 ws 不用、repo 写面
// 无工作区限定——A 区成员持 B 区 agent uuid 即可为他区 agent 增删/启停技能。
// 回归锁定：跨区 agent / 外来 skill 一律 404 且零 repo 写调用（不泄露存在性），
// 同区合法路径不回归。

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

func TestAgentSkillBinding_CrossWorkspaceFence(t *testing.T) {
	repo := newFakeSkillsRepo()
	repo.agentsInWs["ws-1/ag-1"] = true
	repo.skills["sk-1"] = Skill{ID: "sk-1", WorkspaceID: "ws-1", Name: "local"}
	repo.skills["sk-foreign"] = Skill{ID: "sk-foreign", WorkspaceID: "ws-2", Name: "foreign"}
	svc := NewSkillService(repo)
	ctx := context.Background()

	notFound := func(err error) {
		require.Error(t, err)
		var ae *apierr.Error
		require.ErrorAs(t, err, &ae)
		assert.Equal(t, apierr.NotFound, ae.Code)
	}

	// 跨区 agent：五个写面一律 404，且不产生任何 repo 绑定写调用
	_, err := svc.ListAgentSkills(ctx, "ws-1", "ag-foreign")
	require.NoError(t, err, "读面保持原口径：ListAgentSkills 本就带 ws 限定")

	notFound(svc.SetAgentSkills(ctx, "ws-1", "ag-foreign", []string{"sk-1"}))
	notFound(svc.AddAgentSkill(ctx, "ws-1", "ag-foreign", "sk-1"))
	notFound(svc.SetAgentSkillEnabled(ctx, "ws-1", "ag-foreign", "sk-1", false))
	notFound(svc.RemoveAgentSkill(ctx, "ws-1", "ag-foreign", "sk-1"))
	assert.Empty(t, repo.bindingCalls, "跨区 agent 的绑定请求必须零写调用")

	// 同区 agent + 外来 skill：Set/Add 一并校验 skill 归属（此前 FK 只查存在性）
	notFound(svc.SetAgentSkills(ctx, "ws-1", "ag-1", []string{"sk-1", "sk-foreign"}))
	notFound(svc.AddAgentSkill(ctx, "ws-1", "ag-1", "sk-foreign"))
	assert.Empty(t, repo.bindingCalls, "外来 skill 的绑定请求必须零写调用")

	// 同区合法路径不回归：绑定/启停/替换/解绑全通且带 ws 到 repo
	require.NoError(t, svc.AddAgentSkill(ctx, "ws-1", "ag-1", "sk-1"))
	require.NoError(t, svc.SetAgentSkillEnabled(ctx, "ws-1", "ag-1", "sk-1", false))
	require.NoError(t, svc.SetAgentSkills(ctx, "ws-1", "ag-1", []string{"sk-1"}))
	require.NoError(t, svc.RemoveAgentSkill(ctx, "ws-1", "ag-1", "sk-1"))
	assert.Equal(t, []string{
		"add:ws-1/ag-1->sk-1",
		"enable:ws-1/ag-1->sk-1",
		"set:ws-1/ag-1",
		"remove:ws-1/ag-1->sk-1",
	}, repo.bindingCalls)
}
