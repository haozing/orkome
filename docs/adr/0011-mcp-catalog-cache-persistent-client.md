# ADR 0011: MCP catalog cache = persistent client (process-level)

Date: 2026-10-04 | Status: implemented (ADR-ization of plugin-system-design §2.1 enhancement 1)

## Background

Every task-paid MCP tool assembly does Start+Initialize+ListTools (10s budget)—the top
performance item once plugin count grows. Caching ListTools metadata is insufficient: eino tools
bind clients, so a metadata hit still rebuilds the connection.

## Decision

Cache the **client itself**: a process-level map keyed by transport+URL+credential headers
(credential rotation/registration changes naturally miss into new keys), value = {client, tools};
a hit skips the entire handshake; idle 5min lazy reclaim (swept opportunistically by
buildMCPTools, no background goroutine); on a concurrent miss with double-connect on the same key,
last-write wins and the earlier writer is closed to prevent leaks. Task completion does not close
clients owned by the cache (the bridge's close face is retired accordingly).

## Consequences

- Real-MCP fake-service integration: two assemblies share one Initialize; config changes
  re-handshake immediately.
- Persistent clients hold connection resources—the idle TTL is the reclamation discipline; plugin
  crashes do not poison the cache (the next task's failure path is unchanged; the snapshot records
  connect/list failed).
- mcp-go Streamable HTTP client concurrency safety is guaranteed by the library.
