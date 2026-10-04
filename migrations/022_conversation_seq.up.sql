ALTER TABLE chat_session ADD COLUMN msg_seq bigint NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX uq_chat_message_session_seq ON chat_message (session_id, seq);
