# ADR 0001 · Worker execution substrate is homegrown TickWorker, not River

Status: implemented (replaces the earlier River decision)

## Background

The original internal spec (v2.2 decision) specified the background job execution substrate = River
(riverqueue/river): claiming (SKIP LOCKED), retries, leases and the ledger all handled by River
(river_job table family, rivermigrate applied at deployment); job enqueueing supports the same
transaction as business writes (`client.InsertTx`—rollback makes it atomically vanish); duplicate
enqueues resolved via `UniqueOpts.ByArgs`. It also explicitly required **homegrown schedule
enumeration and due-decision** (PlansForScope)—River periodic scheduling is approximate and ignores
previous scheduler state after restarts, incompatible with autopilot's semantics of "enumerate
(anchor, now] take latest, void when >5min late, unique on (job, scope, plan_time)".

At implementation time (M7)
River was not introduced: no riverqueue dependency in go.mod, no rivermigrate step; the three
automation background jobs (wakeup dispatch / autopilot cron / webhook delivery) run on the generic
`TickWorker` in `internal/automation/adapters/workers/jobs.go`.

## Decision

The background job execution substrate is homegrown: `TickWorker` (four fields Name/Tick/Log/Every +
a ticker loop that stops on ctx cancel, logging errors and continuing to the next round), isomorphic
with orchestration's Dispatcher (`internal/orchestration/adapters/workers/dispatcher.go`, 500ms
polling + SKIP LOCKED claiming). Job semantics do not live in the substrate but in each domain:

- Claiming/idempotency: each Tick function carries its own (webhook delivery = SKIP LOCKED
  consumption in `WebhookService.ConsumeOnce`; wakeup = `WakeupService.TickOnce` scanning; cron =
  `AutopilotService.CronTickOnce` advancing);
- Transactionality: wakeup dispatch goes through `db.Within` wrapping "lock wakeup →
  enqueue/confluence → revision CAS" in one transaction (CAS miss rolls back wholesale;
  `SetUow` in `cmd/orkome-server/wiring_automation.go`); business atomicity is carried by domain
  SQL, not job rows;
- Dedup: no ByArgs; business idempotency keys instead (wakeup confluence, cron late-voiding,
  webhook dedup ledger).

**Costs**: no transactional enqueueing (job intent not atomic with business writes—all current
Ticks are "scan DB state" style with no standalone job rows, so the gap has no live exposure);
no ByArgs silent dedup; no unified job execution ledger (no river_job family; job observability is
only logs and metrics counters).
**Gained**: zero third-party dependency and zero deployment steps; PlansForScope semantics fully
owned by the automation domain (late-voiding/confluence/quota decisions unconstrained by substrate
semantics); the substrate (struct + Run loop) is ~25 lines, readable end to end.

## Consequences

- **Positive**: small dependency surface (the dependency allowlist unexpanded); worker shutdown
  semantics unified as ctx cancel; behavior tests hit the service face directly with no River test
  doubles.
- **Negative**: if a job shape appears later that is "one-shot, with a retry budget, needing to land
  in a queue in the same transaction as business writes" (e.g. a batch-driven long tail),
  leases/retries/ledger must be self-built—at a cost exceeding the adoption of River.

Related: [ADR 0003](./0003-observability-metrics-json-counters.md) (job observability counters).
