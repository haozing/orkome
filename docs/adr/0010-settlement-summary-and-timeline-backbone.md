# ADR 0010: settlement summary and timeline backbone (hydration system)

Date: 2026-10-04 | Status: implemented (ADR-ization of context-rehydration-design M1)

## Background

When external participants (human/engine/plugin/runtime) hydrate a work item, the "context" is
missing: timeline activity entries are empty shells, the execution layer (task outcomes) leaves no
trace, and there is no stored summary. Meanwhile the agent client contract demands an onboarding
floor of ≤3 calls and <8KB.

## Decision

- **Task settlement summary**: CompleteRun/FailRun persist `summary` (≤500 runes) in the same
  transaction; v0 degrades to the last text message for result/reason—no LLM call; for external
  completes, the result is first landed as the final text message before settling (final
  conclusion over intermediate progress).
- **Timeline backbone**: activity entries made visible (rendered human-readable lines + structured
  Payload retained), comment double-render deduplicated (the projection never writes
  comment-derived entries—comments render only via the comment union), and
  `task_settled` as a first-class entry (both success and failure leave traces; event-driven
  projection, idempotency key absorbs double Settle).
- **M3 gate ruling**: the L1 REST brief endpoint is **not built**—client-side self-assembly of
  slim+summary measured within target (3 calls, 6.7KB); rebuild trigger conditions recorded. The
  MCP tool face later grew `get_work_item`, a de-facto single-call server-side brief (entity +
  recent timeline + recent task conclusions), which satisfies the same need for runtime clients.

## Consequences

- One source, shared consumers: timeline `task_settled` entries, the MCP `get_work_item` brief,
  and the task read face share the single summary column (plugin event payloads still carry the
  full task result; a ≤1KB summary slice there is a possible follow-up).
- Restarts are not blind: FailRun subtask context injects prior_summary/prior_failure_reason,
  consumed by the engine prompt.
- Incidentally fixed a stock bug: PUT-assigning an agent always 500'd (the subscription-table user
  FK poisoned the transaction).
