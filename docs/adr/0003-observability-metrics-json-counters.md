# ADR 0003 · Observability metrics: in-memory counters + JSON /metrics, deferring OTel+promhttp

Status: implemented; **2026-10-02 migration executed—the original decision (in-memory counters +
JSON /metrics) was superseded by OTel+promhttp (see the migration-execution section at the end)**

## Background

The original observability spec required the OpenTelemetry SDK + Prometheus exporter: `promhttp`
serving /metrics, the metric catalog dimensioned by reason (task status-transition counts, claim
wait, sweep hits, event fast-path/replay, outbox backlog, WS connections/evictions, channel leases,
job status, engine parking depth, etc.), warning about the "Exporter creation self-registers into
the default registry" pitfall. The tracing side requires W3C traceparent propagation across layers
(the global propagator must be Set, or links silently break).

Implementation state at the time (`internal/platform/metrics/metrics.go`, ~60 lines): a `Registry`
holding a name → `atomic.Int64` counter map, exporting `Counter(name)` / `Incr(name)` /
`Snapshot()` / `Handler()` (a JSON endpoint); `cmd/orkome-server/main.go` mounts `/metrics`, and the
Dispatcher injects counting hooks via `RunWithMetrics`. The package comment honestly declared
"in-memory atomic counters + JSON /metrics (Prometheus can be added later)".

## Decision

Single-process in-memory counters + JSON /metrics, deferring the OTel SDK and promhttp. Rationale:
the deployment shape at the time was single-process with no Prometheus scraping environment; the
main metric consumers were e2e assertions and manual debugging (a JSON snapshot suffices); zero
dependencies, zero registration pitfalls, lock-free read/write paths (atomic).

**Known limitations (honest list, pre-migration state)**: counters only, no histogram/gauge; no
label dimensions (reason dimensions via name concatenation, e.g.
`task_transition_failed_runtime_recovery`); resets on process restart (no persistence); no OTel
ecosystem integration (trace correlation, standard exporters); W3C traceparent propagation was also
unimplemented in that batch.

## Consequences

- **Positive**: `/metrics` usable from day one and assertable in e2e; when the backend changes
  later, callers only see the two faces `Counter/Incr`.

Related: `internal/platform/metrics/metrics.go`;
`cmd/orkome-server/main.go` (/metrics mount).

---

## 2026-10-02 migration execution (OTel + promhttp landed, ❌(C)→✅)

Executed 2026-10-02; `internal/platform/metrics` refactored:

**Selection**: `go.opentelemetry.io/otel` v1.46.0 (stable) + `otel/sdk` v1.46.0 +
`exporters/prometheus` v0.68.0 (same release line as the SDK; upgrades must be paired) +
`prometheus/client_golang` v1.24.1 (direct dep—metrics.go imports prometheus/promhttp); all
vendor-locked. Design choice: go through the otel meter → exporter pipeline (rather than
client_golang direct), preserving the path to OTLP/other backends later.

**Facade shape**: `Registry` keeps the `New/Counter/Incr/Snapshot`
signatures (zero consumer changes) and adds `Add/IncrAttrs/Observe/Gauge`; internally dual-track—an
OTel instrument (the real pipeline) + an atomic mirror (the `Snapshot` assertion face, except
histograms). `Counter(name) *atomic.Int64` remains as a thin handle feeding only the mirror (zero
external consumers in-repo). **Explicit `prometheus.NewRegistry()` per instance**
(`otelprom.WithRegisterer`), avoiding the duplicate-registration panic (Exporter/promauto
self-registers into the global registry by default; `Registry` is multi-instance constructible—
tests build their own). `Handler()` = `promhttp.HandlerFor(promReg)` with go/process
collectors; the exporter configured `WithoutScopeInfo()` (otel_scope_* labels are pure noise with a
single scope).

**Metric catalog mapping** (spec item by item → landing point; OTel counters get
`_total` on the prom side):

| Spec item | Metric | Instrumentation |
|---|---|---|
| Task status-transition counts (by reason) | `task_<status>`, `task_failed_reason_<reason>` (flat names, reason via name suffix) | dispatcher.RunWithMetrics (facade gets it free) |
| Claim wait | `task_claim_wait_seconds` histogram (custom second-scale buckets covering OTel's default ms buckets) | dispatcher.Run claim point |
| Sweep hits | `sweep_hits{kind=runtime_recovery/interrupt_timeout/deferred_promotion/checkpoint_janitor/delegated_recovery}` | workers/sweeper.go (interrupt_timeout = 24h timeouts, checkpoint_janitor = reclamation volume) |
| Event fast-path/replay counts | `event_emitted{event}` + `outbox_replayed`/`outbox_replay_attempts` | eventbus.Emit + outboxx.replayOnce |
| outbox backlog depth | `outbox_backlog` gauge (COUNT of unpublished rows, once per round; degradation trade-off below) | outboxx.observeBacklog |
| WS connections/evictions | `ws_connections` gauge + `ws_evictions` | wsx hub (drop idempotent CAS guard) |
| Channel lease status | `channel_leases_held` gauge (held by this replica; sum across replicas to aggregate) | channels Supervisor.sweep/cancelAll |
| River job status | Not landed—this repo has no River (background jobs are the homegrown workersReg + /readyz heartbeat); item not applicable |
| Engine: absorbed retry/failover | `engine_model_retry_absorbed{kind=retry/failover}` | engine/modelretry.go report() (one choke point covers run + aux paths; global meter, zero cmd wiring) |
| Engine: Generate-class call volume | `engine_generate_calls{kind=aux}` | engine.Generate |
| Engine: parking depth / session checkpoint stock | Parking depth not landed (needs an extra SQL scan, deferred); reclamation volume covered by `sweep_hits{kind=checkpoint_janitor}` | — |

**Tracing**: inside `Registry.InstallGlobal()`,
`otel.SetTextMapPropagator(propagation.TraceContext{})` (the silent-break pitfall—without the
global propagator Set, links silently break) + `SetMeterProvider` (engine-side global meter
instrumentation feeds this registry). The `webx.Trace` middleware: upstream traceparent extraction
→ hand-rolled server span → traceparent injected into the response; otelhttp not introduced (a
contrib module; the hand-rolled span already satisfies the required propagation
chain). The engine entry `engine.Execute` span (attributes
task.id/workspace.id) at the engine.go run() entry.

**Access token** (`REALTIME_METRICS_TOKEN`, optional): `HandlerWithToken(token)`—
when non-empty, Bearer/query-param validation (constant-time compare), 404 on mismatch; empty = open.

**Leftovers and trade-offs**:
1. The outbox backlog gauge is a full-table COUNT once per round (1s)—row lifecycles are short and
   steady-state near-zero, acceptable; the degradation path at scale = delete the observeBacklog
   call, keep `outbox_replay_attempts` as a rough signal.
2. The engine-side "parking depth" and "session checkpoint stock" gauges are not landed (they need
   an independent SQL scan face; avoid adding query load for observability—reclaim/timeout counters
   already cover the ops-alerting need).
3. River job status not applicable (no River).
4. Composition-root wiring (main.go + the REALTIME_METRICS_TOKEN env registration) belongs to the
   cmd session: InstallGlobal, the Trace middleware in the chain, HandlerWithToken + the /metrics
   mount, and Metrics injection at each consumer (bus/sweeper/wsHub/relay/channels
   supervisor/dispatcher)—all optional injections, nil-safe.
