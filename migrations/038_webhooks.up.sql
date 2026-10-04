CREATE TABLE webhook_subscription (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  url          text NOT NULL,
  events       text[] NOT NULL DEFAULT '{}',
  secret       text NOT NULL,
  description  text NOT NULL DEFAULT '',
  created_by   uuid REFERENCES "user"(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  revoked_at   timestamptz
);
CREATE INDEX idx_webhook_sub_ws ON webhook_subscription (workspace_id) WHERE revoked_at IS NULL;

CREATE TABLE event_delivery (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subscription_id     uuid NOT NULL REFERENCES webhook_subscription(id),
  event_id            text NOT NULL,
  event_type          text NOT NULL,
  payload             jsonb NOT NULL,
  status              text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','succeeded','dead')),
  attempts            int NOT NULL DEFAULT 0,
  next_attempt_at     timestamptz NOT NULL DEFAULT now(),
  last_response_status int,
  last_error          text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  delivered_at        timestamptz,
  UNIQUE (subscription_id, event_id)
);
CREATE INDEX idx_event_delivery_claim ON event_delivery (next_attempt_at)
  WHERE status = 'pending';
CREATE INDEX idx_event_delivery_sub ON event_delivery (subscription_id, created_at DESC);
