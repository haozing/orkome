UPDATE autopilot SET exec_mode = 'run_only' WHERE exec_mode = 'task_only';
ALTER TABLE autopilot DROP CONSTRAINT autopilot_exec_mode_check;
ALTER TABLE autopilot ADD CONSTRAINT autopilot_exec_mode_check
  CHECK (exec_mode IN ('create_work_item','run_only'));

DELETE FROM work_item_subscriber WHERE source = 'autopilot_template';
ALTER TABLE work_item_subscriber DROP CONSTRAINT work_item_subscriber_level_check;
ALTER TABLE work_item_subscriber DROP COLUMN level;
ALTER TABLE work_item_subscriber DROP CONSTRAINT work_item_subscriber_source_check;
ALTER TABLE work_item_subscriber ADD CONSTRAINT work_item_subscriber_source_check
  CHECK (source IN ('creator','assignee','commenter','mentionee','autopilot','delegation'));
