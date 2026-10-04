ALTER TABLE inbox_item RENAME TO notification;
ALTER INDEX uq_inbox_dedup RENAME TO uq_notification_dedup;
ALTER INDEX idx_inbox_recipient RENAME TO idx_notification_recipient;
