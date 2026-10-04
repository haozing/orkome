# platform packages

Zero-business-knowledge foundation: everything under `internal/platform/` may only depend on the
standard library, same-layer platform packages, and `internal/contract` (enforced by
internal/arch platform-purity tests + .golangci.yml depguard).

| Package | One-line responsibility |
|---|---|
| pg | pool construction, `Within` transactional UoW, `Read[T]` replica reads (strong consistency on primary; replica errors trip a 2s breaker with half-open probing) |
| pgmigrate | NNN_name.up/down.sql migration runner (advisory-lock serialized; first-line `-- migrate:no-transaction` for bare execution; up/down/status subcommands; idempotent) |
| redisx | redis client construction (single-node/sentinel/cluster; rediss TLS) |
| wsx | WS hub: rooms, eviction, connections gauge |
| relayx | realtime relay three-state (memory/sharded/mirrored; fail-fast without Redis) |
| eventbus | in-process bus + `Emitter` post-commit emission (I4) |
| outboxx | transactional outbox: SKIP LOCKED relay + partition-ordered redelivery + backlog gauge |
| cronx | cron parsing and next-fire computation |
| webx | HTTP middleware kit: auth cookie sessions, CSRF double binding, CORS, tracing, access log, rate limit |
| storagex | object storage: Local atomic writes + S3 path-style (MinIO-verified) |
| ids | UUIDv4 minting |
| logx | leveled structured logging |
| metrics | OTel + promhttp facade (see ADR 0003 for the migration history) |
| sealx | credential sealing (AES-256-GCM, 32-byte master key) |
| egressx | SSRF gate (see ADR 0002/0007) |
| redactx | secret redaction (poison word lists, NUL stripping, nested map traversal) |
| flagsx | feature flags (YAML static rules + FF_* env overrides) |

- **metrics is homegrown beyond the archive**: OTel SDK + promhttp were not introduced initially
  (JSON /metrics first); the migration completed 2026-10-02—path recorded in
  `docs/adr/0003-observability-metrics-json-counters.md`.
- Naming and dependency discipline follow internal archive 06/07; per-package README comments
  carry the details.
