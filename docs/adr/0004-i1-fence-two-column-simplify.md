# ADR 0004 · I1 fence index simplified to two columns: no comment_thread_id dimension

Status: implemented (tightened version, deviating from the originally specified three-column
expression index)

## Background

I1 (invariant): at most one unstarted task (queued/dispatched; deferred only
as a channel-media-pending placeholder) per (work item, agent). The originally prescribed
implementation is an **expression unique index including the thread dimension**:

```sql
CREATE UNIQUE INDEX ... ON agent_task_queue (work_item_id, agent_id,
  COALESCE(comment_thread_id, '00000000-...-0000'::uuid))
WHERE status IN ('queued','dispatched')
  OR (status = 'deferred' AND context->>'channel_work_item_media_pending' = 'true');
```

The COALESCE zero-UUID lets "no thread" tasks coexist with each thread's slot—**different comment
threads can each queue** (the comment thread is part of the trigger domain—"thread-scoped dedup").

Implementation (`migrations/007_orchestration.up.sql`, M3): the `agent_task_queue` table was built
without a `comment_thread_id` column (zero hits across all migrations); the fence index is the
two-column partial index `uq_one_pending_task_per_work_item_agent ON (work_item_id, agent_id)` with
the same predicate (deferred media-pending placeholder kept; the context key is
`channel_task_media_pending`).

## Decision

Accept the two-column simplification: **one global** pending slot per (work item, agent), no thread
dimension. The motivation at the time: at M3 the thread column was not yet in the data model (the
trigger-comment chain columns only stabilized in a later milestone), so the stricter fence guaranteed
I1 semantics first.

**Behavioral impact**: the fence is **tightened, not loosened**—triggers of the same work item and
agent in different comment threads **cannot queue in parallel**: when a new-thread comment triggers
while a pending task exists, the trigger protocol takes the confluence
path first (merge into the pending task); if the insert still races into the unique index
(`domain.ErrFenceHit`, SQLSTATE 23505), the protocol falls back to the merge retry—the new comment
merges into the existing pending task (coalesced) instead of opening a new slot. The original
intent was "different threads queue separately, only the same thread merges"; the current state
merges unconditionally. The UX difference: cross-thread follow-ups queue behind the previous round
rather than running in parallel; correctness stays safe, just less parallelism than the
original spec allowed.

## Consequences

- **Positive**: one less column and no backfill at implementation time; the confluence path carries
  all concurrent triggers with predictable behavior; the test face (double-send yielding exactly one
  task) covers it naturally.
- **Negative**: cross-thread parallelism missing; the "COALESCE zero-UUID coexistence semantics"
  unfulfilled.

Related: `migrations/007_orchestration.up.sql`;
`internal/orchestration/adapters/pgrepo/repo.go`.
