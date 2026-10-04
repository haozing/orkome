package app

import (
	"context"
	"math"
	"time"
)

const (
	IntentPending  = "pending"
	IntentLeased   = "leased"
	IntentDone     = "done"
	IntentReleased = "released"
)

type IntentRecord struct {
	ID         string
	WorkSpace  string
	StorageKey string
	State      string
	Attempts   int
	MaxAttempt int
}

type IntentRepo interface {
	ClaimExpired(ctx context.Context, now time.Time, limit int) ([]IntentRecord, error)

	MarkDone(ctx context.Context, ids []string) error

	MarkRetry(ctx context.Context, rec IntentRecord, errStr string, next time.Time, exhausted bool) error
}

func RetryBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	secs := int64(math.Pow(2, float64(attempts)))
	if secs > 900 {
		secs = 900
	}
	return time.Duration(secs) * time.Second
}

type IntentSweeper struct {
	repo  IntentRepo
	store Store
	now   func() time.Time
}

func NewIntentSweeper(repo IntentRepo, store Store, now func() time.Time) *IntentSweeper {
	if now == nil {
		now = time.Now
	}
	return &IntentSweeper{repo: repo, store: store, now: now}
}

func (s *IntentSweeper) Sweep(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 50
	}
	now := s.now()
	claimed, err := s.repo.ClaimExpired(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	if len(claimed) == 0 {
		return 0, nil
	}
	keys := make([]string, 0, len(claimed))
	for _, rec := range claimed {
		keys = append(keys, rec.StorageKey)
	}
	if err := s.store.DeleteMulti(ctx, keys...); err == nil {
		done := make([]string, 0, len(claimed))
		for _, rec := range claimed {
			done = append(done, rec.ID)
		}
		if err := s.repo.MarkDone(ctx, done); err != nil {
			return 0, err
		}
		return len(done), nil
	}
	var done []string
	for _, rec := range claimed {
		if err := s.store.Delete(ctx, rec.StorageKey); err != nil {
			attempts := rec.Attempts + 1
			rec.Attempts = attempts
			exhausted := attempts >= rec.MaxAttempt
			_ = s.repo.MarkRetry(ctx, rec, err.Error(), now.Add(RetryBackoff(attempts)), exhausted)
			continue
		}
		done = append(done, rec.ID)
	}
	if len(done) > 0 {
		if err := s.repo.MarkDone(ctx, done); err != nil {
			return 0, err
		}
	}
	return len(done), nil
}

func (s *IntentSweeper) Name() string { return "content_object_intent" }

func (s *IntentSweeper) Run(ctx context.Context) error {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if _, err := s.Sweep(ctx, 50); err != nil {

				continue
			}
		}
	}
}
