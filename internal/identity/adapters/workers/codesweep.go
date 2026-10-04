package workers

import (
	"context"
	"log/slog"
	"time"
)

type CodeRepo interface {
	DeleteExpiredCodes(ctx context.Context, cutoff time.Time, limit int) (int, error)
}

type CodeSweeper struct {
	Repo CodeRepo
	Log  *slog.Logger

	Every time.Duration

	Retention time.Duration

	BatchLimit int

	Now func() time.Time
}

func (s *CodeSweeper) Name() string { return "identity_vcode_sweep" }

func (s *CodeSweeper) RunOnce(ctx context.Context) (int, error) {
	retention := s.Retention
	if retention <= 0 {
		retention = time.Hour
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
		n, err := s.Repo.DeleteExpiredCodes(ctx, cutoff, limit)
		if err != nil {
			return total, err
		}
		total += n
		if n < limit {
			break
		}
	}
	if total > 0 && s.Log != nil {
		s.Log.Info("verification code sweep", "deleted", total, "retention", retention.String())
	}
	return total, nil
}

func (s *CodeSweeper) Run(ctx context.Context) error {
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
				s.Log.Error("verification code sweep", "err", err)
			}
		}
	}
}
