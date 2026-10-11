DROP INDEX IF EXISTS idx_atq_liveness;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS runtime_id;
DROP TABLE IF EXISTS task_message;
