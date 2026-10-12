package pgrepo

import (
	"context"
	"encoding/json"

	"orkome/internal/orchestration/app"
)

// pack_tool_schema（迁移 1035）读写：B1 pack-provision 安装期写入，
// C1 运行期桥接层读取。接口冻结见 docs/interfaces-job-handle-2026-10-11.md §6。

func (r *Repo) ReplacePackToolSchemas(ctx context.Context, ws, pack string, items []app.PackToolSchema) error {
	tx := r.DB.Tx(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM pack_tool_schema WHERE workspace_id=$1 AND pack=$2`, ws, pack); err != nil {
		return err
	}
	for _, it := range items {
		schema, err := json.Marshal(it.OutputSchema)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO pack_tool_schema (workspace_id, pack, server, tool_name, output_schema, source)
VALUES ($1,$2,$3,$4,$5::jsonb,$6)`,
			ws, pack, it.Server, it.ToolName, schema, it.Source); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) ListPackToolSchemas(ctx context.Context, ws string) ([]app.PackToolSchema, error) {
	rows, err := r.DB.Tx(ctx).Query(ctx, `
SELECT workspace_id::text, pack, server, tool_name, output_schema, source, installed_at
FROM pack_tool_schema WHERE workspace_id=$1 ORDER BY pack, tool_name`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.PackToolSchema
	for rows.Next() {
		it, err := scanPackToolSchema(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (r *Repo) GetPackToolSchema(ctx context.Context, ws, toolName string) (app.PackToolSchema, bool, error) {
	rows, err := r.DB.Tx(ctx).Query(ctx, `
SELECT workspace_id::text, pack, server, tool_name, output_schema, source, installed_at
FROM pack_tool_schema WHERE workspace_id=$1 AND tool_name=$2`, ws, toolName)
	if err != nil {
		return app.PackToolSchema{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return app.PackToolSchema{}, false, rows.Err()
	}
	it, err := scanPackToolSchema(rows)
	if err != nil {
		return app.PackToolSchema{}, false, err
	}
	return it, true, rows.Err()
}

func (r *Repo) DeletePackToolSchemas(ctx context.Context, ws, pack string) error {
	_, err := r.DB.Tx(ctx).Exec(ctx,
		`DELETE FROM pack_tool_schema WHERE workspace_id=$1 AND pack=$2`, ws, pack)
	return err
}

func scanPackToolSchema(rows interface {
	Scan(dest ...any) error
}) (app.PackToolSchema, error) {
	var it app.PackToolSchema
	var raw []byte
	if err := rows.Scan(&it.WorkspaceID, &it.Pack, &it.Server, &it.ToolName, &raw, &it.Source, &it.InstalledAt); err != nil {
		return app.PackToolSchema{}, err
	}
	if err := json.Unmarshal(raw, &it.OutputSchema); err != nil {
		return app.PackToolSchema{}, err
	}
	return it, nil
}
