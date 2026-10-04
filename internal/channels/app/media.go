package app

import (
	"context"
	"time"

	"orkome/internal/channels/domain"
)

type MediaService struct{ repo Repo }

func NewMediaService(repo Repo) *MediaService { return &MediaService{repo: repo} }

type MediaIntentInput struct {
	StorageKey     string
	WorkspaceID    string
	StorageURL     string
	InstallationID string
}

func (s *MediaService) RecordIntents(ctx context.Context, inputs []MediaIntentInput) error {
	if len(inputs) == 0 {
		return nil
	}
	intents := make([]MediaIntent, len(inputs))
	for i, in := range inputs {
		intents[i] = MediaIntent{
			StorageKey:     in.StorageKey,
			WorkspaceID:    in.WorkspaceID,
			StorageURL:     in.StorageURL,
			InstallationID: in.InstallationID,
			State:          domain.MediaPending,
			NextAttemptAt:  time.Now().Add(domain.MediaSettleDelay),
		}
	}
	return s.repo.InsertMediaIntents(ctx, intents)
}

func (s *MediaService) BindToMessage(ctx context.Context, chatMessageID string, storageKeys []string) (int, error) {
	if len(storageKeys) == 0 {
		return 0, nil
	}
	return s.repo.BindMediaToMessage(ctx, chatMessageID, storageKeys)
}
