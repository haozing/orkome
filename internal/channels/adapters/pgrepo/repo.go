package pgrepo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/channels/app"
	"orkome/internal/channels/domain"
	"orkome/internal/channels/wire"
)

type Repo struct{ DB *pg.DB }

func New(db *pg.DB) *Repo { return &Repo{DB: db} }

var _ app.Repo = (*Repo)(nil)

func (r *Repo) q(ctx context.Context) pg.DBTX { return r.DB.Tx(ctx) }
func noRow(err error) bool                    { return errors.Is(err, pgx.ErrNoRows) }

const installationCols = `id::text, workspace_id::text, coalesce(agent_id::text,''),
 coalesce(installer_user_id::text,''), kind, name, status, coalesce(credential_fingerprint,''), config`

func scanInstallation(row pgx.Row) (app.Installation, error) {
	var i app.Installation
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.AgentID, &i.InstallerUserID,
		&i.Kind, &i.Name, &i.Status, &i.Fingerprint, &i.Config)
	return i, err
}

func (r *Repo) ResolveInstallation(ctx context.Context, kind wire.Kind, routingKey string) (app.Installation, error) {
	inst, err := scanInstallation(r.q(ctx).QueryRow(ctx, `
SELECT `+installationCols+` FROM channel_installation
WHERE kind=$1 AND coalesce(config->>'routing_key','')=$2`, string(kind), routingKey))
	if noRow(err) {
		return app.Installation{}, app.ErrInstallationNotFound
	}
	return inst, err
}

func (r *Repo) GetInstallation(ctx context.Context, installationID string) (app.Installation, error) {
	inst, err := scanInstallation(r.q(ctx).QueryRow(ctx,
		`SELECT `+installationCols+` FROM channel_installation WHERE id=$1`, installationID))
	if noRow(err) {
		return app.Installation{}, app.ErrInstallationNotFound
	}
	return inst, err
}

func (r *Repo) ListActiveInstallations(ctx context.Context) ([]app.Installation, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT `+installationCols+` FROM channel_installation WHERE status='active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Installation
	for rows.Next() {
		var i app.Installation
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.AgentID, &i.InstallerUserID,
			&i.Kind, &i.Name, &i.Status, &i.Fingerprint, &i.Config); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if fp := domain.CredentialFingerprint(string(out[i].Kind), out[i].Config); fp != out[i].Fingerprint {
			if _, err := r.q(ctx).Exec(ctx,
				`UPDATE channel_installation SET credential_fingerprint=$2 WHERE id=$1`, out[i].ID, fp); err == nil {
				out[i].Fingerprint = fp
			}
		}
	}
	return out, nil
}

func (r *Repo) ClaimInbound(ctx context.Context, installationID, externalMessageID string) (string, error) {
	var token string
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO channel_inbound_message_dedup (provider, external_id, state, channel_installation_id)
VALUES ((SELECT kind::text FROM channel_installation WHERE id=$1), $2, 'claimed', $1)
ON CONFLICT (provider, external_id) DO NOTHING
RETURNING claim_token::text`, installationID, externalMessageID).Scan(&token)
	if err == nil {
		return token, nil
	}
	if !noRow(err) {
		return "", err
	}

	err = r.q(ctx).QueryRow(ctx, `
UPDATE channel_inbound_message_dedup SET state='claimed', claim_token=gen_random_uuid(), channel_installation_id=$1
WHERE provider=(SELECT kind::text FROM channel_installation WHERE id=$1) AND external_id=$2 AND state='seen'
RETURNING claim_token::text`, installationID, externalMessageID).Scan(&token)
	if noRow(err) {
		return "", app.ErrDuplicateInbound
	}
	return token, err
}

func (r *Repo) MarkInboundProcessed(ctx context.Context, installationID, externalMessageID, claimToken string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE channel_inbound_message_dedup SET state='processed', processed_at=now()
WHERE channel_installation_id=$1 AND external_id=$2 AND claim_token=$3`,
		installationID, externalMessageID, claimToken)
	return err
}

func (r *Repo) ReleaseInbound(ctx context.Context, installationID, externalMessageID, claimToken string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE channel_inbound_message_dedup SET state='seen'
WHERE channel_installation_id=$1 AND external_id=$2 AND claim_token=$3 AND state='claimed'`,
		installationID, externalMessageID, claimToken)
	return err
}

func (r *Repo) CleanStaleInbound(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := r.q(ctx).Exec(ctx,
		`DELETE FROM channel_inbound_message_dedup WHERE created_at < $1`, olderThan)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *Repo) RecordDrop(ctx context.Context, rec app.DropAudit) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO channel_inbound_audit
  (channel_installation_id, kind, external_chat_id, event_type, external_event_id, external_message_id, drop_reason)
VALUES (nullif($1,'')::uuid, $2, nullif($3,''), $4, nullif($5,''), nullif($6,''), $7)`,
		rec.InstallationID, string(rec.Kind), rec.ExternalChatID, rec.EventType,
		rec.ExternalEventID, rec.ExternalMessageID, rec.DropReason)
	return err
}

func (r *Repo) ResolveSender(ctx context.Context, installationID, externalUserID string) (string, error) {
	var userID string
	err := r.q(ctx).QueryRow(ctx, `
SELECT member_user_id::text FROM channel_user_binding
WHERE channel_installation_id=$1 AND external_user_id=$2`, installationID, externalUserID).Scan(&userID)
	if noRow(err) {
		return "", app.ErrSenderUnbound
	}
	return userID, err
}

func (r *Repo) IssueBindingToken(ctx context.Context, installationID, externalUserID string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > app.BindingTokenTTL {
		ttl = app.BindingTokenTTL
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO channel_binding_token (token_hash, workspace_id, channel_installation_id, external_user_id, expires_at)
SELECT $1, workspace_id, $2, $3, now() + make_interval(secs => $4::int)
FROM channel_installation WHERE id=$2`,
		hex.EncodeToString(sum[:]), installationID, externalUserID, int(ttl.Seconds()))
	return token, err
}

func (r *Repo) ConsumeBindingToken(ctx context.Context, workspaceID, token, memberUserID string) error {
	sum := sha256.Sum256([]byte(token))
	return r.DB.Within(ctx, func(ctx context.Context) error {
		tag, err := r.q(ctx).Exec(ctx, `
UPDATE channel_binding_token SET consumed_at=now()
WHERE token_hash=$1 AND workspace_id=$2 AND consumed_at IS NULL AND expires_at > now()`,
			hex.EncodeToString(sum[:]), workspaceID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return app.ErrBindingTokenInvalid
		}
		_, err = r.q(ctx).Exec(ctx, `
INSERT INTO channel_user_binding (workspace_id, member_user_id, channel_installation_id, kind, external_user_id)
SELECT t.workspace_id, $2, t.channel_installation_id, i.kind, t.external_user_id
FROM channel_binding_token t JOIN channel_installation i ON i.id = t.channel_installation_id
WHERE t.token_hash=$1
ON CONFLICT (channel_installation_id, external_user_id)
DO UPDATE SET member_user_id=EXCLUDED.member_user_id, bound_at=now()`,
			hex.EncodeToString(sum[:]), memberUserID)
		return err
	})
}

const routeCols = `id::text, channel_installation_id::text, workspace_id::text, external_chat_id,
 chat_type, coalesce(chat_session_id::text,''), generation, context_revision,
 coalesce(last_message_id,''), coalesce(last_thread_id,''),
 coalesce(title,''), coalesce(title_source,'')`

func scanRoute(row pgx.Row) (app.Route, error) {
	var rt app.Route
	err := row.Scan(&rt.ID, &rt.InstallationID, &rt.WorkspaceID, &rt.ExternalChatID,
		&rt.ChatType, &rt.ChatSessionID, &rt.Generation, &rt.ContextRevision,
		&rt.LastMessageID, &rt.LastThreadID, &rt.Title, &rt.TitleSource)
	return rt, err
}

func (r *Repo) GetRoute(ctx context.Context, installationID, externalChatID string) (app.Route, bool, error) {
	rt, err := scanRoute(r.q(ctx).QueryRow(ctx, `
SELECT `+routeCols+` FROM channel_chat_session_binding
WHERE channel_installation_id=$1 AND external_chat_id=$2 AND retired_at IS NULL`,
		installationID, externalChatID))
	if noRow(err) {
		return app.Route{}, false, nil
	}
	return rt, err == nil, err
}

func (r *Repo) InsertRoute(ctx context.Context, rt app.Route) (app.Route, error) {
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		row := r.q(ctx).QueryRow(ctx, `
INSERT INTO channel_chat_session_binding
  (channel_installation_id, external_chat_id, workspace_id, chat_session_id, chat_type, generation, context_revision)
VALUES ($1, $2, $3, $4, $5, 1, 1)
ON CONFLICT (channel_installation_id, external_chat_id) DO NOTHING
RETURNING `+routeCols,
			rt.InstallationID, rt.ExternalChatID, rt.WorkspaceID, rt.ChatSessionID, string(rt.ChatType))
		out, scanErr := scanRoute(row)
		if noRow(scanErr) {

			tag, err := r.q(ctx).Exec(ctx, `
UPDATE channel_chat_session_binding
SET chat_session_id=$3, generation=generation+1, context_revision=1, retired_at=NULL, updated_at=now()
WHERE channel_installation_id=$1 AND external_chat_id=$2 AND retired_at IS NOT NULL`,
				rt.InstallationID, rt.ExternalChatID, rt.ChatSessionID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return app.ErrRouteConflict
			}
			return nil
		}
		if scanErr != nil {
			return scanErr
		}
		rt = out
		return nil
	})
	if err != nil {
		return app.Route{}, err
	}
	if rt.ID == "" {
		got, ok, err := r.GetRoute(ctx, rt.InstallationID, rt.ExternalChatID)
		if err != nil || !ok {
			return app.Route{}, err
		}
		return got, nil
	}
	_, _ = r.q(ctx).Exec(ctx, `
INSERT INTO channel_chat_context_generation (chat_session_id, revision, initiator_user_id)
VALUES ($1, 1, nullif($2,'')::uuid) ON CONFLICT DO NOTHING`, rt.ChatSessionID, "")
	return rt, nil
}

func (r *Repo) RotateRoute(ctx context.Context, installationID, externalChatID, newChatSessionID string) (app.Route, error) {
	var rt app.Route
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		var scanErr error
		rt, scanErr = scanRoute(r.q(ctx).QueryRow(ctx, `
UPDATE channel_chat_session_binding
SET chat_session_id=$3, generation=generation+1, context_revision=1, retired_at=NULL, updated_at=now()
WHERE channel_installation_id=$1 AND external_chat_id=$2
RETURNING `+routeCols, installationID, externalChatID, newChatSessionID))
		return scanErr
	})
	if err != nil {
		return app.Route{}, err
	}
	_, _ = r.q(ctx).Exec(ctx, `
INSERT INTO channel_chat_context_generation (chat_session_id, revision)
VALUES ($1, 1) ON CONFLICT DO NOTHING`, newChatSessionID)
	return rt, nil
}

func (r *Repo) TouchRouteMessage(ctx context.Context, installationID, externalChatID, externalMessageID, externalThreadID string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE channel_chat_session_binding
SET last_message_id=nullif($3,''), last_thread_id=nullif($4,''), updated_at=now()
WHERE channel_installation_id=$1 AND external_chat_id=$2`,
		installationID, externalChatID, externalMessageID, externalThreadID)
	return err
}

func (r *Repo) CurrentGeneration(ctx context.Context, chatSessionID string) (int64, error) {
	var rev int64
	err := r.q(ctx).QueryRow(ctx, `
SELECT context_revision FROM channel_chat_session_binding
WHERE chat_session_id=$1 AND retired_at IS NULL`, chatSessionID).Scan(&rev)
	if noRow(err) {
		return 1, nil
	}
	return rev, err
}

func (r *Repo) NextGeneration(ctx context.Context, chatSessionID, initiatorUserID string) (int64, error) {
	var rev int64
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		err := r.q(ctx).QueryRow(ctx, `
UPDATE channel_chat_session_binding SET context_revision=context_revision+1, updated_at=now()
WHERE chat_session_id=$1 AND retired_at IS NULL
RETURNING context_revision`, chatSessionID).Scan(&rev)
		if noRow(err) {
			rev = 1
			err = nil
		}
		if err != nil {
			return err
		}
		_, err = r.q(ctx).Exec(ctx, `
INSERT INTO channel_chat_context_generation (chat_session_id, revision, initiator_user_id)
VALUES ($1, $2, nullif($3,'')::uuid)`, chatSessionID, rev, initiatorUserID)
		return err
	})
	return rev, err
}

type armedPayload struct {
	WorkspaceID       string `json:"workspace_id,omitempty"`
	AgentID           string `json:"agent_id,omitempty"`
	UserID            string `json:"user_id,omitempty"`
	MediaPending      bool   `json:"media_pending,omitempty"`
	BindingID         string `json:"binding_id,omitempty"`
	InstallationID    string `json:"installation_id,omitempty"`
	Kind              string `json:"kind,omitempty"`
	ExternalChatID    string `json:"external_chat_id,omitempty"`
	ChatType          string `json:"chat_type,omitempty"`
	ExternalMessageID string `json:"external_message_id,omitempty"`
	ExternalThreadID  string `json:"external_thread_id,omitempty"`
	RouteRevision     int64  `json:"route_revision,omitempty"`
}

func (r *Repo) ArmPendingRun(ctx context.Context, snap app.ArmedPendingContext) (string, error) {
	payload, err := json.Marshal(armedPayload{
		WorkspaceID:       snap.WorkspaceID,
		AgentID:           snap.AgentID,
		UserID:            snap.UserID,
		MediaPending:      snap.MediaPending,
		BindingID:         snap.BindingID,
		InstallationID:    snap.InstallationID,
		Kind:              string(snap.Kind),
		ExternalChatID:    snap.ExternalChatID,
		ChatType:          string(snap.ChatType),
		ExternalMessageID: snap.ExternalMessageID,
		ExternalThreadID:  snap.ExternalThreadID,
		RouteRevision:     snap.RouteRevision,
	})
	if err != nil {
		return "", err
	}
	var token string
	err = r.q(ctx).QueryRow(ctx, `
INSERT INTO channel_chat_context_generation
  (chat_session_id, revision, run_armed_at, run_armed_token, run_armed_payload)
VALUES ($1::uuid, $2, now(), gen_random_uuid(), $3::jsonb)
ON CONFLICT (chat_session_id, revision) DO UPDATE SET
  run_armed_at=now(), run_armed_token=gen_random_uuid(), run_armed_payload=EXCLUDED.run_armed_payload
RETURNING run_armed_token::text`, snap.SessionID, snap.ContextRevision, payload).Scan(&token)
	return token, err
}

func (r *Repo) ClearPendingRun(ctx context.Context, chatSessionID string, revision int64, token string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE channel_chat_context_generation
SET run_armed_at=NULL, run_armed_token=NULL, run_armed_payload=NULL
WHERE chat_session_id=$1::uuid AND revision=$2 AND run_armed_token=$3::uuid`,
		chatSessionID, revision, token)
	return err
}

func (r *Repo) ListArmedPendingContexts(ctx context.Context) ([]app.ArmedPendingContext, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT chat_session_id::text, revision, run_armed_token::text, coalesce(run_armed_payload, '{}'::jsonb)
FROM channel_chat_context_generation
WHERE run_armed_token IS NOT NULL
ORDER BY chat_session_id, revision`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.ArmedPendingContext
	for rows.Next() {
		var snap app.ArmedPendingContext
		var payload []byte
		if err := rows.Scan(&snap.SessionID, &snap.ContextRevision, &snap.Token, &payload); err != nil {
			return nil, err
		}
		var p armedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		snap.WorkspaceID, snap.AgentID, snap.UserID = p.WorkspaceID, p.AgentID, p.UserID
		snap.MediaPending, snap.BindingID, snap.InstallationID = p.MediaPending, p.BindingID, p.InstallationID
		snap.Kind, snap.ExternalChatID, snap.ChatType = wire.Kind(p.Kind), p.ExternalChatID, wire.ChatType(p.ChatType)
		snap.ExternalMessageID, snap.ExternalThreadID, snap.RouteRevision = p.ExternalMessageID, p.ExternalThreadID, p.RouteRevision
		out = append(out, snap)
	}
	return out, rows.Err()
}

func (r *Repo) InsertMediaIntents(ctx context.Context, intents []app.MediaIntent) error {
	for _, in := range intents {
		if _, err := r.q(ctx).Exec(ctx, `
INSERT INTO channel_media_pending_object
  (storage_key, workspace_id, chat_message_id, storage_url, channel_installation_id, state, next_attempt_at)
VALUES ($1, $2, nullif($3,'')::uuid, $4, nullif($5,'')::uuid, 'pending', $6)
ON CONFLICT (storage_key) DO NOTHING`,
			in.StorageKey, in.WorkspaceID, in.ChatMessageID, in.StorageURL,
			in.InstallationID, in.NextAttemptAt); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) BindMediaToMessage(ctx context.Context, chatMessageID string, storageKeys []string) (int, error) {
	tag, err := r.q(ctx).Exec(ctx, `
DELETE FROM channel_media_pending_object
WHERE storage_key = ANY($1) AND state='pending'`, storageKeys)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *Repo) ListDueMedia(ctx context.Context, now time.Time, limit int) ([]app.MediaIntent, error) {
	rows, err := r.q(ctx).Query(ctx, `
SELECT storage_key, workspace_id::text, coalesce(chat_message_id::text,''), storage_url,
 coalesce(channel_installation_id::text,''), state, attempt, tombstone_pass, next_attempt_at, created_at
FROM channel_media_pending_object
WHERE (state='pending' AND next_attempt_at <= $1)
   OR (state='deleting' AND (lease_expires_at IS NULL OR lease_expires_at < $1))
   OR (state='tombstoned' AND next_attempt_at <= $1)
ORDER BY next_attempt_at
LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.MediaIntent
	for rows.Next() {
		var in app.MediaIntent
		if err := rows.Scan(&in.StorageKey, &in.WorkspaceID, &in.ChatMessageID, &in.StorageURL,
			&in.InstallationID, &in.State, &in.Attempt, &in.TombstonePass, &in.NextAttemptAt, &in.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (r *Repo) ClaimMediaIntent(ctx context.Context, storageKey, leaseToken string, expiresAt time.Time) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE channel_media_pending_object
SET state='deleting', lease_token=$2, lease_expires_at=$3, attempt=attempt+1
WHERE storage_key=$1
  AND (state='pending'
       OR (state IN ('deleting','tombstoned') AND lease_expires_at IS NOT NULL AND lease_expires_at < now())
       OR (state='tombstoned' AND next_attempt_at <= now()))`,
		storageKey, leaseToken, expiresAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) SettleMediaIntent(ctx context.Context, storageKey, leaseToken string) error {
	_, err := r.q(ctx).Exec(ctx, `
DELETE FROM channel_media_pending_object WHERE storage_key=$1 AND lease_token=$2`,
		storageKey, leaseToken)
	return err
}

func (r *Repo) MarkMediaTombstoned(ctx context.Context, storageKey, leaseToken string, nextPass int, nextAttemptAt time.Time) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE channel_media_pending_object
SET state='tombstoned', tombstone_pass=$3, next_attempt_at=$4, last_error=NULL,
    lease_token=NULL, lease_expires_at=NULL
WHERE storage_key=$1 AND lease_token=$2`, storageKey, leaseToken, nextPass, nextAttemptAt)
	return err
}

func (r *Repo) ReleaseMediaIntent(ctx context.Context, storageKey, leaseToken string, attempt int, nextAttemptAt time.Time, lastErr string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE channel_media_pending_object
SET state='pending', attempt=$3, next_attempt_at=$4, last_error=nullif($5,''), lease_token=NULL, lease_expires_at=NULL
WHERE storage_key=$1 AND lease_token=$2`, storageKey, leaseToken, attempt, nextAttemptAt, lastErr)
	return err
}

func (r *Repo) InsertTaskDelivery(ctx context.Context, d app.TaskDelivery) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO channel_task_delivery
  (task_id, binding_id, channel_installation_id, kind, external_chat_id, chat_type,
   external_message_id, external_thread_id, route_revision, context_revision, media_pending)
VALUES ($1,$2,$3,$4,$5,$6,nullif($7,''),nullif($8,''),$9,$10,$11)
ON CONFLICT (task_id) DO NOTHING`,
		d.TaskID, d.BindingID, d.InstallationID, string(d.Kind), d.ExternalChatID, string(d.ChatType),
		d.ExternalMessageID, d.ExternalThreadID, d.RouteRevision, d.ContextRevision, d.MediaPending)
	return err
}

func (r *Repo) GetTaskDelivery(ctx context.Context, taskID string) (app.TaskDelivery, bool, error) {
	var d app.TaskDelivery
	var kind, chatType string
	err := r.q(ctx).QueryRow(ctx, `
SELECT task_id::text, binding_id::text, channel_installation_id::text, kind, external_chat_id, chat_type,
 coalesce(external_message_id,''), coalesce(external_thread_id,''), route_revision, context_revision, media_pending
FROM channel_task_delivery WHERE task_id=$1`, taskID).
		Scan(&d.TaskID, &d.BindingID, &d.InstallationID, &kind, &d.ExternalChatID, &chatType,
			&d.ExternalMessageID, &d.ExternalThreadID, &d.RouteRevision, &d.ContextRevision, &d.MediaPending)
	if noRow(err) {
		return app.TaskDelivery{}, false, nil
	}
	d.Kind, d.ChatType = wire.Kind(kind), wire.ChatType(chatType)
	return d, err == nil, err
}

func (r *Repo) RecordOutboundMessage(ctx context.Context, rec app.OutboundRecord) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO channel_outbound_message
  (channel_installation_id, kind, external_message_id, binding_id, route_revision, task_id, outbound_kind)
VALUES ($1,$2,$3,$4,$5,nullif($6,'')::uuid,$7)`,
		rec.InstallationID, string(rec.Kind), rec.ExternalMessageID, rec.BindingID,
		rec.RouteRevision, rec.TaskID, rec.OutboundKind)
	return err
}

func (r *Repo) SettleReplyDelivery(ctx context.Context, turnID, taskID, bindingID, installationID string, kind wire.Kind, externalChatID, externalMessageID, reason string) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO channel_reply_delivery
  (turn_id, task_id, binding_id, channel_installation_id, kind, external_chat_id,
   phase, send_state, external_message_id, settled_reason)
VALUES ($1,$2,$3,$4,$5,$6,'settled','known',$7,$8)
ON CONFLICT (turn_id) DO UPDATE SET
  phase='settled', send_state='known', external_message_id=EXCLUDED.external_message_id,
  settled_reason=EXCLUDED.settled_reason, updated_at=now()
WHERE channel_reply_delivery.phase <> 'settled'`,
		turnID, taskID, bindingID, installationID, string(kind), externalChatID, externalMessageID, reason)
	return err
}

func (r *Repo) BeginReplyStreaming(ctx context.Context, turnID, taskID, bindingID, installationID string, kind wire.Kind, externalChatID string) (bool, error) {
	var opened bool
	err := r.q(ctx).QueryRow(ctx, `
INSERT INTO channel_reply_delivery
  (turn_id, task_id, binding_id, channel_installation_id, kind, external_chat_id, phase, send_state)
VALUES ($1,$2,$3,$4,$5,$6,'streaming','none')
ON CONFLICT (turn_id) DO UPDATE SET task_id=EXCLUDED.task_id, updated_at=now()
WHERE channel_reply_delivery.phase='streaming'
RETURNING true`,
		turnID, taskID, bindingID, installationID, string(kind), externalChatID).Scan(&opened)
	if noRow(err) {
		return false, nil
	}
	return err == nil, err
}

func (r *Repo) RecordReplyPlaceholder(ctx context.Context, turnID, externalMessageID string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE channel_reply_delivery
SET send_state='known', external_message_id=$2, updated_at=now()
WHERE turn_id=$1 AND phase='streaming'`, turnID, externalMessageID)
	return err
}

func (r *Repo) MarkReplyTerminal(ctx context.Context, turnID, externalMessageID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE channel_reply_delivery
SET phase='terminal', send_state='known', external_message_id=$2,
    chunks_sent=chunks_sent+1, owner_token=NULL, owner_expires_at=NULL, updated_at=now()
WHERE turn_id=$1 AND phase IN ('streaming','terminal')`, turnID, externalMessageID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) InsertCardMessage(ctx context.Context, rec app.CardMessage) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO channel_outbound_card_message
  (chat_session_id, task_id, kind, external_chat_id, external_card_message_id, status)
VALUES ($1,nullif($2,'')::uuid,$3,$4,$5,$6)
ON CONFLICT DO NOTHING`,
		rec.ChatSessionID, rec.TaskID, string(rec.Kind), rec.ExternalChatID,
		rec.ExternalCardMessageID, rec.Status)
	return err
}

func (r *Repo) FinishCardMessage(ctx context.Context, taskID string) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE channel_outbound_card_message
SET status='final', last_patched_at=now()
WHERE task_id=$1 AND status IN ('pending','streaming','final')`, taskID)
	return err
}

func (r *Repo) UpdateRouteTitle(ctx context.Context, installationID, externalChatID, title, source string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE channel_chat_session_binding
SET title=$3, title_source=$4, title_updated_at=now(), updated_at=now()
WHERE channel_installation_id=$1 AND external_chat_id=$2
  AND retired_at IS NULL AND coalesce(title_source,'') <> 'explicit'`,
		installationID, externalChatID, title, source)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
