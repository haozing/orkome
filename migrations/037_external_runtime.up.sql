ALTER TABLE agent ADD COLUMN runtime_kind text NOT NULL DEFAULT 'engine'
  CHECK (runtime_kind IN ('engine','external'));

CREATE TABLE agent_credential (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id),
  agent_id     uuid NOT NULL REFERENCES agent(id),
  pat_id       uuid NOT NULL REFERENCES personal_access_token(id),
  name         text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now(),
  revoked_at   timestamptz
);
CREATE INDEX idx_agent_credential_pat ON agent_credential (pat_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_agent_credential_agent ON agent_credential (agent_id) WHERE revoked_at IS NULL;
