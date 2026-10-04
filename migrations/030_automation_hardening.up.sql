ALTER TABLE webhook_delivery ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz;

ALTER TABLE autopilot ADD COLUMN IF NOT EXISTS paused_reason text;
