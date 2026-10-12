package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"

	orchapp "orkome/internal/orchestration/app"
)

type SkillSource interface {
	ListSkills(ctx context.Context, workspaceID, agentID string) ([]SkillMeta, error)

	GetSkill(ctx context.Context, workspaceID, agentID, name string) (string, error)
}

type SkillMeta struct {
	Name        string
	Description string
}

type skillBackend struct {
	src   SkillSource
	wsID  string
	agent orchapp.Agent
}

func (b *skillBackend) List(ctx context.Context) ([]einoskill.FrontMatter, error) {
	metas, err := b.src.ListSkills(ctx, b.wsID, b.agent.ID)
	if err != nil {
		return nil, err
	}
	out := make([]einoskill.FrontMatter, len(metas))
	for i, m := range metas {
		out[i] = einoskill.FrontMatter{Name: m.Name, Description: m.Description}
	}
	return out, nil
}

func (b *skillBackend) Get(ctx context.Context, name string) (einoskill.Skill, error) {
	content, err := b.src.GetSkill(ctx, b.wsID, b.agent.ID, name)
	if err != nil {
		// fail-soft（真模型 E2E 实证）：模型猜了不存在的 skill 名时，fatal 会
		// 杀死整个任务；改为返回可见失败 + 可用 skill 清单让模型自纠。
		// 仅 not-found 走此路，其余 DB 错误照常上抛。
		if strings.Contains(err.Error(), "not found") {
			metas, merr := b.src.ListSkills(ctx, b.wsID, b.agent.ID)
			if merr != nil || len(metas) == 0 {
				return einoskill.Skill{Content: fmt.Sprintf("skill %q not found: no skills are installed for this agent.", name)}, nil
			}
			names := make([]string, 0, len(metas))
			for _, m := range metas {
				names = append(names, m.Name)
			}
			return einoskill.Skill{Content: fmt.Sprintf("skill %q not found. Available skills: %s", name, strings.Join(names, ", "))}, nil
		}
		return einoskill.Skill{}, err
	}
	return einoskill.Skill{Content: content}, nil
}

func (e *EinoEngine) skillMiddleware(ctx context.Context, task orchapp.Task, agent orchapp.Agent) adk.ChatModelAgentMiddleware {
	if e.Skills == nil {
		return nil
	}
	mw, err := einoskill.NewMiddleware(ctx, &einoskill.Config{
		Backend: &skillBackend{src: e.Skills, wsID: task.WorkspaceID, agent: agent},
	})
	if err != nil {
		return nil
	}
	return mw
}
