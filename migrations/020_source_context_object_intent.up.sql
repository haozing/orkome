CREATE TABLE IF NOT EXISTS source_context_object_intent (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id     uuid NOT NULL REFERENCES workspace(id),
  storage_key      text NOT NULL,
  state            text NOT NULL CHECK (state IN ('pending','leased','done','released')) DEFAULT 'pending',
  attempts         int  NOT NULL DEFAULT 0,
  max_attempts     int  NOT NULL DEFAULT 3,
  next_attempt_at  timestamptz NOT NULL DEFAULT now(),
  lease_expires_at timestamptz,
  claimed_by       text,
  last_error       text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_intent_due
  ON source_context_object_intent (next_attempt_at) WHERE state = 'pending';
