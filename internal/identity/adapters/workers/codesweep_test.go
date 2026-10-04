package workers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCodeRepo struct {
	cutoffs []time.Time
	limits  []int

	batches []int
	err     error
}

func (f *fakeCodeRepo) DeleteExpiredCodes(_ context.Context, cutoff time.Time, limit int) (int, error) {
	f.cutoffs = append(f.cutoffs, cutoff)
	f.limits = append(f.limits, limit)
	if f.err != nil {
		return 0, f.err
	}
	if len(f.batches) == 0 {
		return 0, nil
	}
	n := f.batches[0]
	f.batches = f.batches[1:]
	return n, nil
}

func TestCodeSweep_CutoffSemantics(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeCodeRepo{batches: []int{3}}
	s := &CodeSweeper{Repo: repo, Now: func() time.Time { return now }, Retention: time.Hour, BatchLimit: 100}

	n, err := s.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	require.Len(t, repo.cutoffs, 1, "drains when below a full batch, stops after one batch")
	assert.Equal(t, now.Add(-time.Hour), repo.cutoffs[0],
		"cutoff = now - retention: only rows past the retention window enter deletion (just-expired rows kept as buffer)")
	assert.Equal(t, 100, repo.limits[0])
}

func TestCodeSweep_BatchLimitDrains(t *testing.T) {
	now := time.Now().UTC()

	repo := &fakeCodeRepo{batches: []int{500, 500, 12}}
	s := &CodeSweeper{Repo: repo, Now: func() time.Time { return now }}

	n, err := s.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1012, n)
	assert.Len(t, repo.cutoffs, 3, "full batches continue until below a batch")
	for _, l := range repo.limits {
		assert.Equal(t, 500, l, "default batch cap 500")
	}
}

func TestCodeSweep_NothingExpired(t *testing.T) {
	repo := &fakeCodeRepo{}
	s := &CodeSweeper{Repo: repo, Now: func() time.Time { return time.Now().UTC() }}
	n, err := s.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Zero(t, n)
	require.Len(t, repo.cutoffs, 1, "not-yet-due still probed once (cutoff decided SQL-side)")
}

func TestCodeSweep_ErrorPropagates(t *testing.T) {
	repo := &fakeCodeRepo{err: errors.New("db down")}
	s := &CodeSweeper{Repo: repo, Now: func() time.Time { return time.Now().UTC() }}
	_, err := s.RunOnce(context.Background())
	assert.Error(t, err, "cleanup error propagates (worker loop logs and retries next round)")
}
