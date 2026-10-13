ALTER TABLE work_item_batch_item DROP COLUMN skip_reason;
ALTER TABLE work_item_batch DROP COLUMN originator_agent_id;
ALTER TABLE work_item_batch DROP COLUMN originator_task_id;
ALTER TABLE work_item_batch DROP COLUMN source_materialization;
ALTER TABLE work_item_batch DROP CONSTRAINT work_item_batch_status_check;
ALTER TABLE work_item_batch ADD CONSTRAINT work_item_batch_status_check
  CHECK (status IN ('pending','running','paused','completed','failed','cancelled'));
