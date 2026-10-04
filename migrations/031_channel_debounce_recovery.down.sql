DROP INDEX IF EXISTS idx_channel_context_generation_armed;
ALTER TABLE channel_chat_context_generation
  DROP COLUMN IF EXISTS run_armed_at,
  DROP COLUMN IF EXISTS run_armed_token,
  DROP COLUMN IF EXISTS run_armed_payload;
