ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS event_types text[];
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS filter_agent_id uuid;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS filter_task_id uuid;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS filter_actor_type text;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS filter_actor_id uuid;

ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS system_rule text;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS source_task_id uuid;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS parent_comment_id uuid;

ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS expires_at timestamptz;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS paused_reason text;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS timed_out_at timestamptz;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS on_timeout text;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS last_error text;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS mode text;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS disabled_at timestamptz;
ALTER TABLE work_item_wakeup ADD COLUMN IF NOT EXISTS customized_at timestamptz;

ALTER TABLE work_item_wakeup ALTER COLUMN agent_id DROP NOT NULL;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='work_item_wakeup' AND column_name='condition_state' AND data_type='boolean') THEN
    ALTER TABLE work_item_wakeup ALTER COLUMN condition_state TYPE text
      USING CASE WHEN condition_state IS NULL THEN NULL WHEN condition_state THEN 'met' ELSE '' END;
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_wakeup_event_types ON work_item_wakeup USING gin (event_types);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_wakeup_system_children_done
  ON work_item_wakeup (work_item_id) WHERE system_rule = 'children_done';

CREATE TABLE IF NOT EXISTS system_wakeup_setting (
  workspace_id uuid PRIMARY KEY REFERENCES workspace(id),
  enabled      boolean NOT NULL DEFAULT true,
  instruction  text NOT NULL DEFAULT '',
  updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS autopilot_subscriber (
  autopilot_id uuid NOT NULL REFERENCES autopilot(id) ON DELETE CASCADE,
  user_type    text NOT NULL CHECK (user_type IN ('member')),
  user_id      uuid NOT NULL REFERENCES "user"(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (autopilot_id, user_type, user_id)
);

CREATE INDEX IF NOT EXISTS idx_autopilot_subscriber_user ON autopilot_subscriber (user_type, user_id);
