package engine

import (
	"context"

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
