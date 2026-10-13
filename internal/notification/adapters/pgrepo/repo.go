package pgrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/notification/app"
	"orkome/internal/notification/domain"
)

type Repo struct{ DB *pg.DB }

func New(db *pg.DB) *Repo { return &Repo{DB: db} }

var _ app.Repo = (*Repo)(nil)

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }
func noRow(err error) bool                    { return errors.Is(err, pgx.ErrNoRows) }

// InsertNotification writes one inbox row into the framework notification
// table filling BOTH column sets: user_id (the framework read surface —
// notifyhttp list/badge/read — queries it) and workspace_id/recipient_user_id/
// work_item_id (the product projection surface queries them). Idempotency
// rides the framework's uq_notification_dedup unique index (user_id,
// dedup_key): product dedup keys are already namespaced and are stored raw,
// unlike framework Notify() writes which prefix the notification type.
func (r *Repo) InsertNotification(ctx context.Context, ws, uid string, wi *string, typ, title, body, dedup string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO notification (user_id, workspace_id, recipient_user_id, work_item_id, type, title, body, dedup_key)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`,
		uid, ws, uid, wi, typ,
		domain.TruncateRunes(title, domain.TitleMaxRunes),
		domain.TruncateRunes(body, domain.SummaryMaxRunes),
		dedup)
	return err
}

func (r *Repo) ListNotification(ctx context.Context, ws, uid string, archived bool, limit int, before string) ([]app.Item, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	args := []any{ws, uid}
	cond := "workspace_id=$1 AND recipient_user_id=$2"
	if archived {
		cond += " AND archived_at IS NOT NULL"
	} else {
		cond += " AND archived_at IS NULL"
	}
	if before != "" {
		args = append(args, before)
		cond += fmt.Sprintf(" AND created_at < $%d::timestamptz", len(args))
	}
	args = append(args, limit)
	rows, err := r.q(ctx).Query(ctx, `
SELECT id::text, type, title, body, coalesce(work_item_id::text,''), read_at, created_at
FROM notification WHERE `+cond+` ORDER BY created_at DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []app.Item
	for rows.Next() {
		var it app.Item
		if err := rows.Scan(&it.ID, &it.Type, &it.Title, &it.Body, &it.WorkItemID, &it.ReadAt, &it.CreatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, it)
	}
	next := ""
	if len(out) == limit {
		next = out[len(out)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	return out, next, rows.Err()
}

func (r *Repo) UnreadCount(ctx context.Context, ws, uid string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
SELECT count(*) FROM notification
WHERE workspace_id=$1 AND recipient_user_id=$2 AND read_at IS NULL AND archived_at IS NULL`,
		ws, uid).Scan(&n)
	return n, err
}

func (r *Repo) Summary(ctx context.Context, ws, uid string) (app.NotificationSummary, error) {
	var s app.NotificationSummary
	err := r.q(ctx).QueryRow(ctx, `
SELECT count(*) FILTER (WHERE read_at IS NULL),
       count(*) FILTER (WHERE created_at >= date_trunc('day', now()))
FROM notification
WHERE workspace_id=$1 AND recipient_user_id=$2 AND archived_at IS NULL`,
		ws, uid).Scan(&s.Unread, &s.TodayNew)
	return s, err
}

func (r *Repo) Facets(ctx context.Context, ws, uid string) ([]app.Facet, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT type, count(*) FROM notification
WHERE workspace_id=$1 AND recipient_user_id=$2
GROUP BY type ORDER BY count(*) DESC, type`, ws, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Facet
	for rows.Next() {
		var f app.Facet
		if err := rows.Scan(&f.Type, &f.Count); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *Repo) MarkRead(ctx context.Context, ws, uid, id string, read bool) error {
	var err error
	if read {
		_, err = r.q(ctx).Exec(ctx, `
UPDATE notification SET read_at=now() WHERE workspace_id=$1 AND recipient_user_id=$2 AND id=$3 AND read_at IS NULL`, ws, uid, id)
	} else {
		_, err = r.q(ctx).Exec(ctx, `
UPDATE notification SET read_at=NULL WHERE workspace_id=$1 AND recipient_user_id=$2 AND id=$3`, ws, uid, id)
	}
	return err
}

func (r *Repo) MarkAllRead(ctx context.Context, ws, uid string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE notification SET read_at=now()
WHERE workspace_id=$1 AND recipient_user_id=$2 AND read_at IS NULL AND archived_at IS NULL`, ws, uid)
	return err
}

func (r *Repo) Archive(ctx context.Context, ws, uid, id string, archived bool) error {
	var err error
	if archived {
		_, err = r.q(ctx).Exec(ctx, `
UPDATE notification SET archived_at=now() WHERE workspace_id=$1 AND recipient_user_id=$2 AND id=$3 AND archived_at IS NULL`, ws, uid, id)
	} else {
		_, err = r.q(ctx).Exec(ctx, `
UPDATE notification SET archived_at=NULL WHERE workspace_id=$1 AND recipient_user_id=$2 AND id=$3`, ws, uid, id)
	}
	return err
}

func (r *Repo) ArchiveAll(ctx context.Context, ws, uid string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE notification SET archived_at=now()
WHERE workspace_id=$1 AND recipient_user_id=$2 AND archived_at IS NULL`, ws, uid)
	return err
}

func (r *Repo) ArchiveAllRead(ctx context.Context, ws, uid string) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE notification SET archived_at=now()
WHERE workspace_id=$1 AND recipient_user_id=$2 AND archived_at IS NULL AND read_at IS NOT NULL`, ws, uid)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *Repo) WriteNotice(ctx context.Context, workspaceID, userID, typ, title, body, dedupKey string) error {
	if app.MutedFor(ctx, r, workspaceID, userID, domain.MuteTarget{Type: typ}) {
		return nil
	}
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO notification (user_id, workspace_id, recipient_user_id, work_item_id, type, title, body, link, dedup_key)
VALUES ($1,$2,$3,NULL,$4,$5,$6,'',$7)
ON CONFLICT DO NOTHING`,
		userID, workspaceID, userID, typ,
		domain.TruncateRunes(title, domain.TitleMaxRunes),
		domain.TruncateRunes(body, domain.SummaryMaxRunes),
		dedupKey)
	return err
}

func splitMutedTypes(raw []string) (types, sessionIDs []string) {
	for _, e := range raw {
		if id, ok := strings.CutPrefix(e, app.SessionMutePrefix); ok {
			sessionIDs = append(sessionIDs, id)
		} else {
			types = append(types, e)
		}
	}
	return types, sessionIDs
}

func mergeMutedTypes(types, sessionIDs []string) []string {
	out := make([]string, 0, len(types)+len(sessionIDs))
	out = append(out, types...)
	for _, id := range sessionIDs {
		out = append(out, app.SessionMutePrefix+id)
	}
	return out
}

// MuteRowType is the notification_type sentinel marking orkome mute-group
// rows inside the framework notification_preference table (migration 1034):
// framework per-type toggle rows and product mute rows share the table, kept
// apart by the partial unique index uq_notification_preference_mute.
const MuteRowType = "mute"

func (r *Repo) GetPreference(ctx context.Context, ws, uid string) (app.Preference, error) {
	var p app.Preference
	var mt, mw []string
	err := r.q(ctx).QueryRow(ctx, `
SELECT muted_types, muted_work_item_ids FROM notification_preference
WHERE workspace_id=$1 AND user_id=$2 AND notification_type=$3`, ws, uid, MuteRowType).Scan(&mt, &mw)
	if noRow(err) {
		return app.Preference{}, nil
	}
	if err != nil {
		return p, err
	}
	p.MutedTypes, p.MutedConversationIDs = splitMutedTypes(mt)
	p.MutedWorkItemIDs = mw
	return p, nil
}

func (r *Repo) UpsertPreference(ctx context.Context, ws, uid string, p app.Preference) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO notification_preference (user_id, workspace_id, notification_type, muted_types, muted_work_item_ids)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (user_id, workspace_id) WHERE notification_type = 'mute' DO UPDATE
SET muted_types=EXCLUDED.muted_types, muted_work_item_ids=EXCLUDED.muted_work_item_ids, updated_at=now()`,
		uid, ws, MuteRowType,
		emptyIfNil(mergeMutedTypes(p.MutedTypes, p.MutedConversationIDs)),
		emptyIfNil(p.MutedWorkItemIDs))
	return err
}

func (r *Repo) WriteTaskCompleted(ctx context.Context, workspaceID, userID, workItemID, agentName, result string) error {
	if app.MutedFor(ctx, r, workspaceID, userID, domain.MuteTarget{Type: "task_completed", WorkItemID: workItemID}) {
		return nil
	}
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO notification (user_id, workspace_id, recipient_user_id, work_item_id, type, title, body, dedup_key)
VALUES ($1,$2,$3,nullif($4,'')::uuid,'task_completed',$5,$6,$7)
ON CONFLICT DO NOTHING`,
		userID, workspaceID, userID, workItemID,
		domain.TruncateRunes("Agent "+agentName+" completed the task", domain.TitleMaxRunes),
		domain.TruncateRunes(result, domain.SummaryMaxRunes),
		"task_completed:"+userID+":"+workItemID)
	return err
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r *Repo) WriteApprovalRequest(ctx context.Context, ws, userID, taskID, payload string) error {
	if app.MutedFor(ctx, r, ws, userID, domain.MuteTarget{Type: "approval_request"}) {
		return nil
	}
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO notification (user_id, workspace_id, recipient_user_id, type, title, body, dedup_key)
VALUES ($1,$2,$3,'approval_request',$4,$5,$6) ON CONFLICT DO NOTHING`,
		userID, ws, userID,
		domain.TruncateRunes("needs your approval", domain.TitleMaxRunes),
		domain.TruncateRunes(payload, domain.BodyMaxRunes),
		"approval:"+taskID)
	return err
}

func (r *Repo) WriteEvalReport(ctx context.Context, ws, userID, taskID, title, body string) error {
	if app.MutedFor(ctx, r, ws, userID, domain.MuteTarget{Type: "eval_report"}) {
		return nil
	}
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO notification (user_id, workspace_id, recipient_user_id, type, title, body, dedup_key)
VALUES ($1,$2,$3,'eval_report',$4,$5,$6)
ON CONFLICT DO NOTHING`,
		userID, ws, userID,
		domain.TruncateRunes(title, domain.TitleMaxRunes),
		domain.TruncateRunes(body, domain.BodyMaxRunes),
		"eval:"+taskID)
	return err
}

func (r *Repo) DeleteArchivedBefore(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	tag, err := r.q(ctx).Exec(ctx, `
DELETE FROM notification WHERE id IN (
  SELECT id FROM notification
  WHERE archived_at IS NOT NULL AND archived_at < $1
  ORDER BY archived_at
  LIMIT $2
)`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
