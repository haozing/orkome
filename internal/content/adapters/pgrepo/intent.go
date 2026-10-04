package pgrepo

import (
	"context"
	"time"

	"orkome/internal/content/app"
)

var _ app.IntentRepo = (*Repo)(nil)

func (r *Repo) ClaimExpired(ctx context.Context, now time.Time, limit int) ([]app.IntentRecord, error) {
	rows, err := r.q(ctx).Query(ctx, `
WITH due AS (
  SELECT id FROM source_context_object_intent
  WHERE state = 'pending' AND next_attempt_at <= $1
     OR (state = 'leased' AND lease_expires_at < $1)
  ORDER BY next_attempt_at
  LIMIT $2
  FOR UPDATE SKIP LOCKED
)
UPDATE source_context_object_intent i SET
  state = 'leased',
  lease_expires_at = $1 + interval '2 minutes',
  claimed_by = nullif(current_setting('orkome.worker_id', true), ''),
  updated_at = now()
FROM due WHERE i.id = due.id
RETURNING i.id::text, i.workspace_id::text, i.storage_key, i.attempts, i.max_attempts`,
		now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.IntentRecord
	for rows.Next() {
		var rec app.IntentRecord
		if err := rows.Scan(&rec.ID, &rec.WorkSpace, &rec.StorageKey, &rec.Attempts, &rec.MaxAttempt); err != nil {
			return nil, err
		}
		rec.State = "leased"
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *Repo) MarkDone(ctx context.Context, ids []string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE source_context_object_intent
SET state = 'done', updated_at = now(), lease_expires_at = NULL
WHERE id = ANY($1::uuid[]) AND state = 'leased'`, ids)
	return err
}

func (r *Repo) MarkRetry(ctx context.Context, rec app.IntentRecord, errStr string, next time.Time, exhausted bool) error {
	state := "pending"
	if exhausted {
		state = "released"
	}
	_, err := r.q(ctx).Exec(ctx, `
UPDATE source_context_object_intent
SET state = $2, attempts = $3, next_attempt_at = $4, last_error = $5,
    lease_expires_at = NULL, updated_at = now()
WHERE id = $1::uuid`,
		rec.ID, state, rec.Attempts, next, truncateErr(errStr))
	return err
}

func truncateErr(s string) string {
	const max = 2000
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
