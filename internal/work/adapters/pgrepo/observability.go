package pgrepo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"orkome/internal/work/app"
)

func (r *Repo) SearchIndexSweepOrphans(ctx context.Context, olderThan time.Time, limit int) (int, error) {
	tag, err := r.q(ctx).Exec(ctx, `
WITH doomed AS (
  SELECT c.entity_type, c.entity_id FROM search_index_change c
  WHERE c.changed_at < $1
    AND (
      (c.entity_type='work_item' AND NOT EXISTS (SELECT 1 FROM work_item w WHERE w.id = c.entity_id))
      OR (c.entity_type='comment' AND NOT EXISTS (SELECT 1 FROM comment m WHERE m.id = c.entity_id))
      OR (c.entity_type='project' AND NOT EXISTS (SELECT 1 FROM project p WHERE p.id = c.entity_id))
    )
  ORDER BY c.changed_at
  LIMIT $2
  FOR UPDATE SKIP LOCKED
)
DELETE FROM search_index_change c
USING doomed d
WHERE c.entity_type = d.entity_type AND c.entity_id = d.entity_id`,
		olderThan, limit)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *Repo) PruneSearchIndexChanges(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
WITH doomed AS (
  SELECT sic.entity_type, sic.entity_id FROM search_index_change sic
  WHERE sic.changed_at < $1
  ORDER BY sic.changed_at
  LIMIT $2
  FOR UPDATE SKIP LOCKED
), deleted AS (
  DELETE FROM search_index_change c
  USING doomed d
  WHERE c.entity_type = d.entity_type AND c.entity_id = d.entity_id
  RETURNING c.change_xid
), marked AS (
  INSERT INTO search_index_prune_mark (singleton, pruned_through_xid)
  SELECT TRUE, dx.change_xid FROM (
    SELECT change_xid FROM deleted ORDER BY change_xid DESC LIMIT 1
  ) dx
  ON CONFLICT (singleton) DO UPDATE
    SET pruned_through_xid = GREATEST(search_index_prune_mark.pruned_through_xid, EXCLUDED.pruned_through_xid)
)
SELECT count(*) FROM deleted`, cutoff, limit).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (r *Repo) SearchIndexPruneMark(ctx context.Context) (string, bool, error) {
	var mark string
	err := r.q(ctx).QueryRow(ctx,
		`SELECT pruned_through_xid::text FROM search_index_prune_mark WHERE singleton`).Scan(&mark)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return mark, true, nil
}

func (r *Repo) ClientUsageUpsert(ctx context.Context, in app.ClientUsageReport) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO client_usage_daily (workspace_id, user_id, client_type, install_id,
  activity_date, client_version, os, first_active_at, last_active_at)
VALUES ($1, $2, $3, $4, (now() AT TIME ZONE 'utc')::date, $5, $6, now(), now())
ON CONFLICT (user_id, client_type, install_id, activity_date) DO UPDATE SET
  workspace_id    = EXCLUDED.workspace_id,
  client_version  = EXCLUDED.client_version,
  os              = EXCLUDED.os,
  first_active_at = LEAST(client_usage_daily.first_active_at, now()),
  last_active_at  = GREATEST(client_usage_daily.last_active_at, now()),
  updated_at      = now()`,
		in.WorkspaceID, in.UserID, in.ClientType, in.InstallID, in.ClientVersion, in.OS)
	if err != nil {
		return fmt.Errorf("client usage upsert: %w", err)
	}
	return nil
}
