package app

import (
	"context"
	"time"

	"orkome/internal/contract/apierr"
)

type PackUIEntry struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Pack        string    `json:"pack"`
	Label       string    `json:"label"`
	URLTemplate string    `json:"url_template"`
	InstalledAt time.Time `json:"installed_at"`
}

type PackUIEntryRepo interface {
	UpsertPackUIEntry(ctx context.Context, e PackUIEntry) error
	ListPackUIEntries(ctx context.Context, ws string) ([]PackUIEntry, error)
	DeletePackUIEntry(ctx context.Context, ws, pack string) error
}

type PackUIEntryService struct{ repo PackUIEntryRepo }

func NewPackUIEntryService(repo PackUIEntryRepo) *PackUIEntryService {
	return &PackUIEntryService{repo: repo}
}

func (s *PackUIEntryService) Upsert(ctx context.Context, ws, pack, label, urlTemplate string) error {
	if ws == "" || pack == "" || label == "" || urlTemplate == "" {
		return apierr.NewValidation("pack, label and url_template required")
	}
	return s.repo.UpsertPackUIEntry(ctx, PackUIEntry{WorkspaceID: ws, Pack: pack, Label: label, URLTemplate: urlTemplate})
}

func (s *PackUIEntryService) List(ctx context.Context, ws string) ([]PackUIEntry, error) {
	return s.repo.ListPackUIEntries(ctx, ws)
}

func (s *PackUIEntryService) Delete(ctx context.Context, ws, pack string) error {
	return s.repo.DeletePackUIEntry(ctx, ws, pack)
}
