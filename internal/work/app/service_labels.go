package app

import (
	"context"
	"strings"

	"orkome/internal/contract/apierr"
)

func (s *CatalogService) ListAgentLabels(ctx context.Context, workspaceID, agentID string) ([]Label, error) {
	return s.repo.ListAgentLabels(ctx, workspaceID, agentID)
}

func (s *CatalogService) AttachAgentLabel(ctx context.Context, workspaceID, agentID, labelID string) ([]Label, error) {
	ok, err := s.repo.AgentLabelTarget(ctx, workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "agent not found")
	}
	if _, ok, err := s.repo.GetLabel(ctx, workspaceID, labelID); err != nil {
		return nil, err
	} else if !ok {
		return nil, apierr.New(apierr.NotFound, "label not found")
	}
	if err := s.repo.AttachAgentLabel(ctx, workspaceID, agentID, labelID); err != nil {
		return nil, err
	}
	return s.repo.ListAgentLabels(ctx, workspaceID, agentID)
}

func (s *CatalogService) DetachAgentLabel(ctx context.Context, workspaceID, agentID, labelID string) ([]Label, error) {
	if err := s.repo.DetachAgentLabel(ctx, workspaceID, agentID, labelID); err != nil {
		return nil, err
	}
	return s.repo.ListAgentLabels(ctx, workspaceID, agentID)
}

func (s *CatalogService) ListSkillLabels(ctx context.Context, workspaceID, skillID string) ([]Label, error) {
	return s.repo.ListSkillLabels(ctx, workspaceID, skillID)
}

func (s *CatalogService) AttachSkillLabel(ctx context.Context, workspaceID, skillID, labelID string) ([]Label, error) {
	ok, err := s.repo.SkillLabelTarget(ctx, workspaceID, skillID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "skill not found")
	}
	if _, ok, err := s.repo.GetLabel(ctx, workspaceID, labelID); err != nil {
		return nil, err
	} else if !ok {
		return nil, apierr.New(apierr.NotFound, "label not found")
	}
	if err := s.repo.AttachSkillLabel(ctx, workspaceID, skillID, labelID); err != nil {
		return nil, err
	}
	return s.repo.ListSkillLabels(ctx, workspaceID, skillID)
}

func (s *CatalogService) DetachSkillLabel(ctx context.Context, workspaceID, skillID, labelID string) ([]Label, error) {
	if err := s.repo.DetachSkillLabel(ctx, workspaceID, skillID, labelID); err != nil {
		return nil, err
	}
	return s.repo.ListSkillLabels(ctx, workspaceID, skillID)
}

type ClientUsageService struct {
	repo Repo
}

func NewClientUsageService(repo Repo) *ClientUsageService { return &ClientUsageService{repo: repo} }

var knownClientOS = map[string]bool{
	"macos": true, "windows": true, "linux": true, "ios": true, "android": true, "chromeos": true,
}

func (s *ClientUsageService) Report(ctx context.Context, in ClientUsageReport) error {
	in.ClientType = strings.ToLower(strings.TrimSpace(in.ClientType))
	if in.ClientType != "web" && in.ClientType != "desktop" {
		return apierr.NewValidation("client platform must be web or desktop")
	}
	if in.InstallID == "" {
		return apierr.NewValidation("install_id required")
	}

	if !isUUID(in.InstallID) {
		return apierr.NewValidation("install_id must be a uuid",
			apierr.FieldError{Field: "install_id", Problem: "invalid uuid format"})
	}
	in.ClientVersion = strings.TrimSpace(in.ClientVersion)
	if in.ClientVersion == "" {
		in.ClientVersion = "unknown"
	}
	if len(in.ClientVersion) > 64 || !isPrintableASCII(in.ClientVersion) {
		return apierr.NewValidation("invalid client version")
	}
	in.OS = strings.ToLower(strings.TrimSpace(in.OS))
	if !knownClientOS[in.OS] {
		in.OS = "unknown"
	}
	return s.repo.ClientUsageUpsert(ctx, in)
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
