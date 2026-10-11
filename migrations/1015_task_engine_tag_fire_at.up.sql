ALTER TABLE agent_task_queue ADD COLUMN engine_tag text;
ALTER TABLE agent_task_queue ADD COLUMN fire_at timestamptz;
CREATE INDEX idx_atq_fire_at ON agent_task_queue (fire_at) WHERE fire_at IS NOT NULL;
