package pgrepo

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"

	"orkome/internal/orchestration/app"
	"github.com/haozing/ploykit/platform/pg"
)

type BatchRepo struct{ DB *pg.DB }

var _ app.BatchRepo = (*BatchRepo)(nil)

func (r *BatchRepo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }

const batchCols = `id, workspace_id, work_item_id, kind, status, params, source, total,
succeeded, failed, skipped, concurrency, chunk_size, pace_interval_seconds,
coalesce(originator_user_id::text,''), coalesce(last_error,''), created_at, completed_at`

func scanBatch(row pgx.Row) (app.Batch, error) {
	var b app.Batch
	err := row.Scan(&b.ID, &b.WorkspaceID, &b.WorkItemID, &b.Kind, &b.Status, &b.Params, &b.Source,
		&b.Total, &b.Succeeded, &b.Failed, &b.Skipped, &b.Concurrency, &b.ChunkSize, &b.PaceIntervalSeconds,
		&b.OriginatorUserID, &b.LastError, &b.CreatedAt, &b.CompletedAt)
	return b, err
}

func (r *BatchRepo) InsertBatch(ctx context.Context, b app.Batch, payloads []map[string]any) (app.Batch, error) {
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		if err := r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
INSERT INTO work_item_batch (workspace_id, work_item_id, kind, status, params, source, total,
  concurrency, chunk_size, pace_interval_seconds, originator_user_id, accountable_user_id)
VALUES ($1::uuid,$2::uuid,$3,'pending',$4::jsonb,$5::jsonb,$6,$7,$8,$9,nullif($10::text,'')::uuid,nullif($10::text,'')::uuid)
RETURNING %s`, batchCols),
			b.WorkspaceID, b.WorkItemID, b.Kind, mustJSONB(b.Params), mustJSONB(b.Source), b.Total,
			b.Concurrency, b.ChunkSize, b.PaceIntervalSeconds, b.OriginatorUserID).
			Scan(&b.ID, &b.WorkspaceID, &b.WorkItemID, &b.Kind, &b.Status, &b.Params, &b.Source,
				&b.Total, &b.Succeeded, &b.Failed, &b.Skipped, &b.Concurrency, &b.ChunkSize, &b.PaceIntervalSeconds,
				&b.OriginatorUserID, &b.LastError, &b.CreatedAt, &b.CompletedAt); err != nil {
			return err
		}

		const chunk = 1000
		for start := 0; start < len(payloads); start += chunk {
			end := start + chunk
			if end > len(payloads) {
				end = len(payloads)
			}
			vals := ""
			args := []any{b.ID, b.WorkspaceID}
			n := 2
			for i := start; i < end; i++ {
				if i > start {
					vals += ","
				}
				n++
				plPh := n
				vals += fmt.Sprintf("($1::uuid, $2::uuid, %d, $%d::jsonb, 'pending')", i+1, plPh)
				args = append(args, mustJSONB(payloads[i]))
			}
			if _, err := r.q(ctx).Exec(ctx, fmt.Sprintf(`
INSERT INTO work_item_batch_item (batch_id, workspace_id, seq, payload_ref, status) VALUES %s`, vals), args...); err != nil {
				return err
			}
		}
		return nil
	})
	return b, pg.AsNotFound(err, "work item not found")
}

func (r *BatchRepo) GetBatch(ctx context.Context, ws, id string) (app.Batch, bool, error) {
	b, err := scanBatch(r.q(ctx).QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM work_item_batch WHERE workspace_id=$1 AND id=$2`, batchCols), ws, id))
	if noRow(err) {
		return b, false, nil
	}
	return b, err == nil, err
}

func (r *BatchRepo) SetBatchStatus(ctx context.Context, id, status string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_batch SET status=$2,
  started_at = CASE WHEN $2='running' AND started_at IS NULL THEN now() ELSE started_at END,
  completed_at = CASE WHEN $2 IN ('completed','failed','cancelled') THEN now() ELSE completed_at END
WHERE id=$1`, id, status)
	return err
}

func (r *BatchRepo) ClaimItems(ctx context.Context, batchID, worker string, limit int) ([]app.BatchItem, error) {
	rows, err := r.q(ctx).Query(ctx, `
UPDATE work_item_batch_item i SET status='claimed', lease_expires_at=now()+interval '2 minutes',
  claimed_by=$2, attempts=attempts+1, updated_at=now()
WHERE i.id IN (
  SELECT id FROM work_item_batch_item
  WHERE batch_id=$1 AND status='pending'
  ORDER BY seq FOR UPDATE SKIP LOCKED LIMIT $3
) RETURNING i.id, i.batch_id, i.seq, i.payload_ref, i.status, i.attempts, i.max_attempts`,
		batchID, worker, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.BatchItem
	for rows.Next() {
		var it app.BatchItem
		if err := rows.Scan(&it.ID, &it.BatchID, &it.Seq, &it.PayloadRef, &it.Status, &it.Attempts, &it.MaxAttempts); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (r *BatchRepo) FinishItem(ctx context.Context, itemID, status string, result map[string]any, errMsg string) (bool, error) {
	remaining := -1
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_batch_item SET status=$2, result_ref=$3::jsonb, error=nullif($4,''),
  lease_expires_at=NULL, claimed_by=NULL, updated_at=now()
WHERE id=$1 AND status IN ('claimed','processing')`,
			itemID, status, mustJSONB(result), errMsg)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			remaining = 0
			return nil
		}
		if _, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_batch b SET
  succeeded = succeeded + CASE WHEN $2='succeeded' THEN 1 ELSE 0 END,
  failed    = failed    + CASE WHEN $2='failed'    THEN 1 ELSE 0 END,
  skipped   = skipped   + CASE WHEN $2='skipped'   THEN 1 ELSE 0 END,
  status    = CASE WHEN b.status='pending' THEN 'running' ELSE b.status END
WHERE b.id = (SELECT batch_id FROM work_item_batch_item WHERE id=$1)`,
			itemID, status); err != nil {
			return err
		}
		return r.q(ctx).QueryRow(ctx, `
SELECT count(*) FROM work_item_batch_item
WHERE batch_id = (SELECT batch_id FROM work_item_batch_item WHERE id=$1)
  AND status NOT IN ('succeeded','failed','skipped')`, itemID).Scan(&remaining)
	})
	if err != nil {
		return false, err
	}
	return remaining == 0, nil
}

func (r *BatchRepo) CompleteIfAllDone(ctx context.Context, batchID, final string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_batch SET
  status = CASE WHEN succeeded = 0 AND failed > 0 AND failed + skipped = total THEN 'failed'
                ELSE 'completed' END,
  completed_at = now()
WHERE id=$1 AND status IN ('running','pending')
  AND NOT EXISTS (SELECT 1 FROM work_item_batch_item
                  WHERE batch_id=$1 AND status NOT IN ('succeeded','failed','skipped'))`,
		batchID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *BatchRepo) ReclaimExpiredLeases(ctx context.Context) (int, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_batch_item SET status='pending', lease_expires_at=NULL, claimed_by=NULL, updated_at=now()
WHERE status IN ('claimed','processing') AND lease_expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *BatchRepo) RequeueFailed(ctx context.Context, batchID string) (int, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_batch_item SET status='pending', attempts=0, updated_at=now()
WHERE batch_id=$1 AND status='failed'`, batchID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *BatchRepo) GetBatchCounts(ctx context.Context, batchID string) (int, int, int, int, error) {
	var pending, claimed, processing, done int
	err := r.q(ctx).QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE status='pending'),
  count(*) FILTER (WHERE status='claimed'),
  count(*) FILTER (WHERE status='processing'),
  count(*) FILTER (WHERE status IN ('succeeded','failed','skipped'))
FROM work_item_batch_item WHERE batch_id=$1`, batchID).
		Scan(&pending, &claimed, &processing, &done)
	return pending, claimed, processing, done, err
}

func mustJSONB(v map[string]any) []byte {
	if v == nil {
		return []byte("{}")
	}
	b, _ := json.Marshal(v)
	return b
}

func (r *BatchRepo) ListActiveBatches(ctx context.Context) ([]app.Batch, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT `+batchCols+` FROM work_item_batch
WHERE status IN ('pending','running') ORDER BY created_at LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Batch
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BatchRepo) ListBatchesByWorkItem(ctx context.Context, ws, wi string) ([]app.Batch, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT `+batchCols+` FROM work_item_batch WHERE workspace_id=$1 AND work_item_id=$2
ORDER BY created_at DESC LIMIT 50`, ws, wi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Batch
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BatchRepo) ListItems(ctx context.Context, ws, batchID, status string, limit int) ([]app.BatchItem, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `SELECT id, batch_id, seq, payload_ref, status, attempts, max_attempts, coalesce(error,'')
FROM work_item_batch_item WHERE workspace_id=$1 AND batch_id=$2`
	args := []any{ws, batchID}
	if status != "" {
		q += ` AND status=$3`
		args = append(args, status)
	}
	q += ` ORDER BY seq LIMIT ` + strconv.Itoa(limit)
	rows, err := r.q(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.BatchItem
	for rows.Next() {
		var it app.BatchItem
		if err := rows.Scan(&it.ID, &it.BatchID, &it.Seq, &it.PayloadRef, &it.Status,
			&it.Attempts, &it.MaxAttempts, &it.Error); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
