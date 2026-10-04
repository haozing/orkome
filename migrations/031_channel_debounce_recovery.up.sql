ALTER TABLE channel_chat_context_generation
  ADD COLUMN IF NOT EXISTS run_armed_at timestamptz,
  ADD COLUMN IF NOT EXISTS run_armed_token uuid,
  ADD COLUMN IF NOT EXISTS run_armed_payload jsonb;

CREATE INDEX IF NOT EXISTS idx_channel_context_generation_armed
  ON channel_chat_context_generation(run_armed_token) WHERE run_armed_token IS NOT NULL;
