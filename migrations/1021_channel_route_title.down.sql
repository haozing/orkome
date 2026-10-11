DROP INDEX IF EXISTS idx_channel_chat_session_binding_route_title;
ALTER TABLE channel_chat_session_binding
  DROP COLUMN IF EXISTS title,
  DROP COLUMN IF EXISTS title_source,
  DROP COLUMN IF EXISTS title_updated_at;
