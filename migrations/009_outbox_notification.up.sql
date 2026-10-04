CREATE TABLE event_outbox (
  id            text PRIMARY KEY,
  workspace_id  uuid NOT NULL,
  event_type    text NOT NULL,
  payload       jsonb NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  published_at  timestamptz,

  lease_owner   text,
  lease_expires_at timestamptz,
  attempts      int NOT NULL DEFAULT 0,
  next_retry_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_outbox_pending ON event_outbox (workspace_id, id)
  WHERE published_at IS NULL;
CREATE INDEX idx_outbox_lease ON event_outbox (lease_expires_at)
  WHERE published_at IS NULL;

CREATE TABLE notification_preference (
  user_id       uuid NOT NULL REFERENCES "user"(id),
  workspace_id  uuid NOT NULL REFERENCES workspace(id),
  muted_types   text[] NOT NULL DEFAULT '{}',
  muted_work_item_ids uuid[] NOT NULL DEFAULT '{}',
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, workspace_id)
);
