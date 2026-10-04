DROP INDEX IF EXISTS uq_chat_message_session_seq;
ALTER TABLE chat_session DROP COLUMN IF EXISTS msg_seq;
