package workers

import (
	"context"
	"log/slog"
	"time"
)

type SearchIndexRepo interface {
	SearchIndexSweepOrphans(ctx context.Context, olderThan time.Time, limit int) (int, error)
}

type SearchIndexCatchUp struct {
	Repo SearchIndexRepo
	Log  *slog.Logger

	Every time.Duration

	Grace time.Duration

	BatchLimit int

	Now func() time.Time
}

func (s *SearchIndexCatchUp) Name() string { return "work_search_index" }

func (s *SearchIndexCatchUp) RunOnce(ctx context.Context) (int, error) {
	grace := s.Grace
	if grace <= 0 {
		grace = 24 * time.Hour
	}
	limit := s.BatchLimit
	if limit <= 0 {
		limit = 500
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	cutoff := now().Add(-grace)
	total := 0
	for {
		n, err := s.Repo.SearchIndexSweepOrphans(ctx, cutoff, limit)
		if err != nil {
			return total, err
		}
		total += n
		if n < limit {
			break
		}
	}
	if total > 0 && s.Log != nil {
		s.Log.Info("search index catch-up sweep", "orphans_deleted", total, "grace", grace.String())
	}
	return total, nil
}

func (s *SearchIndexCatchUp) Run(ctx context.Context) error {
	if s.Every <= 0 {
		s.Every = 10 * time.Minute
	}
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if _, err := s.RunOnce(ctx); err != nil && s.Log != nil {
				s.Log.Error("search index catch-up sweep", "err", err)
			}
		}
	}
}

type PruneMarkRepo interface {
	PruneSearchIndexChanges(ctx context.Context, cutoff time.Time, limit int) (int, error)
}

type PruneMarkSweeper struct {
	Repo PruneMarkRepo
	Log  *slog.Logger

	Every time.Duration

	Retention time.Duration

	BatchLimit int

	Now func() time.Time
}

func (s *PruneMarkSweeper) Name() string { return "work_search_prune_mark" }

func (s *PruneMarkSweeper) RunOnce(ctx context.Context) (int, error) {
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
		n, err := s.Repo.PruneSearchIndexChanges(ctx, cutoff, limit)
		if err != nil {
			return total, err
		}
		total += n
		if n < limit {
			break
		}
	}
	if total > 0 && s.Log != nil {
		s.Log.Info("search index prune", "deleted", total, "retention", retention.String())
	}
	return total, nil
}

func (s *PruneMarkSweeper) Run(ctx context.Context) error {
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
				s.Log.Error("search index prune", "err", err)
			}
		}
	}
}
