package outboxx

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/contract/events"
)

type Recorder struct{ DB *pg.DB }

func NewRecorder(db *pg.DB) *Recorder { return &Recorder{DB: db} }

func (r *Recorder) Append(ctx context.Context, envs ...events.Envelope) error {
	tx := r.DB.Tx(ctx)
	for _, e := range envs {
		if e.Event == "" {
			return errors.New("outboxx: empty event type")
		}
		id := e.EventID
		if id == "" {
			id = uuid.NewString()
		}
		_, err := tx.Exec(ctx, `
INSERT INTO event_outbox (id, workspace_id, event_type, payload)
VALUES ($1,$2,$3,$4) ON CONFLICT (id) DO NOTHING`,
			id, e.WorkspaceID, e.Event, e.Payload)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Recorder) MarkPublished(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.DB.Tx(ctx).Exec(ctx,
		`UPDATE event_outbox SET published_at=now() WHERE id = ANY($1) AND published_at IS NULL`, ids)
	return err
}

type Relay struct {
	DB        *pg.DB
	Handler   func(ctx context.Context, env events.Envelope) error
	Log       *slog.Logger
	PollEvery time.Duration
	Batch     int

	DecodePayload func(event string, raw []byte) any

	Metrics interface {
		Add(name string, delta int64)
		Gauge(name string, v float64)
	}
	owner string
}

func NewRelay(db *pg.DB, handler func(ctx context.Context, env events.Envelope) error, log *slog.Logger) *Relay {
	return &Relay{DB: db, Handler: handler, Log: log, PollEvery: time.Second, Batch: 100,
		owner: "relay-" + uuid.NewString()[:8]}
}

func (r *Relay) Run(ctx context.Context) error {
	if r.PollEvery <= 0 {
		r.PollEvery = time.Second
	}
	ticker := time.NewTicker(r.PollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.observeBacklog(ctx)
			n, err := r.replayOnce(ctx)
			if err != nil {
				r.Log.Error("outbox relay", "err", err)
			} else if n > 0 {
				r.Log.Info("outbox relay published", "n", n)
			}
		}
	}
}

func (r *Relay) observeBacklog(ctx context.Context) {
	if r.Metrics == nil {
		return
	}
	var n int64
	if err := r.DB.Tx(ctx).QueryRow(ctx,
		`SELECT count(*) FROM event_outbox WHERE published_at IS NULL`).Scan(&n); err != nil {
		return
	}
	r.Metrics.Gauge("outbox_backlog", float64(n))
}

func (r *Relay) ReplayOnce(ctx context.Context) (int, error) { return r.replayOnce(ctx) }

func (r *Relay) replayOnce(ctx context.Context) (int, error) {
	rows, err := r.DB.Tx(ctx).Query(ctx, `
UPDATE event_outbox o SET lease_owner=$1, lease_expires_at=now()+interval '2 minutes',
  attempts = attempts + 1
WHERE o.id IN (
  SELECT id FROM event_outbox
  WHERE published_at IS NULL AND next_retry_at <= now()
    AND (lease_expires_at IS NULL OR lease_expires_at < now())
  ORDER BY workspace_id, id
  FOR UPDATE SKIP LOCKED LIMIT $2
)
RETURNING o.id, o.workspace_id::text, o.event_type, o.payload, o.attempts, o.created_at`,
		r.owner, r.Batch)
	if err != nil {
		return 0, err
	}
	type item struct {
		id, ws, typ string
		payload     []byte
		attempts    int
		createdAt   time.Time
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.ws, &it.typ, &it.payload, &it.attempts, &it.createdAt); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	if r.Metrics != nil {
		r.Metrics.Add("outbox_replay_attempts", int64(len(items)))
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].ws != items[j].ws {
			return items[i].ws < items[j].ws
		}
		return items[i].id < items[j].id
	})

	published := 0

	failedAfter := map[string]time.Time{}
	for _, it := range items {
		if retryAt, failed := failedAfter[it.ws]; failed {

			_, _ = r.DB.Tx(ctx).Exec(ctx, `
UPDATE event_outbox SET next_retry_at=$2, lease_owner=NULL, lease_expires_at=NULL
WHERE id=$1`, it.id, retryAt)
			continue
		}

		occurred := it.createdAt
		if occurred.IsZero() {
			occurred = time.Now().UTC()
		}
		env := events.Envelope{
			EventID: it.id, Event: it.typ, WorkspaceID: it.ws,
			OccurredAt: occurred,
		}
		if r.DecodePayload != nil {
			env.Payload = r.DecodePayload(it.typ, it.payload)
		}
		if err := r.Handler(ctx, env); err != nil {

			backoff := time.Duration(1<<min(it.attempts, 5)) * time.Second
			retryAt := time.Now().UTC().Add(backoff)
			failedAfter[it.ws] = retryAt
			_, _ = r.DB.Tx(ctx).Exec(ctx, `
UPDATE event_outbox SET next_retry_at = $2, lease_owner=NULL, lease_expires_at=NULL
WHERE id=$1`, it.id, retryAt)
			continue
		}
		_, _ = r.DB.Tx(ctx).Exec(ctx, `UPDATE event_outbox SET published_at=now() WHERE id=$1`, it.id)
		published++
	}

	if r.Metrics != nil && published > 0 {
		r.Metrics.Add("outbox_replayed", int64(published))
	}
	return published, nil
}
