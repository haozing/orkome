# Context Hydration System Design: layered read faces and the context backbone (a shared platform capability)

**Version**: v0.4 (2026-10-04. v0.1 split off the same day from the agent client contract series;
v0.2 audit revisions (L1 assumption tagging / M3 gate / evidence levels); v0.3: all three
batches M1-M3 implemented—M1 settlement summary + timeline triple-fix, M2 pagination and endpoint
backfill, M3 gate measured (L1 degraded to not-built) + federated search over HTTP; per-batch
implementation records in §7; v0.4: doc-audit pass—the §1/§3/§4/§5 pre-implementation states
aligned with the as-built record, empirical-value anchors and line refs refreshed)
**Companion docs**: `plugin-system-design-2026-10-03.md` (the five-piece contract—this doc's
settlement summary is the planned slice source for its event catalog's task.completed payload;
payloads currently carry the full task result);
`agent-client-contract-design-2026-10-04.md` (the sixth face—this doc's L1 brief / timeline /
event cursors are its hydration foundation; that doc only depends, never redefines).

**Decision basis** (settled in the 2026-10-03/04 discussion series, each item traceable):
1. **Hydration = a shared platform read capability, subordinate to no single consumer**. Four
   consumer classes, four benefit faces: frontend human users (timeline tells the story), the
   internal engine (productizing its private recipe), the plugin system (settlement summary as
   event payload), external runtimes (the pyramid as onboarding foundation). A standalone doc
   prevents two misplacements: changing the timeline requires reading the external client contract
   doc; client-contract reviews drowning in read-path detail.
2. **Layered disclosure (pyramid)**: a bounded brief for the whole picture first, then drill down
   by cursor. The budget anchor is the recipe the engine already runs in production—work item
   title + 500-char trigger excerpt + the last 5 task conclusions
   (`internal/orchestration/adapters/engine/engine.go` prompt assembly,
   `adapters/pgrepo/repo.go` SessionTranscript, default limit 5)—**not guessed sizes, empirical
   values the platform itself ran out of**.
3. **All state lives on the platform**: no consumer keeps authoritative state; every understanding
   = one on-demand hydration (mirroring how humans "come back and look at the board"). This doc
   only governs "what to read, how to read it, what settlement produces"; "who reads and how the
   write loop works" is in the agent client contract doc.
4. **The timeline is the context backbone**: its shape was already right (comment ∪ activity merged
   stream, new→old, before cursor, limit<=200,
   `internal/work/adapters/pgrepo/repo.go` ListTimeline)—the three content-level gaps it had
   (activity shells, execution layer absent, comment double-render) were fixed by M1 (§4).
5. **The task settlement summary = the highest-leverage new build**: v0 calls no LLM; one source,
   shared consumers (timeline entry / runtime brief today; the plugin event payload slice is the
   planned third).
6. **Explicitly not built**: message-level event push (high volume, aligned with the plugin doc
   §8.2 conclusion—realtime transcripts go over the existing WS channel); a generic
   compression/vector-summary layer (no hot-path need in v0, aligned with the plugin doc's
   "explicitly excluded" discipline).

**Discipline**: summaries are bounded the moment they persist; large content is always "summary +
ID reference, full text via API" (same pattern as the plugin event catalog); read endpoints are
always cursorized; new timeline event kinds require a recorded rationale (anti catalog bloat,
aligned with the event-catalog ADR discipline); key numbers and claims carry evidence levels
(proven/inferred/assumed/bet—conventions and the master table in
`agent-client-contract-design-2026-10-04.md` §9).

---

## 0. One-sentence target shape

Whenever any consumer (human, engine, plugin, external runtime) needs the "full picture" of a
work item, it draws from the same pyramid: first a 2-4KB L1 brief for the whole picture, then
drill down by cursor into the task layer and raw transcripts, or walk the timeline back to "how
did we get here"—**every layer bounded, on-demand, resumable; no dead corner where full pulls are
required to understand**.

```
        +--------------------------------------------+
  D     | Event cursor stream (<=1KB each, keyset      |  Delta: what changed
  L3    | Raw task_messages transcripts (before_seq)   |  On demand: how it was done
  L2    | Single task: full conclusion/failure/chain   |  On demand: what that run produced
  L1    | Brief: entity+children+active tasks+comments  |  2-4KB: the whole picture
  L0    | Work item list (title/status/assignee)        |  <200B/item: my board
        +--------------------------------------------+
   Read order: L0 -> L1 -> (on demand) L2/L3 -> (standing) D
```

---

## 1. Consumer matrix and boundaries

| Consumer | What it takes | State (post M1-M3, 2026-10-04) |
|---|---|---|
| Frontend human users | timeline (detail-page context), L1 brief (reusable aggregate) | timeline tells the story (M1 fixed the three gaps); the L1 REST aggregate deliberately not built (M3 gate, §7) |
| Internal engine (recipe) | last 5 task conclusions (SessionTranscript)—this design turns it from engine-private into a platform read face | productized on the read faces (M2 single-task/slim lists; the MCP `get_work_item` brief serves the same recipe) |
| Plugin system | settlement summary -> event catalog task.completed payload | Phase 1 event push built (webhooks domain + `GET /api/events`); payloads currently carry the full task result—the <=1KB summary slice remains the planned follow-up |
| External runtimes | the whole pyramid + D | read path complete (M2/M3 read faces + client-contract Phase 2b MCP tools / Phase 3 events) |

**Boundary statement**: this doc is the "read model + settlement products" design, excluding
write-path contracts (claim/report/sensitive operations in the agent client contract doc §2);
event **push** (webhook delivery protocol) belongs to the plugin system design §2.3—this doc only
owns the summary source inside event **payloads** and the consumer-side conventions of the
**pull-cursor endpoint**.

---

## 2. Design principles

1. **State on the platform; hydration is a read**: work items, task history, transcripts,
   comments, automation—all with the platform as system of record; consumers work in bursts,
   re-hydrating on wake, never depending on local residue.
2. **Layered disclosure, budgets anchored to the engine recipe**: per task the engine itself takes
   only "title + 500-char excerpt + 5 conclusions, everything else pulled on demand via tools" and
   that has sufficed in production—this is the empirical anchor for "not too big, yet enough to
   understand". The full set of empirical values: task_message persisted truncated at 2000 runes
   (`eventpipe.go:15,113-115`), comment excerpts 500 chars, chat window 20 turns
   (`engine.go:179`), fallback result comment 8000 runes (`service.go:775-780`).
3. **Summaries before bodies**: task conclusions inside events and briefs are always <=500-char
   summaries + task_id references; bodies (full result, raw transcripts) only via drill-down
   endpoints on demand.

---

## 3. The layered-disclosure pyramid

| Layer | Contents | Budget | Status |
|---|---|---|---|
| **L0 board** | my work item list (title/status/assignee) | <200B per item | ✅ exists (`GET /work-items` full filters, keyset cursor) |
| **L1 brief** | entity core fields + children summary + active tasks + last 5 comment excerpts + last 5 task conclusion summaries + drill-down cursors | **2-4KB**【assumed·anchored to the engine recipe, measured at M3: path B self-assembly = 6.7KB】 | ❌ REST aggregate deliberately not built (§7 M3 gate: client self-assembly reached the floor); the MCP tool `get_work_item` is the de-facto single-call brief for runtime clients |
| **L2 task layer** | single-task full conclusion/failure reason/attempt chain | on demand | ✅ `GET /tasks/{id}` + newest-first lists (M2) |
| **L3 transcript** | raw task_messages stream | on demand | ✅ `order=desc + before_seq + kind=` filters (M2) |
| **D delta** | event cursor stream | <=1KB each | ✅ `GET /api/events` keyset `<unixnano>\|<id>` cursor stream (client-contract Phase 3, co-built with plugin Phase 1; keyset decided per the spike correction) |

L1 response shape (v0):

```json
{
  "work_item": {"id","title","description","acceptance_criteria","status","priority","assignee","revision"},
  "children":  [{"id","title","status"}],
  "active_task": {"id","status","agent_id","awaiting_input"},
  "recent_tasks": [{"id","agent_name","finished_at","outcome","summary"}],
  "recent_comments": [{"id","author","excerpt","created_at"}],
  "cursors": {"timeline_before":"...","tasks_before":"..."}
}
```

**If ever rebuilt** (rebuild triggers in §7 M3): the work domain app layer would add
`GetWorkItemDigest` (aggregating existing repo queries, no new tables), mounted at
`internal/work/adapters/http/routes.go`; the external client's MCP tool `get_work_item` would
then be a same-source thin shell (see the client contract doc §2.2).

---

## 4. Timeline backbone-ization: three content-level gaps (all fixed in M1)

The timeline shape was right (new→old, before cursor = the correct read direction of "see the
present first, dig on demand"), but at design time it could not tell the story. The three gaps as
found, and how M1 fixed them (2026-10-04; acceptance in §7):

1. **Activity entries were empty shells**: the ListTimeline SELECT projected empty strings for
   activity rows—`activity_log` stores the event kind and payload JSONB (`WorkItemPayload`
   serialized at write time in `cmd/orkome-server/wiring_work.go`) that the query never fetched.
   **Fixed**: the SELECT carries kind + payload and `work/domain.RenderActivityLine` renders
   human-readable entries like "status: in-progress -> blocked" (structured Payload retained).
2. **The execution layer was wholly absent**: the projection subscribed only to work_item.* and
   comment.*; a task's life (dispatch/failure/retry/completion) produced no entries; only
   "completed without the agent commenting" leaked one fallback comment entry—failures left no
   trace. **Fixed**: the composition root projects a first-class **`task_settled`** entry from
   TaskCompleted/TaskFailed events (idempotency key `task_settled:<taskID>` absorbs double
   Settle), both success and failure leaving traces, summary + outcome included.
3. **Comment double-render**: comment events entered both activity_log and the UNION as their own
   body, so the same comment appeared twice (once with content, once as a shell). **Fixed**: the
   projection never writes comment-derived entries—comments render only via the comment union
   (no query-side filtering).

---

## 5. Task settlement summary (the highest-leverage new build)

- **Generation timing**: persisted at the task settlement point (complete/fail), into a new `summary`
  column on the task row (<=500 chars).
- **v0 calls no LLM**: take the task's last `kind=text` task_message (persisted truncated at 2000
  chars anyway) truncated to 500; tasks ending without text (e.g. ending on tool_result) degrade
  to the first 500 chars of result.
- **v1**: a small model generates a one-line conclusion at settlement (EvalService already has the
  10% sampled-scoring infrastructure to borrow, and its rules already flag >8000-char results as
  "suggest summary"—`internal/orchestration/app/service_eval.go:126-127`; the pain is
  self-evident).
- **One source, shared consumers**: the timeline's task_settled entries and the runtime brief
  (MCP `get_work_item` / task read face) consume the summary today; the plugin event catalog's
  "task.completed <=1KB summary" slice is the planned third consumer (payloads currently carry
  the full task result).
- **Fixed in M1 (was v1)**: blind retries—a new task row did not replay previous messages; now
  the retry subtask context injects prior_summary + prior_failure_reason (`repo.go` FailRun),
  consumed by the engine prompt.

---

## 6. Read-path fix list (design-time; all items resolved by M1-M3, see §7)

#1-#4 landed in M1, #6-#9 in M2, #10 in M2/M3; #5 (L1 aggregate) was closed not-built by the M3
gate. Kept as the original value-for-effort record:

| # | Fix | Code landing spot | Size |
|---|---|---|---|
| 1 | Persist the settlement summary (§5) | orchestration settlement point + new task column | small |
| 2 | Make timeline activity entries visible (kind+payload -> human-readable) | `repo.go:1079` + rendering | tiny |
| 3 | Dedup comment double-render | `wiring_work.go:41-44` or UNION filter | tiny |
| 4 | Project task_settled into the timeline | settlement point -> work-domain projection | small |
| 5 | L1 brief aggregate endpoint | work domain adds `GetWorkItemDigest` | medium |
| 6 | messages: before_seq/descending/kind filters | `repo.go:759-781` | small |
| 7 | Work-item task list newest-first option | `handlers_taskview.go:209-229` | tiny |
| 8 | Comment pagination (before cursor) | `service_comment.go:265-291` | small |
| 9 | Add `GET /tasks/{id}` single-task endpoint; `GET /tasks` list rows stop carrying full result by default | `repo.go:1692-1733` | small |
| 10 | (Incidental) `search_workspace` gets an HTTP endpoint (federated RRF exists, only the engine tool uses it) | `service_catalog.go:806-864` | tiny |

---

## 7. Batched implementation and acceptance

| Batch | Contents (per §6 list) | Acceptance |
|---|---|---|
| **M1 Settlement and context** | #1 settlement summary, #2 activity visibility, #3 comment dedup, #4 task_settled projection | The timeline can tell how a work item "got here today" (including failure history and retry chains); retried tasks see the previous conclusion. **Implemented and accepted 2026-10-04**: migration 036 (summary column); settlement persists the summary in the same transaction (v0 = last text message degrading to result / reason+error); retry subtask context injects prior_summary + prior_failure_reason (consumed by the engine prompt—no blind retries); the composition root projects task_settled (idempotency key absorbs double Settle); timeline activity rendering (work/domain.RenderActivityLine) + comment.* query-side dedup + projection stops writing; frontend components render the lines. Verified: unit tests + 6 new integration tests + live machine (comment-triggered task -> failure -> timeline top "task failed (verify-writer): model_not_configured") |
| **M2 Pagination and endpoint backfill** | #6 messages cursor, #7 newest-first, #8 comment pagination, #9 single-task endpoint + list slimming | Long-task tails reachable in <=2 calls; the work item's latest task fetchable; list responses carry no unbounded full text. **Implemented and accepted 2026-10-04**: messages `order=desc + before_seq + kind=` filters (ascending default unchanged, response carries next_before_seq); work-item task list `order=desc`; comment root-thread pagination (recursive CTE completing descendants, `limit/before` params, no-params = unchanged full folding, response carries next_before); new `GET /api/tasks/{taskId}` single-task detail (route table and route contract test synced); `GET /tasks?fields=slim` list slimming (result/error/context out of lists, summary rides along; full remains the default for the frontend, `fields=slim` is opt-in for runtimes). Route contract tests reconcile both ways; six live-machine verifications (desc/slim/single-task/tail paging/kind filter/comment pagination) |
| **M3 Brief aggregation** | #5 L1 brief endpoint, #10 search HTTP | **2026-10-04 gate measured; L1 degraded to an optional optimization (not built)**: on a real-size sample of 23 task-history entries averaging 1.9KB result, client self-assembly measured—path A (detail + timeline + tasks full-text window) 3 calls 8.6KB **over budget** (confirming the long-history-breaks-the-floor prediction); path B (detail + timeline + tasks?order=desc&limit=5&fields=slim, summary riding along) 3 calls **6.7KB within budget**. Per this table's gate rule: client self-assembly reached the floor -> server brief not built, #5 closed. **#10 implemented**: `GET /api/work-items/search?mode=federated` (three-way RRF federated search over HTTP, previously engine-tool-only; default mode unchanged); work-item task list gained `fields=slim` (symmetric with GET /tasks, the prerequisite of the gate passing). **L1 rebuild triggers** (just re-evaluate, don't preempt): single work items exceeding 50 task-history entries making timeline/paging costs significant, or cross-work-item digest needs appearing, or M2 slim semantics proving insufficient to express the L1 shape |

Gates follow repo conventions: 61-package full tests, route contract test updates, new e2e
goldens. M1/M2 add no new security faces and can start immediately; M3's
endpoints expose under existing PAT permissions as-is (tightening happens in client-contract
Phase 2, see that doc).

---

## 8. Risks and open questions

1. **v0 settlement summary's semantic boundary**: tasks ending on tool_result get poor summaries
   (degraded to result truncation); accept v0 roughness, v1 small-model summaries cover it.
2. **`work_item_dependency` (blocked-by)**: schema exists, zero API
   (`migrations/005_work_core.up.sql:144-152`). The full "browse related work items" experience
   eventually needs it; not blocking v0, listed as this system's backlog.
3. **Event cursor stream shared with plugin system Phase 1**: **built 2026-10-04**—`GET
   /api/events` reads event_outbox with the keyset `(created_at,id)` cursor (EventID stays a
   random UUID v4, per the spike correction; the write side did not switch to ULIDs). Remaining
   item: the 30-day event retention needs its own cleanup (the outbox itself has no retention).
4. **Timeline event-catalog bloat**: with task_settled in, later domains
   (squad_leader_evaluated already a precedent) will keep petitioning to join—new kinds need a
   recorded rationale, and task **message-level** events never enter (aligned with the plugin doc
   §8.2 discipline).
