# ADR 0012: design independence statement

Date: 2026-10-04 | Status: accepted

## Statement

Orkome is an **independent design and independent implementation** made after studying existing
open-source collaboration products. Stated honestly: the team studied a reference implementation
(internal material; research artifacts live in the internal archive, not distributed with the
product); this product is **not clean-room**; however, the following structural decisions differ
substantially from upstream, each traceable to an ADR/design doc in this repository:

## List of independent structural decisions

1. **Architecture**: DDD ten-domain layering (domain/app/adapters + depguard rules enforced)
   + a zero-business-knowledge platform foundation; homegrown architecture guard tests
   (internal/arch: dependency rules, SQL allowlist, table owners; TRUNCATE coverage in
   e2e/guard_truncate_test.go).
2. **Execution engine**: eino in-process execution (no daemon middle layer)—fundamentally different
   from upstream's local daemon dispatch model; external runtimes adopt the client contract
   (ADR 0009: MCP tool face + pull loop), also with no daemon.
3. **Job substrate**: homegrown TickWorker + in-domain SKIP LOCKED (ADR 0001), no third-party
   queue dependency.
4. **Error code family**: own `E_*` catalog + unified error envelope.
5. **Migration sequence**: 39 own migration pairs vs upstream's 589; table names/column
   sets/enums all redesigned (work_item vocabulary vs upstream's issue vocabulary).
6. **Plugin ecosystem**: five-piece contract (MCP/API/events/deep links/slots) + pack-provision
   installer + MCP catalog cache (ADR 0008/0011).
7. **Hydration system**: layered disclosure + settlement summary + timeline backbone (ADR 0010).
8. **Observability**: OTel meter + Prometheus exporter served by promhttp (ADR 0003; migrated
   2026-10-02 from the interim in-memory JSON-counter form).
9. **Query face**: unified cursor pagination (keyset convention, no offset pagination; most lists
   key on `(created_at, id)`, while the timeline and inbox cursors key on `created_at` alone).

## Fact checks (2026-10-04, reproducible)

- Literal copying in Go code/SQL migrations: **zero** (audit conclusion).
- Upstream trace words in the product tree (pattern per the CI trace-guard gate): **zero**
  (re-verified after the 2026-10-04 cleanup).
- Frontend: upstream frontend code was not referenced (this project's own React implementation).

## Discipline

Internal research material is never distributed with the product (physically isolated outside the
product tree); the CI trace gate (.github/workflows/trace-guard.yml) prevents reintroduction.
