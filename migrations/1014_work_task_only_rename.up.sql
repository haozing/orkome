UPDATE autopilot SET exec_mode = 'task_only' WHERE exec_mode = 'run_only';
ALTER TABLE autopilot DROP CONSTRAINT autopilot_exec_mode_check;
ALTER TABLE autopilot ADD CONSTRAINT autopilot_exec_mode_check
  CHECK (exec_mode IN ('create_work_item','task_only'));

ALTER TABLE work_item_subscriber DROP CONSTRAINT work_item_subscriber_source_check;
ALTER TABLE work_item_subscriber ADD CONSTRAINT work_item_subscriber_source_check
  CHECK (source IN ('creator','assignee','commenter','mentionee','autopilot','autopilot_template','delegation'));

ALTER TABLE work_item_subscriber ADD COLUMN level text NOT NULL DEFAULT 'all';
ALTER TABLE work_item_subscriber ADD CONSTRAINT work_item_subscriber_level_check
  CHECK (level IN ('all','delegated'));

UPDATE work_item_subscriber SET level = 'delegated' WHERE source = 'delegation';
