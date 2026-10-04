ALTER TABLE work_item_subscriber DROP CONSTRAINT IF EXISTS work_item_subscriber_source_check;
ALTER TABLE work_item_subscriber ADD CONSTRAINT work_item_subscriber_source_check
  CHECK (source IN ('creator','assignee','commenter','mentionee','autopilot','autopilot_template','delegation','manual'));

ALTER TABLE work_item_status ADD COLUMN IF NOT EXISTS icon text NOT NULL DEFAULT '';

ALTER TABLE work_item_property_def ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
