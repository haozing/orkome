package pgrepo

import (
	"context"
	"encoding/json"

	"orkome/internal/orchestration/app"
)

func (r *Repo) UpsertPackUIEntry(ctx context.Context, e app.PackUIEntry) error {
	// slots 随 entry 同行落库（迁移 1036 的 jsonb 列）；无 slots 写 NULL，
	// 与 label/url_template 一致的整行覆盖语义——重装即重写。
	var slotsJSON any
	if len(e.Slots) > 0 {
		b, err := json.Marshal(e.Slots)
		if err != nil {
			return err
		}
		slotsJSON = b
	}
	if e.PackID == "" {
		// 手动入口（集成页 §2.3）：pack_id 落 NULL，按 (workspace_id, pack)
		// 部分唯一索引（迁移 1047）覆盖；provision 行 pack_id 非 NULL，不落入
		// 该索引谓词，两条路径互不干扰。
		_, err := r.DB.Tx(ctx).Exec(ctx, `
INSERT INTO pack_ui_entry (workspace_id, pack, pack_id, label, url_template, slots)
VALUES ($1,$2,NULL,$3,$4,$5::jsonb)
ON CONFLICT (workspace_id, pack) WHERE pack_id IS NULL DO UPDATE SET label=$3, url_template=$4, slots=$5::jsonb`,
			e.WorkspaceID, e.Pack, e.Label, e.URLTemplate, slotsJSON)
		return err
	}
	_, err := r.DB.Tx(ctx).Exec(ctx, `
INSERT INTO pack_ui_entry (workspace_id, pack, pack_id, label, url_template, slots)
VALUES ($1,$2,$3,$4,$5,$6::jsonb)
ON CONFLICT (workspace_id, pack_id) DO UPDATE SET label=$4, url_template=$5, slots=$6::jsonb`,
		e.WorkspaceID, e.Pack, e.PackID, e.Label, e.URLTemplate, slotsJSON)
	return err
}

func (r *Repo) ListPackUIEntries(ctx context.Context, ws string) ([]app.PackUIEntry, error) {
	rows, err := r.DB.Tx(ctx).Query(ctx, `
SELECT id::text, workspace_id::text, pack, COALESCE(pack_id::text,''), label, url_template, COALESCE(slots, '[]'::jsonb), installed_at
FROM pack_ui_entry WHERE workspace_id=$1 ORDER BY pack`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.PackUIEntry
	for rows.Next() {
		var e app.PackUIEntry
		var slotsRaw []byte
		if err := rows.Scan(&e.ID, &e.WorkspaceID, &e.Pack, &e.PackID, &e.Label, &e.URLTemplate, &slotsRaw, &e.InstalledAt); err != nil {
			return nil, err
		}
		if len(slotsRaw) > 0 {
			if err := json.Unmarshal(slotsRaw, &e.Slots); err != nil {
				return nil, err
			}
		}
		if e.Slots == nil {
			e.Slots = []app.PackUISlot{}
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
