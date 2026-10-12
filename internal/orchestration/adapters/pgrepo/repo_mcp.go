package pgrepo

// MCP 服务器两级注册表（ws 级 workspace_mcp_server / agent 级 agent_mcp_server）
// 的存储实现。W2-3 自 repo.go 拆出为本文件（MCP 存储的独占维护点）。
//
// # 凭证密封（FP-09 P1-3 裁决：对照 agent_model_credential 先例）
//
//   - 写入：credentials 非空时必须经 Seal（sealx AES-GCM）加密，以信封
//     {"sealed":"<base64>"} 落 jsonb；空凭证（nil/{}）恒落 '{}'，无需密封。
//     Seal 未接线时写非空凭证直接报错（fail-closed，与 CreateCredential 同策略）。
//   - 读取：'{}'/空 → 无凭证；信封 → 解密回明文 map（消费方仅 engine 的
//     ResolveForEngine，见 app 层）；其余形状（迁移 1038 之前的存量明文、
//     损坏数据）一律报错拒绝——不做明文回落。存量明文已由迁移 1038 一次性
//     清空（v0 无兼容，重装即重填），读到这里非信封内容即视为异常数据。
//
// # 三态凭证语义（FP-09 P1-4 裁决：两级统一）
//
//   - Update（ws/agent 两级一致）：Credentials == nil → SQL NULL → coalesce
//     保留库中现值；非 nil（含空 map）→ 覆盖写入，空 map = 显式清空。
//     "缺字段 vs 显式空"的指针区分在 HTTP wire 层完成（handlers_mcp.go）。
//   - Create（两级一致）：INSERT + 23505 → 409。agent 级不再 ON CONFLICT
//     静默覆盖（FP-07 P1-4）；PUT 改名撞名同样 23505 → 409（FP-07 P1-5）。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

const wsMCPCols = `id::text, workspace_id::text, name, transport, url, credentials, created_at`

func scanWSMCP(seal CredentialSealer, row pgx.Row) (orchapp.WorkspaceMCP, error) {
	var m orchapp.WorkspaceMCP
	var raw []byte
	if err := row.Scan(&m.ID, &m.WorkspaceID, &m.Name, &m.Transport, &m.URL, &raw, &m.CreatedAt); err != nil {
		return m, err
	}
	creds, err := unsealCreds(seal, raw)
	if err != nil {
		return m, err
	}
	m.Credentials = creds
	m.HasCreds = len(creds) > 0
	return m, nil
}

func (r *Repo) ListWorkspaceMCP(ctx context.Context, wsID string) ([]orchapp.WorkspaceMCP, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+wsMCPCols+` FROM workspace_mcp_server WHERE workspace_id=$1 ORDER BY name`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.WorkspaceMCP
	for rows.Next() {
		m, err := scanWSMCP(r.Seal, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repo) CreateWorkspaceMCP(ctx context.Context, m orchapp.WorkspaceMCP) (orchapp.WorkspaceMCP, error) {
	sealed, err := sealCreds(r.Seal, m.Credentials)
	if err != nil {
		return orchapp.WorkspaceMCP{}, err
	}
	out, err := scanWSMCP(r.Seal, r.q(ctx).QueryRow(ctx, `
INSERT INTO workspace_mcp_server (workspace_id, name, transport, url, credentials)
VALUES ($1,$2,$3,$4,$5) RETURNING `+wsMCPCols,
		m.WorkspaceID, m.Name, orDefault(m.Transport, "http"), m.URL, sealed))
	if isDupName(err) {
		return out, errMCPTaken
	}
	return out, err
}

func (r *Repo) UpdateWorkspaceMCP(ctx context.Context, m orchapp.WorkspaceMCP) (orchapp.WorkspaceMCP, bool, error) {
	sealed, err := r.sealedCredsOrNil(m.Credentials)
	if err != nil {
		return orchapp.WorkspaceMCP{}, false, err
	}
	out, err := scanWSMCP(r.Seal, r.q(ctx).QueryRow(ctx, `
UPDATE workspace_mcp_server SET name=$3, transport=$4, url=$5,
  credentials=coalesce($6::jsonb, workspace_mcp_server.credentials)
WHERE workspace_id=$1 AND id=$2 RETURNING `+wsMCPCols,
		m.WorkspaceID, m.ID, m.Name, orDefault(m.Transport, "http"), m.URL, sealed))
	if noRow(err) {
		return out, false, nil
	}
	if isDupName(err) {
		return out, false, errMCPTaken
	}
	return out, err == nil, err
}

func (r *Repo) DeleteWorkspaceMCP(ctx context.Context, wsID, id string) error {
	_, err := r.q(ctx).Exec(ctx,
		`DELETE FROM workspace_mcp_server WHERE workspace_id=$1 AND id=$2`, wsID, id)
	return err
}

func (r *Repo) ListAgentMCP(ctx context.Context, wsID, agentID string) ([]orchapp.AgentMCP, bool, error) {
	var n int
	if err := r.q(ctx).QueryRow(ctx,
		`SELECT count(*) FROM agent WHERE workspace_id=$1 AND id=$2`, wsID, agentID).Scan(&n); err != nil || n == 0 {
		return nil, false, err
	}
	rows, err := r.q(ctx).Query(ctx, `
SELECT id::text, agent_id::text, name, transport, url, enabled, credentials
FROM agent_mcp_server WHERE agent_id=$1 ORDER BY name`, agentID)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	var out []orchapp.AgentMCP
	for rows.Next() {
		var m orchapp.AgentMCP
		var raw []byte
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Name, &m.Transport, &m.URL, &m.Enabled, &raw); err != nil {
			return nil, true, err
		}
		creds, err := unsealCreds(r.Seal, raw)
		if err != nil {
			return nil, true, err
		}
		m.Credentials = creds
		m.HasCreds = len(creds) > 0
		out = append(out, m)
	}
	return out, true, rows.Err()
}

const agentMCPCols = `id::text, agent_id::text, name, transport, url, enabled, credentials`

func scanAgentMCP(seal CredentialSealer, row pgx.Row) (orchapp.AgentMCP, error) {
	var m orchapp.AgentMCP
	var raw []byte
	if err := row.Scan(&m.ID, &m.AgentID, &m.Name, &m.Transport, &m.URL, &m.Enabled, &raw); err != nil {
		return m, err
	}
	creds, err := unsealCreds(seal, raw)
	if err != nil {
		return m, err
	}
	m.Credentials = creds
	m.HasCreds = len(creds) > 0
	return m, nil
}

// CreateAgentMCP：agent 级创建为 INSERT 语义——重名 23505 → 409，不再
// ON CONFLICT 静默覆盖既有条目（FP-07 P1-4，对齐 ws 级 CreateWorkspaceMCP）。
func (r *Repo) CreateAgentMCP(ctx context.Context, m orchapp.AgentMCP) (orchapp.AgentMCP, error) {
	sealed, err := sealCreds(r.Seal, m.Credentials)
	if err != nil {
		return orchapp.AgentMCP{}, err
	}
	out, err := scanAgentMCP(r.Seal, r.q(ctx).QueryRow(ctx, `
INSERT INTO agent_mcp_server (agent_id, name, transport, url, credentials, enabled)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+agentMCPCols,
		m.AgentID, m.Name, orDefault(m.Transport, "http"), m.URL, sealed, m.Enabled))
	if isDupName(err) {
		return out, errMCPTaken
	}
	return out, err
}

func (r *Repo) UpdateAgentMCP(ctx context.Context, m orchapp.AgentMCP) (orchapp.AgentMCP, bool, error) {
	sealed, err := r.sealedCredsOrNil(m.Credentials)
	if err != nil {
		return orchapp.AgentMCP{}, false, err
	}
	out, err := scanAgentMCP(r.Seal, r.q(ctx).QueryRow(ctx, `
UPDATE agent_mcp_server s SET name=$3, transport=$4, url=$5,
  credentials=coalesce($6::jsonb, s.credentials), enabled=$7
WHERE s.id=$2 AND s.agent_id=$1
  AND EXISTS (SELECT 1 FROM agent a WHERE a.id=s.agent_id AND a.workspace_id=$8)
RETURNING `+agentMCPCols,
		m.AgentID, m.ID, m.Name, orDefault(m.Transport, "http"), m.URL, sealed,
		m.Enabled, m.WorkspaceID))
	if noRow(err) {
		return out, false, nil
	}
	if isDupName(err) {
		return out, false, errMCPTaken
	}
	if err != nil {
		return out, false, err
	}
	return out, true, nil
}

func (r *Repo) DeleteAgentMCP(ctx context.Context, wsID, agentID, id string) error {
	_, err := r.q(ctx).Exec(ctx, `
DELETE FROM agent_mcp_server s
WHERE s.id=$3 AND s.agent_id=$2
  AND EXISTS (SELECT 1 FROM agent a WHERE a.id=s.agent_id AND a.workspace_id=$1)`, wsID, agentID, id)
	return err
}

// errMCPTaken / isDupName：MCP 服务器重名（23505）→ 409 的统一出口。
// 旧实现只在 ws 级 Create 映射，PUT 改名与 agent 级都漏（FP-07 P1-5 实测双 500）。
var errMCPTaken = apierr.New(apierr.Conflict, "mcp server name already taken")

func isDupName(err error) bool {
	var pgErr *pgconn.PgError
	return err != nil && errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// sealCreds 把明文凭证密封成 jsonb 信封。空凭证（nil/{}）恒落 '{}'；
// 非空凭证必须有 Seal，否则 fail-closed 报错（对照 CreateCredential）。
func sealCreds(seal CredentialSealer, c map[string]any) ([]byte, error) {
	if len(c) == 0 {
		return []byte(`{}`), nil
	}
	if seal == nil {
		return nil, errors.New("mcp credential sealer not wired (ENGINE_CREDENTIAL_MASTER_KEY; fail-closed per model-credential precedent)")
	}
	plain, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	blob, err := seal.Encrypt(string(plain))
	if err != nil {
		return nil, err
	}
	return []byte(`{"sealed":"` + base64.StdEncoding.EncodeToString(blob) + `"}`), nil
}

// unsealCreds 读路径：'{}'/空 → nil（无凭证）；信封 → 解密回明文 map；
// 其余形状（存量明文、损坏数据）fail-closed 报错，绝不回落明文。
func unsealCreds(seal CredentialSealer, raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("mcp credentials: not a sealed envelope (fail-closed): %w", err)
	}
	if len(envelope) == 0 {
		return nil, nil
	}
	var b64 string
	if err := json.Unmarshal(envelope["sealed"], &b64); err != nil || b64 == "" {
		return nil, errors.New("mcp credentials: legacy plaintext rejected (fail-closed; migration 1038 clears legacy plaintext — re-enter the credential)")
	}
	if seal == nil {
		return nil, errors.New("mcp credential sealer not wired (ENGINE_CREDENTIAL_MASTER_KEY; fail-closed)")
	}
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("mcp credentials: sealed envelope malformed: %w", err)
	}
	plain, err := seal.Decrypt(blob)
	if err != nil {
		return nil, fmt.Errorf("mcp credentials: unseal failed (master key mismatch or corrupted ciphertext): %w", err)
	}
	var creds map[string]any
	if err := json.Unmarshal([]byte(plain), &creds); err != nil {
		return nil, fmt.Errorf("mcp credentials: sealed payload malformed: %w", err)
	}
	return creds, nil
}

// sealedCredsOrNil：三态的 repo 侧入口——nil map → nil（SQL NULL，coalesce
// 保留库中现值）；非 nil（含空 map）→ 密封字节（空 map 密封为 '{}' = 清空）。
func (r *Repo) sealedCredsOrNil(c map[string]any) ([]byte, error) {
	if c == nil {
		return nil, nil
	}
	return sealCreds(r.Seal, c)
}
