# platform packages（ploykit rebase 后的本地保留集）

Zero-business-knowledge foundation. After the ploykit rebase (Phase 2, 2026-10-10), the bulk of
the platform layer is consumed from the framework module
(`github.com/haozing/ploykit/platform/*`: pg / pgmigrate / redisx / storagex / cronx / egressx /
sealx / redactx / ids / logx / metrics). The packages kept **locally** are the ones the framework
replaced with a different substrate (River events / schedule domain — decision D7) or that are
scheduled to move in a later phase:

| Package | One-line responsibility | Disposition |
|---|---|---|
| eventbus | in-process bus + `Emitter` post-commit emission (I4) | local (D7; framework moved to River `platform/events`) |
| outboxx | transactional outbox: SKIP LOCKED relay + partition-ordered redelivery + backlog gauge | local (D7; owns the product `event_outbox` table) |
| schedx | schedule compensation helper | local (D7; framework moved to the `schedule` domain) |
| flagsx | feature flags (YAML static rules + FF_* env overrides) | local (D7) |
| webx | HTTP middleware kit: JWT cookie auth, CSRF double binding, CORS, OTel tracing, access log, rate limit | local until Phase 3 (framework Authenticate needs framework session tables first) |
| wsx | WS hub: rooms, eviction, connections gauge | local until Phase 5 (swapped with the frontend WsClient, atomically) |
| relayx | realtime relay three-state (memory/sharded/mirrored; fail-fast without Redis) | local until Phase 5 |

Rules: same-layer imports allowed (outboxx→pg, schedx→cronx, relayx→logx now point at the
framework packages); no business imports — enforced by internal/arch platform-purity tests
(extended to allow `github.com/haozing/ploykit/platform/*` imports) + .golangci.yml depguard.

Notable behavior delta accepted with the framework swap (Phase 2): egressx is fail-closed on
unresolvable https hosts (the old local gate passed them best-effort; failures used to surface
at connect time as `connect_failed`, now they are classified as `ssrf_rejected` at the gate).
