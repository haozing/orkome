package main

import (
	"context"

	"orkome/internal/orchestration/adapters/engine"
	"github.com/haozing/ploykit/platform/pg"
	skillhttp "orkome/internal/skills/adapters/http"
	skillpgrepo "orkome/internal/skills/adapters/pgrepo"
	skillapp "orkome/internal/skills/app"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
)

func wiringSkills(db *pg.DB, cfg *Config, memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)) skillhttp.Deps {
	repo := skillpgrepo.New(db)
	wRepo := &workpgrepo.Repo{DB: db}

	imp := skillapp.NewImportServiceWithOptions(repo, skillapp.ImportOptions{
		GitHubToken:     cfg.GitHubToken,
		SkillsShEnabled: cfg.SkillsShIntegrationEnabled,
	})
	return skillhttp.Deps{
		Skills:      skillapp.NewSkillService(repo),
		Import:      imp,
		MemberCheck: memberCheck,

		SkillLabels: skillhttp.SkillLabelEndpoints{
			List: func(ctx context.Context, ws, skillID string) ([]skillhttp.SkillLabelView, error) {
				return skillLabelViewsOf(wRepo.ListSkillLabels(ctx, ws, skillID))
			},
			Attach: func(ctx context.Context, ws, skillID, labelID string) ([]skillhttp.SkillLabelView, error) {
				if err := wRepo.AttachSkillLabel(ctx, ws, skillID, labelID); err != nil {
					return nil, err
				}
				return skillLabelViewsOf(wRepo.ListSkillLabels(ctx, ws, skillID))
			},
			Detach: func(ctx context.Context, ws, skillID, labelID string) ([]skillhttp.SkillLabelView, error) {
				if err := wRepo.DetachSkillLabel(ctx, ws, skillID, labelID); err != nil {
					return nil, err
				}
				return skillLabelViewsOf(wRepo.ListSkillLabels(ctx, ws, skillID))
			},
		},
	}
}

func skillLabelViewsOf(ls []workapp.Label, err error) ([]skillhttp.SkillLabelView, error) {
	if err != nil {
		return nil, err
	}
	out := make([]skillhttp.SkillLabelView, 0, len(ls))
	for _, l := range ls {
		out = append(out, skillhttp.SkillLabelView{ID: l.ID, Name: l.Name, Color: l.Color})
	}
	return out, nil
}

type skillSource struct{ repo *skillpgrepo.Repo }

func (s skillSource) ListSkills(ctx context.Context, ws, agentID string) ([]engine.SkillMeta, error) {
	bound, err := s.repo.ListAgentSkills(ctx, ws, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]engine.SkillMeta, 0, len(bound))
	for _, b := range bound {
		if b.Enabled {
			out = append(out, engine.SkillMeta{Name: b.Name, Description: b.Description})
		}
	}
	return out, nil
}

func (s skillSource) GetSkill(ctx context.Context, ws, agentID, name string) (string, error) {

	return skillapp.NewSkillService(s.repo).MainByName(ctx, ws, name)
}

func skillRepoOf(db *pg.DB) *skillpgrepo.Repo { return skillpgrepo.New(db) }

func skillSourceOf(repo *skillpgrepo.Repo) engine.SkillSource { return skillSource{repo: repo} }
