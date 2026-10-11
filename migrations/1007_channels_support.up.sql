CREATE TABLE channel_installation (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  kind         text NOT NULL DEFAULT 'fake',
  name         text NOT NULL,
  status       text NOT NULL DEFAULT 'active',
  lease_owner  text,
  lease_expires_at timestamptz,
  config       jsonb NOT NULL DEFAULT '{}',
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, kind, name)
);
CREATE INDEX idx_installation_lease ON channel_installation (lease_expires_at) WHERE status='active';

CREATE TABLE channel_inbound_message_dedup (
  provider       text NOT NULL,
  external_id    text NOT NULL,
  workspace_id   uuid,
  state          text NOT NULL DEFAULT 'seen',
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (provider, external_id)
);

CREATE TABLE channel_chat_session_binding (
  channel_installation_id uuid NOT NULL REFERENCES channel_installation(id),
  external_chat_id  text NOT NULL,
  workspace_id      uuid NOT NULL REFERENCES workspace(id),
  chat_session_id   uuid REFERENCES chat_session(id),
  generation        int NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_installation_id, external_chat_id)
);

CREATE TABLE skill (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  name         text NOT NULL,
  description  text NOT NULL DEFAULT '',
  origin       text NOT NULL DEFAULT 'manual',
  origin_ref   text,
  created_by   uuid REFERENCES "user"(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);

CREATE TABLE skill_file (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  skill_id   uuid NOT NULL REFERENCES skill(id),
  path       text NOT NULL,
  content    text NOT NULL,
  content_sha256 text NOT NULL,
  size_bytes int NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (skill_id, path)
);

CREATE TABLE agent_skill (
  agent_id  uuid NOT NULL REFERENCES agent(id),
  skill_id  uuid NOT NULL REFERENCES skill(id),
  enabled   boolean NOT NULL DEFAULT true,
  PRIMARY KEY (agent_id, skill_id)
);

CREATE TABLE attachment (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  host_kind    text NOT NULL CHECK (host_kind IN ('work_item','comment','chat_session','chat_message','task')),
  host_id      uuid,
  filename     text NOT NULL,
  content_type text NOT NULL DEFAULT 'application/octet-stream',
  size_bytes   bigint NOT NULL DEFAULT 0,
  storage_key  text NOT NULL,
  sha256       text NOT NULL DEFAULT '',
  created_by   uuid REFERENCES "user"(id),
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_attachment_host ON attachment (host_kind, host_id);
