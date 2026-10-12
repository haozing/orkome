package pgrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/automation/app"
	"orkome/internal/automation/domain"
)

type Repo struct{ DB *pg.DB }

func New(db *pg.DB) *Repo { return &Repo{DB: db} }

var _ app.Repo = (*Repo)(nil)

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }
func noRow(err error) bool                    { return errors.Is(err, pgx.ErrNoRows) }

func wrapLockTimeout(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
		return fmt.Errorf("%w: %s", domain.ErrLockTimeout, pgErr.Message)
	}
	return err
}

func (r *Repo) CreateWakeup(ctx context.Context, w app.Wakeup) (app.Wakeup, error) {
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO work_item_wakeup (workspace_id, work_item_id, agent_id, created_by, kind, instruction,
  interval_seconds, cron_expression, timezone, next_fire_at, max_fires, condition,
  event_types, filter_agent_id, filter_task_id, filter_actor_type, filter_actor_id, expires_at)
VALUES ($1,$2,nullif($3,'')::uuid,nullif($4,'')::uuid,$5,$6,$7,nullif($8,''),$9,$10,$11,$12::jsonb,
  $13,nullif($14,'')::uuid,nullif($15,'')::uuid,nullif($16,''),nullif($17,'')::uuid,$18)
RETURNING id, fire_count`,
		w.WorkspaceID, w.WorkItemID, w.AgentID, w.CreatedBy, w.Kind, w.Instruction,
		w.IntervalSeconds, w.CronExpression, w.Timezone, w.NextFireAt, w.MaxFires, condJSON(w.Condition),
		w.EventTypes, w.FilterAgentID, w.FilterTaskID, w.FilterActorType, w.FilterActorID, w.ExpiresAt).
		Scan(&w.ID, &w.FireCount)
	return w, err
}

func condJSON(m map[string]any) []byte {
	if m == nil {
		return []byte("{}")
	}
	b, _ := json.Marshal(m)
	return b
}

const wakeupSel = `SELECT id, workspace_id, work_item_id, coalesce(agent_id::text,''), coalesce(created_by::text,''),
kind, instruction, coalesce(interval_seconds,0), coalesce(cron_expression,''), timezone, next_fire_at,
enabled, fire_count, coalesce(max_fires,0), condition, condition_state, revision,
coalesce(event_types,'{}'::text[]), coalesce(filter_agent_id::text,''), coalesce(filter_task_id::text,''),
coalesce(filter_actor_type,''), coalesce(filter_actor_id::text,''), coalesce(system_rule,''),
expires_at, coalesce(paused_reason,''), coalesce(last_task_id::text,'') FROM work_item_wakeup`

func scanWakeup(row pgx.Row) (app.Wakeup, error) {
	var w app.Wakeup
	err := row.Scan(&w.ID, &w.WorkspaceID, &w.WorkItemID, &w.AgentID, &w.CreatedBy, &w.Kind, &w.Instruction,
		&w.IntervalSeconds, &w.CronExpression, &w.Timezone, &w.NextFireAt, &w.Enabled, &w.FireCount,
		&w.MaxFires, &w.Condition, &w.ConditionState, &w.Revision,
		&w.EventTypes, &w.FilterAgentID, &w.FilterTaskID, &w.FilterActorType, &w.FilterActorID,
		&w.SystemRule, &w.ExpiresAt, &w.PausedReason, &w.LastTaskID)
	return w, err
}

func (r *Repo) GetWakeup(ctx context.Context, ws, id string) (app.Wakeup, bool, error) {
	w, err := scanWakeup(r.q(ctx).QueryRow(ctx, wakeupSel+` WHERE workspace_id=$1 AND id=$2`, ws, id))
	if noRow(err) {
		return w, false, nil
	}
	return w, err == nil, err
}

func (r *Repo) ListWakeupsByWorkItem(ctx context.Context, wi string) ([]app.Wakeup, error) {
	rows, err := r.q(ctx).Query(ctx, wakeupSel+` WHERE work_item_id=$1 ORDER BY created_at`, wi)
	return collectWakeups(rows, err)
}

func (r *Repo) ListWorkspaceWakeups(ctx context.Context, ws string) ([]app.Wakeup, error) {
	rows, err := r.q(ctx).Query(ctx, wakeupSel+` WHERE workspace_id=$1 ORDER BY created_at`, ws)
	return collectWakeups(rows, err)
}

func (r *Repo) ListPausedWakeups(ctx context.Context, ws string) ([]app.Wakeup, error) {
	rows, err := r.q(ctx).Query(ctx,
		wakeupSel+` WHERE workspace_id=$1 AND coalesce(paused_reason,'') <> '' ORDER BY created_at`, ws)
	return collectWakeups(rows, err)
}

func collectWakeups(rows pgx.Rows, err error) ([]app.Wakeup, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Wakeup
	for rows.Next() {
		w, err := scanWakeup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r *Repo) SetWakeupEnabled(ctx context.Context, ws, id string, en bool) error {
	_, err := r.q(ctx).Exec(ctx, `UPDATE work_item_wakeup SET enabled=$3, revision=revision+1 WHERE workspace_id=$1 AND id=$2`, ws, id, en)
	return err
}

func (r *Repo) DeleteWakeup(ctx context.Context, ws, id string) error {
	_, err := r.q(ctx).Exec(ctx, `DELETE FROM work_item_wakeup WHERE workspace_id=$1 AND id=$2`, ws, id)
	return err
}

func (r *Repo) UpdateWakeup(ctx context.Context, w app.Wakeup, expectedRev int64) (app.Wakeup, bool, error) {
	row := r.q(ctx).QueryRow(ctx, `
UPDATE work_item_wakeup SET
  instruction=$3, interval_seconds=$4, cron_expression=nullif($5,''),
  timezone=coalesce(nullif($6,''),'UTC'), next_fire_at=$7, max_fires=$8,
  event_types=$9, filter_agent_id=nullif($10,'')::uuid, filter_task_id=nullif($11,'')::uuid,
  filter_actor_type=nullif($12,''), filter_actor_id=nullif($13,'')::uuid, condition=$14::jsonb,
  revision=revision+1
WHERE workspace_id=$1 AND id=$2 AND revision=$15
RETURNING id, workspace_id, work_item_id, coalesce(agent_id::text,''), coalesce(created_by::text,''),
kind, instruction, coalesce(interval_seconds,0), coalesce(cron_expression,''), timezone, next_fire_at,
enabled, fire_count, coalesce(max_fires,0), condition, condition_state, revision,
coalesce(event_types,'{}'::text[]), coalesce(filter_agent_id::text,''), coalesce(filter_task_id::text,''),
coalesce(filter_actor_type,''), coalesce(filter_actor_id::text,''), coalesce(system_rule,''),
expires_at, coalesce(paused_reason,''), coalesce(last_task_id::text,'')`,
		w.WorkspaceID, w.ID, w.Instruction, w.IntervalSeconds, w.CronExpression, w.Timezone,
		w.NextFireAt, w.MaxFires, w.EventTypes, w.FilterAgentID, w.FilterTaskID, w.FilterActorType,
		w.FilterActorID, condJSON(w.Condition), expectedRev)
	out, err := scanWakeup(row)
	if noRow(err) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) SetWakeupInstruction(ctx context.Context, ws, id, instruction string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx,
		`UPDATE work_item_wakeup SET instruction=$3, revision=revision+1 WHERE workspace_id=$1 AND id=$2`,
		ws, id, instruction)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) ListWakeupSummaries(ctx context.Context, ws string) ([]app.WakeupSummary, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT work_item_id::text, count(*)::int, count(*) FILTER (WHERE enabled)::int,
  count(*) FILTER (WHERE coalesce(paused_reason,'') <> '')::int,
  min(next_fire_at) FILTER (WHERE enabled),
  coalesce((array_agg(last_task_id ORDER BY created_at DESC) FILTER (WHERE last_task_id IS NOT NULL))[1]::text, '')
FROM work_item_wakeup WHERE workspace_id=$1
GROUP BY work_item_id ORDER BY min(created_at)`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.WakeupSummary
	for rows.Next() {
		var s app.WakeupSummary
		if err := rows.Scan(&s.WorkItemID, &s.Total, &s.Enabled, &s.Paused, &s.NextFireAt, &s.LastTaskID); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

const dueScanSQL = `SELECT * FROM (
SELECT id, workspace_id, work_item_id, coalesce(agent_id::text,''), coalesce(created_by::text,''),
kind, instruction, coalesce(interval_seconds,0), coalesce(cron_expression,''), timezone, next_fire_at,
enabled, fire_count, coalesce(max_fires,0), condition, condition_state, revision,
coalesce(event_types,'{}'::text[]), coalesce(filter_agent_id::text,''), coalesce(filter_task_id::text,''),
coalesce(filter_actor_type,''), coalesce(filter_actor_id::text,''), coalesce(system_rule,''),
expires_at, coalesce(paused_reason,''), coalesce(last_task_id::text,''),
'due' AS scan_source, ''::text AS receipt_id, 0::bigint AS receipt_rev, ''::text AS event_key, NULL::timestamptz AS captured_at
FROM work_item_wakeup
WHERE enabled AND coalesce(paused_reason,'')='' AND kind='time' AND next_fire_at IS NOT NULL AND next_fire_at <= $1
UNION ALL
SELECT id, workspace_id, work_item_id, coalesce(agent_id::text,''), coalesce(created_by::text,''),
kind, instruction, coalesce(interval_seconds,0), coalesce(cron_expression,''), timezone, next_fire_at,
enabled, fire_count, coalesce(max_fires,0), condition, condition_state, revision,
coalesce(event_types,'{}'::text[]), coalesce(filter_agent_id::text,''), coalesce(filter_task_id::text,''),
coalesce(filter_actor_type,''), coalesce(filter_actor_id::text,''), coalesce(system_rule,''),
expires_at, coalesce(paused_reason,''), coalesce(last_task_id::text,''),
'expire', ''::text, 0::bigint, ''::text, NULL::timestamptz
FROM work_item_wakeup
WHERE enabled AND expires_at IS NOT NULL AND expires_at <= $1
UNION ALL
SELECT id, workspace_id, work_item_id, coalesce(agent_id::text,''), coalesce(created_by::text,''),
kind, instruction, coalesce(interval_seconds,0), coalesce(cron_expression,''), timezone, next_fire_at,
enabled, fire_count, coalesce(max_fires,0), condition, condition_state, revision,
coalesce(event_types,'{}'::text[]), coalesce(filter_agent_id::text,''), coalesce(filter_task_id::text,''),
coalesce(filter_actor_type,''), coalesce(filter_actor_id::text,''), coalesce(system_rule,''),
expires_at, coalesce(paused_reason,''), coalesce(last_task_id::text,''),
'condition', ''::text, 0::bigint, ''::text, NULL::timestamptz
FROM work_item_wakeup
WHERE enabled AND coalesce(paused_reason,'')='' AND kind IN ('condition','system')
UNION ALL
SELECT w.id, w.workspace_id, w.work_item_id, coalesce(w.agent_id::text,''), coalesce(w.created_by::text,''),
w.kind, w.instruction, coalesce(w.interval_seconds,0), coalesce(w.cron_expression,''), w.timezone, w.next_fire_at,
w.enabled, w.fire_count, coalesce(w.max_fires,0), w.condition, w.condition_state, w.revision,
coalesce(w.event_types,'{}'::text[]), coalesce(w.filter_agent_id::text,''), coalesce(w.filter_task_id::text,''),
coalesce(w.filter_actor_type,''), coalesce(w.filter_actor_id::text,''), coalesce(w.system_rule,''),
w.expires_at, coalesce(w.paused_reason,''), coalesce(w.last_task_id::text,''),
'receipt', rec.id::text, rec.revision, rec.event_key, rec.captured_at
FROM work_item_wakeup_receipt rec JOIN work_item_wakeup w ON w.id = rec.wakeup_id
WHERE rec.processed_at IS NULL AND w.enabled AND coalesce(w.paused_reason,'')=''
) t ORDER BY t.captured_at ASC NULLS LAST, t.id LIMIT $2`

func (r *Repo) ListDueWakeups(ctx context.Context, now time.Time, limit int) ([]app.DueWakeup, error) {
	rows, err := r.q(ctx).Query(ctx, dueScanSQL, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.DueWakeup
	for rows.Next() {
		var d app.DueWakeup
		var w app.Wakeup
		if err := rows.Scan(&w.ID, &w.WorkspaceID, &w.WorkItemID, &w.AgentID, &w.CreatedBy, &w.Kind,
			&w.Instruction, &w.IntervalSeconds, &w.CronExpression, &w.Timezone, &w.NextFireAt,
			&w.Enabled, &w.FireCount, &w.MaxFires, &w.Condition, &w.ConditionState, &w.Revision,
			&w.EventTypes, &w.FilterAgentID, &w.FilterTaskID, &w.FilterActorType, &w.FilterActorID,
			&w.SystemRule, &w.ExpiresAt, &w.PausedReason, &w.LastTaskID,
			&d.Source, &d.ReceiptID, &d.ReceiptRev, &d.EventKey, &d.CapturedAt); err != nil {
			return nil, err
		}
		d.Wakeup = w
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repo) ListConditionWakeups(ctx context.Context) ([]app.Wakeup, error) {
	rows, err := r.q(ctx).Query(ctx, wakeupSel+` WHERE enabled AND kind='condition'`)
	return collectWakeups(rows, err)
}

func (r *Repo) ListEventWakeups(ctx context.Context, ws, eventType string) ([]app.Wakeup, error) {
	rows, err := r.q(ctx).Query(ctx,
		wakeupSel+` WHERE enabled AND kind='event' AND workspace_id=$1 AND $2 = ANY(event_types)`, ws, eventType)
	return collectWakeups(rows, err)
}

func (r *Repo) InsertReceipt(ctx context.Context, rc app.Receipt) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO work_item_wakeup_receipt (wakeup_id, revision, event_key, workspace_id, payload)
VALUES ($1,$2,$3,$4,$5::jsonb)
ON CONFLICT (wakeup_id, revision, event_key) DO NOTHING`,
		rc.WakeupID, rc.Revision, rc.EventKey, rc.WorkspaceID, payloadJSON(rc.Payload))
	return err
}

func payloadJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

func (r *Repo) MarkReceiptProcessed(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE work_item_wakeup_receipt SET processed_at=now() WHERE id=$1 AND processed_at IS NULL`, id)
	return err
}

func (r *Repo) MarkWakeupExpired(ctx context.Context, id string, expectedRev int64) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_wakeup SET enabled=false, timed_out_at=now(), paused_reason='expired', revision=revision+1
WHERE id=$1 AND revision=$2 AND enabled`, id, expectedRev)
	if err != nil {
		return false, wrapLockTimeout(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) ListRecentWakeupFires(ctx context.Context, wakeupID string, since time.Time, limit int) ([]time.Time, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := r.q(ctx).Query(ctx, `
SELECT captured_at FROM work_item_wakeup_receipt
WHERE wakeup_id=$1::uuid AND captured_at >= $2
ORDER BY captured_at DESC LIMIT $3`, wakeupID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) PauseWakeupBurst(ctx context.Context, id string, expectedRev int64, resumeAt time.Time) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_wakeup SET paused_reason=$3, next_fire_at=$4, revision=revision+1
WHERE id=$1 AND revision=$2 AND enabled AND coalesce(paused_reason,'')=''`,
		id, expectedRev, domain.PausedReasonBurst, resumeAt)
	if err != nil {
		return false, wrapLockTimeout(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) ListBurstPausedWakeups(ctx context.Context, now time.Time, limit int) ([]app.Wakeup, error) {
	rows, err := r.q(ctx).Query(ctx,
		wakeupSel+` WHERE paused_reason=$1 AND enabled AND next_fire_at IS NOT NULL AND next_fire_at <= $2
LIMIT $3`, domain.PausedReasonBurst, now, limit)
	return collectWakeups(rows, err)
}

func (r *Repo) ResumeWakeupBurst(ctx context.Context, id string, expectedRev int64) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_wakeup SET paused_reason=NULL, next_fire_at=NULL, revision=revision+1
WHERE id=$1 AND revision=$2 AND paused_reason=$3`,
		id, expectedRev, domain.PausedReasonBurst)
	if err != nil {
		return false, wrapLockTimeout(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) LatestWakeupChain(ctx context.Context, ws, workItemID, agentID string) ([]string, error) {
	var raw []byte
	err := r.q(ctx).QueryRow(ctx, `
SELECT t.context->'wakeup_chain' FROM agent_task_queue t
WHERE t.workspace_id=$1::uuid AND t.work_item_id=$2::uuid AND t.agent_id=$3::uuid
  AND jsonb_typeof(t.context->'wakeup_chain') = 'array'
ORDER BY t.created_at DESC LIMIT 1`, ws, workItemID, agentID).Scan(&raw)
	if noRow(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var chain []string
	if err := json.Unmarshal(raw, &chain); err != nil {
		return nil, nil
	}
	return chain, nil
}

func (r *Repo) PauseWakeupLoop(ctx context.Context, id string, expectedRev int64) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_wakeup SET paused_reason=$3, revision=revision+1
WHERE id=$1 AND revision=$2 AND enabled AND coalesce(paused_reason,'')=''`,
		id, expectedRev, domain.PausedReasonLoop)
	if err != nil {
		return false, wrapLockTimeout(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) LockTaskOwner(ctx context.Context, workItemID string) error {
	if _, err := r.q(ctx).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, workItemID); err != nil {
		return wrapLockTimeout(err)
	}
	var one string
	err := r.q(ctx).QueryRow(ctx, `SELECT id FROM work_item WHERE id=$1::uuid FOR UPDATE`, workItemID).Scan(&one)
	if noRow(err) {
		return nil
	}
	return wrapLockTimeout(err)
}

func (r *Repo) LockWakeup(ctx context.Context, id string) (app.Wakeup, bool, error) {
	w, err := scanWakeup(r.q(ctx).QueryRow(ctx, wakeupSel+` WHERE id=$1 FOR UPDATE`, id))
	if noRow(err) {
		return w, false, nil
	}
	if err != nil {
		return w, false, wrapLockTimeout(err)
	}
	return w, true, nil
}

func (r *Repo) FireWakeup(ctx context.Context, id string, expectedRev int64, nextFire *time.Time, condState *string, taskID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_wakeup SET fire_count = fire_count + 1,
  next_fire_at = $2, condition_state = coalesce($3, condition_state),
  last_task_id = nullif($4,'')::uuid, revision = revision + 1
WHERE id = $1 AND revision = $5`, id, nextFire, condState, taskID, expectedRev)
	if err != nil {
		return false, wrapLockTimeout(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) ListSystemWakeups(ctx context.Context, ws string) ([]app.Wakeup, error) {
	rows, err := r.q(ctx).Query(ctx,
		wakeupSel+` WHERE workspace_id=$1 AND system_rule IS NOT NULL ORDER BY created_at`, ws)
	return collectWakeups(rows, err)
}

func (r *Repo) ListSystemWakeupsByWorkItem(ctx context.Context, ws, wi string) ([]app.Wakeup, error) {
	rows, err := r.q(ctx).Query(ctx,
		wakeupSel+` WHERE workspace_id=$1 AND work_item_id=$2 AND system_rule IS NOT NULL ORDER BY created_at`, ws, wi)
	return collectWakeups(rows, err)
}

func (r *Repo) EnsureSystemWakeup(ctx context.Context, w app.Wakeup) (app.Wakeup, error) {
	// W2-2 归属裁决：created_by 有意不写入（保持 NULL）——system wakeup 由平台
	// 播种、系统发起无主，不存在"创建人"；fire 侧据此走 unattributed 分支
	// （wiring_automation.go 已去 orZeroUUID 补零），而非撞 originator FK。
	row := r.q(ctx).QueryRow(ctx, `
INSERT INTO work_item_wakeup (workspace_id, work_item_id, agent_id, kind, instruction,
  system_rule, condition, condition_state, enabled)
VALUES ($1,$2,NULL,$3,$4,$5,$6::jsonb,$7,$8)
ON CONFLICT (work_item_id) WHERE system_rule = 'children_done' DO NOTHING
RETURNING id, workspace_id, work_item_id, coalesce(agent_id::text,''), coalesce(created_by::text,''),
kind, instruction, coalesce(interval_seconds,0), coalesce(cron_expression,''), timezone, next_fire_at,
enabled, fire_count, coalesce(max_fires,0), condition, condition_state, revision,
coalesce(event_types,'{}'::text[]), coalesce(filter_agent_id::text,''), coalesce(filter_task_id::text,''),
coalesce(filter_actor_type,''), coalesce(filter_actor_id::text,''), coalesce(system_rule,''),
expires_at, coalesce(paused_reason,''), coalesce(last_task_id::text,'')`,
		w.WorkspaceID, w.WorkItemID, w.Kind, w.Instruction, w.SystemRule,
		condJSON(w.Condition), w.ConditionState, w.Enabled)
	out, err := scanWakeup(row)
	if noRow(err) {
		rows, err2 := r.ListSystemWakeupsByWorkItem(ctx, w.WorkspaceID, w.WorkItemID)
		if err2 != nil || len(rows) == 0 {
			return out, err2
		}
		return rows[0], nil
	}
	return out, err
}

func (r *Repo) UpdateSystemWakeup(ctx context.Context, ws, id string, instruction *string, enabled *bool) (app.Wakeup, bool, error) {
	row := r.q(ctx).QueryRow(ctx, `
UPDATE work_item_wakeup SET
  instruction = CASE WHEN $3::text IS NULL THEN instruction ELSE $3 END,
  enabled = coalesce($4, enabled), customized_at = now(), revision = revision + 1
WHERE workspace_id=$1 AND id=$2 AND system_rule IS NOT NULL
RETURNING id, workspace_id, work_item_id, coalesce(agent_id::text,''), coalesce(created_by::text,''),
kind, instruction, coalesce(interval_seconds,0), coalesce(cron_expression,''), timezone, next_fire_at,
enabled, fire_count, coalesce(max_fires,0), condition, condition_state, revision,
coalesce(event_types,'{}'::text[]), coalesce(filter_agent_id::text,''), coalesce(filter_task_id::text,''),
coalesce(filter_actor_type,''), coalesce(filter_actor_id::text,''), coalesce(system_rule,''),
expires_at, coalesce(paused_reason,''), coalesce(last_task_id::text,'')`,
		ws, id, instruction, enabled)
	out, err := scanWakeup(row)
	if noRow(err) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) GetSystemWakeupDefault(ctx context.Context, ws string) (app.SystemWakeupDefault, bool, error) {
	var d app.SystemWakeupDefault
	err := r.q(ctx).QueryRow(ctx,
		`SELECT enabled, coalesce(instruction,'') FROM system_wakeup_setting WHERE workspace_id=$1`, ws).
		Scan(&d.Enabled, &d.Instruction)
	if noRow(err) {
		return d, false, nil
	}
	return d, err == nil, err
}

func (r *Repo) SetSystemWakeupDefault(ctx context.Context, ws string, d app.SystemWakeupDefault) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO system_wakeup_setting (workspace_id, enabled, instruction) VALUES ($1,$2,nullif($3,''))
ON CONFLICT (workspace_id) DO UPDATE SET enabled=$2, instruction=nullif($3,'')`,
		ws, d.Enabled, d.Instruction)
	return err
}

func (r *Repo) CreateAutopilot(ctx context.Context, a app.Autopilot) (app.Autopilot, error) {
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO autopilot (workspace_id, name, exec_mode, template, target_agent_id, daily_limit, created_by)
VALUES ($1,$2,$3,$4,nullif($5,'')::uuid,$6,nullif($7,'')::uuid) RETURNING id`,
		a.WorkspaceID, a.Name, a.ExecMode, a.Template, a.TargetAgentID, a.DailyLimit, a.CreatedBy).Scan(&a.ID)
	return a, err
}

func (r *Repo) GetAutopilot(ctx context.Context, ws, id string) (app.Autopilot, bool, error) {
	var a app.Autopilot
	err := r.q(ctx).QueryRow(ctx, `
SELECT id, workspace_id, name, exec_mode, template, coalesce(target_agent_id::text,''), enabled, daily_limit,
coalesce(created_by::text,''), coalesce(paused_reason,'') FROM autopilot WHERE id=$2 AND ($1::text='' OR workspace_id::text=$1)`, ws, id).
		Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.ExecMode, &a.Template, &a.TargetAgentID, &a.Enabled, &a.DailyLimit, &a.CreatedBy, &a.PausedReason)
	if noRow(err) {
		return a, false, nil
	}
	return a, err == nil, err
}

func (r *Repo) ListAutopilots(ctx context.Context, ws string, limit int) ([]app.Autopilot, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT id, workspace_id, name, exec_mode, template, coalesce(target_agent_id::text,''), enabled, daily_limit,
coalesce(created_by::text,''), coalesce(paused_reason,'') FROM autopilot WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT $2`, ws, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Autopilot
	for rows.Next() {
		var a app.Autopilot
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.ExecMode, &a.Template, &a.TargetAgentID, &a.Enabled, &a.DailyLimit, &a.CreatedBy, &a.PausedReason); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repo) UpdateAutopilot(ctx context.Context, ws, id string, name, execMode *string,
	template map[string]any, targetAgentID *string, enabled *bool) (app.Autopilot, bool, error) {
	var tpl []byte
	if template != nil {
		tpl = condJSON(template)
	}
	var a app.Autopilot
	err := r.q(ctx).QueryRow(ctx, `
UPDATE autopilot SET
  name = CASE WHEN $3::text IS NULL THEN name ELSE $3 END,
  exec_mode = CASE WHEN $4::text IS NULL THEN exec_mode ELSE $4 END,
  template = CASE WHEN $5::jsonb IS NULL THEN template ELSE $5 END,
  target_agent_id = CASE WHEN $6::text IS NULL THEN target_agent_id ELSE nullif($6,'')::uuid END,
  enabled = coalesce($7, enabled),
  paused_reason = CASE WHEN coalesce($7, enabled) THEN NULL ELSE paused_reason END
WHERE workspace_id=$1 AND id=$2
RETURNING id, workspace_id, name, exec_mode, template, coalesce(target_agent_id::text,''), enabled, daily_limit,
coalesce(created_by::text,''), coalesce(paused_reason,'')`,
		ws, id, name, execMode, tpl, targetAgentID, enabled).
		Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.ExecMode, &a.Template, &a.TargetAgentID, &a.Enabled, &a.DailyLimit, &a.CreatedBy, &a.PausedReason)
	if noRow(err) {
		return a, false, nil
	}
	return a, err == nil, err
}

func (r *Repo) PauseAutopilotsByFailureRate(ctx context.Context, window time.Duration, minRuns int, failRate float64, pausedReason string) ([]app.AutopilotFailureStat, error) {
	rows, err := r.q(ctx).Query(ctx, `
WITH win AS (
  SELECT r.autopilot_id, count(*)::int AS runs,
    count(*) FILTER (WHERE r.status='failed')::int AS failed
  FROM autopilot_run r
  WHERE r.planned_at >= now() - ($1::int * interval '1 second')
  GROUP BY r.autopilot_id
), hit AS (
  SELECT w.autopilot_id, w.runs, w.failed FROM win w
  WHERE w.runs >= $2 AND w.failed::float8 / w.runs >= $3
), paused AS (
  UPDATE autopilot a SET enabled=false, paused_reason=$4
  FROM hit h
  WHERE a.id = h.autopilot_id AND a.enabled
  RETURNING a.id, a.workspace_id::text, a.name, coalesce(a.created_by::text,''), h.runs, h.failed
)
SELECT autopilot_id, workspace_id, name, created_by, runs, failed FROM paused`,
		int(window.Seconds()), minRuns, failRate, pausedReason)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.AutopilotFailureStat
	for rows.Next() {
		var s app.AutopilotFailureStat
		if err := rows.Scan(&s.AutopilotID, &s.WorkspaceID, &s.Name, &s.CreatedBy, &s.Runs, &s.Failed); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) DeleteAutopilot(ctx context.Context, ws, id string) (app.DeleteOutcome, error) {
	var out app.DeleteOutcome
	err := r.q(ctx).QueryRow(ctx, `
	WITH target AS (
	  SELECT a.id,
	    EXISTS (SELECT 1 FROM autopilot_trigger t WHERE t.autopilot_id = a.id AND t.enabled
	      AND (t.kind = 'webhook' OR (t.kind = 'schedule' AND coalesce(t.cron_expression,'') <> ''))) AS has_enabled_triggers,
	    EXISTS (SELECT 1 FROM autopilot_run r WHERE r.autopilot_id = a.id AND r.status IN ('pending','running')) AS has_active_runs
	  FROM autopilot a WHERE a.workspace_id = $1::uuid AND a.id = $2::uuid
	), guard AS (
  SELECT id FROM target WHERE NOT has_enabled_triggers AND NOT has_active_runs
), del_runs AS (
  DELETE FROM autopilot_run r USING guard g WHERE r.autopilot_id = g.id
), del_deliveries AS (
  DELETE FROM automation_webhook_delivery d USING guard g, autopilot_trigger t
  WHERE d.trigger_id = t.id AND t.autopilot_id = g.id
), del_quota AS (
  DELETE FROM autopilot_quota_reservation q USING guard g WHERE q.autopilot_id = g.id
), del_triggers AS (
  DELETE FROM autopilot_trigger t USING guard g WHERE t.autopilot_id = g.id
), del_ap AS (
  DELETE FROM autopilot a USING guard g WHERE a.id = g.id RETURNING a.id
)
SELECT EXISTS (SELECT 1 FROM target),
  EXISTS (SELECT 1 FROM del_ap),
  COALESCE((SELECT has_enabled_triggers FROM target), false),
  COALESCE((SELECT has_active_runs FROM target), false)`,
		ws, id).Scan(&out.Found, &out.Deleted, &out.BlockedTriggers, &out.BlockedRuns)
	return out, err
}

func (r *Repo) CreateTrigger(ctx context.Context, t app.Trigger) (app.Trigger, error) {

	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO autopilot_trigger (autopilot_id, kind, cron_expression, timezone, next_run_at, webhook_token, signing_secret, enabled)
VALUES ($1,$2,$3,$4,$5,nullif($6,''),nullif($7,''),$8) RETURNING id`,
		t.AutopilotID, t.Kind, t.CronExpression, t.Timezone, t.NextRunAt, t.WebhookToken, t.SigningSecret, t.Enabled).Scan(&t.ID)
	return t, err
}

func scanTrigger(row pgx.Row) (app.Trigger, error) {
	var t app.Trigger
	var ws string
	err := row.Scan(&t.ID, &t.AutopilotID, &t.Kind, &t.CronExpression, &t.Timezone, &t.NextRunAt,
		&t.WebhookToken, &t.SigningSecret, &t.Enabled, &ws)
	t.SetWorkspace(ws)
	return t, err
}

const triggerSel = `SELECT t.id, t.autopilot_id, t.kind, coalesce(t.cron_expression,''), t.timezone, t.next_run_at,
coalesce(t.webhook_token,''), coalesce(t.signing_secret,''), t.enabled, a.workspace_id
FROM autopilot_trigger t JOIN autopilot a ON a.id = t.autopilot_id`

func (r *Repo) GetTriggerByToken(ctx context.Context, token string) (app.Trigger, bool, error) {
	t, err := scanTrigger(r.q(ctx).QueryRow(ctx, triggerSel+` WHERE t.webhook_token=$1`, token))
	if noRow(err) {
		return t, false, nil
	}
	return t, err == nil, err
}

func (r *Repo) GetTriggerByID(ctx context.Context, id string) (app.Trigger, bool, error) {
	t, err := scanTrigger(r.q(ctx).QueryRow(ctx, triggerSel+` WHERE t.id=$1`, id))
	if noRow(err) {
		return t, false, nil
	}
	return t, err == nil, err
}

func (r *Repo) ListDueScheduleTriggers(ctx context.Context, now time.Time) ([]app.Trigger, error) {
	rows, err := r.q(ctx).Query(ctx, triggerSel+`
WHERE t.enabled AND t.kind='schedule' AND t.next_run_at IS NOT NULL AND t.next_run_at <= $1`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Trigger
	for rows.Next() {
		t, err := scanTrigger(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) ClaimDueTrigger(ctx context.Context, id string, from, to *time.Time) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE autopilot_trigger SET next_run_at=$3
WHERE id=$1::uuid AND next_run_at IS NOT DISTINCT FROM $2`, id, from, to)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) UpdateTrigger(ctx context.Context, ws, autopilotID, triggerID string, cron, tz *string, nextRun *time.Time, enabled *bool) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE autopilot_trigger t SET
  cron_expression = CASE WHEN $4::text IS NULL THEN t.cron_expression ELSE nullif($4,'') END,
  timezone = CASE WHEN $5::text IS NULL THEN t.timezone ELSE $5 END,
  next_run_at = $6, enabled = coalesce($7, t.enabled)
WHERE t.id=$3::uuid`+triggerScope,
		ws, autopilotID, triggerID, cron, tz, nextRun, enabled)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) DeleteTrigger(ctx context.Context, ws, autopilotID, triggerID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx,
		`DELETE FROM autopilot_trigger t WHERE t.id=$3::uuid`+triggerScope, ws, autopilotID, triggerID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

const triggerScope = ` AND EXISTS (
  SELECT 1 FROM autopilot a WHERE a.id = t.autopilot_id AND a.workspace_id = $1::uuid AND a.id = $2::uuid)`

func (r *Repo) SetTriggerSigningSecret(ctx context.Context, ws, autopilotID, triggerID, secret string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE autopilot_trigger t SET signing_secret=$4 WHERE t.id=$3::uuid`+triggerScope,
		ws, autopilotID, triggerID, secret)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) SetTriggerWebhookToken(ctx context.Context, ws, autopilotID, triggerID, token string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE autopilot_trigger t SET webhook_token=$4 WHERE t.id=$3::uuid`+triggerScope,
		ws, autopilotID, triggerID, token)
	if err != nil {
		if isDup(err) {
			return false, fmt.Errorf("webhook token rotate: %w", domain.ErrWebhookTokenCollision)
		}
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

const runCols = `r.id, r.autopilot_id, coalesce(r.trigger_id::text,''), r.source, r.status,
coalesce(r.work_item_id::text,''), coalesce(r.task_id::text,''), r.planned_at, r.triggered_at,
coalesce(r.reason_code,'')`

func scanRun(row pgx.Row) (app.Run, error) {
	var run app.Run
	err := row.Scan(&run.ID, &run.AutopilotID, &run.TriggerID, &run.Source, &run.Status,
		&run.WorkItemID, &run.TaskID, &run.PlannedAt, &run.TriggeredAt, &run.ReasonCode)
	return run, err
}

func (r *Repo) ListRunsByAutopilot(ctx context.Context, ws, autopilotID string, limit int) ([]app.Run, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT `+runCols+` FROM autopilot_run r JOIN autopilot a ON a.id = r.autopilot_id
WHERE a.workspace_id=$1 AND ($2::text='' OR r.autopilot_id=$2::uuid)
ORDER BY r.planned_at DESC LIMIT $3`, ws, autopilotID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (r *Repo) GetRun(ctx context.Context, ws, runID string) (app.Run, bool, error) {
	run, err := scanRun(r.q(ctx).QueryRow(ctx, `
SELECT `+runCols+` FROM autopilot_run r JOIN autopilot a ON a.id = r.autopilot_id
WHERE a.workspace_id=$1 AND r.id=$2::uuid`, ws, runID))
	if noRow(err) {
		return run, false, nil
	}
	return run, err == nil, err
}

func (r *Repo) InsertRun(ctx context.Context, run app.Run) (app.Run, error) {
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO autopilot_run (autopilot_id, trigger_id, source, status, planned_at, reason_code, quota_reservation_id)
VALUES ($1,nullif($2,'')::uuid,$3,$4,$5,nullif($6,''),nullif($7,'')::uuid) RETURNING id`,
		run.AutopilotID, run.TriggerID, run.Source, run.Status, run.PlannedAt, run.ReasonCode, run.QuotaReservationID).Scan(&run.ID)
	return run, err
}

func (r *Repo) SetRunTask(ctx context.Context, id, taskID, wi string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE autopilot_run SET task_id=$2, work_item_id=nullif($3,'')::uuid, triggered_at=now()
WHERE id=$1`, id, taskID, wi)
	return err
}

func (r *Repo) SetRunStatus(ctx context.Context, id, status, reason string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE autopilot_run SET status=$2, failure_reason=nullif($3,''),
completed_at=CASE WHEN $2 IN ('completed','failed','skipped') THEN now() ELSE completed_at END
WHERE id=$1`, id, status, reason)
	return err
}

func (r *Repo) SetRunStatusByTask(ctx context.Context, taskID, status string) error {
	_, err := r.q(ctx).Exec(ctx, `
WITH done AS (
  UPDATE autopilot_run SET status=$2,
    completed_at=CASE WHEN $2 IN ('completed','failed') THEN now() ELSE completed_at END
  WHERE task_id=$1 AND status='running'
  RETURNING quota_reservation_id
)
UPDATE autopilot_quota_reservation q
SET reserved = q.reserved - 1,
    committed = q.committed + 1,
    status = CASE WHEN q.reserved - 1 <= 0 THEN 'committed' ELSE 'reserved' END,
    committed_at = now()
FROM done d
WHERE q.id = d.quota_reservation_id AND q.reserved > 0`, taskID, status)
	return err
}

func (r *Repo) ReserveQuota(ctx context.Context, autopilotID string, windowStart, windowEnd time.Time, limit int) (string, error) {
	var id string
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO autopilot_quota_reservation (autopilot_id, window_start, window_end, reserved)
VALUES ($1,$2,$3,1)
ON CONFLICT (autopilot_id, window_start) DO UPDATE
  SET reserved = autopilot_quota_reservation.reserved + 1
  WHERE autopilot_quota_reservation.reserved + autopilot_quota_reservation.committed < $4
RETURNING id`, autopilotID, windowStart, windowEnd, limit).Scan(&id)
	if noRow(err) {
		return "", fmt.Errorf("quota window: %w", domain.ErrQuotaExceeded)
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

func (r *Repo) SetDispatchLockTimeout(ctx context.Context) error {
	_, err := r.q(ctx).Exec(ctx,
		fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", domain.DispatchLockTimeout.Milliseconds()))
	return err
}

func (r *Repo) InsertDelivery(ctx context.Context, ws, triggerID, key string, payload []byte) (string, bool, error) {
	var id string
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO automation_webhook_delivery (workspace_id, trigger_id, dedupe_key, payload)
VALUES ($1,$2,$3,$4::jsonb) RETURNING id`, ws, triggerID, key, payload).Scan(&id)
	if err != nil {
		if isDup(err) {
			return "", true, nil
		}
		return "", false, err
	}
	return id, false, nil
}

func isDup(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (r *Repo) ClaimDelivery(ctx context.Context) (string, string, string, []byte, int, bool, error) {
	var id, trig, ws string
	var payload []byte
	var attempts int
	err := r.q(ctx).QueryRow(ctx, `
UPDATE automation_webhook_delivery d SET status='processing', attempts=attempts+1,
  lease_token=gen_random_uuid()::text, lease_expires_at=now()+interval '2 minutes'
WHERE d.id = (
  SELECT id FROM automation_webhook_delivery
  WHERE status='queued' AND (next_attempt_at IS NULL OR next_attempt_at <= now())
  ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
) RETURNING d.id, d.trigger_id, d.workspace_id::text, d.payload, d.attempts`).
		Scan(&id, &trig, &ws, &payload, &attempts)
	if noRow(err) {
		return "", "", "", nil, 0, false, nil
	}
	if err != nil {
		return "", "", "", nil, 0, false, err
	}
	return id, trig, ws, payload, attempts, true, nil
}

func (r *Repo) FinishDelivery(ctx context.Context, id, status, errMsg string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE automation_webhook_delivery SET status=$2, error=nullif($3,''), next_attempt_at=NULL,
delivered_at=CASE WHEN $2='delivered' THEN now() ELSE delivered_at END,
lease_token=NULL, lease_expires_at=NULL WHERE id=$1`, id, status, errMsg)
	return err
}

func (r *Repo) FailDelivery(ctx context.Context, id string, deadLetter bool, nextAttemptAt *time.Time, errMsg string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE automation_webhook_delivery SET
  status=CASE WHEN $2 THEN 'failed' ELSE 'queued' END,
  error=nullif($3,''), next_attempt_at=$4, lease_token=NULL, lease_expires_at=NULL
WHERE id=$1`, id, deadLetter, errMsg, nextAttemptAt)
	return err
}

func (r *Repo) GetDelivery(ctx context.Context, ws, id string) (app.Delivery, bool, error) {
	var d app.Delivery
	err := r.q(ctx).QueryRow(ctx, `
SELECT id, workspace_id::text, trigger_id::text, status, attempts, coalesce(error,''), created_at, next_attempt_at
FROM automation_webhook_delivery WHERE workspace_id=$1 AND id=$2`, ws, id).
		Scan(&d.ID, &d.WorkspaceID, &d.TriggerID, &d.Status, &d.Attempts, &d.Error, &d.CreatedAt, &d.NextAttemptAt)
	if noRow(err) {
		return d, false, nil
	}
	return d, err == nil, err
}

func (r *Repo) ListDeliveries(ctx context.Context, ws string, limit int) ([]app.Delivery, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT id, workspace_id::text, trigger_id::text, status, attempts, coalesce(error,''), created_at, next_attempt_at
FROM automation_webhook_delivery WHERE workspace_id=$1 ORDER BY created_at DESC LIMIT $2`, ws, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Delivery
	for rows.Next() {
		var d app.Delivery
		if err := rows.Scan(&d.ID, &d.WorkspaceID, &d.TriggerID, &d.Status, &d.Attempts, &d.Error, &d.CreatedAt, &d.NextAttemptAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repo) ReplayDelivery(ctx context.Context, ws, id string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE automation_webhook_delivery SET status='queued', attempts=0, next_attempt_at=NULL,
  error=NULL, lease_token=NULL, lease_expires_at=NULL
WHERE workspace_id=$1 AND id=$2`, ws, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) ListTemplateSubscribers(ctx context.Context, autopilotID string) ([]app.SubscriberRef, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT s.user_id::text FROM autopilot_subscriber s
JOIN autopilot a ON a.id = s.autopilot_id
WHERE s.autopilot_id=$1 AND s.user_type='member'
ORDER BY s.created_at, s.user_id`, autopilotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.SubscriberRef
	for rows.Next() {
		var s app.SubscriberRef
		if err := rows.Scan(&s.UserID); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) InsertRuleVersion(ctx context.Context, v app.RuleVersion) (app.RuleVersion, error) {
	summary := v.ConfigSummary
	if len(summary) == 0 {
		summary = []byte("{}")
	}
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO autopilot_rule_version (workspace_id, autopilot_id, published_by_type, published_by_id, config_summary)
VALUES ($1,$2,$3,nullif($4,'')::uuid,$5::jsonb)
RETURNING id, created_at`,
		v.WorkspaceID, v.AutopilotID, v.PublishedByType, v.PublishedByID, summary).
		Scan(&v.ID, &v.CreatedAt)
	return v, err
}

const collaboratorScope = ` AND EXISTS (
  SELECT 1 FROM autopilot a WHERE a.id = c.autopilot_id AND a.workspace_id = $1::uuid AND a.id = $2::uuid)`

const collaboratorCols = `c.autopilot_id, c.workspace_id::text, c.user_type, c.user_id::text, c.granted_by::text, c.created_at`

func (r *Repo) ListCollaborators(ctx context.Context, ws, autopilotID string) ([]app.Collaborator, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT `+collaboratorCols+` FROM autopilot_collaborator c WHERE TRUE`+collaboratorScope+`
ORDER BY c.created_at, c.user_id`, ws, autopilotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Collaborator
	for rows.Next() {
		var c app.Collaborator
		var cws string
		if err := rows.Scan(&c.AutopilotID, &cws, &c.UserType, &c.UserID, &c.GrantedBy, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) AddCollaborator(ctx context.Context, ws, autopilotID, userID, grantedBy string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
INSERT INTO autopilot_collaborator (autopilot_id, workspace_id, user_type, user_id, granted_by)
SELECT a.id, a.workspace_id, 'member', $3::uuid, $4::uuid
FROM autopilot a
WHERE a.workspace_id=$1::uuid AND a.id=$2::uuid
ON CONFLICT (autopilot_id, user_type, user_id) DO NOTHING`, ws, autopilotID, userID, grantedBy)
	if err != nil {
		return false, err
	}

	if tag.RowsAffected() == 0 {
		return r.autopilotExists(ctx, ws, autopilotID)
	}
	return true, nil
}

func (r *Repo) RemoveCollaborator(ctx context.Context, ws, autopilotID, userID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
DELETE FROM autopilot_collaborator c WHERE c.user_id=$3::uuid`+collaboratorScope,
		ws, autopilotID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) autopilotExists(ctx context.Context, ws, autopilotID string) (bool, error) {
	var ok bool
	err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM autopilot WHERE workspace_id=$1 AND id=$2)`, ws, autopilotID).Scan(&ok)
	return ok, err
}
