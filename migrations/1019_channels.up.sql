ALTER TABLE channel_installation
  ADD COLUMN IF NOT EXISTS agent_id uuid,
  ADD COLUMN IF NOT EXISTS installer_user_id uuid,
  ADD COLUMN IF NOT EXISTS credential_fingerprint text,
  ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS idx_channel_installation_agent
  ON channel_installation(agent_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_channel_installation_routing
  ON channel_installation(kind, (config->>'routing_key'));

ALTER TABLE channel_inbound_message_dedup
  ADD COLUMN IF NOT EXISTS claim_token uuid NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN IF NOT EXISTS processed_at timestamptz,
  ADD COLUMN IF NOT EXISTS channel_installation_id uuid;
CREATE INDEX IF NOT EXISTS idx_channel_inbound_dedup_created
  ON channel_inbound_message_dedup(created_at);

ALTER TABLE channel_chat_session_binding
  ADD COLUMN IF NOT EXISTS id uuid NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX IF NOT EXISTS uq_channel_chat_session_binding_id
  ON channel_chat_session_binding(id);
ALTER TABLE channel_chat_session_binding
  ADD COLUMN IF NOT EXISTS chat_type text NOT NULL DEFAULT 'p2p'
    CHECK (chat_type IN ('p2p', 'group')),
  ADD COLUMN IF NOT EXISTS last_message_id text,
  ADD COLUMN IF NOT EXISTS last_thread_id text,
  ADD COLUMN IF NOT EXISTS context_revision bigint NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS retired_at timestamptz,
  ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS channel_user_binding (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id      uuid NOT NULL REFERENCES workspace(id),
  member_user_id    uuid NOT NULL REFERENCES "user"(id),
  channel_installation_id uuid NOT NULL REFERENCES channel_installation(id),
  kind              text NOT NULL,
  external_user_id  text NOT NULL,
  config            jsonb NOT NULL DEFAULT '{}',
  bound_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (channel_installation_id, external_user_id)
);
CREATE INDEX IF NOT EXISTS idx_channel_user_binding_user
  ON channel_user_binding(workspace_id, external_user_id);
CREATE INDEX IF NOT EXISTS idx_channel_user_binding_member
  ON channel_user_binding(member_user_id);

CREATE TABLE IF NOT EXISTS channel_binding_token (
  token_hash    text PRIMARY KEY,
  workspace_id  uuid NOT NULL,
  channel_installation_id uuid NOT NULL,
  external_user_id text NOT NULL,
  expires_at    timestamptz NOT NULL,
  consumed_at   timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT channel_binding_token_ttl_cap
    CHECK (expires_at <= created_at + INTERVAL '15 minutes')
);
CREATE INDEX IF NOT EXISTS idx_channel_binding_token_installation
  ON channel_binding_token(channel_installation_id, expires_at);

CREATE TABLE IF NOT EXISTS channel_inbound_audit (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_installation_id uuid,
  kind              text NOT NULL,
  external_chat_id  text,
  event_type        text NOT NULL DEFAULT 'message',
  external_event_id text,
  external_message_id text,
  drop_reason       text NOT NULL,
  received_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_channel_inbound_audit_installation
  ON channel_inbound_audit(channel_installation_id, received_at DESC);
CREATE INDEX IF NOT EXISTS idx_channel_inbound_audit_reason
  ON channel_inbound_audit(drop_reason, received_at DESC);

CREATE TABLE IF NOT EXISTS channel_outbound_message (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_installation_id uuid NOT NULL,
  kind              text NOT NULL,
  external_message_id text NOT NULL,
  binding_id        uuid NOT NULL,
  route_revision    bigint NOT NULL,
  task_id           uuid,
  outbound_kind     text NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_channel_outbound_message_binding
  ON channel_outbound_message(binding_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_channel_outbound_message_task
  ON channel_outbound_message(task_id) WHERE task_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS channel_outbound_card_message (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  chat_session_id         uuid NOT NULL,
  task_id                 uuid,
  kind                    text NOT NULL,
  external_chat_id        text NOT NULL,
  external_card_message_id text NOT NULL,
  status                  text NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'streaming', 'final', 'error')),
  last_patched_at         timestamptz,
  created_at              timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_outbound_card_task
  ON channel_outbound_card_message(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_channel_outbound_card_session
  ON channel_outbound_card_message(chat_session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS channel_reply_delivery (
  turn_id           uuid PRIMARY KEY,
  task_id           uuid NOT NULL,
  binding_id        uuid NOT NULL,
  channel_installation_id uuid NOT NULL,
  kind              text NOT NULL,
  external_chat_id  text NOT NULL,
  phase             text NOT NULL
    CHECK (phase IN ('streaming', 'terminal', 'settled')),
  send_state        text NOT NULL DEFAULT 'none'
    CHECK (send_state IN ('none', 'in_flight', 'known', 'unknown')),
  external_message_id text NOT NULL DEFAULT '',
  chunks_sent       int NOT NULL DEFAULT 0 CHECK (chunks_sent >= 0),
  owner_token       uuid,
  owner_expires_at  timestamptz,
  settled_reason    text NOT NULL DEFAULT '',
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_channel_reply_delivery_installation
  ON channel_reply_delivery(channel_installation_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS channel_task_delivery (
  task_id             uuid PRIMARY KEY,
  binding_id          uuid NOT NULL,
  channel_installation_id uuid NOT NULL,
  kind                text NOT NULL,
  external_chat_id    text NOT NULL,
  chat_type           text NOT NULL,
  external_message_id text,
  external_thread_id  text,
  route_revision      bigint NOT NULL,
  context_revision    bigint NOT NULL DEFAULT 1,
  media_pending       boolean NOT NULL DEFAULT false,
  created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_channel_task_delivery_binding
  ON channel_task_delivery(binding_id, created_at DESC);

CREATE TABLE IF NOT EXISTS channel_media_pending_object (
  storage_key       text PRIMARY KEY,
  workspace_id      uuid NOT NULL,
  chat_message_id   uuid,
  storage_url       text NOT NULL DEFAULT '',
  channel_installation_id uuid,
  state             text NOT NULL DEFAULT 'pending'
    CHECK (state IN ('pending', 'deleting', 'tombstoned')),
  lease_token       uuid,
  lease_expires_at  timestamptz,
  attempt           int NOT NULL DEFAULT 0,
  next_attempt_at   timestamptz NOT NULL DEFAULT now(),
  last_error        text,
  tombstone_pass    int NOT NULL DEFAULT 0,
  created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_channel_media_pending_due
  ON channel_media_pending_object(next_attempt_at) WHERE state <> 'tombstoned';
CREATE INDEX IF NOT EXISTS idx_channel_media_pending_message
  ON channel_media_pending_object(chat_message_id) WHERE chat_message_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS channel_chat_context_generation (
  chat_session_id    uuid NOT NULL,
  revision           bigint NOT NULL,
  history_start_message_id text,
  history_end_message_id   text,
  history_boundary_pending boolean NOT NULL DEFAULT false,
  pending_fresh      boolean NOT NULL DEFAULT false,
  initiator_user_id  uuid,
  created_at         timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (chat_session_id, revision)
);
