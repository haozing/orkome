package workers

import (
	"context"
	"log/slog"
	"time"
)

type ArchiveRepo interface {
	DeleteArchivedBefore(ctx context.Context, cutoff time.Time, limit int) (int, error)
}

type ArchiveSweeper struct {
	Repo ArchiveRepo
	Log  *slog.Logger

	Every time.Duration

	Retention time.Duration

	BatchLimit int

	Now func() time.Time
}

func (s *ArchiveSweeper) Name() string { return "notification_archive" }

func (s *ArchiveSweeper) RunOnce(ctx context.Context) (int, error) {
	retention := s.Retention
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	limit := s.BatchLimit
	if limit <= 0 {
		limit = 500
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	cutoff := now().Add(-retention)
	total := 0
	for {
		n, err := s.Repo.DeleteArchivedBefore(ctx, cutoff, limit)
		if err != nil {
			return total, err
		}
		total += n
		if n < limit {
			break
		}
	}
	if total > 0 && s.Log != nil {
		s.Log.Info("notification archive sweep", "deleted", total, "retention", retention.String())
	}
	return total, nil
}

func (s *ArchiveSweeper) Run(ctx context.Context) error {
	if s.Every <= 0 {
		s.Every = time.Hour
	}
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if _, err := s.RunOnce(ctx); err != nil && s.Log != nil {
				s.Log.Error("notification archive sweep", "err", err)
			}
		}
	}
}
