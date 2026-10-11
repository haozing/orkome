// Package pgrepo persists the product account extensions (sidebar pins,
// ui tokens) against the shared database. Users, sessions, PATs, workspaces
// and members are framework-owned (ploykit migrations 002-004); this repo
// only READS the member table to gate product routes.
package pgrepo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/identity/app"
)

type Repo struct{ DB *pg.DB }

var _ app.PinRepo = (*Repo)(nil)

func New(db *pg.DB) *Repo { return &Repo{DB: db} }

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }

func errNoRow(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// GetMember mirrors the framework workspace repo semantics: active rows
// only (removed_at IS NULL).
func (r *Repo) GetMember(ctx context.Context, wsID, userID string) (app.Member, bool, error) {
	var m app.Member
	err := r.q(ctx).QueryRow(ctx, `
SELECT id, workspace_id, user_id, role, created_at
FROM member WHERE workspace_id=$1 AND user_id=$2 AND removed_at IS NULL`,
		wsID, userID).
		Scan(&m.ID, &m.WorkspaceID, &m.UserID, &m.Role, &m.CreatedAt)
	if errNoRow(err) {
		return m, false, nil
	}
	return m, err == nil, err
}

func (r *Repo) ListPins(ctx context.Context, wsID, userID string) ([]app.Pin, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT p.id, p.workspace_id, p.item_type, p.item_id, p.position, p.created_at
FROM pinned_item p JOIN workspace w ON w.id = p.workspace_id
WHERE p.workspace_id=$1 AND p.user_id=$2
ORDER BY p.position, p.created_at`, wsID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Pin
	for rows.Next() {
		var p app.Pin
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.ItemType, &p.ItemID, &p.Position, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) AddPin(ctx context.Context, wsID, userID, itemType, itemID string) (app.Pin, bool, error) {
	var p app.Pin
	err := r.q(ctx).QueryRow(ctx, `
WITH next AS (
  SELECT coalesce(max(position), 0) + 1 AS pos FROM pinned_item
  WHERE workspace_id=$1 AND user_id=$2
)
INSERT INTO pinned_item (workspace_id, user_id, item_type, item_id, position)
SELECT $1, $2, $3, $4, pos FROM next
ON CONFLICT (workspace_id, user_id, item_type, item_id) DO NOTHING
RETURNING id, workspace_id, item_type, item_id, position, created_at`,
		wsID, userID, itemType, itemID).
		Scan(&p.ID, &p.WorkspaceID, &p.ItemType, &p.ItemID, &p.Position, &p.CreatedAt)
	if errNoRow(err) {
		err = r.q(ctx).QueryRow(ctx, `
SELECT id, workspace_id, item_type, item_id, position, created_at
FROM pinned_item
WHERE workspace_id=$1 AND user_id=$2 AND item_type=$3 AND item_id=$4`,
			wsID, userID, itemType, itemID).
			Scan(&p.ID, &p.WorkspaceID, &p.ItemType, &p.ItemID, &p.Position, &p.CreatedAt)
		return p, false, err
	}
	return p, err == nil, err
}

func (r *Repo) ReorderPins(ctx context.Context, wsID, userID string, order []string) error {
	return r.DB.Within(ctx, func(ctx context.Context) error {
		for i, id := range order {
			tag, err := r.q(ctx).Exec(ctx, `
UPDATE pinned_item SET position=$3
WHERE workspace_id=$1 AND user_id=$2 AND id=$4`, wsID, userID, i+1, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return errors.New("pgrepo: pin row not owned by user during reorder")
			}
		}
		return nil
	})
}

func (r *Repo) RemovePin(ctx context.Context, wsID, userID, itemType, itemID string) error {
	_, err := r.q(ctx).Exec(ctx, `
DELETE FROM pinned_item
WHERE workspace_id=$1 AND user_id=$2 AND item_type=$3 AND item_id=$4`,
		wsID, userID, itemType, itemID)
	return err
}
