DROP INDEX IF EXISTS uq_notification_preference_mute;

ALTER TABLE notification_preference
  DROP COLUMN IF EXISTS workspace_id,
  DROP COLUMN IF EXISTS muted_types,
  DROP COLUMN IF EXISTS muted_work_item_ids;
