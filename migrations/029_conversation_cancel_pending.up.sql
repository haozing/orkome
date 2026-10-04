CREATE TABLE chat_cancel_pending (
  task_id            uuid PRIMARY KEY,
  workspace_id       uuid NOT NULL REFERENCES workspace(id),
  session_id         uuid NOT NULL REFERENCES chat_session(id),
  initiator_user_id  uuid NOT NULL REFERENCES "user"(id),
  created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_chat_cancel_pending_session ON chat_cancel_pending (session_id);
