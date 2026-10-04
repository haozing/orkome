package workers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSweepRepo struct {
	cutoffs []time.Time
	limits  []int
	batches []int
	err     error

	calls int
	errOn int
}

func (f *fakeSweepRepo) SearchIndexSweepOrphans(_ context.Context, olderThan time.Time, limit int) (int, error) {
	f.cutoffs = append(f.cutoffs, olderThan)
	f.limits = append(f.limits, limit)
	f.calls++
	if f.err != nil && (f.errOn == 0 || f.calls >= f.errOn) {
		return 0, f.err
	}
	if len(f.batches) == 0 {
		return 0, nil
	}
	n := f.batches[0]
	f.batches = f.batches[1:]
	return n, nil
}

func TestSearchIndexCatchUp_GraceSemantics(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeSweepRepo{batches: []int{4}}
	s := &SearchIndexCatchUp{Repo: repo, Now: func() time.Time { return now },
		Grace: 24 * time.Hour, BatchLimit: 100}

	n, err := s.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	require.Len(t, repo.cutoffs, 1, "drains when below a full batch")
	assert.Equal(t, now.Add(-24*time.Hour), repo.cutoffs[0], "cutoff = now - grace")
	assert.Equal(t, 100, repo.limits[0])
}

func TestSearchIndexCatchUp_BatchLimitDrains(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeSweepRepo{batches: []int{500, 500, 7}}
	s := &SearchIndexCatchUp{Repo: repo, Now: func() time.Time { return now }}

	n, err := s.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1007, n)
	assert.Len(t, repo.cutoffs, 3, "first two full batches continue, third partial batch stops")

	assert.Equal(t, now.Add(-24*time.Hour), repo.cutoffs[0])
	assert.Equal(t, 500, repo.limits[0])
}

func TestSearchIndexCatchUp_ErrorStops(t *testing.T) {
	boom := errors.New("db down")
	repo := &fakeSweepRepo{batches: []int{10, 10}, err: boom, errOn: 2}
	s := &SearchIndexCatchUp{Repo: repo, BatchLimit: 10, Now: func() time.Time { return time.Now() }}

	n, err := s.RunOnce(context.Background())
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, 10, n, "first-batch counts kept then stopped")
	assert.Equal(t, 2, repo.calls, "full batch (=BatchLimit) triggers the next, next batch failure returns immediately")
}

type fakePruneRepo struct {
	cutoffs []time.Time
	limits  []int
	batches []int
	err     error
}

func (f *fakePruneRepo) PruneSearchIndexChanges(_ context.Context, cutoff time.Time, limit int) (int, error) {
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

func TestPruneMarkSweep_RetentionSemantics(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakePruneRepo{batches: []int{2}}
	s := &PruneMarkSweeper{Repo: repo, Now: func() time.Time { return now },
		Retention: 30 * 24 * time.Hour, BatchLimit: 500}

	n, err := s.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	require.Len(t, repo.cutoffs, 1)
	assert.Equal(t, now.Add(-30*24*time.Hour), repo.cutoffs[0])
}

func TestPruneMarkSweep_EmptyRoundNoop(t *testing.T) {
	repo := &fakePruneRepo{}
	s := &PruneMarkSweeper{Repo: repo, Now: func() time.Time { return time.Now() }}

	n, err := s.RunOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Len(t, repo.cutoffs, 1, "stop as soon as 0 < batch — single batch call")
}
