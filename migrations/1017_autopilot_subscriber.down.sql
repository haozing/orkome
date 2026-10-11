DROP TABLE IF EXISTS autopilot_subscriber;
DROP TABLE IF EXISTS system_wakeup_setting;
DROP INDEX IF EXISTS uniq_wakeup_system_children_done;
DROP INDEX IF EXISTS idx_wakeup_event_types;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='work_item_wakeup' AND column_name='condition_state' AND data_type='text') THEN
    ALTER TABLE work_item_wakeup ALTER COLUMN condition_state TYPE boolean
      USING CASE WHEN condition_state IS NULL THEN NULL WHEN condition_state = 'met' THEN true ELSE false END;
  END IF;
END $$;

DELETE FROM work_item_wakeup WHERE system_rule IS NOT NULL;
ALTER TABLE work_item_wakeup ALTER COLUMN agent_id SET NOT NULL;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS customized_at;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS disabled_at;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS mode;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS last_error;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS on_timeout;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS timed_out_at;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS paused_reason;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS expires_at;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS parent_comment_id;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS source_task_id;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS system_rule;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS filter_actor_id;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS filter_actor_type;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS filter_task_id;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS filter_agent_id;
ALTER TABLE work_item_wakeup DROP COLUMN IF EXISTS event_types;
