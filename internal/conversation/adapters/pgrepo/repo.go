package pgrepo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/conversation/app"
)

type Repo struct{ DB *pg.DB }

func New(db *pg.DB) *Repo { return &Repo{DB: db} }

var _ app.Repo = (*Repo)(nil)

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }
func noRow(err error) bool                    { return errors.Is(err, pgx.ErrNoRows) }

func (r *Repo) Within(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.DB.Within(ctx, fn)
}

const sessCols = `id, workspace_id, agent_id, creator_id, title, coalesce(session_ref,''), status,
(pinned_at IS NOT NULL) AS pinned, created_at, updated_at`

func (r *Repo) CreateSession(ctx context.Context, s app.Session) (app.Session, error) {
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status, explicitly_created_at)
VALUES ($1,$2,$3,$4,$5,now())
RETURNING `+sessCols,
		s.WorkspaceID, s.AgentID, s.CreatorID, s.Title, s.Status).
		Scan(&s.ID, &s.WorkspaceID, &s.AgentID, &s.CreatorID, &s.Title, &s.SessionRef, &s.Status, &s.Pinned, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func (r *Repo) GetSession(ctx context.Context, wsID, id string) (app.Session, bool, error) {
	var s app.Session
	err := r.q(ctx).QueryRow(ctx, `
SELECT `+sessCols+`
FROM chat_session WHERE workspace_id=$1 AND id=$2`, wsID, id).
		Scan(&s.ID, &s.WorkspaceID, &s.AgentID, &s.CreatorID, &s.Title, &s.SessionRef, &s.Status, &s.Pinned, &s.CreatedAt, &s.UpdatedAt)
	if noRow(err) {
		return s, false, nil
	}
	return s, err == nil, err
}

func (r *Repo) ListSessions(ctx context.Context, wsID, userID string) ([]app.Session, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT `+sessCols+`
FROM chat_session WHERE workspace_id=$1 AND creator_id=$2 AND status='active'
ORDER BY pinned_at DESC NULLS LAST, updated_at DESC LIMIT 100`, wsID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Session
	for rows.Next() {
		var s app.Session
		if err := rows.Scan(&s.ID, &s.WorkspaceID, &s.AgentID, &s.CreatorID, &s.Title, &s.SessionRef, &s.Status, &s.Pinned, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) ArchiveSession(ctx context.Context, wsID, id string, archived bool) error {
	st := "archived"
	if !archived {
		st = "active"
	}
	_, err := r.q(ctx).Exec(ctx, `UPDATE chat_session SET status=$3, updated_at=now() WHERE workspace_id=$1 AND id=$2`, wsID, id, st)
	return err
}

func (r *Repo) UpdateSessionTitle(ctx context.Context, wsID, id, title string) (app.Session, bool, error) {
	var s app.Session
	err := r.q(ctx).QueryRow(ctx, `
UPDATE chat_session SET title=$3, updated_at=now()
WHERE workspace_id=$1 AND id=$2
RETURNING `+sessCols, wsID, id, title).
		Scan(&s.ID, &s.WorkspaceID, &s.AgentID, &s.CreatorID, &s.Title, &s.SessionRef, &s.Status, &s.Pinned, &s.CreatedAt, &s.UpdatedAt)
	if noRow(err) {
		return s, false, nil
	}
	return s, err == nil, err
}

func (r *Repo) SetSessionPin(ctx context.Context, wsID, id string, pinned bool) (app.Session, bool, error) {
	var s app.Session
	err := r.q(ctx).QueryRow(ctx, `
UPDATE chat_session
SET pinned_at = CASE WHEN $3 THEN now() ELSE NULL END
WHERE workspace_id=$1 AND id=$2
RETURNING `+sessCols, wsID, id, pinned).
		Scan(&s.ID, &s.WorkspaceID, &s.AgentID, &s.CreatorID, &s.Title, &s.SessionRef, &s.Status, &s.Pinned, &s.CreatedAt, &s.UpdatedAt)
	if noRow(err) {
		return s, false, nil
	}
	return s, err == nil, err
}

func (r *Repo) DeleteSessionCascade(ctx context.Context, wsID, id string) (bool, error) {
	deleted := false
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		if _, err := r.q(ctx).Exec(ctx,
			`DELETE FROM chat_cancel_pending WHERE workspace_id=$1 AND session_id=$2`, wsID, id); err != nil {
			return err
		}
		if _, err := r.q(ctx).Exec(ctx,
			`DELETE FROM chat_draft_restore WHERE workspace_id=$1 AND session_id=$2`, wsID, id); err != nil {
			return err
		}
		if _, err := r.q(ctx).Exec(ctx,
			`DELETE FROM chat_message WHERE workspace_id=$1 AND session_id=$2`, wsID, id); err != nil {
			return err
		}
		tag, err := r.q(ctx).Exec(ctx,
			`DELETE FROM chat_session WHERE workspace_id=$1 AND id=$2`, wsID, id)
		if err != nil {
			return asSessionInUse(err)
		}
		deleted = tag.RowsAffected() > 0
		return nil
	})
	return deleted, err
}

func asSessionInUse(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return app.SessionInUseError("session is still bound to a channel route; retire the route first")
	}
	return err
}

func (r *Repo) NextSeq(ctx context.Context, sessionID string) (int64, error) {
	var seq int64
	err := r.q(ctx).QueryRow(ctx,
		`UPDATE chat_session SET msg_seq = msg_seq + 1 WHERE id=$1 RETURNING msg_seq`, sessionID).Scan(&seq)
	return seq, err
}

func (r *Repo) InsertMessage(ctx context.Context, m app.Message) (app.Message, error) {
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO chat_message (session_id, workspace_id, role, kind, content, task_id, seq)
VALUES ($1,(SELECT workspace_id FROM chat_session WHERE id=$1),$2,$3,$4,nullif($5,'')::uuid,$6)
RETURNING id, session_id, role, kind, content, coalesce(task_id::text,''), seq, created_at`,
		m.SessionID, m.Role, m.Kind, m.Content, m.TaskID, m.Seq).
		Scan(&m.ID, &m.SessionID, &m.Role, &m.Kind, &m.Content, &m.TaskID, &m.Seq, &m.CreatedAt)
	return m, err
}

func (r *Repo) InsertMessageParts(ctx context.Context, m app.Message, parts []map[string]any) (app.Message, error) {
	pb, err := json.Marshal(parts)
	if err != nil {
		return m, err
	}
	err = r.q(ctx).QueryRow(ctx, `
INSERT INTO chat_message (session_id, workspace_id, role, kind, content, task_id, seq, parts)
VALUES ($1,(SELECT workspace_id FROM chat_session WHERE id=$1),$2,$3,$4,nullif($5,'')::uuid,$6,$7::jsonb)
RETURNING id, session_id, role, kind, content, coalesce(task_id::text,''), seq, created_at`,
		m.SessionID, m.Role, m.Kind, m.Content, m.TaskID, m.Seq, pb).
		Scan(&m.ID, &m.SessionID, &m.Role, &m.Kind, &m.Content, &m.TaskID, &m.Seq, &m.CreatedAt)
	return m, err
}

func (r *Repo) ListMessages(ctx context.Context, sessionID string, limit int, beforeSeq int64) ([]app.Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.q(ctx).Query(ctx, `
SELECT id, session_id, role, kind, content, coalesce(task_id::text,''), seq, created_at
FROM chat_message WHERE session_id=$1 AND seq < $2 ORDER BY seq DESC LIMIT $3`,
		sessionID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Message
	for rows.Next() {
		var m app.Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Kind, &m.Content, &m.TaskID, &m.Seq, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repo) CountAssistant(ctx context.Context, sessionID string) (int, error) {
	var n int
	err := r.q(ctx).QueryRow(ctx,
		`SELECT count(*) FROM chat_message WHERE session_id=$1 AND role='assistant' AND kind IN ('message','no_response')`, sessionID).Scan(&n)
	return n, err
}

func (r *Repo) DeleteUserMessagesSince(ctx context.Context, sessionID string, sinceSeq int64) error {
	_, err := r.q(ctx).Exec(ctx, `DELETE FROM chat_message WHERE session_id=$1 AND role='user' AND seq >= $2`, sessionID, sinceSeq)
	return err
}

func (r *Repo) UpsertDraftRestore(ctx context.Context, wsID, sessionID, userID, content string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO chat_draft_restore (workspace_id, session_id, user_id, content, finalized)
VALUES ($1,$2,$3,$4,'pending')
ON CONFLICT DO NOTHING`, wsID, sessionID, userID, content)
	return err
}

func (r *Repo) FinalizeDraft(ctx context.Context, sessionID string, outcome string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE chat_draft_restore SET finalized=$2 WHERE session_id=$1 AND finalized='pending'`, sessionID, outcome)
	return err
}

func (r *Repo) GetPendingDraft(ctx context.Context, sessionID, userID string) (string, bool, error) {
	var content string
	err := r.q(ctx).QueryRow(ctx,
		`SELECT content FROM chat_draft_restore WHERE session_id=$1 AND user_id=$2 AND finalized='pending' ORDER BY created_at DESC LIMIT 1`,
		sessionID, userID).Scan(&content)
	if noRow(err) {
		return "", false, nil
	}
	return content, err == nil, err
}

func (r *Repo) UpdateResumePointer(ctx context.Context, sessionID, ref string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE chat_session SET session_ref=$2, updated_at=now()
WHERE id=$1 AND ($2 <> '' AND (session_ref IS NULL OR session_ref <> $2))`, sessionID, ref)
	return err
}

func (r *Repo) ClearResumePointer(ctx context.Context, sessionID string) error {
	_, err := r.q(ctx).Exec(ctx,
		`UPDATE chat_session SET session_ref=NULL, updated_at=now() WHERE id=$1`, sessionID)
	return err
}

func (r *Repo) SetUnread(ctx context.Context, sessionID string, _ time.Time) error {
	_, err := r.q(ctx).Exec(ctx, `UPDATE chat_session SET unread_since=now(), updated_at=now() WHERE id=$1`, sessionID)
	return err
}

func (r *Repo) MarkRead(ctx context.Context, sessionID string) error {
	_, err := r.q(ctx).Exec(ctx, `UPDATE chat_session SET unread_since=NULL, last_read_at=now() WHERE id=$1`, sessionID)
	return err
}

func (r *Repo) AppendMessageContent(ctx context.Context, messageID, appendage, dedupMarker string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE chat_message SET content = content || $2
WHERE id=$1 AND strpos(content, $3) = 0`, messageID, appendage, dedupMarker)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) GetMessage(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	var exists bool
	err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM chat_message WHERE id=$1::uuid)`, id).Scan(&exists)
	return exists, err
}

func (r *Repo) ArchiveSessionsByUser(ctx context.Context, wsID, userID string) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx,
		`UPDATE chat_session SET status='archived', updated_at=now() WHERE workspace_id=$1 AND creator_id=$2`, wsID, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *Repo) MarkCancelPending(ctx context.Context, sessionID, taskID, userID string) error {
	if taskID == "" {
		return nil
	}
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO chat_cancel_pending (task_id, workspace_id, session_id, initiator_user_id)
SELECT $1::uuid, s.workspace_id, s.id, $3::uuid FROM chat_session s WHERE s.id=$2::uuid
ON CONFLICT (task_id) DO NOTHING`, taskID, sessionID, userID)
	return err
}

func (r *Repo) GetCancelPending(ctx context.Context, sessionID, taskID string) (string, bool, error) {
	if taskID == "" {
		return "", false, nil
	}
	var initiator string
	err := r.q(ctx).QueryRow(ctx,
		`SELECT initiator_user_id::text FROM chat_cancel_pending WHERE session_id=$1 AND task_id=$2::uuid`,
		sessionID, taskID).Scan(&initiator)
	if noRow(err) {
		return "", false, nil
	}
	return initiator, err == nil, err
}

func (r *Repo) ClearCancelPending(ctx context.Context, sessionID, taskID string) error {
	if taskID == "" {
		return nil
	}
	_, err := r.q(ctx).Exec(ctx,
		`DELETE FROM chat_cancel_pending WHERE session_id=$1 AND task_id=$2::uuid`, sessionID, taskID)
	return err
}

func (r *Repo) HasStoppedMessage(ctx context.Context, sessionID, taskID string) (bool, error) {
	if taskID == "" {
		return false, nil
	}
	var exists bool
	err := r.q(ctx).QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM chat_message WHERE session_id=$1 AND task_id=$2::uuid AND kind='system_stopped')`,
		sessionID, taskID).Scan(&exists)
	return exists, err
}

func (r *Repo) ListMessagesForTest(sessionID string) ([]app.Message, error) {
	return r.ListMessagesAsc(context.Background(), sessionID)
}

func (r *Repo) ListMessagesAsc(ctx context.Context, sessionID string) ([]app.Message, error) {
	desc, err := r.ListMessages(ctx, sessionID, 200, 1<<62)
	if err != nil {
		return nil, err
	}
	out := make([]app.Message, 0, len(desc))
	for i := len(desc) - 1; i >= 0; i-- {
		out = append(out, desc[i])
	}
	return out, nil
}

const qaCols = `id::text, workspace_id::text, user_id::text, label, prompt, is_private, created_at`

func (r *Repo) InsertQuickAction(ctx context.Context, q app.QuickAction) (app.QuickAction, error) {
	var out app.QuickAction
	err := r.DB.Tx(ctx).QueryRow(ctx, `
INSERT INTO quick_action (workspace_id, user_id, label, prompt, is_private)
VALUES ($1,$2,$3,$4,$5) RETURNING `+qaCols,
		q.WorkspaceID, q.UserID, q.Label, q.Prompt, q.IsPrivate).
		Scan(&out.ID, &out.WorkspaceID, &out.UserID, &out.Label, &out.Prompt, &out.IsPrivate, &out.CreatedAt)
	return out, err
}

func (r *Repo) ListQuickActions(ctx context.Context, wsID, userID string) ([]app.QuickAction, error) {
	rows, err := r.DB.Tx(ctx).Query(ctx,
		`SELECT `+qaCols+` FROM quick_action WHERE workspace_id=$1 AND (user_id=$2 OR is_private=false) ORDER BY created_at DESC`, wsID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.QuickAction
	for rows.Next() {
		var q app.QuickAction
		if err := rows.Scan(&q.ID, &q.WorkspaceID, &q.UserID, &q.Label, &q.Prompt, &q.IsPrivate, &q.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (r *Repo) UpdateQuickAction(ctx context.Context, wsID, id, userID, label, prompt string) (app.QuickAction, bool, error) {
	var out app.QuickAction
	err := r.DB.Tx(ctx).QueryRow(ctx, `
UPDATE quick_action SET label=$4, prompt=$5
WHERE workspace_id=$1 AND id=$2 AND user_id=$3
RETURNING `+qaCols, wsID, id, userID, label, prompt).
		Scan(&out.ID, &out.WorkspaceID, &out.UserID, &out.Label, &out.Prompt, &out.IsPrivate, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	return out, err == nil, err
}

func (r *Repo) DeleteQuickAction(ctx context.Context, wsID, id, userID string) error {
	_, err := r.DB.Tx(ctx).Exec(ctx,
		`DELETE FROM quick_action WHERE workspace_id=$1 AND id=$2 AND user_id=$3`, wsID, id, userID)
	return err
}

func (r *Repo) UpsertPinnedAgent(ctx context.Context, wsID, userID, agentID string) error {
	_, err := r.DB.Tx(ctx).Exec(ctx, `
INSERT INTO chat_pinned_agent (user_id, workspace_id, agent_id, position)
VALUES ($1,$2,$3,(SELECT coalesce(max(position),0)+1 FROM chat_pinned_agent WHERE user_id=$1 AND workspace_id=$2))
ON CONFLICT (user_id, workspace_id, agent_id) DO NOTHING`, userID, wsID, agentID)
	return err
}

func (r *Repo) ListPinnedAgents(ctx context.Context, wsID, userID string) ([]app.PinnedAgent, error) {
	rows, err := r.DB.Tx(ctx).Query(ctx, `
SELECT p.agent_id::text, a.name, p.position
FROM chat_pinned_agent p JOIN agent a ON a.id = p.agent_id
WHERE p.workspace_id=$1 AND p.user_id=$2
ORDER BY p.position`, wsID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.PinnedAgent
	for rows.Next() {
		var p app.PinnedAgent
		if err := rows.Scan(&p.AgentID, &p.Name, &p.Position); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) DeletePinnedAgent(ctx context.Context, wsID, userID, agentID string) error {
	_, err := r.DB.Tx(ctx).Exec(ctx,
		`DELETE FROM chat_pinned_agent WHERE workspace_id=$1 AND user_id=$2 AND agent_id=$3`, wsID, userID, agentID)
	return err
}
