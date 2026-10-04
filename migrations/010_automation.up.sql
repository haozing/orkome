CREATE TABLE work_item_wakeup (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id   uuid NOT NULL REFERENCES workspace(id),
  work_item_id   uuid NOT NULL REFERENCES work_item(id),
  agent_id       uuid NOT NULL REFERENCES agent(id),
  created_by     uuid REFERENCES "user"(id),
  kind           text NOT NULL CHECK (kind IN ('time','event','condition','system')),
  instruction    text NOT NULL DEFAULT '',
  interval_seconds int,
  cron_expression text,
  timezone       text NOT NULL DEFAULT 'UTC',
  next_fire_at   timestamptz,
  enabled        boolean NOT NULL DEFAULT true,
  revision       bigint NOT NULL DEFAULT 1,
  last_task_id   uuid,
  max_fires      int,
  fire_count     int NOT NULL DEFAULT 0,
  condition      jsonb,
  condition_state boolean,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_wakeup_due ON work_item_wakeup (next_fire_at)
  WHERE enabled AND kind IN ('time','system');
CREATE INDEX idx_wakeup_cond ON work_item_wakeup (work_item_id)
  WHERE enabled AND kind = 'condition';
CREATE INDEX idx_wakeup_event ON work_item_wakeup (work_item_id)
  WHERE enabled AND kind = 'event';

CREATE TABLE work_item_wakeup_receipt (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  wakeup_id   uuid NOT NULL REFERENCES work_item_wakeup(id),
  revision    bigint NOT NULL,
  event_key   text NOT NULL,
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  captured_at timestamptz NOT NULL DEFAULT now(),
  processed_at timestamptz,
  payload     jsonb,
  UNIQUE (wakeup_id, revision, event_key)
);
CREATE INDEX idx_receipt_unprocessed ON work_item_wakeup_receipt (captured_at)
  WHERE processed_at IS NULL;

CREATE TABLE autopilot (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  name         text NOT NULL,
  exec_mode    text NOT NULL CHECK (exec_mode IN ('create_work_item','run_only')),
  template     jsonb NOT NULL DEFAULT '{}',
  target_agent_id uuid REFERENCES agent(id),
  target_squad_id uuid,
  enabled      boolean NOT NULL DEFAULT true,
  daily_limit  int NOT NULL DEFAULT 10,
  last_run_at  timestamptz,
  created_by   uuid REFERENCES "user"(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, name)
);

CREATE TABLE autopilot_trigger (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  autopilot_id    uuid NOT NULL REFERENCES autopilot(id),
  kind            text NOT NULL CHECK (kind IN ('schedule','webhook','manual')),
  cron_expression text,
  timezone        text NOT NULL DEFAULT 'UTC',
  next_run_at     timestamptz,
  webhook_token   text UNIQUE,
  signing_secret  text,
  enabled         boolean NOT NULL DEFAULT true,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_trigger_due ON autopilot_trigger (next_run_at)
  WHERE enabled AND kind = 'schedule';

CREATE TABLE autopilot_run (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  autopilot_id  uuid NOT NULL REFERENCES autopilot(id),
  trigger_id    uuid REFERENCES autopilot_trigger(id),
  source        text NOT NULL,
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed','skipped')),
  work_item_id  uuid REFERENCES work_item(id),
  task_id       uuid,
  planned_at    timestamptz NOT NULL DEFAULT now(),
  triggered_at  timestamptz,
  completed_at  timestamptz,
  failure_reason text,
  reason_code   text,
  webhook_delivery_id uuid
);
CREATE INDEX idx_run_task ON autopilot_run (task_id) WHERE task_id IS NOT NULL;

CREATE TABLE autopilot_quota_period (
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  period_day   date NOT NULL,
  runs         int NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, period_day)
);

CREATE TABLE webhook_delivery (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  trigger_id   uuid NOT NULL REFERENCES autopilot_trigger(id),
  dedupe_key   text NOT NULL UNIQUE,
  provider     text NOT NULL DEFAULT 'generic',
  status       text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','processing','delivered','failed','skipped')),
  attempts     int NOT NULL DEFAULT 0,
  lease_token  text,
  lease_expires_at timestamptz,
  payload      jsonb NOT NULL,
  error        text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  delivered_at timestamptz
);
CREATE INDEX idx_delivery_pending ON webhook_delivery (created_at)
  WHERE status IN ('queued','processing');
