CREATE TABLE work_item_status (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  key          text NOT NULL,
  name         text NOT NULL,
  category     text NOT NULL CHECK (category IN ('unstarted','started','done','closed')),
  is_builtin   boolean NOT NULL DEFAULT false,
  position     int NOT NULL DEFAULT 0,
  archived_at  timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, key)
);

INSERT INTO work_item_status (workspace_id, key, name, category, is_builtin, position)
SELECT w.id, s.key, s.name, s.category, true, s.pos
FROM workspace w
CROSS JOIN (VALUES
  ('backlog','Backlog','unstarted',0),
  ('todo','Todo','unstarted',1),
  ('in_progress','In Progress','started',2),
  ('in_review','In Review','started',3),
  ('blocked','Blocked','started',4),
  ('done','Done','done',5),
  ('cancelled','Cancelled','closed',6)
) AS s(key, name, category, pos)
ON CONFLICT (workspace_id, key) DO NOTHING;

CREATE TABLE project (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  name         text NOT NULL,
  description  text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);

CREATE TABLE project_resource (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id   uuid NOT NULL REFERENCES project(id),
  kind         text NOT NULL CHECK (kind IN ('repo','local_dir')),
  uri          text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE work_item (
  id                         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id               uuid NOT NULL REFERENCES workspace(id),
  number                     bigint NOT NULL,
  title                      text NOT NULL,
  description                text NOT NULL DEFAULT '',
  acceptance_criteria        text NOT NULL DEFAULT '',
  status_key                 text NOT NULL,
  priority                   int NOT NULL DEFAULT 0,
  assignee_type              text NOT NULL DEFAULT 'none' CHECK (assignee_type IN ('none','user','agent','squad')),
  assignee_id                uuid,
  creator_type               text NOT NULL DEFAULT 'user' CHECK (creator_type IN ('user','agent','system')),
  creator_id                 uuid NOT NULL,
  parent_work_item_id        uuid REFERENCES work_item(id),
  project_id                 uuid REFERENCES project(id),
  position                   bigint NOT NULL DEFAULT 0,
  due_date                   date,
  start_date                 date,
  metadata                   jsonb NOT NULL DEFAULT '{}',
  properties                 jsonb NOT NULL DEFAULT '{}',
  stage                      text,
  origin_type                text,
  origin_id                  uuid,
  revision                   bigint NOT NULL DEFAULT 1,
  triage_state               text,
  duplicate_of_work_item_id  uuid REFERENCES work_item(id),
  first_executed_at          timestamptz,
  created_at                 timestamptz NOT NULL DEFAULT now(),
  updated_at                 timestamptz NOT NULL DEFAULT now(),
  last_activity_at           timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, number)
);

CREATE INDEX idx_wi_ws_status   ON work_item (workspace_id, status_key);
CREATE INDEX idx_wi_ws_assignee ON work_item (workspace_id, assignee_type, assignee_id);
CREATE INDEX idx_wi_ws_updated  ON work_item (workspace_id, updated_at DESC);
CREATE INDEX idx_wi_parent      ON work_item (parent_work_item_id);
CREATE INDEX idx_wi_project     ON work_item (project_id);
CREATE INDEX idx_wi_title_trgm  ON work_item USING gin (title gin_trgm_ops);
CREATE INDEX idx_wi_desc_trgm   ON work_item USING gin (description gin_trgm_ops);

CREATE TABLE work_item_property_def (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  key          text NOT NULL,
  name         text NOT NULL,
  type         text NOT NULL CHECK (type IN ('text','url','number','checkbox','date','select','multi_select','actor','multi_actor')),
  options      jsonb NOT NULL DEFAULT '[]',
  archived_at  timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, key)
);

CREATE TABLE label (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  name         text NOT NULL,
  color        text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);

CREATE TABLE work_item_to_label (
  work_item_id uuid NOT NULL REFERENCES work_item(id),
  label_id     uuid NOT NULL REFERENCES label(id),
  PRIMARY KEY (work_item_id, label_id)
);

CREATE TABLE work_item_view (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  user_id      uuid NOT NULL REFERENCES "user"(id),
  name         text NOT NULL,
  filters      jsonb NOT NULL DEFAULT '{}',
  group_by     text,
  sort         jsonb NOT NULL DEFAULT '[]',
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE work_item_view_preference (
  user_id      uuid NOT NULL REFERENCES "user"(id),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  default_view_id uuid REFERENCES work_item_view(id),
  PRIMARY KEY (user_id, workspace_id)
);

CREATE TABLE work_item_subscriber (
  work_item_id  uuid NOT NULL REFERENCES work_item(id),
  user_id       uuid NOT NULL REFERENCES "user"(id),
  source        text NOT NULL CHECK (source IN ('creator','assignee','commenter','mentionee','autopilot','delegation')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (work_item_id, user_id)
);

CREATE TABLE work_item_dependency (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  work_item_id      uuid NOT NULL REFERENCES work_item(id),
  depends_on_id     uuid NOT NULL REFERENCES work_item(id),
  workspace_id      uuid NOT NULL REFERENCES workspace(id),
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (work_item_id, depends_on_id),
  CHECK (work_item_id <> depends_on_id)
);

CREATE TABLE work_item_child_event (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  parent_id         uuid NOT NULL REFERENCES work_item(id),
  child_id          uuid NOT NULL REFERENCES work_item(id),
  workspace_id      uuid NOT NULL REFERENCES workspace(id),
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (parent_id, child_id)
);

CREATE TABLE work_item_reaction (
  work_item_id uuid NOT NULL REFERENCES work_item(id),
  user_id      uuid NOT NULL REFERENCES "user"(id),
  emoji        text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (work_item_id, user_id, emoji)
);

CREATE TABLE work_item_source_context (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  source_digest text NOT NULL,
  snapshot     jsonb NOT NULL,
  created_by   uuid NOT NULL REFERENCES "user"(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, source_digest)
);
