package schedx

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSchedx_PlansForScope_RespectsTimezone(t *testing.T) {
	s := Schedule{Expr: "0 9 * * *", TZ: "Asia/Shanghai"}
	anchor := ts("2026-01-01T00:00:00Z")
	now := ts("2026-01-03T00:00:00Z")

	plans, err := PlansForScope(s, anchor, now)
	require.NoError(t, err)
	require.Len(t, plans, 2)

	assert.Equal(t, ts("2026-01-01T01:00:00Z"), plans[0].UTC())
	assert.Equal(t, ts("2026-01-02T01:00:00Z"), plans[1].UTC())
	assert.Equal(t, "Asia/Shanghai", plans[0].Location().String())

	utc := Schedule{Expr: "0 9 * * *"}
	plans, err = PlansForScope(utc, anchor, ts("2026-01-02T00:00:00Z"))
	require.NoError(t, err)
	require.Len(t, plans, 1)
	assert.Equal(t, ts("2026-01-01T09:00:00Z"), plans[0].UTC())
}

func TestSchedx_PlansForScope_HalfOpenWindow(t *testing.T) {
	s := Schedule{Expr: "* * * * *"}
	anchor := ts("2026-01-01T10:00:00Z")
	now := ts("2026-01-01T10:05:00Z")

	plans, err := PlansForScope(s, anchor, now)
	require.NoError(t, err)
	require.Len(t, plans, 5)
	assert.Equal(t, ts("2026-01-01T10:01:00Z"), plans[0].UTC())
	assert.Equal(t, ts("2026-01-01T10:05:00Z"), plans[4].UTC())

	empty, err := PlansForScope(s, anchor, anchor)
	require.NoError(t, err)
	assert.Empty(t, empty)
	empty, err = PlansForScope(s, anchor, anchor.Add(-time.Minute))
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestSchedx_LatestDue(t *testing.T) {
	minute := Schedule{Expr: "* * * * *"}

	at, ok, err := LatestDue(minute, ts("2026-01-01T10:00:00Z"), ts("2026-01-01T10:03:30Z"))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, ts("2026-01-01T10:03:00Z"), at.UTC())

	at10 := Schedule{Expr: "0 10 * * *"}
	at, ok, err = LatestDue(at10, ts("2026-01-01T10:00:00Z"), ts("2026-01-02T10:05:00Z"))
	require.NoError(t, err)
	require.True(t, ok, "exactly 5min late not voided")
	assert.Equal(t, ts("2026-01-02T10:00:00Z"), at.UTC())

	_, ok, err = LatestDue(at10, ts("2026-01-01T10:00:00Z"), ts("2026-01-02T10:05:01Z"))
	require.NoError(t, err)
	assert.False(t, ok, "  5min+1s late voided")

	daily := Schedule{Expr: "0 3 * * *"}
	_, ok, err = LatestDue(daily, ts("2026-01-01T00:00:00Z"), ts("2026-01-02T12:00:00Z"))
	require.NoError(t, err)
	assert.False(t, ok, "a 03:00 schedule discovered at 12:00 should be voided")

	_, ok, err = LatestDue(daily, ts("2026-01-01T04:00:00Z"), ts("2026-01-01T12:00:00Z"))
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestSchedx_IdempotentDoubleCall(t *testing.T) {
	s := Schedule{Expr: "*/5 * * * *", TZ: "America/New_York"}
	anchor := ts("2026-06-01T00:00:00Z")
	now := ts("2026-06-01T01:00:00Z")

	a, err := PlansForScope(s, anchor, now)
	require.NoError(t, err)
	b, err := PlansForScope(s, anchor, now)
	require.NoError(t, err)
	require.NotEmpty(t, a)
	assert.Equal(t, a, b)

	la, oka, err := LatestDue(s, anchor, now)
	require.NoError(t, err)
	lb, okb, err := LatestDue(s, anchor, now)
	require.NoError(t, err)
	assert.Equal(t, la, lb)
	assert.Equal(t, oka, okb)
}

func TestSchedx_BadExprAndTimezone(t *testing.T) {
	_, err := PlansForScope(Schedule{Expr: "not-a-cron"}, time.Now(), time.Now().Add(time.Hour))
	assert.Error(t, err)

	_, err = PlansForScope(Schedule{Expr: "* * * * *", TZ: "Mars/Olympus"}, time.Now(), time.Now().Add(time.Hour))
	assert.Error(t, err)
}

func TestSchedx_ScanWindowCap(t *testing.T) {
	_, err := PlansForScope(Schedule{Expr: "* * * * *"}, ts("2000-01-01T00:00:00Z"), ts("2026-01-01T00:00:00Z"))
	assert.ErrorContains(t, err, "scan window")
}
