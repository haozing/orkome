ALTER TABLE autopilot DROP COLUMN IF EXISTS paused_reason;
ALTER TABLE webhook_delivery DROP COLUMN IF EXISTS next_attempt_at;
