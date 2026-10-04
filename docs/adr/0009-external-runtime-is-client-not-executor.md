# ADR 0009: external runtime is a client, not an executor

Date: 2026-10-04 | Status: accepted (ADR-ization of the agent-client-contract-design core stance)

## Background

Two models exist for "connecting local agents": the executor model of the reference
implementation/runlynk (platform assembles prompt → runs locally once) and the
collaboration model (the runtime uses the platform continuously like a human: watches its own
board, reads context, uses platform features, settles autonomously).

## Decision

orkome chooses the **collaboration model**: local CLI agents (dsh/claude/codex…) become digital
colleagues of the platform through the "agent client contract" (the sixth face: REST + platform
MCP tool face + event cursors + external execution loop). No daemon middle layer, no exe bridge
(dsh native MCP direct connection proven—adding a layer is pure burden—rejected in the 2026-10-04
review). If the executor model is needed in the future (unattended batch), runlynk adds an
adapter layer, coexisting with this contract without merging.

## Consequences

- Credentials v0: synthetic service user + `agent_credential` binding (PAT hit →
  Principal.AgentID); write guards lock the agent subject inside the "own tasks + comments" cell.
- The engine does not take such orders (Dispatcher skips runtime_kind=external); watchdog exempt
  (no instance heartbeat, no false kills).
- 2026-10-04 dsh live session end-to-end verification: the runtime autonomously started the server,
  wrote its own config, and cleared its board (6 completed 1 failed including self-written
  summaries)—the first real-form verification of an autonomous digital colleague.
