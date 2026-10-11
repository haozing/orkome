ALTER TABLE work_item_status DROP COLUMN IF EXISTS icon;
ALTER TABLE work_item_property_def DROP COLUMN IF EXISTS description;
ALTER TABLE work_item_subscriber DROP CONSTRAINT IF EXISTS work_item_subscriber_source_check;
ALTER TABLE work_item_subscriber ADD CONSTRAINT work_item_subscriber_source_check
  CHECK (source IN ('creator','assignee','commenter','mentionee','autopilot','autopilot_template','delegation'));
