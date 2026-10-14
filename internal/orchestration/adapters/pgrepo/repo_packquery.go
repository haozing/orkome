package pgrepo

// Pack 聚合读端口（pack 一等公民化 P1，W1-2）：workspace_mcp_server / agent
// 的 pack_id 维度读取（迁移 1042 加列）。返回 orchestration 本域类型
//（arch iron law rule 3：adapters 不跨域 import）——pack 域的引用形状由
// 组合根适配器转换（cmd/orkome-server/wiring_pack.go）。

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

// ListWorkspaceMCPByPack 返回 workspace_mcp_server.pack_id 命中的注册表行。
// 只报 HasCreds 有无、不解封凭证（密封信封不出 orchestration 域，FP-09 纪律），
// Credentials 恒 nil。
func (r *Repo) ListWorkspaceMCPByPack(ctx context.Context, wsID, packID string) ([]orchapp.WorkspaceMCP, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT `+wsMCPCols+` FROM workspace_mcp_server WHERE workspace_id=$1 AND pack_id=$2 ORDER BY name`, wsID, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.WorkspaceMCP
	for rows.Next() {
		var m orchapp.WorkspaceMCP
		var raw []byte
		var packIDCol string
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &m.Name, &m.Transport, &m.URL, &raw, &m.CreatedAt, &packIDCol); err != nil {
			return nil, err
		}
		m.PackID = packIDCol // 空串口径=手工；此处恒非空（WHERE pack_id=$2）
		m.HasCreds = len(raw) > 0 && string(raw) != "{}"
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListAgentsByPack 返回 agent.pack_id 命中的完整行（含 archived——详情页资产
// 区块区分展示；归档是 uninstall 的显式动作，读取侧不过滤）。
func (r *Repo) ListAgentsByPack(ctx context.Context, wsID, packID string) ([]orchapp.Agent, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+agentCols+` FROM agent WHERE workspace_id=$1 AND pack_id=$2 ORDER BY created_at`, wsID, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAgentPack 挂载/卸载 agent 的能力包归属（1042 的 agent.pack_id 列）。
// packID nil = 置 NULL（卸下）；非 nil = 直接 UPDATE。ws 限定 + rows affected
// 收口与 ArchiveAgent 同款：跨区一律 404，不泄露存在性。非法 uuid 字面量
//（22P02）映射 400——pack pgrepo 的 invalidUUID 同策略。
func (r *Repo) SetAgentPack(ctx context.Context, wsID, agentID string, packID *string) error {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE agent SET pack_id=$3
WHERE workspace_id=$1 AND id=$2`, wsID, agentID, packID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
			return apierr.NewValidation("invalid agent or pack id")
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return apierr.New(apierr.NotFound, "agent not found in this workspace")
	}
	return nil
}

// ListPackToolSchemasDetailed 按 pack_id 读存档行，带 output_schema 原文
//（能力包平台化：usage-document 渲染"工具契约"区块需要完整输出契约，
// ListPackToolSchemasByPack 那份不含）。OutputSchemaJSON 是 jsonb 的原样
// 文本——格式化/降级由 pack 域渲染层决定，这里不做反序列化有损转换。
func (r *Repo) ListPackToolSchemasDetailed(ctx context.Context, wsID, packID string) ([]orchapp.PackToolSchemaDetail, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT server, tool_name, source, coalesce(output_schema::text,''), installed_at
FROM pack_tool_schema WHERE workspace_id=$1 AND pack_id=$2 ORDER BY server, tool_name`, wsID, packID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "22P02" {
			return nil, apierr.NewValidation("invalid pack id")
		}
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.PackToolSchemaDetail
	for rows.Next() {
		var it orchapp.PackToolSchemaDetail
		if err := rows.Scan(&it.Server, &it.ToolName, &it.Source, &it.OutputSchemaJSON, &it.InstalledAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
