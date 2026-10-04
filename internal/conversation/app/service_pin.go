package app

import (
	"context"
	"strings"
)

type PinnedAgent struct {
	AgentID  string `json:"agent_id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

type PinRepo interface {
	UpsertPinnedAgent(ctx context.Context, workspaceID, userID, agentID string) error
	ListPinnedAgents(ctx context.Context, workspaceID, userID string) ([]PinnedAgent, error)
	DeletePinnedAgent(ctx context.Context, workspaceID, userID, agentID string) error
}

type PinService struct{ repo PinRepo }

func NewPinService(repo PinRepo) *PinService { return &PinService{repo: repo} }

func (s *PinService) Add(ctx context.Context, workspaceID, userID, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return errValidation("agent_id required")
	}
	return s.repo.UpsertPinnedAgent(ctx, workspaceID, userID, agentID)
}

func (s *PinService) List(ctx context.Context, workspaceID, userID string) ([]PinnedAgent, error) {
	out, err := s.repo.ListPinnedAgents(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []PinnedAgent{}
	}
	return out, nil
}

func (s *PinService) Remove(ctx context.Context, workspaceID, userID, agentID string) error {
	return s.repo.DeletePinnedAgent(ctx, workspaceID, userID, agentID)
}
