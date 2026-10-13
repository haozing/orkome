package pgrepo

// Pack 聚合读端口（pack 一等公民化 P1，W1-2）：workspace_mcp_server / agent
// 的 pack_id 维度读取（迁移 1042 加列）。返回 orchestration 本域类型
//（arch iron law rule 3：adapters 不跨域 import）——pack 域的引用形状由
// 组合根适配器转换（cmd/orkome-server/wiring_pack.go）。

import (
	"context"

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
