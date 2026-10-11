package outboxx

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/contract/events"
)

func testDB(t *testing.T) *pg.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pg.Connect(ctx, dsn, pg.Options{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func TestOutboxx_ReplayOccurredAtUsesCreatedAt(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ws := uuid.NewString()
	id := "tb-occurredat-1"
	created := time.Now().UTC().Add(-1 * time.Hour)
	_, err := db.Tx(ctx).Exec(ctx,
		`INSERT INTO event_outbox (id, workspace_id, event_type, payload, created_at)
		 VALUES ($1,$2,$3,$4,$5)`,
		id, ws, "workspace.created", []byte(`{}`), created)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Tx(ctx).Exec(ctx, `DELETE FROM event_outbox WHERE id=$1`, id) })

	var got events.Envelope
	relay := NewRelay(db, func(_ context.Context, env events.Envelope) error { got = env; return nil }, slog.Default())
	n, err := relay.ReplayOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, id, got.EventID)

	assert.WithinDuration(t, created, got.OccurredAt, 2*time.Second,
		"OccurredAt should take created_at from the table")
	assert.True(t, got.OccurredAt.Before(time.Now().UTC().Add(-30*time.Minute)),
		"OccurredAt should not be the replay time (time.Now)")
}

func TestOutboxx_ReplayFailurePreservesPartitionOrder(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	wsA, wsB := uuid.NewString(), uuid.NewString()
	insert := func(id, ws string) {
		_, err := db.Tx(ctx).Exec(ctx,
			`INSERT INTO event_outbox (id, workspace_id, event_type, payload) VALUES ($1,$2,$3,$4)`,
			id, ws, "workspace.updated", []byte(`{}`))
		require.NoError(t, err)
	}
	ids := []string{"tb-order-a1", "tb-order-a2", "tb-order-a3", "tb-order-b1"}
	for _, id := range ids[:3] {
		insert(id, wsA)
	}
	insert(ids[3], wsB)
	t.Cleanup(func() {
		_, _ = db.Tx(ctx).Exec(ctx, `DELETE FROM event_outbox WHERE id = ANY($1)`, ids)
	})

	failA1 := true
	var delivered []string
	relay := NewRelay(db, func(_ context.Context, env events.Envelope) error {
		delivered = append(delivered, env.EventID)
		if failA1 && env.EventID == "tb-order-a1" {
			return errors.New("inject failure")
		}
		return nil
	}, slog.Default())

	n, err := relay.ReplayOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only b1 should be published")

	assert.ElementsMatch(t, []string{"tb-order-a1", "tb-order-b1"}, delivered,
		"a2/a3 must not deliver before failed a1")

	for _, id := range ids[:3] {
		var published *time.Time
		var nextRetry time.Time
		err := db.Tx(ctx).QueryRow(ctx,
			`SELECT published_at, next_retry_at FROM event_outbox WHERE id=$1`, id).
			Scan(&published, &nextRetry)
		require.NoError(t, err)
		require.Nil(t, published, "  %s should not be published yet", id)
		assert.True(t, nextRetry.After(time.Now()),
			"  %s should defer retry (next_retry_at in the future)", id)
	}

	failA1 = false
	_, err = db.Tx(ctx).Exec(ctx,
		`UPDATE event_outbox SET next_retry_at=now() WHERE id = ANY($1) AND published_at IS NULL`, ids[:3])
	require.NoError(t, err)

	n, err = relay.ReplayOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, []string{"tb-order-a1", "tb-order-a2", "tb-order-a3"}, delivered[len(delivered)-3:],
		"redelivery must keep partition id order")
}
