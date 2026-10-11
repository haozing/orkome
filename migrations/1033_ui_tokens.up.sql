CREATE TABLE ui_token (
  jti          text PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  user_id      uuid NOT NULL REFERENCES "user"(id),
  plugin       text NOT NULL,
  expires_at   timestamptz NOT NULL,
  consumed_at  timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_ui_token_gc ON ui_token (expires_at);

CREATE TABLE pack_ui_entry (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id  uuid NOT NULL REFERENCES workspace(id),
  pack          text NOT NULL,
  label         text NOT NULL,
  url_template  text NOT NULL,
  installed_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, pack)
);
