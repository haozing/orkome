DROP TABLE IF EXISTS pinned_item;

CREATE TABLE pinned_item (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  user_id      uuid NOT NULL REFERENCES "user"(id),
  item_type    text NOT NULL CHECK (item_type IN ('work_item','chat_session')),
  item_id      uuid NOT NULL,
  position     int NOT NULL DEFAULT 0,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, user_id, item_type, item_id)
);
CREATE INDEX idx_pinned_item_user_order ON pinned_item (workspace_id, user_id, position);

COMMENT ON TABLE pinned_item IS 'sidebar pins (internal archive 04 §2.8; position = user''s in-workspace MAX+1 increment; reorder is a full 1..n overwrite)';
