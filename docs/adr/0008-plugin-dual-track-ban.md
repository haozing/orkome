# ADR 0008: plugin dual-track ban (first-party and third-party go through the same door)

Date: 2026-10-04 | Status: accepted (ADR-ization of plugin-system-design decision 2/§8.4—the design
doc required "discipline written into an ADR")

## Background

While the plugin system (five-piece contract) landed, first-party verticals (agentblog etc.) and
future third parties share the same integration mechanism. Three tempting shapes appeared
historically: one big backend migration, code domains moving in, compile-time embedding
(InMemoryTransport).

## Decision

**Never open an "embedded/shared-library/privileged-channel" backdoor for first-party plugins.**
First-party and third-party go through the same door: standalone service + MCP tool face + pack
declaration. Compile-time embedding may appear only as a future transport optimization, changing
no contract and serving as no integration method. Violating this entry declares the plugin system
dead.

## Consequences

- First-party "inconvenience" (must deploy, must go through provisioning) is the opportunity cost
  of a healthy ecosystem—pay it.
- Precedent simplified: any proposal to "make our own plugins a bit faster" is rejected on the spot
  by citing this ADR.
- Phase 1-3 platform mechanisms (event push / pack-provision / deep links) proven to treat
  first-party and third-party identically.
