package app

import (
	"context"
	"errors"

	"orkome/internal/channels/domain"
)

type TitleCallbackService struct {
	repo Repo
}

func NewTitleCallbackService(repo Repo) *TitleCallbackService {
	return &TitleCallbackService{repo: repo}
}

var ErrTitleCallbackRejected = errors.New("channels: title callback rejected")

func (s *TitleCallbackService) ApplyTitleCallback(ctx context.Context, installationID, externalChatID, title string) (applied bool, err error) {
	normalized := domain.TitleCallbackNormalize(title)
	if normalized == "" {
		return false, ErrTitleCallbackRejected
	}
	return s.repo.UpdateRouteTitle(ctx, installationID, externalChatID, normalized, domain.TitleSourceLLM)
}
