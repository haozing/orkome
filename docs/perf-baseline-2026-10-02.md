# Performance baseline (M9 collection, 2026-10-02)

> M9 DoD: "load-test baseline on record". Numbers aim to be **on record +
> re-runnable**, not extremely precise. The baseline is not a regression gate; rerun commands appear at
> the end of each section. Environment changes (host load, Docker allocation) bring ±30%-class
> fluctuation—paired comparisons (outbox) and same-run relative values matter more than absolutes.

## Environment

| Item | Value |
|---|---|
| Host | Windows 10 22631 x64 (dev machine, not exclusive) |
| PG | Docker `orkome-pg` (localhost:55432, container-volume storage) |
| Redis | Docker `orkome-redis` (localhost:55379; only for relayx unit tests, the baseline does not depend on it) |
| Go | go1.26.2 windows/amd64, vendor mode |
| Test DB | Dedicated `orkome_perf` (DROP+CREATE rebuilt on every rerun, dropped wholesale afterwards) |

Master rerun command (Git Bash, `-p 1` serial to avoid DB collisions):

```bash
TEST_DATABASE_URL="postgres://postgres:dev@localhost:55432/orkome?sslmode=disable" ORKOME_PERF_BASELINE=1 go test ./e2e/ -run "TestPerf_" -count=1 -v -p 1 -timeout 30m
```

Run a single item: `-run "TestPerf_EnqueueToClaim"` (likewise for the others). WS fanout
connection count can be lowered: `PERF_WS_CONNS=100` (default 1000).

## 1. Enqueue-to-claim latency

Method: `TestPerf_EnqueueToClaim`—200 tasks enqueued first (4 agents x 50, each task with its own
work item—zero I1 fence interference), then 4 concurrent claim loops (single-replica dispatcher
semaphore shape) drain the queue; latency = claim return time − enqueue commit time. **Includes
backlog queueing time** (a queue-drain view under a 200-task burst, not single-task cold start);
claim loops have no poll interval—the production dispatcher's `PollEvery=500ms` would add up to one
more polling period of waiting.

| Metric | Value |
|---|---|
| N | 200 |
| Concurrent claimers | 4 |
| P50 | **246–273 ms** (three runs: 272.9 / 252.3 / 245.9) |
| P99 | **364–413 ms** |
| max | 369–419 ms |

Claim throughput ≈ 200 tasks / 0.37–0.42 s ≈ **480–540 claims/sec** (4 concurrent). A single
ClaimNext (SKIP LOCKED + fence subquery + concurrency-count subquery) runs ~7–8 ms/call/lane at
drain steady state.

## 2. Terminal transaction (CompleteRun) row counts and latency

Method: `TestPerf_CompleteRun`—100 running tasks (insert→claim→start untimed), then
`CompleteRun` one by one (CAS terminal UPDATE + checkpoint DELETE + task_usage INSERT in one
transaction).

| Metric | Value |
|---|---|
| N | 100 |
| Mean | **2.9–3.3 ms** |
| P50 | 2.8–3.2 ms |
| P99 | **4.1–5.6 ms** |
| Rows per transaction | **2 writes** (agent_task_queue UPDATE x1 + task_usage INSERT x1; the checkpoint DELETE hits 0 rows—no checkpoint lands for non-parked tasks) |

## 3. outbox overhead comparison

Method: `TestPerf_OutboxOverhead`—`EnqueueAssignment` x200 each in two compared runs: no recorder
(no outbox) vs `SetOutbox(recorder, db.Within)` (business write + event_outbox row in the same
transaction + post-commit MarkPublished). The event bus has zero subscribers (emit cost identical
across runs).

| Run | Latency | Throughput |
|---|---|---|
| No outbox | 567 ms | **352 ops/sec** |
| With outbox | 1037 ms | **193 ops/sec** |
| Delta | **+82.8%/op** (one extra transaction wrapper + outbox INSERT + MarkPublished UPDATE) |
| outbox rows persisted | 200 (exactly one per enqueue) |

Reading: with outbox on, the enqueue path's per-op cost roughly doubles (3ms→5ms class); the
absolute value stays in single-digit milliseconds—the "degrade only when
write amplification exceeds budget" threshold is nowhere near; the terminal transaction (already
single-transaction) is expected to increase less.

## 4. Table-view queries (10k work_items + 20 properties)

Method: `TestPerf_TableView`—10k work_items (20 property definitions + 20-key jsonb properties per
row, status/priority rotation), `ViewService` rows/groups/facets 50 iterations each; filter
`status=todo + assignee=none`.

| Endpoint | P50 | P99 | max |
|---|---|---|---|
| rows (limit 50) | 3.16 ms | **11.3 ms** | 11.3 ms |
| groups (by status) | 2.58 ms | **4.17 ms** | 4.17 ms |
| groups (by assignee) | 2.62 ms | **3.79 ms** | 3.79 ms |
| facets (status+assignee, two dimensions) | 4.27 ms | **5.87 ms** | 5.87 ms |

All P99 < 15 ms (current values without dedicated index tuning; the rows P99 first iteration
includes plan-cache cold start).

## 5. WS broadcast fanout (1000-connection terminal-event arrival P99)

Method: `TestPerf_WSBroadcastFanout`—wsx hub + httptest local server, 1000 clients (first-frame
PAT auth → subscribe workspace scope; dial concurrency capped at 64—Windows listen backlog), a
single `task:completed` broadcast frame, each client reads until the target event_id and records
arrival latency.

| Metric | Value |
|---|---|
| Connections | **1000** (not downgraded) |
| P50 | 1.12 ms |
| P99 | **2.14 ms** |
| max | 2.14 ms |

Full fanout to 1000 connections completes in the ~2 ms class on local loopback (hub synchronous
delivery + per-client send buffers + the write pump landing frames asynchronously).

## Fluctuation and re-test discipline

- Same-machine multiple runs: enqueue→claim P50 varies ±6%, terminal P99 varies ±30% (Windows dev
  machine jitter);
- Comparative conclusions (the outbox delta, view relative values) should use **same-run** numbers;
- Baseline tests are skipped by default (`ORKOME_PERF_BASELINE!=1`) and stay out of the CI fast
  path.
