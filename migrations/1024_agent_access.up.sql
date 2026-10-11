ALTER TABLE agent ADD COLUMN IF NOT EXISTS conversation_starters jsonb NOT NULL DEFAULT '[]';

DROP TABLE IF EXISTS agent_invocation_target;
CREATE TABLE agent_invocation_target (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  agent_id       uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  target_type    text NOT NULL CHECK (target_type IN ('workspace','member')),
  target_user_id uuid,
  UNIQUE NULLS NOT DISTINCT (agent_id, target_type, target_user_id)
);
ALTER TABLE agent_invocation_target
  ADD CONSTRAINT chk_ait_target_user CHECK (
    (target_type = 'member'   AND target_user_id IS NOT NULL) OR
    (target_type = 'workspace' AND target_user_id IS NULL)
  );
