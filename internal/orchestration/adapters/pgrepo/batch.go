package pgrepo

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/haozing/ploykit/platform/pg"
	"github.com/haozing/ploykit/platform/sealx"
	"orkome/internal/orchestration/app"
	"orkome/internal/platform/pgxerr"
)

type BatchRepo struct {
	DB *pg.DB
	// Seal 凭证密封器：物化代调/条目执行解析 MCP 凭证时解 sealx 信封
	//（fail-closed：未接线 = 解密报错，绝不回落明文）。
	Seal *sealx.Sealer
}

var _ app.BatchRepo = (*BatchRepo)(nil)

func (r *BatchRepo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }

const batchCols = `id, workspace_id, work_item_id, kind, status, params, source, total,
succeeded, failed, skipped, concurrency, chunk_size, pace_interval_seconds,
coalesce(originator_user_id::text,''), coalesce(originator_task_id,''), coalesce(originator_agent_id::text,''),
coalesce(source_materialization,'{}'::jsonb), coalesce(last_error,''), created_at, completed_at`

func scanBatch(row pgx.Row) (app.Batch, error) {
	var b app.Batch
	err := row.Scan(&b.ID, &b.WorkspaceID, &b.WorkItemID, &b.Kind, &b.Status, &b.Params, &b.Source,
		&b.Total, &b.Succeeded, &b.Failed, &b.Skipped, &b.Concurrency, &b.ChunkSize, &b.PaceIntervalSeconds,
		&b.OriginatorUserID, &b.OriginatorTaskID, &b.OriginatorAgentID, &b.SourceMaterialization,
		&b.LastError, &b.CreatedAt, &b.CompletedAt)
	return b, err
}

func (r *BatchRepo) InsertBatch(ctx context.Context, b app.Batch, payloads []map[string]any) (app.Batch, error) {
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		if err := r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
INSERT INTO work_item_batch (workspace_id, work_item_id, kind, status, params, source, total,
  concurrency, chunk_size, pace_interval_seconds, originator_user_id, accountable_user_id,
  originator_task_id, originator_agent_id, source_materialization)
VALUES ($1::uuid,$2::uuid,$3,$4,$5::jsonb,$6::jsonb,$7,$8,$9,$10,nullif($11::text,'')::uuid,nullif($11::text,'')::uuid,
  nullif($12,''),nullif($13::text,'')::uuid,$14::jsonb)
RETURNING %s`, batchCols),
			b.WorkspaceID, b.WorkItemID, b.Kind, b.Status, mustJSONB(b.Params), mustJSONB(b.Source), b.Total,
			b.Concurrency, b.ChunkSize, b.PaceIntervalSeconds, b.OriginatorUserID,
			b.OriginatorTaskID, b.OriginatorAgentID, mustJSONB(b.SourceMaterialization)).
			Scan(&b.ID, &b.WorkspaceID, &b.WorkItemID, &b.Kind, &b.Status, &b.Params, &b.Source,
				&b.Total, &b.Succeeded, &b.Failed, &b.Skipped, &b.Concurrency, &b.ChunkSize, &b.PaceIntervalSeconds,
				&b.OriginatorUserID, &b.OriginatorTaskID, &b.OriginatorAgentID, &b.SourceMaterialization,
				&b.LastError, &b.CreatedAt, &b.CompletedAt); err != nil {
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
	return b, pgxerr.AsNotFound(err, "work item not found")
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

// StartBatch draft→pending 转移（通道 B 预览确认后开跑）。返回是否发生了
// 转移（非 draft = false，由 service 层转语义错误）。
func (r *BatchRepo) StartBatch(ctx context.Context, batchID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_batch SET status='pending', started_at=now()
WHERE id=$1 AND status='draft'`, batchID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListItemsPage 结果收割分页：seq 游标（afterSeq 之后的 limit 条），收割
// 工具逐页翻（不给一次全量的口子）。
func (r *BatchRepo) ListItemsPage(ctx context.Context, ws, batchID, status string, afterSeq, limit int) ([]app.BatchItem, error) {
	q := `SELECT id, batch_id, seq, payload_ref, status, attempts, max_attempts,
  coalesce(skip_reason,''), coalesce(error,'')
FROM work_item_batch_item WHERE workspace_id=$1 AND batch_id=$2 AND seq > $3`
	args := []any{ws, batchID, afterSeq}
	if status != "" {
		q += ` AND status=$4`
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
			&it.Attempts, &it.MaxAttempts, &it.SkipReason, &it.Error); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// InsertSourceItems 通道 B 落表：batch 行（status=draft + 对账）+ 成品与
// skipped 占位同序写满（seq 1..N 连续，skipped 行带 skip_reason）。一次
// 事务，物化结果就是表中所见。
func (r *BatchRepo) InsertSourceItems(ctx context.Context, b app.Batch, mat *app.MaterializedSource) (app.Batch, error) {
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		if err := r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
INSERT INTO work_item_batch (workspace_id, work_item_id, kind, status, params, source, total,
  concurrency, chunk_size, pace_interval_seconds, originator_user_id, accountable_user_id,
  originator_task_id, originator_agent_id, source_materialization)
VALUES ($1::uuid,$2::uuid,$3,'draft',$4::jsonb,$5::jsonb,$6,$7,$8,$9,nullif($10::text,'')::uuid,nullif($10::text,'')::uuid,
  nullif($11,''),nullif($12::text,'')::uuid,$13::jsonb)
RETURNING %s`, batchCols),
			b.WorkspaceID, b.WorkItemID, b.Kind, mustJSONB(b.Params), mustJSONB(b.Source), b.Total,
			b.Concurrency, b.ChunkSize, b.PaceIntervalSeconds, b.OriginatorUserID,
			b.OriginatorTaskID, b.OriginatorAgentID, mustJSONB(b.SourceMaterialization)).
			Scan(&b.ID, &b.WorkspaceID, &b.WorkItemID, &b.Kind, &b.Status, &b.Params, &b.Source,
				&b.Total, &b.Succeeded, &b.Failed, &b.Skipped, &b.Concurrency, &b.ChunkSize, &b.PaceIntervalSeconds,
				&b.OriginatorUserID, &b.OriginatorTaskID, &b.OriginatorAgentID, &b.SourceMaterialization,
				&b.LastError, &b.CreatedAt, &b.CompletedAt); err != nil {
			return err
		}
		// 同序合并：mat.Payloads（成品，按抽取序）与 mat.SkipReasons（被跳过，
		// 按抽取序）交织还原 seq 1..N。成品行 status=pending（start 后可跑），
		// skipped 行 status=skipped + skip_reason（worker 永不 claim）。
		const chunk = 1000
		flush := func(rows []app.BatchItem) error {
			if len(rows) == 0 {
				return nil
			}
			vals, args := "", []any{b.ID, b.WorkspaceID}
			n := 2
			for _, it := range rows {
				if vals != "" {
					vals += ","
				}
				n++
				plPh, stPh, srPh := n, n+1, n+2
				n += 2
				vals += fmt.Sprintf("($1::uuid, $2::uuid, %d, $%d::jsonb, $%d, nullif($%d,''))", it.Seq, plPh, stPh, srPh)
				args = append(args, mustJSONB(it.PayloadRef), it.Status, it.SkipReason)
			}
			_, err := r.q(ctx).Exec(ctx, fmt.Sprintf(`
INSERT INTO work_item_batch_item (batch_id, workspace_id, seq, payload_ref, status, skip_reason) VALUES %s`, vals), args...)
			return err
		}
		var buf []app.BatchItem
		pi, si := 0, 0
		seq := 0
		for pi < len(mat.Payloads) || si < len(mat.SkipReasons) {
			seq++
			if si < len(mat.SkipReasons) && mat.SkipReasons[si] != "" {
				// 被跳过的抽取位（原序占位；它的 payload 不存在）
				buf = append(buf, app.BatchItem{Seq: seq, Status: "skipped", SkipReason: mat.SkipReasons[si]})
				si++
			} else if pi < len(mat.Payloads) {
				buf = append(buf, app.BatchItem{Seq: seq, Status: "pending", PayloadRef: mat.Payloads[pi]})
				pi++
			} else {
				break
			}
			if len(buf) >= chunk {
				if err := flush(buf); err != nil {
					return err
				}
				buf = buf[:0]
			}
		}
		return flush(buf)
	})
	return b, pgxerr.AsNotFound(err, "work item not found")
}
