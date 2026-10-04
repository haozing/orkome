CREATE TABLE work_item_batch (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  work_item_id uuid NOT NULL REFERENCES work_item(id),
  kind text NOT NULL,
  status text NOT NULL CHECK (status IN ('pending','running','paused','completed','failed','cancelled')),
  params jsonb NOT NULL DEFAULT '{}',
  source jsonb NOT NULL,
  total int NOT NULL,
  succeeded int NOT NULL DEFAULT 0,
  failed int NOT NULL DEFAULT 0,
  skipped int NOT NULL DEFAULT 0,
  concurrency int NOT NULL DEFAULT 4,
  chunk_size int NOT NULL DEFAULT 100,
  pace_interval_seconds int NOT NULL DEFAULT 0,
  originator_user_id uuid REFERENCES "user"(id),
  accountable_user_id uuid REFERENCES "user"(id),
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  started_at timestamptz,
  completed_at timestamptz
);
CREATE INDEX idx_batch_work_item ON work_item_batch (work_item_id);
CREATE INDEX idx_batch_pending ON work_item_batch (created_at) WHERE status IN ('pending','running');

CREATE TABLE work_item_batch_item (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id uuid NOT NULL REFERENCES work_item_batch(id),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  seq int NOT NULL,
  payload_ref jsonb NOT NULL,
  status text NOT NULL CHECK (status IN ('pending','claimed','processing','succeeded','failed','skipped')),
  attempts int NOT NULL DEFAULT 0,
  max_attempts int NOT NULL DEFAULT 3,
  lease_expires_at timestamptz,
  claimed_by text,
  result_ref jsonb,
  error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (batch_id, seq)
);
CREATE INDEX idx_batch_item_pending ON work_item_batch_item (batch_id, seq) WHERE status = 'pending';
CREATE INDEX idx_batch_item_lease ON work_item_batch_item (lease_expires_at)
  WHERE status IN ('claimed','processing');
CREATE INDEX idx_batch_item_retry ON work_item_batch_item (batch_id) WHERE status = 'failed';
