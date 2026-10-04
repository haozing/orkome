package app

import (
	"context"
	"time"

	"orkome/internal/contract/apierr"
)

type BuilderDraft struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	UserID      string         `json:"user_id"`
	Title       string         `json:"title"`
	Draft       map[string]any `json:"draft"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type BuilderDraftRepo interface {
	InsertBuilderDraft(ctx context.Context, d BuilderDraft) (BuilderDraft, error)
	GetBuilderDraft(ctx context.Context, workspaceID, id string) (BuilderDraft, bool, error)
	ListBuilderDrafts(ctx context.Context, workspaceID, userID string) ([]BuilderDraft, error)

	UpsertBuilderDraft(ctx context.Context, d BuilderDraft) (BuilderDraft, bool, error)
	DeleteBuilderDraft(ctx context.Context, workspaceID, id, userID string) error
}

type AgentBuilderService struct {
	repo BuilderDraftRepo
}

func NewAgentBuilderService(repo BuilderDraftRepo) *AgentBuilderService {
	return &AgentBuilderService{repo: repo}
}

func (s *AgentBuilderService) Create(ctx context.Context, d BuilderDraft) (*BuilderDraft, error) {
	if len(d.Draft) == 0 {
		d.Draft = map[string]any{}
	}
	out, err := s.repo.InsertBuilderDraft(ctx, d)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *AgentBuilderService) Get(ctx context.Context, workspaceID, id string) (*BuilderDraft, error) {
	d, ok, err := s.repo.GetBuilderDraft(ctx, workspaceID, id)
	if err != nil || !ok {
		return nil, apierr.New(apierr.NotFound, "draft not found")
	}
	return &d, nil
}

func (s *AgentBuilderService) List(ctx context.Context, workspaceID, userID string) ([]BuilderDraft, error) {
	return s.repo.ListBuilderDrafts(ctx, workspaceID, userID)
}

func (s *AgentBuilderService) UpsertDraft(ctx context.Context, d BuilderDraft) (*BuilderDraft, error) {
	if len(d.Draft) == 0 {
		d.Draft = map[string]any{}
	}
	out, ok, err := s.repo.UpsertBuilderDraft(ctx, d)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "draft not found")
	}
	return &out, nil
}

func (s *AgentBuilderService) Delete(ctx context.Context, workspaceID, id, userID string) error {
	return s.repo.DeleteBuilderDraft(ctx, workspaceID, id, userID)
}
