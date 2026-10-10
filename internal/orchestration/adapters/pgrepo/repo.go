package pgrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"orkome/internal/contract/apierr"

	"orkome/internal/contract/failuretax"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/orchestration/domain"
	"github.com/haozing/ploykit/platform/pg"
)

type CredentialSealer interface {
	Encrypt(plaintext string) ([]byte, error)
	Decrypt(blob []byte) (string, error)
}

type Repo struct {
	DB *pg.DB

	Seal CredentialSealer
}

func New(db *pg.DB) *Repo { return &Repo{DB: db} }

var _ orchapp.Repo = (*Repo)(nil)

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }
func noRow(err error) bool                    { return errors.Is(err, pgx.ErrNoRows) }

func (r *Repo) CreateAgent(ctx context.Context, a orchapp.Agent) (orchapp.Agent, error) {
	if a.ConversationStarters == nil {
		a.ConversationStarters = []orchapp.ConversationStarter{}
	}
	row := r.q(ctx).QueryRow(ctx, `
INSERT INTO agent (workspace_id, name, description, runtime_config, max_concurrent_tasks, visibility, owner_user_id, conversation_starters, runtime_kind)
VALUES ($1,$2,$3,$4,$5,$6,nullif($7,'')::uuid,$8,coalesce(nullif($9,''),'engine'))
RETURNING `+agentCols,
		a.WorkspaceID, a.Name, a.Description, a.RuntimeConfig, a.MaxConcurrent, a.Visibility,
		a.OwnerUserID, a.ConversationStarters, a.RuntimeKind)
	return scanAgent(row)
}

const agentCols = `id, workspace_id, name, description, runtime_config, max_concurrent_tasks, visibility,
coalesce(owner_user_id::text,''), archived_at IS NOT NULL, created_at, conversation_starters, runtime_kind`

func scanAgent(row pgx.Row) (orchapp.Agent, error) {
	var a orchapp.Agent
	err := row.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.Description, &a.RuntimeConfig, &a.MaxConcurrent,
		&a.Visibility, &a.OwnerUserID, &a.Archived, &a.CreatedAt, &a.ConversationStarters, &a.RuntimeKind)
	return a, err
}

func (r *Repo) GetAgent(ctx context.Context, wsID, id string) (orchapp.Agent, bool, error) {
	a, err := scanAgent(r.q(ctx).QueryRow(ctx,
		`SELECT `+agentCols+` FROM agent WHERE workspace_id=$1 AND id=$2`, wsID, id))
	if noRow(err) {
		return a, false, nil
	}
	return a, err == nil, err
}

func (r *Repo) GetAgentByName(ctx context.Context, wsID, name string) (orchapp.Agent, bool, error) {
	a, err := scanAgent(r.q(ctx).QueryRow(ctx,
		`SELECT `+agentCols+` FROM agent WHERE workspace_id=$1 AND name=$2`, wsID, name))
	if noRow(err) {
		return a, false, nil
	}
	return a, err == nil, err
}

func (r *Repo) ListAgents(ctx context.Context, wsID string) ([]orchapp.Agent, error) {
	rows, err := r.q(ctx).Query(ctx, `SELECT `+agentCols+` FROM agent WHERE workspace_id=$1 ORDER BY created_at`, wsID)
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

func (r *Repo) ListAllAgents(ctx context.Context) ([]orchapp.Agent, error) {
	rows, err := r.q(ctx).Query(ctx, `SELECT `+agentCols+` FROM agent ORDER BY created_at`)
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

func (r *Repo) ArchiveAgent(ctx context.Context, wsID, id string, archived bool) error {
	if archived {
		_, err := r.q(ctx).Exec(ctx, `UPDATE agent SET archived_at=now() WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL`, wsID, id)
		return err
	}
	_, err := r.q(ctx).Exec(ctx, `UPDATE agent SET archived_at=NULL WHERE workspace_id=$1 AND id=$2`, wsID, id)
	return err
}

func (r *Repo) UpdateAgent(ctx context.Context, a orchapp.Agent) (orchapp.Agent, bool, error) {
	if a.ConversationStarters == nil {
		a.ConversationStarters = []orchapp.ConversationStarter{}
	}
	row := r.q(ctx).QueryRow(ctx, `
UPDATE agent SET name=$3, description=$4, runtime_config=$5, max_concurrent_tasks=$6,
  visibility=$7, conversation_starters=$8, updated_at=now()
WHERE workspace_id=$1 AND id=$2
RETURNING `+agentCols, a.WorkspaceID, a.ID, a.Name, a.Description, a.RuntimeConfig,
		a.MaxConcurrent, a.Visibility, a.ConversationStarters)
	out, err := scanAgent(row)
	if noRow(err) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) ListInvocationTargets(ctx context.Context, wsID string) (map[string][]orchapp.InvocationTarget, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT t.agent_id::text, t.target_type, coalesce(t.target_user_id::text,'')
FROM agent_invocation_target t JOIN agent a ON a.id = t.agent_id
WHERE a.workspace_id = $1
ORDER BY t.agent_id, t.target_type, t.target_user_id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]orchapp.InvocationTarget{}
	for rows.Next() {
		var agentID, tType, tUser string
		if err := rows.Scan(&agentID, &tType, &tUser); err != nil {
			return nil, err
		}
		out[agentID] = append(out[agentID], orchapp.InvocationTarget{Type: tType, UserID: tUser})
	}
	return out, rows.Err()
}

func (r *Repo) GetInvocationTargets(ctx context.Context, wsID, agentID string) ([]orchapp.InvocationTarget, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT t.target_type, coalesce(t.target_user_id::text,'')
FROM agent_invocation_target t JOIN agent a ON a.id = t.agent_id
WHERE a.workspace_id = $1 AND t.agent_id = $2::uuid
ORDER BY t.target_type, t.target_user_id`, wsID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.InvocationTarget
	for rows.Next() {
		var t orchapp.InvocationTarget
		if err := rows.Scan(&t.Type, &t.UserID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) ReplaceInvocationTargets(ctx context.Context, wsID, agentID string, targets []orchapp.InvocationTarget) error {
	if targets == nil {
		targets = []orchapp.InvocationTarget{}
	}
	payload, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	_, err = r.q(ctx).Exec(ctx, `
WITH cleared AS (
  DELETE FROM agent_invocation_target t USING agent a
  WHERE t.agent_id = a.id AND a.workspace_id = $1 AND a.id = $2::uuid
), inserted AS (
  INSERT INTO agent_invocation_target (agent_id, target_type, target_user_id)
  SELECT $2::uuid, e->>'type', nullif(e->>'user_id','')::uuid
  FROM jsonb_array_elements($3::jsonb) AS e
  WHERE EXISTS (SELECT 1 FROM agent ga WHERE ga.id = $2::uuid AND ga.workspace_id = $1)
)
SELECT 1`, wsID, agentID, payload)
	return err
}

func (r *Repo) CancelAgentTasks(ctx context.Context, wsID, agentID string) (int, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE agent_task_queue SET status='cancelled', completed_at=now()
WHERE workspace_id=$1 AND agent_id=$2 AND status IN ('queued','dispatched')`, wsID, agentID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

var _ orchapp.CredentialRepo = (*Repo)(nil)

func (r *Repo) CreateCredential(ctx context.Context, wsID, provider, secret, createdBy string) (orchapp.Credential, error) {
	var c orchapp.Credential
	if r.Seal == nil {
		return c, errors.New("credential sealer not wired (ENGINE_CREDENTIAL_MASTER_KEY; internal archive 09 §6.2 fail-closed)")
	}
	blob, err := r.Seal.Encrypt(secret)
	if err != nil {
		return c, err
	}
	err = r.q(ctx).QueryRow(ctx, `
INSERT INTO agent_model_credential (workspace_id, provider, encrypted_secret, created_by)
VALUES ($1,$2,$3,nullif($4,'')::uuid) RETURNING id, provider, created_at`,
		wsID, provider, blob, createdBy).Scan(&c.ID, &c.Provider, &c.CreatedAt)
	return c, err
}

func (r *Repo) ListCredentials(ctx context.Context, wsID string) ([]orchapp.Credential, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT id, provider, created_at FROM agent_model_credential WHERE workspace_id=$1
ORDER BY created_at DESC, id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.Credential
	for rows.Next() {
		var c orchapp.Credential
		if err := rows.Scan(&c.ID, &c.Provider, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) DeleteCredential(ctx context.Context, wsID, id string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx,
		`DELETE FROM agent_model_credential WHERE workspace_id=$1 AND id=$2`, wsID, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) GetCredential(ctx context.Context, wsID, id string) (string, string, bool, error) {
	if r.Seal == nil {
		return "", "", false, errors.New("credential sealer not wired (ENGINE_CREDENTIAL_MASTER_KEY; internal archive 09 §6.2 fail-closed)")
	}
	var provider string
	var blob []byte
	err := r.q(ctx).QueryRow(ctx,
		`SELECT provider, encrypted_secret FROM agent_model_credential WHERE workspace_id=$1 AND id=$2`,
		wsID, id).Scan(&provider, &blob)
	if noRow(err) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	secret, err := r.Seal.Decrypt(blob)
	if err != nil {
		return "", "", false, err
	}
	return provider, secret, true, nil
}

func asFenceErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: %s", domain.ErrFenceHit, pgErr.ConstraintName)
	}
	return err
}

const taskCols = `id, workspace_id, agent_id, coalesce(work_item_id::text,''), status, priority, attempt, max_attempts,
context, coalesce(originator_user_id::text,''), coalesce(accountable_user_id::text,''), originator_source,
coalesce(trigger_evidence_kind,''), coalesce(session_ref_in,''), is_leader_task, coalesce(squad_id::text,''), coalesce(failure_reason,''), coalesce(error,''), coalesce(result,''), created_at, completed_at, coalesce(summary,'')`

func scanTask(row pgx.Row) (orchapp.Task, error) {
	var t orchapp.Task
	err := row.Scan(&t.ID, &t.WorkspaceID, &t.AgentID, &t.WorkItemID, &t.Status, &t.Priority,
		&t.Attempt, &t.MaxAttempts, &t.Context, &t.OriginatorUserID, &t.AccountableUserID,
		&t.OriginatorSource, &t.TriggerEvidenceKind, &t.SessionRefIn, &t.IsLeaderTask, &t.SquadID, &t.FailureReason, &t.Error, &t.Result, &t.CreatedAt, &t.CompletedAt, &t.Summary)
	return t, err
}

func (r *Repo) InsertTask(ctx context.Context, t orchapp.Task) (orchapp.Task, error) {

	if t.OriginatorSource == "" {
		t.OriginatorSource = "direct_human"
	}
	sqlq := fmt.Sprintf(`INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status, priority, max_attempts, context,
  originator_user_id, accountable_user_id, originator_source, trigger_evidence_kind, session_ref_in,
  is_leader_task, squad_id)
VALUES ($1,$2,nullif($3,'')::uuid,'queued',$4,$5,$6,nullif($7,'')::uuid,nullif($7,'')::uuid,$8,$9,nullif($10,''),
  $11,nullif($12,'')::uuid)
RETURNING %s`, taskCols)
	out, err := scanTask(r.q(ctx).QueryRow(ctx, sqlq,
		t.WorkspaceID, t.AgentID, t.WorkItemID, t.Priority, t.MaxAttempts, t.Context,
		t.OriginatorUserID, t.OriginatorSource, t.TriggerEvidenceKind, t.SessionRefIn, t.IsLeaderTask, t.SquadID))
	return out, asFenceErr(err)
}

func (r *Repo) GetTask(ctx context.Context, wsID, id string) (orchapp.Task, bool, error) {
	t, err := scanTask(r.q(ctx).QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM agent_task_queue WHERE workspace_id=$1 AND id=$2`, taskCols), wsID, id))
	if noRow(err) {
		return t, false, nil
	}
	return t, err == nil, err
}

func (r *Repo) ClaimNext(ctx context.Context, agentID string, maxConcurrent int) (orchapp.Task, bool, error) {
	t, err := scanTask(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
UPDATE agent_task_queue t SET status='dispatched', dispatched_at=now(),
    prepare_lease_expires_at = now() + interval '45 seconds'
WHERE t.id = (
  SELECT c.id FROM agent_task_queue c
  WHERE c.agent_id = $1 AND c.status = 'queued'
    AND (c.fire_at IS NULL OR c.fire_at <= now())
    AND NOT EXISTS (
      SELECT 1 FROM agent_task_queue x
      WHERE x.work_item_id = c.work_item_id AND x.id <> c.id
        AND x.status IN ('dispatched','running'))
    AND (SELECT count(*) FROM agent_task_queue r
         WHERE r.agent_id = $1 AND r.status IN ('dispatched','running')) < $2
  ORDER BY c.priority DESC, c.created_at
  FOR UPDATE SKIP LOCKED LIMIT 1
) RETURNING %s`, tTaskCols), agentID, maxConcurrent))
	if noRow(err) {
		return t, false, nil
	}
	return t, err == nil, err
}

const tTaskCols = `t.id, t.workspace_id, t.agent_id, coalesce(t.work_item_id::text,''), t.status, t.priority,
t.attempt, t.max_attempts, t.context, coalesce(t.originator_user_id::text,''), coalesce(t.accountable_user_id::text,''),
t.originator_source, coalesce(t.trigger_evidence_kind,''), coalesce(t.session_ref_in,''), t.is_leader_task, coalesce(t.squad_id::text,''), coalesce(t.failure_reason,''), coalesce(t.error,''),
coalesce(t.result,''), t.created_at, t.completed_at, coalesce(t.summary,'')`

func (r *Repo) lastText(ctx context.Context, taskID string) string {
	var s string
	if err := r.q(ctx).QueryRow(ctx,
		`SELECT content FROM task_message WHERE task_id=$1 AND kind='text' ORDER BY seq DESC LIMIT 1`,
		taskID).Scan(&s); err != nil {
		return ""
	}
	return s
}

func (r *Repo) StartTask(ctx context.Context, id, runtimeID, engineTag string) (orchapp.Task, error) {
	t, err := scanTask(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
UPDATE agent_task_queue SET status='running', started_at=now(), prepare_lease_expires_at=NULL,
  runtime_id=nullif($2,''), engine_tag=nullif($3,'')
WHERE id=$1 AND status IN ('dispatched') RETURNING %s`, taskCols), id, runtimeID, engineTag))
	return t, err
}

func (r *Repo) CompleteRun(ctx context.Context, id, result, sessionRef, engineTag string, u orchapp.UsageReport) error {
	return r.DB.Within(ctx, func(ctx context.Context) error {
		summary := domain.TaskSummary(r.lastText(ctx, id), result)
		tag, err := r.q(ctx).Exec(ctx, `
UPDATE agent_task_queue SET status='completed', completed_at=now(), result=$2,
  session_ref_out=nullif($3,''), engine_tag=nullif($4,''), summary=nullif($5,'')
WHERE id=$1 AND status='running'`, id, result, sessionRef, engineTag, summary)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("already finalized")
		}
		_, _ = r.q(ctx).Exec(ctx, `DELETE FROM agent_task_checkpoint WHERE id='task:'||$1`, id)
		_, err = r.q(ctx).Exec(ctx, `
INSERT INTO task_usage (workspace_id, task_id, agent_id, provider, model,
  prompt_tokens, completion_tokens, cost_usd_ticks, agent_label, model_retries, model_failovers)
SELECT workspace_id, id, agent_id, $2, $3, $4, $5, $6, $7, $8, $9
FROM agent_task_queue WHERE id=$1`,
			id, u.Provider, u.Model, u.PromptTokens, u.CompletionTokens, u.CostUSDTicks,
			orDefault(u.AgentLabel, "primary"), u.ModelRetries, u.ModelFailovers)
		return err
	})
}

func (r *Repo) FailRun(ctx context.Context, id, reason, errMsg string) (bool, error) {
	attempted := false
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		summary := domain.TaskSummary(r.lastText(ctx, id), reason+": "+errMsg)
		var t orchapp.Task
		err := r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
UPDATE agent_task_queue SET status='failed', completed_at=now(), failure_reason=$2, error=$3, summary=$4
WHERE id=$1 AND status='running'
RETURNING %s`, taskCols), id, reason, errMsg, summary).Scan(
			&t.ID, &t.WorkspaceID, &t.AgentID, &t.WorkItemID, &t.Status, &t.Priority,
			&t.Attempt, &t.MaxAttempts, &t.Context, &t.OriginatorUserID, &t.AccountableUserID,
			&t.OriginatorSource, &t.TriggerEvidenceKind, &t.SessionRefIn, &t.IsLeaderTask, &t.SquadID, &t.FailureReason, &t.Error, &t.Result, &t.CreatedAt, &t.CompletedAt, &t.Summary)
		if noRow(err) {
			return nil
		}
		if err != nil {
			return err
		}
		_, _ = r.q(ctx).Exec(ctx, `DELETE FROM agent_task_checkpoint WHERE id='task:'||$1`, id)

		if domain.RetryEligible(reason, t.Attempt, t.MaxAttempts) {
			delaySecs := int(domain.RetryDelay(reason, t.Attempt, t.MaxAttempts).Seconds())
			if _, err := r.q(ctx).Exec(ctx, `
INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status, priority,
  attempt, max_attempts, context, originator_user_id, accountable_user_id, originator_source, retry_of_task_id, fire_at)
SELECT workspace_id, agent_id, work_item_id, 'queued', priority,
  $3, max_attempts, context || jsonb_build_object('prior_summary', nullif($5,''), 'prior_failure_reason', $6::text), originator_user_id, accountable_user_id, originator_source, $2,
  CASE WHEN $4::int > 0 THEN now() + ($4::int * interval '1 second') ELSE NULL END
FROM agent_task_queue WHERE id=$1
ON CONFLICT DO NOTHING`, id, id, t.Attempt+1, delaySecs, summary, reason); err != nil {
				return err
			}
			attempted = true
		}
		return nil
	})
	return attempted, err
}

func (r *Repo) CancelRun(ctx context.Context, wsID, id string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE agent_task_queue SET status='cancelled', completed_at=now()
WHERE workspace_id=$1 AND id=$2 AND status IN ('queued','dispatched')`, wsID, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) ListTasksByWorkItem(ctx context.Context, workItemID string, newestFirst bool) ([]orchapp.Task, error) {
	order := "created_at"
	if newestFirst {
		order = "created_at DESC"
	}
	return r.listTasks(ctx, fmt.Sprintf(`SELECT %s FROM agent_task_queue WHERE work_item_id=$1 ORDER BY %s`, taskCols, order), workItemID)
}

func (r *Repo) ListActiveTasks(ctx context.Context, workItemID string) ([]orchapp.Task, error) {
	return r.listTasks(ctx, fmt.Sprintf(`SELECT %s FROM agent_task_queue WHERE work_item_id=$1 AND status IN ('queued','dispatched','running') ORDER BY created_at`, taskCols), workItemID)
}

func (r *Repo) listTasks(ctx context.Context, sql string, args ...any) ([]orchapp.Task, error) {
	rows, err := r.q(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.Task
	for rows.Next() {
		var t orchapp.Task
		if err := rows.Scan(&t.ID, &t.WorkspaceID, &t.AgentID, &t.WorkItemID, &t.Status, &t.Priority,
			&t.Attempt, &t.MaxAttempts, &t.Context, &t.OriginatorUserID, &t.AccountableUserID,
			&t.OriginatorSource, &t.TriggerEvidenceKind, &t.SessionRefIn, &t.IsLeaderTask, &t.SquadID, &t.FailureReason, &t.Error, &t.Result, &t.CreatedAt, &t.CompletedAt, &t.Summary); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) RecoverDispatched(ctx context.Context) (int, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE agent_task_queue SET status='queued', prepare_lease_expires_at=NULL
WHERE status='dispatched'`)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *Repo) FailRunningAtStartup(ctx context.Context) (int, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE agent_task_queue SET status='failed', completed_at=now(),
  failure_reason=$1, error='process restart: run state not recoverable'
WHERE status='running'
  AND NOT EXISTS ( -- external runtime exemption (contract Phase 2a): client-side execution, survives server restarts
    SELECT 1 FROM agent xa WHERE xa.id = agent_task_queue.agent_id AND xa.runtime_kind = 'external')`, failuretax.RuntimeRecovery)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

const wsMCPCols = `id::text, workspace_id::text, name, transport, url, credentials, created_at`

func scanWSMCP(row pgx.Row) (orchapp.WorkspaceMCP, error) {
	var m orchapp.WorkspaceMCP
	err := row.Scan(&m.ID, &m.WorkspaceID, &m.Name, &m.Transport, &m.URL, &m.Credentials, &m.CreatedAt)
	m.HasCreds = len(m.Credentials) > 0
	return m, err
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
		m, err := scanWSMCP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repo) CreateWorkspaceMCP(ctx context.Context, m orchapp.WorkspaceMCP) (orchapp.WorkspaceMCP, error) {
	out, err := scanWSMCP(r.q(ctx).QueryRow(ctx, `
INSERT INTO workspace_mcp_server (workspace_id, name, transport, url, credentials)
VALUES ($1,$2,$3,$4,$5) RETURNING `+wsMCPCols,
		m.WorkspaceID, m.Name, orDefault(m.Transport, "http"), m.URL, credJSON(m.Credentials)))

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return out, apierr.New(apierr.Conflict, "mcp server name already taken")
	}
	return out, err
}

func (r *Repo) UpdateWorkspaceMCP(ctx context.Context, m orchapp.WorkspaceMCP) (orchapp.WorkspaceMCP, bool, error) {
	out, err := scanWSMCP(r.q(ctx).QueryRow(ctx, `
UPDATE workspace_mcp_server SET name=$3, transport=$4, url=$5, credentials=$6
WHERE workspace_id=$1 AND id=$2 RETURNING `+wsMCPCols,
		m.WorkspaceID, m.ID, m.Name, orDefault(m.Transport, "http"), m.URL, credJSON(m.Credentials)))
	if noRow(err) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) DeleteWorkspaceMCP(ctx context.Context, wsID, id string) error {
	_, err := r.q(ctx).Exec(ctx,
		`DELETE FROM workspace_mcp_server WHERE workspace_id=$1 AND id=$2`, wsID, id)
	return err
}

func credJSON(c map[string]any) []byte {
	if len(c) == 0 {
		return []byte(`{}`)
	}
	b, _ := json.Marshal(c)
	return b
}

func credJSONOrNull(c map[string]any) any {
	if c == nil {
		return nil
	}
	return credJSON(c)
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
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Name, &m.Transport, &m.URL, &m.Enabled, &m.Credentials); err != nil {
			return nil, true, err
		}
		m.HasCreds = len(m.Credentials) > 0
		out = append(out, m)
	}
	return out, true, rows.Err()
}

const agentMCPCols = `id::text, agent_id::text, name, transport, url, enabled, credentials`

func scanAgentMCP(row pgx.Row) (orchapp.AgentMCP, error) {
	var m orchapp.AgentMCP
	if err := row.Scan(&m.ID, &m.AgentID, &m.Name, &m.Transport, &m.URL, &m.Enabled, &m.Credentials); err != nil {
		return m, err
	}
	m.HasCreds = len(m.Credentials) > 0
	return m, nil
}

func (r *Repo) UpsertAgentMCP(ctx context.Context, m orchapp.AgentMCP) (orchapp.AgentMCP, error) {
	return scanAgentMCP(r.q(ctx).QueryRow(ctx, `
INSERT INTO agent_mcp_server (agent_id, name, transport, url, credentials, enabled)
VALUES ($1,$2,$3,$4,coalesce($5::jsonb,'{}'::jsonb),$6)
ON CONFLICT (agent_id, name) DO UPDATE SET
  transport=EXCLUDED.transport, url=EXCLUDED.url, enabled=EXCLUDED.enabled,
  credentials=coalesce($5::jsonb, agent_mcp_server.credentials)
RETURNING `+agentMCPCols,
		m.AgentID, m.Name, orDefault(m.Transport, "http"), m.URL, credJSONOrNull(m.Credentials), m.Enabled))
}

func (r *Repo) UpdateAgentMCP(ctx context.Context, m orchapp.AgentMCP) (orchapp.AgentMCP, bool, error) {
	out, err := scanAgentMCP(r.q(ctx).QueryRow(ctx, `
UPDATE agent_mcp_server s SET name=$3, transport=$4, url=$5,
  credentials=coalesce($6::jsonb, s.credentials), enabled=$7
WHERE s.id=$2 AND s.agent_id=$1
  AND EXISTS (SELECT 1 FROM agent a WHERE a.id=s.agent_id AND a.workspace_id=$8)
RETURNING `+agentMCPCols,
		m.AgentID, m.ID, m.Name, orDefault(m.Transport, "http"), m.URL, credJSONOrNull(m.Credentials),
		m.Enabled, m.WorkspaceID))
	if noRow(err) {
		return out, false, nil
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

func (r *Repo) UpsertRuntime(ctx context.Context, instanceID, engineName string, caps []string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO agent_runtime (instance_id, engine, capabilities) VALUES ($1,$2,$3)
ON CONFLICT (instance_id) DO UPDATE SET engine=EXCLUDED.engine, capabilities=EXCLUDED.capabilities,
  started_at=now(), last_seen_at=now()`, instanceID, engineName, caps)
	return err
}

func (r *Repo) HeartbeatRuntime(ctx context.Context, instanceID string) error {
	_, err := r.q(ctx).Exec(ctx, `UPDATE agent_runtime SET last_seen_at=now() WHERE instance_id=$1`, instanceID)
	return err
}

func (r *Repo) SetRuntime(ctx context.Context, taskID, instanceID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE agent_task_queue SET runtime_id=$2 WHERE id=$1 AND status='running'`, taskID, instanceID)
	return err
}

func (r *Repo) ClaimExternalTask(ctx context.Context, wsID, taskID string) (orchapp.Task, bool, error) {
	t, err := scanTask(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
UPDATE agent_task_queue SET status='running', started_at=now(), prepare_lease_expires_at=NULL
WHERE id=$1 AND workspace_id=$2 AND status='queued'
RETURNING %s`, taskCols), taskID, wsID))
	if noRow(err) {
		return t, false, nil
	}
	if err != nil {
		return t, false, err
	}
	return t, true, nil
}

func (r *Repo) GetAgentIDByPATID(ctx context.Context, patID string) (string, bool, error) {
	var agentID string
	err := r.q(ctx).QueryRow(ctx,
		`SELECT agent_id::text FROM agent_credential WHERE pat_id=$1::uuid AND revoked_at IS NULL LIMIT 1`,
		patID).Scan(&agentID)
	if noRow(err) {
		return "", false, nil
	}
	return agentID, err == nil, err
}

func (r *Repo) CountStaleExternalQueued(ctx context.Context, olderThanMinutes int) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
SELECT count(*) FROM agent_task_queue t
JOIN agent a ON a.id = t.agent_id
WHERE t.status='queued' AND a.runtime_kind='external'
  AND t.created_at < now() - ($1::int * interval '1 minute')`, olderThanMinutes).Scan(&n)
	return n, err
}

func (r *Repo) FailStaleRunning(ctx context.Context, staleSeconds int) ([]string, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT t.id::text FROM agent_task_queue t
WHERE t.status='running' AND t.awaiting_input_since IS NULL
  AND (t.runtime_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM agent_runtime ar
    WHERE ar.instance_id = t.runtime_id AND ar.last_seen_at > now() - ($1::int * interval '1 second')))
  AND NOT EXISTS ( -- external runtime exemption (contract Phase 2a: no instance heartbeat)
    SELECT 1 FROM agent xa WHERE xa.id = t.agent_id AND xa.runtime_kind = 'external')`,
		staleSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const taskMessageCols = `id::text, task_id::text, seq, kind, coalesce(call_id,''), content, truncated, created_at`

func (r *Repo) AppendTaskMessage(ctx context.Context, wsID, taskID, kind, callID, content string, truncated bool) (int64, error) {
	var seq int64
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO task_message (workspace_id, task_id, seq, kind, call_id, content, truncated)
SELECT $1, $2, coalesce(max(seq),0)+1, $3, nullif($4,''), $5, $6
FROM task_message WHERE task_id=$2 RETURNING seq`,
		wsID, taskID, kind, callID, content, truncated).Scan(&seq)
	return seq, err
}

func (r *Repo) ListTaskMessages(ctx context.Context, wsID, taskID string, q orchapp.TaskMessagesQuery) ([]orchapp.TaskMessage, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 200
	}
	cond := ""
	args := []any{wsID, taskID}
	if q.BeforeSeq > 0 {
		args = append(args, q.BeforeSeq)
		cond += fmt.Sprintf(" AND seq < $%d", len(args))
	}
	if len(q.Kinds) > 0 {
		args = append(args, q.Kinds)
		cond += fmt.Sprintf(" AND kind = ANY($%d)", len(args))
	}
	order := "ASC"
	if q.Desc {
		order = "DESC"
	}
	rows, err := r.q(ctx).Query(ctx, fmt.Sprintf(`
SELECT %s FROM task_message
WHERE task_id=$2 AND workspace_id=$1%s
ORDER BY seq %s LIMIT %d`, taskMessageCols, cond, order, q.Limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.TaskMessage
	for rows.Next() {
		var m orchapp.TaskMessage
		if err := rows.Scan(&m.ID, &m.TaskID, &m.Seq, &m.Kind, &m.CallID, &m.Content, &m.Truncated, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repo) CountPendingFence(ctx context.Context, workItemID, agentID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
SELECT count(*) FROM agent_task_queue
WHERE work_item_id=$1::uuid AND agent_id=$2::uuid
  AND (status IN ('queued','dispatched') OR (status='deferred' AND context->>'channel_task_media_pending'='true'))`,
		workItemID, agentID).Scan(&n)
	return n, err
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (r *Repo) InsertChatTask(ctx context.Context, t orchapp.Task) (orchapp.Task, error) {
	out, err := scanTask(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
INSERT INTO agent_task_queue (workspace_id, agent_id, chat_session_id, status, priority, max_attempts, context,
  originator_user_id, accountable_user_id, originator_source, trigger_evidence_kind)
VALUES ($1,$2,$3,'queued',0,2,
  jsonb_build_object('trigger','chat','chat_session_id',$6::text,'message',$4::text),
  nullif($5,'')::uuid, nullif($5,'')::uuid, 'direct_human','chat')
RETURNING %s`, taskCols),
		t.WorkspaceID, t.AgentID, t.Context["chat_session_id"], t.Context["message"], t.OriginatorUserID, t.Context["chat_session_id"]))
	return out, err
}

func (r *Repo) GetTaskByChatSession(ctx context.Context, wsID, sessionID string) (orchapp.Task, bool, error) {
	t, err := scanTask(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
SELECT %s FROM agent_task_queue WHERE workspace_id=$1 AND chat_session_id=$2
ORDER BY created_at DESC LIMIT 1`, taskCols), wsID, sessionID))
	if noRow(err) {
		return t, false, nil
	}
	return t, err == nil, err
}

func (r *Repo) TrySteer(ctx context.Context, wsID, workItemID, agentID, content, authorUserID string) (bool, error) {
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		var taskID string
		err := r.q(ctx).QueryRow(ctx, `
SELECT id FROM agent_task_queue
WHERE workspace_id=$1 AND work_item_id=$2::uuid AND agent_id=$3::uuid AND status='running'
ORDER BY created_at DESC LIMIT 1 FOR UPDATE`,
			wsID, workItemID, agentID).Scan(&taskID)
		if err != nil {
			return err
		}
		_, err = r.q(ctx).Exec(ctx, `
INSERT INTO task_supplement (task_id, status, content, author_user_id) VALUES ($1,'pending',$2,nullif($3,'')::uuid)`,
			taskID, content, authorUserID)
		return err
	})
	if err != nil {
		if noRow(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *Repo) ClaimNextSupplement(ctx context.Context, taskID string) (id, content string, ok bool, err error) {
	err = r.q(ctx).QueryRow(ctx, `
UPDATE task_supplement s SET status='delivering'
WHERE s.id = (
  SELECT id FROM task_supplement WHERE task_id=$1 AND status='pending'
  ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
) RETURNING id, content`, taskID).Scan(&id, &content)
	if noRow(err) {
		return "", "", false, nil
	}
	return id, content, err == nil, err
}

func (r *Repo) AckSupplementDelivered(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx, `UPDATE task_supplement SET status='delivered' WHERE id=$1`, id)
	return err
}

func (r *Repo) SettlePendingSupplements(ctx context.Context, taskID string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE task_supplement SET status='failed', failure_code='turn_ended'
WHERE task_id=$1 AND status IN ('pending','delivering')`, taskID)
	return err
}

func (r *Repo) MergeIntoPending(ctx context.Context, wsID, workItemID, agentID, commentID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE agent_task_queue SET context = jsonb_set(context, '{coalesced_comment_ids}',
  coalesce(context->'coalesced_comment_ids','[]'::jsonb) || to_jsonb($4::text))
WHERE id = (
  SELECT id FROM agent_task_queue
  WHERE workspace_id=$1 AND work_item_id=$2::uuid AND agent_id=$3::uuid
    AND status IN ('queued','dispatched')
  ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED
)`, wsID, workItemID, agentID, commentID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) HasActiveTask(ctx context.Context, wsID, workItemID, agentID string) (bool, error) {
	var exists bool
	err := r.q(ctx).QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM agent_task_queue
WHERE workspace_id=$1 AND work_item_id=$2::uuid AND agent_id=$3::uuid AND status='running')`,
		wsID, workItemID, agentID).Scan(&exists)
	return exists, err
}

func (r *Repo) InsertDeferredTask(ctx context.Context, t orchapp.Task) (orchapp.Task, error) {
	if t.OriginatorSource == "" {
		t.OriginatorSource = "direct_human"
	}
	sqlq := fmt.Sprintf(`INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status, priority, max_attempts, context,
  originator_user_id, accountable_user_id, originator_source, trigger_evidence_kind)
VALUES ($1,$2,nullif($3,'')::uuid,'deferred',$4,$5,$6,nullif($7,'')::uuid,nullif($7,'')::uuid,$8,$9)
RETURNING %s`, taskCols)
	out, err := scanTask(r.q(ctx).QueryRow(ctx, sqlq,
		t.WorkspaceID, t.AgentID, t.WorkItemID, t.Priority, t.MaxAttempts, t.Context,
		t.OriginatorUserID, t.OriginatorSource, t.TriggerEvidenceKind))
	return out, asFenceErr(err)
}

func (r *Repo) PromoteDeferred(ctx context.Context) ([]orchapp.Task, error) {
	rows, err := r.q(ctx).Query(ctx, fmt.Sprintf(`
WITH pick AS (
  SELECT DISTINCT ON (work_item_id, agent_id) id
  FROM agent_task_queue d
  WHERE d.status = 'deferred'
    AND (d.fire_at IS NULL OR d.fire_at <= now())
    AND coalesce(d.context->>'channel_task_media_pending','') <> 'true'
    AND NOT EXISTS (
      SELECT 1 FROM agent_task_queue x
      WHERE x.work_item_id = d.work_item_id AND x.agent_id = d.agent_id
        AND x.id <> d.id
        AND (x.status IN ('queued','dispatched','running')
             OR (x.status = 'deferred' AND x.context->>'channel_task_media_pending' = 'true')))
  ORDER BY d.work_item_id, d.agent_id, d.created_at
)
UPDATE agent_task_queue t SET status='queued'
FROM pick WHERE t.id = pick.id AND t.status='deferred'
RETURNING %s`, tTaskCols))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.Task
	for rows.Next() {
		var t orchapp.Task
		if err := rows.Scan(&t.ID, &t.WorkspaceID, &t.AgentID, &t.WorkItemID, &t.Status, &t.Priority,
			&t.Attempt, &t.MaxAttempts, &t.Context, &t.OriginatorUserID, &t.AccountableUserID,
			&t.OriginatorSource, &t.TriggerEvidenceKind, &t.SessionRefIn, &t.IsLeaderTask, &t.SquadID,
			&t.FailureReason, &t.Error, &t.Result, &t.CreatedAt, &t.CompletedAt, &t.Summary); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) CancelActiveTasksByWorkItem(ctx context.Context, wsID, workItemID string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE agent_task_queue SET status='cancelled', completed_at=now()
WHERE workspace_id=$1 AND work_item_id=$2 AND status IN ('queued','deferred','dispatched','running')`,
		wsID, workItemID)
	return err
}

const cancelByCommentSQL = `
UPDATE agent_task_queue SET status='cancelled', completed_at=now()
WHERE workspace_id=$1 AND context->>'trigger_comment_id'=$2
  AND status IN ('queued','deferred','dispatched')
RETURNING id::text`

func (r *Repo) CancelTasksByTriggerComment(ctx context.Context, wsID, commentID string) ([]string, error) {
	var ids []string
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		rows, err := r.q(ctx).Query(ctx, cancelByCommentSQL, wsID, commentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			if err := r.SettlePendingSupplements(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (r *Repo) HistoryAgentIDs(ctx context.Context, workItemID string) ([]string, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT DISTINCT agent_id::text FROM agent_task_queue WHERE work_item_id=$1::uuid`, workItemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *Repo) InsertRecoveryTask(ctx context.Context, src orchapp.Task, note string) (orchapp.Task, error) {
	out, err := scanTask(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status, priority,
  attempt, max_attempts, context, originator_user_id, accountable_user_id, originator_source, trigger_evidence_kind)
SELECT workspace_id, agent_id, work_item_id, 'queued', 0, 1, 1,
  context || jsonb_build_object('recovery_note',$2::text,'delegated_from',$3::text),
  originator_user_id, accountable_user_id, 'delegation', 'delegated'
FROM agent_task_queue WHERE id=$1
RETURNING %s`, taskCols), src.ID, note, src.ID))
	return out, err
}

func (r *Repo) CountRecoveriesFrom(ctx context.Context, originTaskID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
SELECT count(*) FROM agent_task_queue
WHERE trigger_evidence_kind='delegated'
  AND context->>'delegated_from' = (
    SELECT coalesce(context->>'delegated_from', id::text) FROM agent_task_queue WHERE id=$1)`,
		originTaskID).Scan(&n)
	return n, err
}

const getLastSessionRefSQL = `
SELECT session_ref_out FROM agent_task_queue
WHERE work_item_id=$1::uuid AND agent_id=$2::uuid
  AND status IN ('completed','failed','cancelled')
  AND session_ref_out IS NOT NULL AND session_ref_out <> ''
  AND coalesce(failure_reason,'') NOT IN ('agent_error.context_overflow','agent_error.state_oversized')
  AND ($3 = '' OR coalesce(engine_tag,'') IN ('', $3))
ORDER BY completed_at DESC LIMIT 1`

func (r *Repo) GetLastSessionRef(ctx context.Context, workItemID, agentID, engineTag string) (string, bool, error) {
	var ref string
	err := r.q(ctx).QueryRow(ctx, getLastSessionRefSQL, workItemID, agentID, engineTag).Scan(&ref)
	if noRow(err) {
		return "", false, nil
	}
	return ref, err == nil, err
}

func (r *Repo) ListSquadFacts(ctx context.Context, wsID string) ([]domain.SquadFacts, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT s.id::text, s.leader_agent_id::text,
  coalesce((SELECT array_agg(sm.agent_id::text) FROM squad_member sm WHERE sm.squad_id=s.id), '{}')
FROM squad s WHERE s.workspace_id=$1`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SquadFacts
	for rows.Next() {
		var sq domain.SquadFacts
		var members []byte
		if err := rows.Scan(&sq.ID, &sq.LeaderID, &members); err != nil {
			return nil, err
		}
		_ = jsonUnmarshalStrings(members, &sq.MemberIDs)
		out = append(out, sq)
	}
	return out, rows.Err()
}

func (r *Repo) ParkTask(ctx context.Context, id string, it orchapp.InterruptInfo) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE agent_task_queue SET awaiting_input_since=now(),
  context = context || jsonb_build_object('interrupt', jsonb_build_object(
    'target_id',$2::text,'kind',$3::text,'payload',$4::text))
WHERE id=$1 AND status='running'`, id, it.TargetID, it.Kind, it.Payload)
	return err
}

func (r *Repo) ClearAwaiting(ctx context.Context, id string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE agent_task_queue SET awaiting_input_since=NULL WHERE id=$1 AND status='running'`, id)
	return err
}

func (r *Repo) ListExpiredAwaiting(ctx context.Context, olderThanMinutes int) ([]string, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT id::text FROM agent_task_queue
WHERE status='running' AND awaiting_input_since < now() - ($1::int * interval '1 minute')`, olderThanMinutes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *Repo) CancelRunningTask(ctx context.Context, wsID, id string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE agent_task_queue SET status='cancelled', completed_at=now()
WHERE workspace_id=$1 AND id=$2 AND status='running'`, wsID, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) DeleteTaskCheckpoints(ctx context.Context, taskID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`DELETE FROM agent_task_checkpoint WHERE id = 'task:' || $1`, taskID)
	return err
}

func (r *Repo) DeleteStaleSessionCheckpoints(ctx context.Context, olderThanHours int) (int, error) {
	tag, err := r.q(ctx).Exec(ctx, `
DELETE FROM agent_task_checkpoint c
WHERE c.id LIKE 'session:%'
  AND c.updated_at < now() - ($1::int * interval '1 hour')
  AND NOT EXISTS (
    SELECT 1 FROM chat_session s WHERE s.session_ref = c.id)
  AND NOT EXISTS (
    SELECT 1 FROM agent_task_queue t WHERE t.session_ref_out = c.id)`,
		olderThanHours)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *Repo) ListUnsettledDelegatedFailures(ctx context.Context) ([]orchapp.Task, error) {
	return r.listTasks(ctx, fmt.Sprintf(`
SELECT %s FROM agent_task_queue t
WHERE t.status = 'failed'
  AND EXISTS (
    SELECT 1
    FROM jsonb_array_elements_text(coalesce(t.context->'delivered_comment_ids','[]'::jsonb)) AS dc(cid)
    JOIN comment c ON c.id = dc.cid::uuid
    WHERE c.recovery_settled_at IS NULL)
ORDER BY t.created_at`, tTaskCols))
}

const uncoveredTriggerCommentsSQL = `
WITH RECURSIVE thread_root AS (
  SELECT c.id, c.id AS root_id
  FROM comment c
  WHERE c.parent_id IS NULL
  UNION ALL
  SELECT c.id, tr.root_id
  FROM comment c JOIN thread_root tr ON c.parent_id = tr.id
)
SELECT DISTINCT ON (tc.id)
  tc.workspace_id::text, tc.id::text, tc.work_item_id::text,
  tc.author_type, coalesce(tc.author_id::text,''), tc.content,
  coalesce(pc.author_type,''), coalesce(pc.author_id::text,''),
  coalesce(rc.author_type,''), coalesce(rc.author_id::text,''),
  wi.assignee_type, coalesce(wi.assignee_id::text,'')
FROM comment tc
JOIN agent_task_queue t ON t.context->>'trigger_comment_id' = tc.id::text
JOIN thread_root tr ON tr.id = tc.id
LEFT JOIN comment pc ON pc.id = tc.parent_id
LEFT JOIN comment rc ON rc.id = tr.root_id
JOIN work_item wi ON wi.id = tc.work_item_id
WHERE tc.deleted_at IS NULL
  AND t.status IN ('completed','failed','cancelled')
  AND t.completed_at < now() - interval '10 minutes'
  AND NOT EXISTS (
    SELECT 1 FROM agent_task_queue m
    WHERE m.context->>'trigger_comment_id' = tc.id::text
      AND m.context->>'reconcile_attempt' IS NOT NULL)
  AND NOT EXISTS (
    SELECT 1 FROM comment ac
    JOIN thread_root atr ON atr.id = ac.id
    WHERE atr.root_id = tr.root_id
      AND ac.author_type = 'agent'
      AND ac.deleted_at IS NULL
      AND ac.created_at > tc.created_at)
ORDER BY tc.id, t.completed_at DESC
LIMIT 10`

func (r *Repo) ListUncoveredTriggerComments(ctx context.Context) ([]orchapp.UncoveredComment, error) {
	rows, err := r.q(ctx).Query(ctx, uncoveredTriggerCommentsSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.UncoveredComment
	for rows.Next() {
		var u orchapp.UncoveredComment
		if err := rows.Scan(&u.WorkspaceID, &u.CommentID, &u.WorkItemID,
			&u.AuthorType, &u.AuthorID, &u.Content,
			&u.ParentAuthorType, &u.ParentAuthorID,
			&u.RootAuthorType, &u.RootAuthorID,
			&u.AssigneeType, &u.AssigneeID); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *Repo) GetTaskAwaiting(ctx context.Context, wsID, id string) (orchapp.Task, bool, error) {
	t, err := scanTask(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
SELECT %s FROM agent_task_queue
WHERE workspace_id=$1 AND id=$2 AND status='running' AND awaiting_input_since IS NOT NULL`,
		taskCols), wsID, id))
	if noRow(err) {
		return t, false, nil
	}
	return t, err == nil, err
}

func (r *Repo) ResetSupplementToPending(ctx context.Context, wsID, taskID, supplementID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE task_supplement s SET status='pending', failure_code=NULL
FROM agent_task_queue t
WHERE s.id=$3 AND s.task_id=$2 AND s.task_id=t.id
  AND t.workspace_id=$1 AND s.status='failed'`,
		wsID, taskID, supplementID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func jsonUnmarshalStrings(b []byte, out *[]string) error {
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

func (r *Repo) SessionTranscript(ctx context.Context, workItemID, agentID string, limit int) ([]orchapp.SessionTurn, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	rows, err := r.q(ctx).Query(ctx, `
SELECT result FROM agent_task_queue
WHERE work_item_id=$1::uuid AND agent_id=$2::uuid AND status='completed'
  AND coalesce(result,'') <> ''
ORDER BY completed_at DESC LIMIT $3`, workItemID, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var desc []orchapp.SessionTurn
	for rows.Next() {
		var res string
		if err := rows.Scan(&res); err != nil {
			return nil, err
		}
		desc = append(desc, orchapp.SessionTurn{Role: "assistant", Content: res})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]orchapp.SessionTurn, len(desc))
	for i := range desc {
		out[len(desc)-1-i] = desc[i]
	}
	return out, nil
}

func (r *Repo) InsertAuxUsage(ctx context.Context, wsID string, u orchapp.UsageReport) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO task_usage (workspace_id, provider, model,
  prompt_tokens, completion_tokens, cost_usd_ticks, agent_label, model_retries, model_failovers)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		wsID, u.Provider, u.Model, u.PromptTokens, u.CompletionTokens, u.CostUSDTicks,
		orDefault(u.AgentLabel, "aux"), u.ModelRetries, u.ModelFailovers)
	return err
}

const bdCols = `id::text, workspace_id::text, user_id::text, title, draft, created_at, updated_at`

func scanBD(row pgx.Row) (orchapp.BuilderDraft, error) {
	var d orchapp.BuilderDraft
	err := row.Scan(&d.ID, &d.WorkspaceID, &d.UserID, &d.Title, &d.Draft, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func (r *Repo) InsertBuilderDraft(ctx context.Context, d orchapp.BuilderDraft) (orchapp.BuilderDraft, error) {
	return scanBD(r.q(ctx).QueryRow(ctx, `
INSERT INTO agent_builder_draft (workspace_id, user_id, title, draft)
VALUES ($1,$2,$3,$4) RETURNING `+bdCols, d.WorkspaceID, d.UserID, d.Title, draftJSON(d.Draft)))
}

func (r *Repo) GetBuilderDraft(ctx context.Context, wsID, id string) (orchapp.BuilderDraft, bool, error) {
	out, err := scanBD(r.q(ctx).QueryRow(ctx,
		`SELECT `+bdCols+` FROM agent_builder_draft WHERE workspace_id=$1 AND id=$2`, wsID, id))
	if noRow(err) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) ListBuilderDrafts(ctx context.Context, wsID, userID string) ([]orchapp.BuilderDraft, error) {
	rows, err := r.q(ctx).Query(ctx,
		`SELECT `+bdCols+` FROM agent_builder_draft WHERE workspace_id=$1 AND user_id=$2 ORDER BY updated_at DESC`, wsID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.BuilderDraft
	for rows.Next() {
		d, err := scanBD(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repo) UpsertBuilderDraft(ctx context.Context, d orchapp.BuilderDraft) (orchapp.BuilderDraft, bool, error) {
	out, err := scanBD(r.q(ctx).QueryRow(ctx, `
UPDATE agent_builder_draft SET title=$3, draft=$4, updated_at=now()
WHERE id=$1 AND workspace_id=$2 AND user_id=$5
RETURNING `+bdCols, d.ID, d.WorkspaceID, d.Title, draftJSON(d.Draft), d.UserID))
	if noRow(err) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) DeleteBuilderDraft(ctx context.Context, wsID, id, userID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`DELETE FROM agent_builder_draft WHERE workspace_id=$1 AND id=$2 AND user_id=$3`, wsID, id, userID)
	return err
}

func draftJSON(d map[string]any) []byte {
	if len(d) == 0 {
		return []byte(`{}`)
	}
	b, _ := json.Marshal(d)
	return b
}

func (r *Repo) CountEvalToday(ctx context.Context, wsID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx, `
SELECT count(*) FROM task_usage
WHERE workspace_id=$1 AND agent_label='eval' AND created_at >= date_trunc('day', now())`, wsID).Scan(&n)
	return n, err
}

func (r *Repo) GetSquad(ctx context.Context, wsID, squadID string) (orchapp.SquadInfo, bool, error) {
	var sq orchapp.SquadInfo
	err := r.q(ctx).QueryRow(ctx, `
SELECT id::text, name, instructions, choreography FROM squad WHERE workspace_id=$1 AND id=$2`,
		wsID, squadID).Scan(&sq.ID, &sq.Name, &sq.Instructions, &sq.Choreography)
	if noRow(err) {
		return sq, false, nil
	}
	if err != nil {
		return sq, true, err
	}
	rows, err := r.q(ctx).Query(ctx, `
SELECT sm.agent_id::text, sm.role, a.name, a.description, a.runtime_config
FROM squad_member sm JOIN agent a ON a.id = sm.agent_id
WHERE sm.squad_id=$1 ORDER BY sm.role, a.name`, squadID)
	if err != nil {
		return sq, true, err
	}
	defer rows.Close()
	for rows.Next() {
		var m orchapp.SquadMember
		if err := rows.Scan(&m.AgentID, &m.Role, &m.Name, &m.Description, &m.RuntimeConfig); err != nil {
			return sq, true, err
		}
		sq.Members = append(sq.Members, m)
	}
	return sq, true, rows.Err()
}

func (r *Repo) LeaderAgentID(ctx context.Context, squadID string) string {
	var id string
	_ = r.q(ctx).QueryRow(ctx,
		`SELECT coalesce(leader_agent_id::text,'') FROM squad WHERE id=$1`, squadID).Scan(&id)
	return id
}

func (r *Repo) InsertMemberUsage(ctx context.Context, taskID, agentID, label string, u orchapp.UsageReport) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO task_usage (workspace_id, task_id, agent_id, provider, model,
  prompt_tokens, completion_tokens, cost_usd_ticks, agent_label, model_retries, model_failovers)
SELECT workspace_id, id, $2, $3, $4, $5, $6, $7, $8, $9, $10
FROM agent_task_queue WHERE id=$1`,
		taskID, agentID, u.Provider, u.Model, u.PromptTokens, u.CompletionTokens,
		u.CostUSDTicks, orDefault(label, "member"), u.ModelRetries, u.ModelFailovers)
	return err
}

func (r *Repo) RollupUsageHourly(ctx context.Context, upto time.Time) (int, error) {
	buckets := 0
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		tag, err := r.q(ctx).Exec(ctx, `
INSERT INTO task_usage_hourly (workspace_id, hour_bucket, agent_key, provider, model,
  prompt_tokens, completion_tokens, cost_usd_ticks, runs)
SELECT workspace_id, date_trunc('hour', created_at), coalesce(agent_id::text,''), provider, model,
  sum(prompt_tokens), sum(completion_tokens), sum(cost_usd_ticks), count(*)
FROM task_usage
WHERE created_at < $1
  AND created_at > (SELECT last_rolled_at FROM task_usage_hourly_state WHERE id=1)
GROUP BY 1, 2, 3, 4, 5
ON CONFLICT (workspace_id, hour_bucket, agent_key, provider, model) DO UPDATE SET
  prompt_tokens = task_usage_hourly.prompt_tokens + EXCLUDED.prompt_tokens,
  completion_tokens = task_usage_hourly.completion_tokens + EXCLUDED.completion_tokens,
  cost_usd_ticks = task_usage_hourly.cost_usd_ticks + EXCLUDED.cost_usd_ticks,
  runs = task_usage_hourly.runs + EXCLUDED.runs`, upto)
		if err != nil {
			return err
		}
		buckets = int(tag.RowsAffected())

		_, err = r.q(ctx).Exec(ctx, `
INSERT INTO task_usage_hourly_state (id, last_rolled_at) VALUES (1, $1)
ON CONFLICT (id) DO UPDATE SET last_rolled_at = greatest(task_usage_hourly_state.last_rolled_at, $1)`, upto)
		return err
	})
	return buckets, err
}

func (r *Repo) WorkItemUsage(ctx context.Context, wsID, workItemID string) (orchapp.WorkItemUsage, bool, error) {
	var exists bool
	if err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM work_item WHERE workspace_id=$1 AND id=$2::uuid)`,
		wsID, workItemID).Scan(&exists); err != nil {
		return orchapp.WorkItemUsage{}, false, err
	}
	if !exists {
		return orchapp.WorkItemUsage{}, false, nil
	}
	out := orchapp.WorkItemUsage{WorkItemID: workItemID, ByProvider: []orchapp.UsageByProvider{}}
	if err := r.q(ctx).QueryRow(ctx, `
SELECT count(DISTINCT u.task_id),
  coalesce(sum(u.prompt_tokens),0)::bigint,
  coalesce(sum(u.completion_tokens),0)::bigint,
  coalesce(sum(u.cost_usd_ticks),0)::bigint
FROM task_usage u JOIN agent_task_queue t ON t.id = u.task_id
WHERE t.workspace_id=$1 AND t.work_item_id=$2::uuid`,
		wsID, workItemID).Scan(&out.Tasks, &out.TotalInputTokens,
		&out.TotalOutputTokens, &out.TotalCostUSDTicks); err != nil {
		return out, true, err
	}
	rows, err := r.q(ctx).Query(ctx, `
SELECT u.provider, u.model,
  sum(u.prompt_tokens)::bigint, sum(u.completion_tokens)::bigint, sum(u.cost_usd_ticks)::bigint
FROM task_usage u JOIN agent_task_queue t ON t.id = u.task_id
WHERE t.workspace_id=$1 AND t.work_item_id=$2::uuid
GROUP BY u.provider, u.model ORDER BY u.provider, u.model`, wsID, workItemID)
	if err != nil {
		return out, true, err
	}
	defer rows.Close()
	for rows.Next() {
		var p orchapp.UsageByProvider
		if err := rows.Scan(&p.Provider, &p.Model, &p.InputTokens, &p.OutputTokens, &p.CostUSDTicks); err != nil {
			return out, true, err
		}
		out.ByProvider = append(out.ByProvider, p)
	}
	return out, true, rows.Err()
}

func (r *Repo) ListTasksByAgent(ctx context.Context, wsID, agentID string, limit int) ([]orchapp.Task, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.q(ctx).Query(ctx, fmt.Sprintf(`
SELECT %s FROM agent_task_queue WHERE workspace_id=$1 AND agent_id=$2
ORDER BY created_at DESC LIMIT %d`, taskCols, limit), wsID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.Task
	for rows.Next() {
		var t orchapp.Task
		if err := rows.Scan(&t.ID, &t.WorkspaceID, &t.AgentID, &t.WorkItemID, &t.Status, &t.Priority,
			&t.Attempt, &t.MaxAttempts, &t.Context, &t.OriginatorUserID, &t.AccountableUserID,
			&t.OriginatorSource, &t.TriggerEvidenceKind, &t.SessionRefIn, &t.IsLeaderTask, &t.SquadID,
			&t.FailureReason, &t.Error, &t.Result, &t.CreatedAt, &t.CompletedAt, &t.Summary); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) GetActiveTask(ctx context.Context, wsID, workItemID string) (orchapp.Task, bool, error) {
	t, err := scanTask(r.q(ctx).QueryRow(ctx, fmt.Sprintf(`
SELECT %s FROM agent_task_queue
WHERE workspace_id=$1 AND work_item_id=$2 AND status IN ('running','dispatched','queued')
ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'dispatched' THEN 1 ELSE 2 END, created_at DESC
LIMIT 1`, taskCols), wsID, workItemID))
	if noRow(err) {
		return t, false, nil
	}
	return t, err == nil, err
}

func (r *Repo) ListWorkspaceActiveTasks(ctx context.Context, wsID string) ([]orchapp.AgentRunSnapshotItem, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT id::text, coalesce(work_item_id::text,''), agent_id::text, status,
  coalesce(started_at, dispatched_at)
FROM agent_task_queue
WHERE workspace_id=$1 AND status IN ('queued','dispatched','running')
ORDER BY created_at, id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.AgentRunSnapshotItem
	for rows.Next() {
		var it orchapp.AgentRunSnapshotItem
		if err := rows.Scan(&it.TaskID, &it.WorkItemID, &it.AgentID, &it.Status, &it.StartedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (r *Repo) ListWorkspaceTasks(ctx context.Context, wsID string, f orchapp.TaskListFilter) ([]orchapp.TaskListRow, string, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	where := `t.workspace_id=$1`
	args := []any{wsID}
	if len(f.Statuses) > 0 {

		conds := make([]string, 0, len(f.Statuses))
		for _, s := range f.Statuses {
			switch s {
			case "awaiting_input":
				conds = append(conds, "(t.status='running' AND t.awaiting_input_since IS NOT NULL)")
			default:
				conds = append(conds, "t.status='"+s+"'")
			}
		}
		where += " AND (" + strings.Join(conds, " OR ") + ")"
	}
	if f.AgentID != "" {
		args = append(args, f.AgentID)
		where += fmt.Sprintf(" AND t.agent_id = $%d", len(args))
	}
	if f.Cursor != "" {
		at, id, err := decodeTaskCursor(f.Cursor)
		if err != nil {
			return nil, "", apierr.NewValidation("invalid cursor")
		}
		args = append(args, at, id)
		where += fmt.Sprintf(" AND (t.created_at, t.id) < ($%d, $%d)", len(args)-1, len(args))
	}

	rows, err := r.q(ctx).Query(ctx, fmt.Sprintf(`
SELECT t.id, t.workspace_id, t.agent_id, coalesce(t.work_item_id::text,''),
  CASE WHEN t.status='running' AND t.awaiting_input_since IS NOT NULL THEN 'awaiting_input' ELSE t.status END,
  t.priority,
  t.attempt, t.max_attempts, t.context, coalesce(t.originator_user_id::text,''),
  coalesce(t.accountable_user_id::text,''), t.originator_source, coalesce(t.trigger_evidence_kind,''),
  coalesce(t.session_ref_in,''), t.is_leader_task, coalesce(t.squad_id::text,''),
  coalesce(t.failure_reason,''), coalesce(t.error,''), coalesce(t.result,''), t.created_at, t.completed_at,
  coalesce(t.summary,''),
  a.name, coalesce(wi.title,'')
FROM agent_task_queue t
JOIN agent a ON a.id = t.agent_id
LEFT JOIN work_item wi ON wi.id = t.work_item_id
WHERE %s
ORDER BY t.created_at DESC, t.id DESC
LIMIT %d`, where, limit+1), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []orchapp.TaskListRow
	for rows.Next() {
		var x orchapp.TaskListRow
		if err := rows.Scan(&x.ID, &x.WorkspaceID, &x.AgentID, &x.WorkItemID, &x.Status, &x.Priority,
			&x.Attempt, &x.MaxAttempts, &x.Context, &x.OriginatorUserID, &x.AccountableUserID,
			&x.OriginatorSource, &x.TriggerEvidenceKind, &x.SessionRefIn, &x.IsLeaderTask, &x.SquadID,
			&x.FailureReason, &x.Error, &x.Result, &x.CreatedAt, &x.CompletedAt, &x.Summary,
			&x.AgentName, &x.WorkItemTitle); err != nil {
			return nil, "", err
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		last := out[limit-1]
		next = encodeTaskCursor(last.CreatedAt, last.ID)
		out = out[:limit]
	}
	return out, next, nil
}

func decodeTaskCursor(s string) (time.Time, string, error) {
	at, id, ok := strings.Cut(s, "|")
	if !ok || id == "" {
		return time.Time{}, "", fmt.Errorf("bad cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("bad cursor time")
	}
	return t, id, nil
}

func encodeTaskCursor(t time.Time, id string) string {
	return t.UTC().Format(time.RFC3339Nano) + "|" + id
}
func (r *Repo) InsertSupplementForPending(ctx context.Context, wsID, workItemID, agentID, content, authorUserID string) (bool, error) {

	steered, err := r.TrySteer(ctx, wsID, workItemID, agentID, content, authorUserID)
	if err != nil {
		return false, err
	}
	if steered {
		return true, nil
	}

	tag, err := r.q(ctx).Exec(ctx, `
UPDATE agent_task_queue SET context = context || jsonb_build_object('supplement', $4::text)
WHERE id = (
  SELECT id FROM agent_task_queue
  WHERE workspace_id=$1 AND work_item_id=$2::uuid AND agent_id=$3::uuid
    AND status IN ('queued','dispatched')
  ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED
)`, wsID, workItemID, agentID, content)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) InsightWorkingAgents(ctx context.Context, wsID string) ([]map[string]any, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT a.id::text, a.name, count(t.id) AS active
FROM agent a JOIN agent_task_queue t ON t.agent_id=a.id
WHERE a.workspace_id=$1 AND t.status IN ('running','dispatched','queued')
GROUP BY a.id, a.name ORDER BY active DESC`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, name string
		var n int
		if err := rows.Scan(&id, &name, &n); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"agent_id": id, "name": name, "active": n})
	}
	return out, rows.Err()
}

func (r *Repo) InsightRunCounts(ctx context.Context, wsID string) ([]map[string]any, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT a.id::text, a.name,
  count(*) FILTER (WHERE t.status='completed') AS completed,
  count(*) FILTER (WHERE t.status='failed') AS failed,
  count(*) FILTER (WHERE t.status='cancelled') AS cancelled
FROM agent a LEFT JOIN agent_task_queue t ON t.agent_id=a.id
WHERE a.workspace_id=$1 GROUP BY a.id, a.name ORDER BY a.name`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, name string
		var c, f, x int
		if err := rows.Scan(&id, &name, &c, &f, &x); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"agent_id": id, "name": name,
			"completed": c, "failed": f, "cancelled": x})
	}
	return out, rows.Err()
}

func (r *Repo) InsightActivity30d(ctx context.Context, wsID string) ([]map[string]any, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT date_trunc('day', created_at)::date::text AS day, count(*) AS n
FROM agent_task_queue
WHERE workspace_id=$1 AND created_at >= now() - interval '30 days'
GROUP BY 1 ORDER BY 1`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var day string
		var n int
		if err := rows.Scan(&day, &n); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"day": day, "tasks": n})
	}
	return out, rows.Err()
}

const squadCols = `id::text, workspace_id::text, name, instructions, coalesce(leader_agent_id::text,''), choreography, created_at`

func (r *Repo) CreateSquad(ctx context.Context, wsID, name, instructions, leaderID string, choreo bool) (map[string]any, error) {
	var id string
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO squad (workspace_id, name, instructions, leader_agent_id, choreography)
VALUES ($1,$2,$3,nullif($4,'')::uuid,$5) RETURNING id::text`,
		wsID, name, instructions, leaderID, choreo).Scan(&id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "name": name, "instructions": instructions,
		"leader_agent_id": leaderID, "choreography": choreo}, nil
}

func (r *Repo) ListSquads(ctx context.Context, wsID string) ([]map[string]any, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT id::text, name, instructions, coalesce(leader_agent_id::text,''), choreography,
  coalesce((SELECT array_to_json(array_agg(sm.agent_id::text))::text FROM squad_member sm WHERE sm.squad_id=squad.id), '[]') AS members
FROM squad WHERE workspace_id=$1 ORDER BY name`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, name, instr, leader string
		var choreo bool
		var members []byte
		if err := rows.Scan(&id, &name, &instr, &leader, &choreo, &members); err != nil {
			return nil, err
		}
		out = append(out, squadRowMap(id, name, instr, leader, choreo, members))
	}
	return out, rows.Err()
}

func squadRowMap(id, name, instr, leader string, choreo bool, members []byte) map[string]any {
	var ids []string
	_ = json.Unmarshal(members, &ids)
	return map[string]any{"id": id, "name": name, "instructions": instr,
		"leader_agent_id": leader, "choreography": choreo, "members": ids}
}

func (r *Repo) GetSquadRow(ctx context.Context, wsID, squadID string) (map[string]any, bool, error) {
	var id, name, instr, leader string
	var choreo bool
	var members []byte
	err := r.q(ctx).QueryRow(ctx, `
SELECT id::text, name, instructions, coalesce(leader_agent_id::text,''), choreography,
  coalesce((SELECT array_to_json(array_agg(sm.agent_id::text))::text FROM squad_member sm WHERE sm.squad_id=squad.id), '[]')
FROM squad WHERE workspace_id=$1 AND id=$2::uuid`,
		wsID, squadID).Scan(&id, &name, &instr, &leader, &choreo, &members)
	if noRow(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return squadRowMap(id, name, instr, leader, choreo, members), true, nil
}

func (r *Repo) SquadNameTaken(ctx context.Context, wsID, name, excludeSquadID string) (bool, error) {
	var exists bool
	err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM squad WHERE workspace_id=$1 AND name=$2 AND id <> $3::uuid)`,
		wsID, name, excludeSquadID).Scan(&exists)
	return exists, err
}

func (r *Repo) UpdateSquad(ctx context.Context, wsID, squadID, name, instructions, leaderAgentID string, choreography, joinLeader bool) (map[string]any, bool, error) {
	var out map[string]any
	ok := false
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		tag, err := r.q(ctx).Exec(ctx, `
UPDATE squad SET name=$3, instructions=$4, leader_agent_id=nullif($5,'')::uuid, choreography=$6
WHERE workspace_id=$1 AND id=$2::uuid`,
			wsID, squadID, name, instructions, leaderAgentID, choreography)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if joinLeader {
			if _, err := r.q(ctx).Exec(ctx, `
INSERT INTO squad_member (squad_id, agent_id, role)
SELECT $1::uuid, $2::uuid, 'leader'
WHERE EXISTS (SELECT 1 FROM squad s WHERE s.id=$1::uuid AND s.workspace_id=$3)
  AND EXISTS (SELECT 1 FROM agent a WHERE a.id=$2::uuid AND a.workspace_id=$3)
ON CONFLICT (squad_id, agent_id) DO NOTHING`, squadID, leaderAgentID, wsID); err != nil {
				return err
			}
		}
		out, ok, err = r.GetSquadRow(ctx, wsID, squadID)
		return err
	})
	return out, ok, err
}

func (r *Repo) DeleteSquad(ctx context.Context, wsID, squadID string) (orchapp.SquadDeleteResult, error) {
	res := orchapp.SquadNotFound
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		var belongs bool
		if err := r.q(ctx).QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM squad WHERE id=$1::uuid AND workspace_id=$2)`,
			squadID, wsID).Scan(&belongs); err != nil {
			return err
		}
		if !belongs {
			return nil
		}
		var n int
		if err := r.q(ctx).QueryRow(ctx,
			`SELECT count(*) FROM squad_member WHERE squad_id=$1::uuid`, squadID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			res = orchapp.SquadHasMembers
			return nil
		}
		if err := r.q(ctx).QueryRow(ctx, `
SELECT count(*) FROM agent_task_queue
WHERE squad_id=$1::uuid AND status IN ('queued','dispatched','running')`,
			squadID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			res = orchapp.SquadHasActiveTasks
			return nil
		}

		if _, err := r.q(ctx).Exec(ctx,
			`UPDATE agent_task_queue SET squad_id=NULL WHERE squad_id=$1::uuid`, squadID); err != nil {
			return err
		}
		if _, err := r.q(ctx).Exec(ctx,
			`DELETE FROM squad WHERE id=$1::uuid AND workspace_id=$2`, squadID, wsID); err != nil {
			return err
		}
		res = orchapp.SquadDeleted
		return nil
	})
	return res, err
}

func (r *Repo) AddSquadMember(ctx context.Context, wsID, squadID, agentID, role string) error {

	tag, err := r.q(ctx).Exec(ctx, `
INSERT INTO squad_member (squad_id, agent_id, role)
SELECT $1::uuid, $2::uuid, $3
WHERE EXISTS (SELECT 1 FROM squad s WHERE s.id=$1::uuid AND s.workspace_id=$4)
  AND EXISTS (SELECT 1 FROM agent a WHERE a.id=$2::uuid AND a.workspace_id=$4)
ON CONFLICT (squad_id, agent_id) DO UPDATE SET role=EXCLUDED.role`, squadID, agentID, role, wsID)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return apierr.New(apierr.NotFound, "squad or agent not found")
	}
	return nil
}

func (r *Repo) RemoveSquadMember(ctx context.Context, wsID, squadID, agentID string) error {

	_, err := r.q(ctx).Exec(ctx, `
DELETE FROM squad_member WHERE squad_id=$1::uuid AND agent_id=$2::uuid
  AND EXISTS (SELECT 1 FROM squad s WHERE s.id=$1::uuid AND s.workspace_id=$3)`, squadID, agentID, wsID)
	return err
}

func (r *Repo) UpdateSquadMemberRole(ctx context.Context, wsID, squadID, agentID, role string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE squad_member sm SET role=$4 FROM squad s
WHERE s.id=sm.squad_id AND s.workspace_id=$1 AND sm.squad_id=$2::uuid AND sm.agent_id=$3::uuid`,
		wsID, squadID, agentID, role)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) ListSquadMembers(ctx context.Context, wsID, squadID string) ([]orchapp.SquadMemberRow, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT sm.agent_id::text, a.name, sm.role, (a.archived_at IS NOT NULL)
FROM squad_member sm
JOIN squad s ON s.id=sm.squad_id
JOIN agent a ON a.id=sm.agent_id
WHERE s.workspace_id=$1 AND sm.squad_id=$2::uuid
ORDER BY sm.agent_id`, wsID, squadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.SquadMemberRow
	for rows.Next() {
		var m orchapp.SquadMemberRow
		if err := rows.Scan(&m.AgentID, &m.Name, &m.Role, &m.Archived); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repo) ListSquadMemberTasks(ctx context.Context, wsID, squadID string) ([]orchapp.SquadMemberTaskRow, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT sm.agent_id::text, a.name, sm.role, coalesce(t.id::text,''), coalesce(t.status,'')
FROM squad_member sm
JOIN squad s ON s.id=sm.squad_id
JOIN agent a ON a.id=sm.agent_id
LEFT JOIN agent_task_queue t ON t.agent_id=sm.agent_id
  AND t.status IN ('queued','dispatched','running','deferred')
WHERE s.workspace_id=$1 AND sm.squad_id=$2::uuid
ORDER BY sm.agent_id, t.created_at`, wsID, squadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []orchapp.SquadMemberTaskRow
	for rows.Next() {
		var row orchapp.SquadMemberTaskRow
		if err := rows.Scan(&row.AgentID, &row.Name, &row.Role, &row.TaskID, &row.TaskStatus); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r *Repo) SquadLeader(ctx context.Context, wsID, squadID string) (string, bool, error) {
	var leader string
	err := r.q(ctx).QueryRow(ctx,
		`SELECT coalesce(leader_agent_id::text,'') FROM squad WHERE workspace_id=$1 AND id=$2::uuid`,
		wsID, squadID).Scan(&leader)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return leader, true, nil
}

func (r *Repo) CredentialChain(ctx context.Context, wsID, provider, excludeID string, limit int) ([]string, error) {
	if r.Seal == nil {
		return nil, errors.New("credential sealer not wired (ENGINE_CREDENTIAL_MASTER_KEY; internal archive 09 §6.2 fail-closed)")
	}
	if limit <= 0 {
		limit = 2
	}
	rows, err := r.q(ctx).Query(ctx, `
SELECT encrypted_secret FROM agent_model_credential
WHERE workspace_id=$1 AND provider=$2 AND id <> nullif($3,'')::uuid
ORDER BY created_at, id LIMIT $4`, wsID, provider, excludeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return nil, err
		}
		secret, err := r.Seal.Decrypt(blob)
		if err != nil {
			return nil, err
		}
		out = append(out, secret)
	}
	return out, rows.Err()
}

func (r *Repo) PatchTaskContext(ctx context.Context, taskID string, patch map[string]any) error {
	if len(patch) == 0 {
		return nil
	}
	blob, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = r.q(ctx).Exec(ctx,
		`UPDATE agent_task_queue SET context = coalesce(context,'{}'::jsonb) || $2::jsonb WHERE id=$1`,
		taskID, blob)
	return err
}
