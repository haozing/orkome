ALTER TABLE channel_chat_session_binding
  ADD COLUMN IF NOT EXISTS title text,
  ADD COLUMN IF NOT EXISTS title_source text
    CHECK (title_source IN ('derived', 'llm', 'explicit')),
  ADD COLUMN IF NOT EXISTS title_updated_at timestamptz;

CREATE INDEX IF NOT EXISTS idx_channel_chat_session_binding_route_title
  ON channel_chat_session_binding(channel_installation_id, external_chat_id)
  WHERE title_source IS NOT NULL;
