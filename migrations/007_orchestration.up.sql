CREATE TABLE agent (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id          uuid NOT NULL REFERENCES workspace(id),
  name                  text NOT NULL,
  description           text NOT NULL DEFAULT '',
  avatar_url            text,
  runtime_config        jsonb NOT NULL DEFAULT '{}',
  max_concurrent_tasks  int NOT NULL DEFAULT 6 CHECK (max_concurrent_tasks BETWEEN 1 AND 50),
  visibility            text NOT NULL DEFAULT 'public' CHECK (visibility IN ('private','public','public_to')),
  owner_user_id         uuid REFERENCES "user"(id),
  conversation_starters jsonb NOT NULL DEFAULT '[]',
  archived_at           timestamptz,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);

CREATE TABLE agent_invocation_target (
  agent_id    uuid NOT NULL REFERENCES agent(id),
  target_type text NOT NULL CHECK (target_type IN ('workspace','member')),
  target_id   text NOT NULL DEFAULT '',
  PRIMARY KEY (agent_id, target_type, target_id)
);

CREATE TABLE workspace_mcp_server (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  name         text NOT NULL,
  transport    text NOT NULL DEFAULT 'http',
  url          text NOT NULL,
  credentials  jsonb NOT NULL DEFAULT '{}',
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);

CREATE TABLE agent_mcp_server (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  agent_id    uuid NOT NULL REFERENCES agent(id),
  name        text NOT NULL,
  transport   text NOT NULL DEFAULT 'http',
  url         text NOT NULL,
  credentials jsonb NOT NULL DEFAULT '{}',
  enabled     boolean NOT NULL DEFAULT true,
  workspace_server_id uuid REFERENCES workspace_mcp_server(id),
  UNIQUE (agent_id, name)
);

CREATE TABLE squad (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  name         text NOT NULL,
  instructions text NOT NULL DEFAULT '',
  leader_agent_id uuid REFERENCES agent(id),
  choreography boolean NOT NULL DEFAULT false,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);

CREATE TABLE squad_member (
  squad_id uuid NOT NULL REFERENCES squad(id),
  agent_id uuid NOT NULL REFERENCES agent(id),
  role     text NOT NULL DEFAULT 'member',
  PRIMARY KEY (squad_id, agent_id)
);

CREATE TABLE agent_runtime (
  instance_id text PRIMARY KEY,
  engine      text NOT NULL,
  capabilities text[] NOT NULL DEFAULT '{}',
  started_at  timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agent_task_queue (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id          uuid NOT NULL REFERENCES workspace(id),
  agent_id              uuid NOT NULL REFERENCES agent(id),
  work_item_id          uuid REFERENCES work_item(id),
  chat_session_id       uuid,
  status                text NOT NULL CHECK (status IN ('queued','dispatched','running','completed','failed','cancelled','deferred')),
  priority              int NOT NULL DEFAULT 0,
  attempt               int NOT NULL DEFAULT 1,
  max_attempts          int NOT NULL DEFAULT 2,
  prepare_lease_expires_at timestamptz,
  awaiting_input_since  timestamptz,
  context               jsonb NOT NULL DEFAULT '{}',
  session_ref_in        text,
  session_ref_out       text,
  originator_user_id    uuid REFERENCES "user"(id),
  accountable_user_id   uuid REFERENCES "user"(id),
  originator_source     text NOT NULL DEFAULT 'direct_human',
  trigger_evidence_kind text,
  trigger_evidence_ref_id uuid,
  failure_reason        text,
  error                 text,
  result                text,
  retry_of_task_id      uuid REFERENCES agent_task_queue(id),
  is_leader_task        boolean NOT NULL DEFAULT false,
  squad_id              uuid REFERENCES squad(id),
  created_at            timestamptz NOT NULL DEFAULT now(),
  dispatched_at         timestamptz,
  started_at            timestamptz,
  completed_at          timestamptz
);

CREATE UNIQUE INDEX uq_one_pending_task_per_work_item_agent
  ON agent_task_queue (work_item_id, agent_id)
  WHERE status IN ('queued','dispatched')
     OR (status = 'deferred' AND context->>'channel_task_media_pending' = 'true');

CREATE INDEX idx_atq_claim ON agent_task_queue (agent_id, priority DESC, created_at)
  WHERE status = 'queued';
CREATE INDEX idx_atq_sweep ON agent_task_queue (status, prepare_lease_expires_at);
CREATE INDEX idx_atq_work_item ON agent_task_queue (work_item_id);

CREATE TABLE task_supplement (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id      uuid NOT NULL REFERENCES agent_task_queue(id),
  status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivering','delivered','acked','settled','failed')),
  content      text NOT NULL,
  failure_code text,
  author_user_id uuid REFERENCES "user"(id),
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE task_usage (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    uuid NOT NULL REFERENCES workspace(id),
  task_id         uuid REFERENCES agent_task_queue(id),
  agent_id        uuid REFERENCES agent(id),
  provider        text NOT NULL,
  model           text NOT NULL,
  prompt_tokens   bigint NOT NULL DEFAULT 0,
  completion_tokens bigint NOT NULL DEFAULT 0,
  cost_usd_ticks  bigint NOT NULL DEFAULT 0,
  agent_label     text NOT NULL DEFAULT 'primary',
  model_retries   int NOT NULL DEFAULT 0,
  model_failovers int NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_usage_ws_time ON task_usage (workspace_id, created_at);

CREATE TABLE agent_model_credential (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    uuid NOT NULL REFERENCES workspace(id),
  provider        text NOT NULL,
  encrypted_secret bytea NOT NULL,
  created_by      uuid NOT NULL REFERENCES "user"(id),
  created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agent_task_checkpoint (
  id           text PRIMARY KEY,
  workspace_id uuid NOT NULL,
  agent_id     uuid NOT NULL,
  task_id      uuid,
  scope_kind   text,
  scope_id     uuid,
  state        bytea NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_ckpt_task ON agent_task_checkpoint (task_id) WHERE task_id IS NOT NULL;

CREATE TABLE inbox_item (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id  uuid NOT NULL REFERENCES workspace(id),
  recipient_user_id uuid REFERENCES "user"(id),
  recipient_agent_id uuid REFERENCES agent(id),
  work_item_id  uuid REFERENCES work_item(id),
  type          text NOT NULL,
  title         text NOT NULL,
  body          text NOT NULL DEFAULT '',
  link          text,
  dedup_key     text,
  read_at       timestamptz,
  archived_at   timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_inbox_dedup ON inbox_item (dedup_key) WHERE dedup_key IS NOT NULL;
CREATE INDEX idx_inbox_recipient ON inbox_item (recipient_user_id, created_at DESC);
