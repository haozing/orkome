CREATE TABLE comment (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  work_item_id        uuid NOT NULL REFERENCES work_item(id),
  workspace_id        uuid NOT NULL REFERENCES workspace(id),
  author_type         text NOT NULL CHECK (author_type IN ('user','agent','system')),
  author_id           uuid,
  content             text NOT NULL,
  type                text NOT NULL DEFAULT 'user' CHECK (type IN ('user','agent','system')),
  parent_id           uuid REFERENCES comment(id),
  revision            bigint NOT NULL DEFAULT 1,
  resolved_at         timestamptz,
  resolved_by_type    text,
  resolved_by_id      uuid,
  source_task_id      uuid,
  quick_action_id     uuid,
  recovery_settled_at timestamptz,
  deleted_at          timestamptz,
  suppressed_agent_ids uuid[] NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_comment_item_time ON comment (work_item_id, created_at);
CREATE INDEX idx_comment_thread_root ON comment (work_item_id, id) WHERE parent_id IS NULL;
CREATE INDEX idx_comment_parent ON comment (parent_id);

CREATE TABLE comment_reaction (
  comment_id uuid NOT NULL REFERENCES comment(id),
  user_id    uuid NOT NULL REFERENCES "user"(id),
  emoji      text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (comment_id, user_id, emoji)
);

CREATE TABLE activity_log (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id     text NOT NULL,
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  work_item_id uuid REFERENCES work_item(id),
  actor_type   text,
  actor_id     uuid,
  kind         text NOT NULL,
  payload      jsonb NOT NULL DEFAULT '{}',
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_activity_event ON activity_log (event_id);
CREATE INDEX idx_activity_item ON activity_log (work_item_id, created_at DESC);
