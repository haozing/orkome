DROP TABLE IF EXISTS agent_invocation_target;
CREATE TABLE IF NOT EXISTS agent_invocation_target (
  agent_id    uuid NOT NULL REFERENCES agent(id),
  target_type text NOT NULL CHECK (target_type IN ('workspace','member')),
  target_id   text NOT NULL DEFAULT '',
  PRIMARY KEY (agent_id, target_type, target_id)
);
