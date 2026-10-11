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

-- orkome notification projection columns on the framework notification table
-- (workspace-scoped inbox; agent recipients; work-item linkage with cascade).
ALTER TABLE notification
  ADD COLUMN IF NOT EXISTS workspace_id uuid NOT NULL REFERENCES workspace(id),
  ADD COLUMN IF NOT EXISTS recipient_user_id uuid REFERENCES "user"(id),
  ADD COLUMN IF NOT EXISTS recipient_agent_id uuid REFERENCES agent(id),
  ADD COLUMN IF NOT EXISTS work_item_id uuid REFERENCES work_item(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_notification_recipient
  ON notification (recipient_user_id, created_at DESC);
