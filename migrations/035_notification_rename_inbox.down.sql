ALTER TABLE notification RENAME TO inbox_item;
ALTER INDEX uq_notification_dedup RENAME TO uq_inbox_dedup;
ALTER INDEX idx_notification_recipient RENAME TO idx_inbox_recipient;
