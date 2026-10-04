package app

import (
	"context"
	"strings"
	"time"

	"orkome/internal/contract/apierr"
)

type Credential struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	CreatedAt time.Time `json:"created_at"`
}

type CredentialRepo interface {
	CreateCredential(ctx context.Context, workspaceID, provider, secret, createdBy string) (Credential, error)
	ListCredentials(ctx context.Context, workspaceID string) ([]Credential, error)
	DeleteCredential(ctx context.Context, workspaceID, id string) (bool, error)
}

type CredentialService struct {
	repo CredentialRepo
}

func NewCredentialService(repo CredentialRepo) *CredentialService {
	return &CredentialService{repo: repo}
}

func (s *CredentialService) Create(ctx context.Context, workspaceID, provider, secret, createdBy string) (*Credential, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" || len(provider) > 64 {
		return nil, apierr.NewValidation("invalid credential provider")
	}
	if secret == "" || len(secret) > 4096 {
		return nil, apierr.NewValidation("invalid credential secret")
	}
	c, err := s.repo.CreateCredential(ctx, workspaceID, provider, secret, createdBy)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *CredentialService) List(ctx context.Context, workspaceID string) ([]Credential, error) {
	list, err := s.repo.ListCredentials(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Credential{}
	}
	return list, nil
}

func (s *CredentialService) Delete(ctx context.Context, workspaceID, id string) error {
	ok, err := s.repo.DeleteCredential(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.New(apierr.NotFound, "credential not found")
	}
	return nil
}
