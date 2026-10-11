CREATE TABLE chat_session (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id  uuid NOT NULL REFERENCES workspace(id),
  agent_id      uuid NOT NULL REFERENCES agent(id),
  creator_id    uuid NOT NULL REFERENCES "user"(id),
  title         text NOT NULL DEFAULT '',
  session_ref   text,
  engine_tag    text,
  status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived','deleted')),
  unread_since  timestamptz,
  last_read_at  timestamptz,
  pinned_at     timestamptz,
  is_agent_intro boolean NOT NULL DEFAULT false,
  project_id    uuid,
  explicitly_created_at timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_chat_session_ws ON chat_session (workspace_id, updated_at DESC);
CREATE INDEX idx_chat_session_agent ON chat_session (agent_id);

CREATE TABLE chat_message (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id  uuid NOT NULL REFERENCES chat_session(id),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  role        text NOT NULL CHECK (role IN ('user','assistant')),
  kind        text NOT NULL DEFAULT 'message' CHECK (kind IN ('message','no_response','system_stopped','channel_command','onboarding')),
  content     text NOT NULL DEFAULT '',
  failure_reason text,
  task_id     uuid,
  call_id     text,
  seq         bigint NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (session_id, call_id)
);
CREATE INDEX idx_chat_msg_session ON chat_message (session_id, seq);

CREATE TABLE chat_draft_restore (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  session_id  uuid NOT NULL REFERENCES chat_session(id),
  user_id     uuid NOT NULL REFERENCES "user"(id),
  content     text NOT NULL,
  finalized   text NOT NULL CHECK (finalized IN ('pending','restored','discarded')),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_draft_session ON chat_draft_restore (session_id) WHERE finalized = 'pending';

CREATE TABLE chat_pinned_agent (
  user_id     uuid NOT NULL REFERENCES "user"(id),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  agent_id    uuid NOT NULL REFERENCES agent(id),
  position    int NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, workspace_id, agent_id)
);

CREATE TABLE quick_action (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  user_id     uuid NOT NULL REFERENCES "user"(id),
  label       text NOT NULL,
  prompt      text NOT NULL,
  is_private  boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now()
);
