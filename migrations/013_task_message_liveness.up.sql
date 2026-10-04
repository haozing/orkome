CREATE TABLE task_message (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  task_id      uuid NOT NULL REFERENCES agent_task_queue(id),
  seq          bigint NOT NULL,
  kind         text NOT NULL CHECK (kind IN ('text','tool_use','tool_result','error','approval')),
  call_id      text,
  content      text NOT NULL DEFAULT '',
  truncated    boolean NOT NULL DEFAULT false,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (task_id, seq)
);
CREATE INDEX idx_task_message_task ON task_message (task_id, seq);

ALTER TABLE agent_task_queue ADD COLUMN runtime_id text;

CREATE INDEX idx_atq_liveness ON agent_task_queue (runtime_id) WHERE status = 'running' AND awaiting_input_since IS NULL;
