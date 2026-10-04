package pgrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"orkome/internal/platform/pg"
	"orkome/internal/webhooks/app"
)

type Repo struct{ DB *pg.DB }

func (r *Repo) InsertDelivery(ctx context.Context, subID string, ev app.CatalogEvent) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = r.DB.Tx(ctx).Exec(ctx, `
INSERT INTO event_delivery (subscription_id, event_id, event_type, payload)
VALUES ($1,$2,$3,$4)
ON CONFLICT (subscription_id, event_id) DO NOTHING`, subID, ev.ID, ev.Type, string(body))
	return err
}

func (r *Repo) CreateSubscription(ctx context.Context, ws, url string, evs []string, secret, description, createdBy string) (app.Subscription, error) {
	if evs == nil {
		evs = []string{}
	}
	s := app.Subscription{}
	err := r.DB.Tx(ctx).QueryRow(ctx, `
INSERT INTO webhook_subscription (workspace_id, url, events, secret, description, created_by)
VALUES ($1,$2,$3,$4,$5,nullif($6,'')::uuid)
RETURNING id, workspace_id, url, events, description, created_at`,
		ws, url, evs, secret, description, createdBy).
		Scan(&s.ID, &s.WorkspaceID, &s.URL, &s.Events, &s.Description, &s.CreatedAt)
	return s, err
}

func (r *Repo) ListSubscriptions(ctx context.Context, ws string) ([]app.Subscription, error) {
	rows, err := r.DB.Tx(ctx).Query(ctx, `
SELECT id, workspace_id, url, events, description, created_at
FROM webhook_subscription WHERE workspace_id=$1 AND revoked_at IS NULL
ORDER BY created_at`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubs(rows)
}

func (r *Repo) ListActiveSubscriptions(ctx context.Context, ws string) ([]app.Subscription, error) {

	rows, err := r.DB.Tx(ctx).Query(ctx, `
SELECT id, workspace_id, url, events, description, created_at, secret
FROM webhook_subscription WHERE workspace_id=$1 AND revoked_at IS NULL`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Subscription
	for rows.Next() {
		var s app.Subscription
		if err := rows.Scan(&s.ID, &s.WorkspaceID, &s.URL, &s.Events, &s.Description, &s.CreatedAt, &s.Secret); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) GetSubscription(ctx context.Context, id string) (app.Subscription, bool, error) {
	s := app.Subscription{}
	err := r.DB.Tx(ctx).QueryRow(ctx, `
SELECT id, workspace_id, url, events, description, created_at, secret
FROM webhook_subscription WHERE id=$1 AND revoked_at IS NULL`, id).
		Scan(&s.ID, &s.WorkspaceID, &s.URL, &s.Events, &s.Description, &s.CreatedAt, &s.Secret)
	if err == pgx.ErrNoRows {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	return s, true, nil
}

func (r *Repo) RevokeSubscription(ctx context.Context, id string) error {
	_, err := r.DB.Tx(ctx).Exec(ctx,
		`UPDATE webhook_subscription SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, id)
	return err
}

func scanSubs(rows pgx.Rows) ([]app.Subscription, error) {
	var out []app.Subscription
	for rows.Next() {
		var s app.Subscription
		if err := rows.Scan(&s.ID, &s.WorkspaceID, &s.URL, &s.Events, &s.Description, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) ListDeliveries(ctx context.Context, subID, status string, limit int) ([]app.Delivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cond := ""
	args := []any{subID}
	if status != "" {
		args = append(args, status)
		cond = fmt.Sprintf(" AND status=$%d", len(args))
	}
	args = append(args, limit)
	rows, err := r.DB.Tx(ctx).Query(ctx, `
SELECT id, subscription_id, event_id, event_type, status, attempts, next_attempt_at,
  last_response_status, coalesce(last_error,''), created_at, delivered_at
FROM event_delivery WHERE subscription_id=$1`+cond+`
ORDER BY created_at DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeliveries(rows)
}

func scanDeliveries(rows pgx.Rows) ([]app.Delivery, error) {
	var out []app.Delivery
	for rows.Next() {
		var d app.Delivery
		if err := rows.Scan(&d.ID, &d.SubscriptionID, &d.EventID, &d.EventType, &d.Status, &d.Attempts,
			&d.NextAttemptAt, &d.LastResponseStatus, &d.LastError, &d.CreatedAt, &d.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repo) ClaimPendingDeliveries(ctx context.Context, batch int) ([]app.Delivery, error) {
	if batch <= 0 || batch > 20 {
		batch = 20
	}
	rows, err := r.DB.Tx(ctx).Query(ctx, `
UPDATE event_delivery d SET attempts = attempts + 1, next_attempt_at = now() + interval '2 minutes'
WHERE d.id IN (
  SELECT id FROM event_delivery
  WHERE status='pending' AND next_attempt_at <= now()
  ORDER BY next_attempt_at LIMIT $1
  FOR UPDATE SKIP LOCKED
)
RETURNING d.id, d.subscription_id, d.event_id, d.event_type, d.payload, d.status, d.attempts, d.next_attempt_at,
  d.last_response_status, coalesce(d.last_error,''), d.created_at, d.delivered_at`, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Delivery
	for rows.Next() {
		var d app.Delivery
		var payload string
		if err := rows.Scan(&d.ID, &d.SubscriptionID, &d.EventID, &d.EventType, &payload, &d.Status, &d.Attempts,
			&d.NextAttemptAt, &d.LastResponseStatus, &d.LastError, &d.CreatedAt, &d.DeliveredAt); err != nil {
			return nil, err
		}
		d.Payload = payload
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repo) MarkDelivered(ctx context.Context, id string, httpStatus int) error {
	_, err := r.DB.Tx(ctx).Exec(ctx,
		`UPDATE event_delivery SET status='succeeded', delivered_at=now(), last_response_status=$2 WHERE id=$1`, id, httpStatus)
	return err
}

func (r *Repo) MarkFailed(ctx context.Context, id string, httpStatus int, errMsg string, nextAttempt time.Time) error {
	_, err := r.DB.Tx(ctx).Exec(ctx,
		`UPDATE event_delivery SET status='pending', last_response_status=$2, last_error=$3, next_attempt_at=$4 WHERE id=$1`,
		id, httpStatus, errMsg, nextAttempt)
	return err
}

func (r *Repo) MarkDead(ctx context.Context, id string, httpStatus int, errMsg string) error {
	_, err := r.DB.Tx(ctx).Exec(ctx,
		`UPDATE event_delivery SET status='dead', last_response_status=$2, last_error=$3 WHERE id=$1`, id, httpStatus, errMsg)
	return err
}

func (r *Repo) ReplayDelivery(ctx context.Context, id string) error {
	tag, err := r.DB.Tx(ctx).Exec(ctx,
		`UPDATE event_delivery SET status='pending', attempts=0, next_attempt_at=now() WHERE id=$1 AND status IN ('dead','pending')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("delivery not replayable")
	}
	return nil
}

func (r *Repo) ListEvents(ctx context.Context, ws string, afterCreatedAt time.Time, afterID string, limit int) ([]app.CatalogEvent, error) {
	cond := ""
	args := []any{ws}
	if !afterCreatedAt.IsZero() {
		args = append(args, afterCreatedAt, afterID)
		cond = fmt.Sprintf(" AND (created_at, id) > ($%d, $%d)", len(args)-1, len(args))
	}
	args = append(args, limit)
	rows, err := r.DB.Tx(ctx).Query(ctx, `
SELECT id, event_type, created_at, workspace_id::text, payload
FROM event_outbox WHERE workspace_id=$1`+cond+`
ORDER BY created_at, id LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.CatalogEvent
	for rows.Next() {
		var e app.CatalogEvent
		var payload []byte
		var occurred time.Time
		if err := rows.Scan(&e.ID, &e.Type, &occurred, &e.WorkspaceID, &payload); err != nil {
			return nil, err
		}
		e.OccurredAt = occurred
		e.Data = payload
		out = append(out, e)
	}
	return out, rows.Err()
}
