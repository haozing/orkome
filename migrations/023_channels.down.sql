DROP TABLE IF EXISTS channel_chat_context_generation;
DROP TABLE IF EXISTS channel_media_pending_object;
DROP TABLE IF EXISTS channel_task_delivery;
DROP TABLE IF EXISTS channel_reply_delivery;
DROP TABLE IF EXISTS channel_outbound_card_message;
DROP TABLE IF EXISTS channel_outbound_message;
DROP TABLE IF EXISTS channel_inbound_audit;
DROP TABLE IF EXISTS channel_binding_token;
DROP TABLE IF EXISTS channel_user_binding;

ALTER TABLE channel_chat_session_binding
  DROP COLUMN IF EXISTS chat_type,
  DROP COLUMN IF EXISTS last_message_id,
  DROP COLUMN IF EXISTS last_thread_id,
  DROP COLUMN IF EXISTS context_revision,
  DROP COLUMN IF EXISTS retired_at,
  DROP COLUMN IF EXISTS updated_at,
  DROP COLUMN IF EXISTS id;
DROP INDEX IF EXISTS uq_channel_chat_session_binding_id;

ALTER TABLE channel_inbound_message_dedup
  DROP COLUMN IF EXISTS claim_token,
  DROP COLUMN IF EXISTS processed_at,
  DROP COLUMN IF EXISTS channel_installation_id;
DROP INDEX IF EXISTS idx_channel_inbound_dedup_created;

ALTER TABLE channel_installation
  DROP COLUMN IF EXISTS agent_id,
  DROP COLUMN IF EXISTS installer_user_id,
  DROP COLUMN IF EXISTS credential_fingerprint,
  DROP COLUMN IF EXISTS updated_at;
DROP INDEX IF EXISTS uq_channel_installation_routing;
DROP INDEX IF EXISTS idx_channel_installation_agent;
