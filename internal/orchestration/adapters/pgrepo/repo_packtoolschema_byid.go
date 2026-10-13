package pgrepo

// Pack 聚合读端口的另一半（pack 一等公民化 P1，W1-2）：pack_tool_schema 与
// pack_ui_entry 的 pack_id 维度读取（迁移 1042 加列）。返回 orchestration
// 本域类型（arch iron law rule 3）——pack 域的引用形状由组合根适配器转换。
// Replace/Delete 的 pack 维度写面仍在 repo_packtoolschema.go / repo_uientry.go
// 的 text 键版本——provision 改造（R3）切换到 pack_id 键时再动。

import (
	"context"
	"encoding/json"

	orchapp "orkome/internal/orchestration/app"
)

// ListPackToolSchemasByPack 按 pack_id 读存档行（详情页"工具"区块）。
func (r *Repo) ListPackToolSchemasByPack(ctx context.Context, ws, packID string) ([]orchapp.PackToolSchema, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT workspace_id::text, pack, server, tool_name, output_schema, source, installed_at
FROM pack_tool_schema WHERE workspace_id=$1 AND pack_id=$2 ORDER BY server, tool_name`, ws, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.PackToolSchema
	for rows.Next() {
		it, err := scanPackToolSchema(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetPackUIEntryByPack 按 pack_id 读 UI 入口（详情页"界面"区块；含 slots）。
// 无入口返回 (nil, nil)——该 pack 未进驻 UI，不是错误。
func (r *Repo) GetPackUIEntryByPack(ctx context.Context, ws, packID string) (*orchapp.PackUIEntry, error) {
	row := r.q(ctx).QueryRow(ctx, `
SELECT id::text, workspace_id::text, pack, label, url_template, COALESCE(slots, '[]'::jsonb), installed_at
FROM pack_ui_entry WHERE workspace_id=$1 AND pack_id=$2`, ws, packID)
	var e orchapp.PackUIEntry
	var raw []byte
	if err := row.Scan(&e.ID, &e.WorkspaceID, &e.Pack, &e.Label, &e.URLTemplate, &raw, &e.InstalledAt); err != nil {
		if noRow(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &e.Slots); err != nil {
			return nil, err
		}
	}
	if e.Slots == nil {
		e.Slots = []orchapp.PackUISlot{}
	}
	return &e, nil
}
