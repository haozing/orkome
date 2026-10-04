# ADR 0006 · Multi-replica safety matrix and schedule-plan claiming (ClaimDueTrigger)

- Status: accepted (2026-10-02)
- Background: the TickWorker homegrown execution substrate (ADR 0001 ruled out River) is correct
  with a default single-replica deployment; under multi-replica horizontal scaling the background
  loops differ in cross-instance deduplication. This ADR gives the per-loop safety matrix and fixes
  the single real gap.

## Multi-replica safety matrix (per loop)

| Loop | Dedup mechanism | Status |
|---|---|---|
| Dispatcher claiming | `FOR UPDATE SKIP LOCKED` row-level claiming | ✅ safe by construction |
| LivenessSweeper | death-verdict/sweep are both idempotent CAS (FailStaleRunning/PromoteDeferred fenced); concurrent replicas only re-scan | ✅ idempotent |
| BatchWorker | SKIP LOCKED + 2min lease; batch terminal CAS | ✅ |
| webhook ConsumeOnce | SKIP LOCKED claiming + next_attempt_at due filter | ✅ |
| wakeup dispatch TickOnce | `FireWakeup` revision CAS (lock row → revision compare → enqueue in one transaction) | ✅ |
| outbox relay | SKIP LOCKED + 2min lease + partition ordering | ✅ |
| searchindex/intentsweep/codesweep/archive | SKIP LOCKED batch claiming or idempotent DELETE | ✅ |
| **autopilot CronTickOnce** | ~~none~~ (enumerate → StartRun → unconditional UPDATE advance; two replicas in the same window can **double-create runs**) | ❌ → **fixed by this ADR** |

## Decision: schedule-plan claiming (per-trigger CAS)

`CronTickOnce` switches to claiming: after enumerating due triggers, **first** advance
`next_run_at` via the `ClaimDueTrigger` CAS (`WHERE next_run_at IS NOT DISTINCT FROM $enumerated
snapshot value`); only the **winner** calls `StartRun`; losers yield (no retry; the next due
naturally re-enters enumeration). Late plans (>5min) likewise claim before voiding.

- Anchoring on the enumerated snapshot value rather than just the id: concurrent PATCHes
  re-scheduling a trigger's cron are not swallowed;
- Semantics isomorphic to webhook SKIP LOCKED: row-level ownership, losers have zero side effects;
- Loss semantics: claim winner crashing afterwards → the plan already advanced, run not created =
  that round lost (the same crash window as the old single-replica behavior, no new loss; quota
  rows are guaranteed residue-free by StartRun's own transaction).

## Related rulings (two long-tail entries on file)

1. **dbreader full face**: the read-replica requirement reads
   "`Read[T]` (strong consistency on primary; replica connection-class errors trip the breaker for
   2s once, half-open probe recovery)"—the current `platform/pg.Read` implementation **matches it**.
   The reference implementation additionally has business
   dimensions/generation race guards/Prometheus Recorder superset enhancements, beyond the
   requirement; revisit at M9 load-testing if replicas scale out.
2. **Redis lease fail-closed Ready**: the current lease backend is PG-only (Redis is an interface
   placeholder, `CHANNEL_WS_LEASE_BACKEND` reserved). Preconditions unmet—when the Redis backend
   lands it **must** implement fail-closed Ready semantics (if Ready fails, the Supervisor
   disables; no degraded dual-holding); this entry is the acceptance constraint for that
   implementation.
