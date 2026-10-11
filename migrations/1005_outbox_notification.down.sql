DROP TABLE event_outbox;
ALTER TABLE notification DROP COLUMN IF EXISTS workspace_id;
ALTER TABLE notification DROP COLUMN IF EXISTS recipient_user_id;
ALTER TABLE notification DROP COLUMN IF EXISTS recipient_agent_id;
ALTER TABLE notification DROP COLUMN IF EXISTS work_item_id;
DROP INDEX IF EXISTS idx_notification_recipient;
