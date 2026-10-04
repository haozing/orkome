package pgrepo

import (
	"context"

	"orkome/internal/orchestration/app"
)

func (r *Repo) UpsertPackUIEntry(ctx context.Context, e app.PackUIEntry) error {
	_, err := r.DB.Tx(ctx).Exec(ctx, `
INSERT INTO pack_ui_entry (workspace_id, pack, label, url_template)
VALUES ($1,$2,$3,$4)
ON CONFLICT (workspace_id, pack) DO UPDATE SET label=$3, url_template=$4`,
		e.WorkspaceID, e.Pack, e.Label, e.URLTemplate)
	return err
}

func (r *Repo) ListPackUIEntries(ctx context.Context, ws string) ([]app.PackUIEntry, error) {
	rows, err := r.DB.Tx(ctx).Query(ctx, `
SELECT id::text, workspace_id::text, pack, label, url_template, installed_at
FROM pack_ui_entry WHERE workspace_id=$1 ORDER BY pack`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.PackUIEntry
	for rows.Next() {
		var e app.PackUIEntry
		if err := rows.Scan(&e.ID, &e.WorkspaceID, &e.Pack, &e.Label, &e.URLTemplate, &e.InstalledAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repo) DeletePackUIEntry(ctx context.Context, ws, pack string) error {
	_, err := r.DB.Tx(ctx).Exec(ctx,
		`DELETE FROM pack_ui_entry WHERE workspace_id=$1 AND pack=$2`, ws, pack)
	return err
}
