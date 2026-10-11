package pgrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/content/app"
)

type Repo struct{ DB *pg.DB }

func New(db *pg.DB) *Repo { return &Repo{DB: db} }

var _ app.Repo = (*Repo)(nil)

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }

const aCols = `id::text, workspace_id::text, host_kind, coalesce(host_id::text,''), filename,
content_type, size_bytes, storage_key, sha256, coalesce(created_by::text,''), created_at`

func scanA(row pgx.Row) (app.Attachment, error) {
	var a app.Attachment
	err := row.Scan(&a.ID, &a.WorkspaceID, &a.HostKind, &a.HostID, &a.Filename,
		&a.ContentType, &a.SizeBytes, &a.StorageKey, &a.SHA256, &a.CreatedBy, &a.CreatedAt)
	return a, err
}

func (r *Repo) InsertAttachment(ctx context.Context, a app.Attachment) (app.Attachment, error) {
	row := r.q(ctx).QueryRow(ctx, `
INSERT INTO attachment (workspace_id, host_kind, host_id, filename, content_type, size_bytes, storage_key, sha256, created_by)
VALUES ($1,$2,nullif($3,'')::uuid,$4,$5,$6,$7,$8,nullif($9,'')::uuid)
RETURNING `+aCols,
		a.WorkspaceID, a.HostKind, a.HostID, a.Filename, a.ContentType, a.SizeBytes,
		a.StorageKey, a.SHA256, a.CreatedBy)
	return scanA(row)
}

func (r *Repo) GetAttachment(ctx context.Context, wsID, id string) (app.Attachment, bool, error) {
	a, err := scanA(r.q(ctx).QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM attachment WHERE workspace_id=$1 AND id=$2`, aCols), wsID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, false, nil
	}
	return a, err == nil, err
}

func (r *Repo) GetAttachmentByID(ctx context.Context, id string) (app.Attachment, bool, error) {
	a, err := scanA(r.q(ctx).QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM attachment WHERE id=$1`, aCols), id))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, false, nil
	}
	return a, err == nil, err
}

func (r *Repo) HostUsage(ctx context.Context, wsID, hostKind, hostID string) (app.HostUsage, error) {
	var u app.HostUsage
	err := r.q(ctx).QueryRow(ctx, `
SELECT count(*), coalesce(sum(size_bytes),0) FROM attachment
WHERE workspace_id=$1 AND host_kind=$2 AND coalesce(host_id::text,'')=$3`,
		wsID, hostKind, hostID).Scan(&u.Count, &u.Bytes)
	return u, err
}

func (r *Repo) DeleteAttachment(ctx context.Context, wsID, id string) (string, bool, error) {
	var key string
	err := r.q(ctx).QueryRow(ctx,
		`DELETE FROM attachment WHERE workspace_id=$1 AND id=$2 RETURNING storage_key`, wsID, id).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return key, err == nil, err
}

func (r *Repo) ListByHost(ctx context.Context, wsID, hostKind, hostID string) ([]app.Attachment, error) {
	rows, err := r.q(ctx).Query(ctx, fmt.Sprintf(
		`SELECT %s FROM attachment WHERE workspace_id=$1 AND host_kind=$2 AND host_id=$3 ORDER BY created_at`, aCols),
		wsID, hostKind, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Attachment
	for rows.Next() {
		a, err := scanA(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
