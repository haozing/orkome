package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type retryCall struct {
	rec       IntentRecord
	errStr    string
	next      time.Time
	exhausted bool
}

type fakeIntentRepo struct {
	claimed []IntentRecord
	done    []string
	retries []retryCall
}

func (f *fakeIntentRepo) ClaimExpired(ctx context.Context, now time.Time, limit int) ([]IntentRecord, error) {
	if limit > len(f.claimed) {
		limit = len(f.claimed)
	}
	out := append([]IntentRecord(nil), f.claimed[:limit]...)
	f.claimed = f.claimed[limit:]
	return out, nil
}
func (f *fakeIntentRepo) MarkDone(ctx context.Context, ids []string) error {
	f.done = append(f.done, ids...)
	return nil
}
func (f *fakeIntentRepo) MarkRetry(ctx context.Context, rec IntentRecord, errStr string, next time.Time, exhausted bool) error {
	f.retries = append(f.retries, retryCall{rec: rec, errStr: errStr, next: next, exhausted: exhausted})
	return nil
}

func TestRetryBackoffExponential(t *testing.T) {
	assert.Equal(t, 2*time.Second, RetryBackoff(1))
	assert.Equal(t, 8*time.Second, RetryBackoff(3))
	assert.Equal(t, 15*time.Minute, RetryBackoff(11), "cap 15 minutes")
	assert.Equal(t, 2*time.Second, RetryBackoff(0), "non-positive attempts treated as tier 1")
}

func TestSweepDeletesAndFinalizes(t *testing.T) {
	repo := &fakeIntentRepo{claimed: []IntentRecord{
		{ID: "i1", StorageKey: "ws/a/f1"}, {ID: "i2", StorageKey: "ws/a/f2"},
	}}
	fs := newFakeStore()
	fs.objs["ws/a/f1"] = storeObj{data: []byte("x")}
	fs.objs["ws/a/f2"] = storeObj{data: []byte("y")}
	sw := NewIntentSweeper(repo, fs, nil)

	n, err := sw.Sweep(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, []string{"i1", "i2"}, repo.done)
	_, exists := fs.objs["ws/a/f1"]
	assert.False(t, exists, "objects cleaned")
}

func TestSweepRetriesWithBackoff(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repo := &fakeIntentRepo{claimed: []IntentRecord{
		{ID: "i1", StorageKey: "ws/a/boom", Attempts: 1, MaxAttempt: 3},
	}}
	sw := NewIntentSweeper(repo, &failingStore{Store: newFakeStore()}, func() time.Time { return now })

	n, err := sw.Sweep(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	require.Len(t, repo.retries, 1)
	assert.Equal(t, 2, repo.retries[0].rec.Attempts, "attempts+1")
	assert.False(t, repo.retries[0].exhausted, "not exhausted — back to pending for backoff retry")
	assert.Equal(t, now.Add(RetryBackoff(2)), repo.retries[0].next, "next_attempt_at = now + 2^attempts")
}

func TestSweepReleasesExhausted(t *testing.T) {

	repo := &fakeIntentRepo{claimed: []IntentRecord{
		{ID: "i1", StorageKey: "ws/a/boom", Attempts: 0, MaxAttempt: 1},
	}}
	sw := NewIntentSweeper(repo, &failingStore{Store: newFakeStore()}, nil)
	_, err := sw.Sweep(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, repo.retries, 1)
	assert.True(t, repo.retries[0].exhausted, "attempts>=max → dead letter released")
}

type failingStore struct{ Store }

func (f *failingStore) Delete(ctx context.Context, key string) error {
	return errors.New("storage down")
}

func (f *failingStore) DeleteMulti(ctx context.Context, keys ...string) error {
	var errs []error
	for _, k := range keys {
		errs = append(errs, fmt.Errorf("%s: %w", k, errors.New("storage down")))
	}
	return errors.Join(errs...)
}

type countingStore struct {
	Store
	deletes     int
	deleteMulti int
}

func (c *countingStore) Delete(ctx context.Context, key string) error {
	c.deletes++
	return c.Store.Delete(ctx, key)
}

func (c *countingStore) DeleteMulti(ctx context.Context, keys ...string) error {
	c.deleteMulti++
	return c.Store.DeleteMulti(ctx, keys...)
}

func TestSweepBatchFastPath(t *testing.T) {
	repo := &fakeIntentRepo{claimed: []IntentRecord{
		{ID: "i1", StorageKey: "ws/a/f1"}, {ID: "i2", StorageKey: "ws/a/f2"}, {ID: "i3", StorageKey: "ws/a/f3"},
	}}
	base := newFakeStore()
	for i, k := range []string{"ws/a/f1", "ws/a/f2", "ws/a/f3"} {
		base.objs[k] = storeObj{data: []byte{byte(i)}}
	}
	cs := &countingStore{Store: base}
	sw := NewIntentSweeper(repo, cs, nil)

	n, err := sw.Sweep(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, []string{"i1", "i2", "i3"}, repo.done)
	assert.Equal(t, 1, cs.deleteMulti, "batch fast path exactly one DeleteMulti")
	assert.Zero(t, cs.deletes, "full delete succeeds without per-row Delete")
	assert.Empty(t, base.objs, "all objects cleaned")
}

func TestSweepBatchPartialFallsBack(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repo := &fakeIntentRepo{claimed: []IntentRecord{
		{ID: "i1", StorageKey: "ws/a/ok", Attempts: 1, MaxAttempt: 3},
		{ID: "i2", StorageKey: "ws/a/boom", Attempts: 1, MaxAttempt: 3},
	}}
	base := newFakeStore()
	base.objs["ws/a/ok"] = storeObj{data: []byte("x")}
	base.objs["ws/a/boom"] = storeObj{data: []byte("y")}
	fs := &partialFailStore{Store: base, failKeys: map[string]bool{"ws/a/boom": true}}
	sw := NewIntentSweeper(repo, fs, func() time.Time { return now })

	n, err := sw.Sweep(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "successful keys settled")
	assert.Equal(t, []string{"i1"}, repo.done)
	require.Len(t, repo.retries, 1, "failed keys back off individually (per-record semantics kept)")
	assert.Equal(t, "i2", repo.retries[0].rec.ID)
	_, exists := base.objs["ws/a/boom"]
	assert.True(t, exists, "failed object not deleted")
}

type partialFailStore struct {
	Store
	failKeys map[string]bool
}

func (p *partialFailStore) Delete(ctx context.Context, key string) error {
	if p.failKeys[key] {
		return errors.New("boom")
	}
	return p.Store.Delete(ctx, key)
}

func (p *partialFailStore) DeleteMulti(ctx context.Context, keys ...string) error {
	var errs []error
	for _, k := range keys {
		if p.failKeys[k] {
			errs = append(errs, fmt.Errorf("%s: boom", k))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return p.Store.DeleteMulti(ctx, keys...)
}
