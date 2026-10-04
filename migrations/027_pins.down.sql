DROP TABLE IF EXISTS pinned_item;

CREATE TABLE pinned_item (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    uuid NOT NULL REFERENCES "user"(id),
  kind       text NOT NULL,
  ref_id     uuid NOT NULL,
  position   int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, kind, ref_id)
);
