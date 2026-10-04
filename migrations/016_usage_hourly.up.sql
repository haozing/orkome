CREATE TABLE task_usage_hourly (
  workspace_id     uuid NOT NULL REFERENCES workspace(id),
  hour_bucket      timestamptz NOT NULL,
  agent_key        text NOT NULL DEFAULT '',
  provider         text NOT NULL DEFAULT '',
  model            text NOT NULL DEFAULT '',
  prompt_tokens    bigint NOT NULL DEFAULT 0,
  completion_tokens bigint NOT NULL DEFAULT 0,
  cost_usd_ticks   bigint NOT NULL DEFAULT 0,
  runs             bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (workspace_id, hour_bucket, agent_key, provider, model)
);
CREATE INDEX idx_usage_hourly_ws_time ON task_usage_hourly (workspace_id, hour_bucket DESC);

CREATE TABLE task_usage_hourly_state (
  id             int PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  last_rolled_at timestamptz NOT NULL DEFAULT '-infinity'
);
INSERT INTO task_usage_hourly_state (id, last_rolled_at) VALUES (1, '-infinity');
