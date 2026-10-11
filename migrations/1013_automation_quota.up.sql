CREATE TABLE IF NOT EXISTS autopilot_quota_reservation (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  autopilot_id uuid NOT NULL REFERENCES autopilot(id),
  window_start timestamptz NOT NULL,
  window_end   timestamptz NOT NULL,
  reserved     int NOT NULL DEFAULT 0,
  committed    int NOT NULL DEFAULT 0,
  status       text NOT NULL DEFAULT 'reserved'
    CHECK (status IN ('reserved','committed')),
  reserved_at  timestamptz NOT NULL DEFAULT now(),
  committed_at timestamptz,

  UNIQUE (autopilot_id, window_start)
);
CREATE INDEX IF NOT EXISTS idx_quota_reservation_open
  ON autopilot_quota_reservation (window_end) WHERE status = 'reserved';

ALTER TABLE autopilot_run
  ADD COLUMN IF NOT EXISTS quota_reservation_id uuid REFERENCES autopilot_quota_reservation(id);
