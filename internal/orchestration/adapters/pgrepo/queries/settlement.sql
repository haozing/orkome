-- @id: SETTLE-FINALIZE-COMPLETED
-- @func: (*pgrepo.Repo).CompleteRun
-- @anchor: agent_task_queue
-- @foreign: none

UPDATE agent_task_queue SET status='completed', completed_at=now(), result=$2,
  session_ref_out=nullif($3,''), engine_tag=nullif($4,''), summary=nullif($5,'')
WHERE id=$1 AND status='running';

-- @id: SETTLE-FINALIZE-FAILED
-- @func: (*pgrepo.Repo).FailRun
-- @anchor: agent_task_queue
-- @foreign: none

UPDATE agent_task_queue SET status='failed', completed_at=now(), failure_reason=$2, error=$3, summary=$4
WHERE id=$1 AND status='running'
RETURNING id, workspace_id, agent_id, coalesce(work_item_id::text,''), status, priority, attempt, max_attempts,
context, coalesce(originator_user_id::text,''), coalesce(accountable_user_id::text,''), originator_source,
coalesce(trigger_evidence_kind,''), coalesce(session_ref_in,''), is_leader_task, coalesce(squad_id::text,''), coalesce(failure_reason,''), coalesce(error,''), coalesce(result,''), created_at, completed_at, coalesce(summary,'');

-- @id: SETTLE-USAGE-INSERT
-- @func: (*pgrepo.Repo).CompleteRun
-- @anchor: task_usage
-- @foreign: none

INSERT INTO task_usage (workspace_id, task_id, agent_id, provider, model,
  prompt_tokens, completion_tokens, cost_usd_ticks, agent_label, model_retries, model_failovers)
SELECT workspace_id, id, agent_id, $2, $3, $4, $5, $6, $7, $8, $9
FROM agent_task_queue WHERE id=$1;

-- @id: SETTLE-USAGE-INSERT-MEMBER
-- @func: (*pgrepo.Repo).InsertMemberUsage
-- @anchor: task_usage
-- @foreign: none

INSERT INTO task_usage (workspace_id, task_id, agent_id, provider, model,
  prompt_tokens, completion_tokens, cost_usd_ticks, agent_label, model_retries, model_failovers)
SELECT workspace_id, id, $2, $3, $4, $5, $6, $7, $8, $9, $10
FROM agent_task_queue WHERE id=$1;

-- @id: SETTLE-SUPPLEMENT-FAILED
-- @func: (*pgrepo.Repo).SettlePendingSupplements
-- @anchor: task_supplement
-- @foreign: none

UPDATE task_supplement SET status='failed', failure_code='turn_ended'
WHERE task_id=$1 AND status IN ('pending','delivering');

-- @id: SETTLE-SUPPLEMENT-RESET
-- @func: (*pgrepo.Repo).ResetSupplementToPending
-- @anchor: task_supplement
-- @foreign: none

UPDATE task_supplement s SET status='pending', failure_code=NULL
FROM agent_task_queue t
WHERE s.id=$3 AND s.task_id=$2 AND s.task_id=t.id
  AND t.workspace_id=$1 AND s.status='failed';

-- @id: RETRY-CHILD-CREATE
-- @func: (*pgrepo.Repo).FailRun
-- @anchor: agent_task_queue
-- @foreign: none

INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status, priority,
  attempt, max_attempts, context, originator_user_id, accountable_user_id, originator_source, retry_of_task_id, fire_at)
SELECT workspace_id, agent_id, work_item_id, 'queued', priority,
  $3, max_attempts, context || jsonb_build_object('prior_summary', nullif($5,''), 'prior_failure_reason', $6::text), originator_user_id, accountable_user_id, originator_source, $2,
  CASE WHEN $4::int > 0 THEN now() + ($4::int * interval '1 second') ELSE NULL END
FROM agent_task_queue WHERE id=$1
ON CONFLICT DO NOTHING;

-- @id: CANCEL-BY-TRIGGER-COMMENT
-- @func: (*pgrepo.Repo).CancelTasksByTriggerComment
-- @anchor: agent_task_queue
-- @foreign: none

UPDATE agent_task_queue SET status='cancelled', completed_at=now()
WHERE workspace_id=$1 AND context->>'trigger_comment_id'=$2
  AND status IN ('queued','deferred','dispatched')
RETURNING id::text;

-- @id: FENCE-LOCK-STEER
-- @func: (*pgrepo.Repo).TrySteer
-- @anchor: agent_task_queue
-- @foreign: none

SELECT id FROM agent_task_queue
WHERE workspace_id=$1 AND work_item_id=$2::uuid AND agent_id=$3::uuid AND status='running'
ORDER BY created_at DESC LIMIT 1 FOR UPDATE;

-- @id: FENCE-MERGE-COALESCE
-- @func: (*pgrepo.Repo).MergeIntoPending
-- @anchor: agent_task_queue
-- @foreign: none

UPDATE agent_task_queue SET context = jsonb_set(context, '{coalesced_comment_ids}',
  coalesce(context->'coalesced_comment_ids','[]'::jsonb) || to_jsonb($4::text))
WHERE id = (
  SELECT id FROM agent_task_queue
  WHERE workspace_id=$1 AND work_item_id=$2::uuid AND agent_id=$3::uuid
    AND status IN ('queued','dispatched')
  ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED
);

-- @id: FENCE-MERGE-SUPPLEMENT
-- @func: (*pgrepo.Repo).InsertSupplementForPending
-- @anchor: agent_task_queue
-- @foreign: none

UPDATE agent_task_queue SET context = context || jsonb_build_object('supplement', $4::text)
WHERE id = (
  SELECT id FROM agent_task_queue
  WHERE workspace_id=$1 AND work_item_id=$2::uuid AND agent_id=$3::uuid
    AND status IN ('queued','dispatched')
  ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED
);

-- @id: PROMOTE-DEFERRED
-- @func: (*pgrepo.Repo).PromoteDeferred
-- @anchor: agent_task_queue
-- @foreign: none

WITH pick AS (
  SELECT DISTINCT ON (work_item_id, agent_id) id
  FROM agent_task_queue d
  WHERE d.status = 'deferred'
    AND (d.fire_at IS NULL OR d.fire_at <= now())
    AND coalesce(d.context->>'channel_task_media_pending','') <> 'true'
    AND NOT EXISTS (
      SELECT 1 FROM agent_task_queue x
      WHERE x.work_item_id = d.work_item_id AND x.agent_id = d.agent_id
        AND x.id <> d.id
        AND (x.status IN ('queued','dispatched','running')
             OR (x.status = 'deferred' AND x.context->>'channel_task_media_pending' = 'true')))
  ORDER BY d.work_item_id, d.agent_id, d.created_at
)
UPDATE agent_task_queue t SET status='queued'
FROM pick WHERE t.id = pick.id AND t.status='deferred'
RETURNING t.id, t.workspace_id, t.agent_id, coalesce(t.work_item_id::text,''), t.status, t.priority,
t.attempt, t.max_attempts, t.context, coalesce(t.originator_user_id::text,''), coalesce(t.accountable_user_id::text,''),
t.originator_source, coalesce(t.trigger_evidence_kind,''), coalesce(t.session_ref_in,''), t.is_leader_task, coalesce(t.squad_id::text,''), coalesce(t.failure_reason,''), coalesce(t.error,''),
coalesce(t.result,''), t.created_at, t.completed_at, coalesce(t.summary,'');

-- @id: AWAITING-24H-SCAN
-- @func: (*pgrepo.Repo).ListExpiredAwaiting
-- @anchor: agent_task_queue
-- @foreign: none

SELECT id::text FROM agent_task_queue
WHERE status='running' AND awaiting_input_since < now() - ($1::int * interval '1 minute');

-- @id: CHECKPOINT-DELETE-TASK
-- @func: (*pgrepo.Repo).DeleteTaskCheckpoints
-- @anchor: agent_task_checkpoint
-- @foreign: none

DELETE FROM agent_task_checkpoint WHERE id = 'task:' || $1;

-- @id: CHECKPOINT-JANITOR-SESSION
-- @func: (*pgrepo.Repo).DeleteStaleSessionCheckpoints
-- @anchor: agent_task_checkpoint
-- @foreign: chat_session

DELETE FROM agent_task_checkpoint c
WHERE c.id LIKE 'session:%'
  AND c.updated_at < now() - ($1::int * interval '1 hour')
  AND NOT EXISTS (
    SELECT 1 FROM chat_session s WHERE s.session_ref = c.id)
  AND NOT EXISTS (
    SELECT 1 FROM agent_task_queue t WHERE t.session_ref_out = c.id);

-- @id: RECOVERY-DELEGATED-SCAN
-- @func: (*pgrepo.Repo).ListUnsettledDelegatedFailures
-- @anchor: agent_task_queue
-- @foreign: comment

SELECT t.id, t.workspace_id, t.agent_id, coalesce(t.work_item_id::text,''), t.status, t.priority,
t.attempt, t.max_attempts, t.context, coalesce(t.originator_user_id::text,''), coalesce(t.accountable_user_id::text,''),
t.originator_source, coalesce(t.trigger_evidence_kind,''), coalesce(t.session_ref_in,''), t.is_leader_task, coalesce(t.squad_id::text,''), coalesce(t.failure_reason,''), coalesce(t.error,''),
coalesce(t.result,''), t.created_at, t.completed_at, coalesce(t.summary,'') FROM agent_task_queue t
WHERE t.status = 'failed'
  AND EXISTS (
    SELECT 1
    FROM jsonb_array_elements_text(coalesce(t.context->'delivered_comment_ids','[]'::jsonb)) AS dc(cid)
    JOIN comment c ON c.id = dc.cid::uuid
    WHERE c.recovery_settled_at IS NULL)
ORDER BY t.created_at;

-- @id: RECONCILE-UNCOVERED-SCAN
-- @func: (*pgrepo.Repo).ListUncoveredTriggerComments
-- @anchor: agent_task_queue
-- @foreign: comment work_item

WITH RECURSIVE thread_root AS (
  SELECT c.id, c.id AS root_id
  FROM comment c
  WHERE c.parent_id IS NULL
  UNION ALL
  SELECT c.id, tr.root_id
  FROM comment c JOIN thread_root tr ON c.parent_id = tr.id
)
SELECT DISTINCT ON (tc.id)
  tc.workspace_id::text, tc.id::text, tc.work_item_id::text,
  tc.author_type, coalesce(tc.author_id::text,''), tc.content,
  coalesce(pc.author_type,''), coalesce(pc.author_id::text,''),
  coalesce(rc.author_type,''), coalesce(rc.author_id::text,''),
  wi.assignee_type, coalesce(wi.assignee_id::text,'')
FROM comment tc
JOIN agent_task_queue t ON t.context->>'trigger_comment_id' = tc.id::text
JOIN thread_root tr ON tr.id = tc.id
LEFT JOIN comment pc ON pc.id = tc.parent_id
LEFT JOIN comment rc ON rc.id = tr.root_id
JOIN work_item wi ON wi.id = tc.work_item_id
WHERE tc.deleted_at IS NULL
  AND t.status IN ('completed','failed','cancelled')
  AND t.completed_at < now() - interval '10 minutes'
  AND NOT EXISTS (
    SELECT 1 FROM agent_task_queue m
    WHERE m.context->>'trigger_comment_id' = tc.id::text
      AND m.context->>'reconcile_attempt' IS NOT NULL)
  AND NOT EXISTS (
    SELECT 1 FROM comment ac
    JOIN thread_root atr ON atr.id = ac.id
    WHERE atr.root_id = tr.root_id
      AND ac.author_type = 'agent'
      AND ac.deleted_at IS NULL
      AND ac.created_at > tc.created_at)
ORDER BY tc.id, t.completed_at DESC
LIMIT 10;
