# External Runtime Collaboration Design: the Agent Client Contract (the platform's sixth face)

**Version**: v0.7 (2026-10-04. v0.4/0.5: Phase 2a/2b implementation and dsh end-to-end
acceptance; v0.6: Phase 3 (the eyes) implemented—the `GET /api/events` keyset cursor stream
built jointly with the plugin system's Phase 1 event-push subsystem (webhooks domain + HMAC
delivery + dead-letter replay + egressx ADR 0007), one investment unlocking two lines; see the
Phase 3 row in §7; v0.7: doc-audit pass—§2.2 verb catalog aligned to the as-built nine verbs,
credential snapshots and evidence-table file:line refs refreshed against code)
**Companion docs**: `plugin-system-design-2026-10-03.md` (the five-piece contract—the plugin
system answers "how others **extend** the platform"; this doc answers "how others **operate** the
platform". The two are orthogonal, the five-piece contract unchanged);
`context-rehydration-design-2026-10-04.md` (the hydration foundation—the underpinnings of this
doc's §2.2 read verbs and the read faces of §4 continuity are all provided there).
**Reference frame**: the reference implementation—a mature reference for the executor model
(daemon outbound WS + claiming + callback API + 26 runtime adapters, **the model this design
deliberately does not adopt**, but whose callback contract, task-level tokens and adapter
implementation are the most valuable borrowing source); runlynk—a shape reference for the channel
layer and the agent verb face (its 7 verbs and lease/fencing semantics are borrowable).

**Decision basis** (settled in the 2026-10-03/04 discussion series, each item traceable):
1. **The external runtime's position = client and participant, not executor**. The goal is not
   the worker model of the reference implementation/runlynk ("platform
   assembles prompt -> run locally once"), but a runtime that **uses the platform continuously
   like a human**: it sees its own assigned work, browses task and work-item context itself, uses
   platform features itself (including creating automations), and reports back when done; users
   may even replace the platform frontend entirely with an interface built from the runtime's own
   plugin system (e.g. dsh plugins), still collaborating on the same platform.
2. **The platform = an API-first collaboration substrate**. Humans operate the platform via the
   web frontend; AI runtimes operate the same platform via API/MCP—two equal classes of "users"
   (the relationship of GitHub's website to bot clients).
3. **Orthogonal to the plugin system; this contract is the sixth face**. The five-piece contract
   (MCP/API/events/deep links/slots) faces "plugins that extend capabilities"; the sixth face
   (REST + platform MCP tool face + event stream + external execution contract) faces "clients
   that operate the platform".
4. **Vocabulary discipline**: **platform plugins (packs) and runtime/client plugins are two
   different things, never conflated** (§1.3). Sharing one name would cause a lasting
   communication disaster.
5. **The interaction physical layer assumes "the client can pull"**. Local runtimes sit behind
   NAT; the platform never requires reachability (the existing five-piece MCP URL / webhook URL
   assumptions all inapplicable here): eyes = polling in v0 -> event cursor stream; runlynk's
   outbound WS is an optional reuse as a "doorbell" optimization, not a premise. **Conscious
   deviation statement**: the reference implementation and Slack Socket Mode
   both have outbound WS; this design's v0 is more conservative than the reference (simplicity
   traded for latency), with the v2 doorbell as backstop.
6. **The hydration foundation is its own doc** (context-rehydration-design-2026-10-04.md): the
   layered pyramid (L1 brief/timeline/settlement summary/event cursor) is defined and implemented
   there first—it simultaneously serves frontend humans, the internal engine, the plugin system
   and external runtimes; it is this contract's prerequisite, not a component.
7. **PAT scope narrowing moves up from "long-term" to external-execution launch time**. A client
   that can create automations can rewrite workspace behavior logic, a far larger permission
   face than task execution—the plugin doc §2.2's "long-term enhancement" classification is
   voided.
8. **API parity = product discipline (a bet, written into governance)**. Every capability the
   human interface has, API/MCP must match; "UI first, API later" kills this vision outright
   (self-interface users bypass deep links/slots/frontend investment—that is the intended
   audience split, not an anomaly).
9. **Onboarding has two tiers (v0.3 revision)**: **the human-driven mode is truly zero-code**—a
   user configures one MCP address in codex/claude and the agent works as a platform client
   (MCP server capability already in the dependency tree: mcp-go v0.43.0 includes the Streamable
   HTTP server【proven, see §9】); **the auto-wake mode needs a minimal client kit**—a local
   supervisor loop (polling + launching the runtime + a brief template), a Phase 2 deliverable,
   not an ecosystem-period item. dsh's MCP support was unverified【assumption】, spike before the
   first dsh onboarding. The platform hardcodes no adapter for any runtime (the reference
   implementation shipping 26 built-in runtimes is the anti-example; adapter
   diversity goes to declarative profiles and the ecosystem).
10. **Credentials v0 = synthetic service user + agent_credential binding table (settled by the F1
    audit)**: a native agent principal is greenfield (~9 touch points + a parallel authorization
    story, see §2.1), not worth it with only external runtimes as consumers; upgrade when the
    trigger appears.

**Discipline**: all writes idempotent and attributable to the PAT owner; events inform, never
orchestrate; all outbound goes through egressx; runtimes run with full authority on their own
machines and the platform does not vouch for their behavior (§5.1); key claims carry evidence
levels per §9's conventions (proven/inferred/assumed/bet).

---

## 0. One-sentence target shape

Any local CLI agent (codex / claude / dsh / …) joins orkome with one credential + one MCP address
and becomes a "digital colleague" in the workspace: assigned a work item, it claims the work
itself, hydrates context through the layered read faces to understand the whole picture, and
while executing can browse history, look up related work items, post comments, (once authorized)
create automations, and finally settles the task with streaming messages + a conclusion; the user
can watch it work from the orkome frontend, or live entirely inside an interface built with dsh
plugins—the platform treats both alike.

```
                 (6) Agent client contract
   +------------+   eyes: polling / event cursor stream (pull)   +------------------+
   | orkome     | <--------------------------------------------- | local runtime    |
   | (collab    |                                                | (codex/claude/   |
   |  substrate)| --------------------------------------------->  |  dsh)            |
   |            |   hands: platform MCP tool face + REST          |  +- runtime-side |
   | frontend   |   loop: claim -> report -> settle               |     plugins      |
   |  (human)   |                                                |  +- self-built   |
   +------------+                                                |     UI           |
        ^                    |                                    +------------------+
        | five pieces (plugin system, unchanged)                 | all state lives on
   +----+-----+              v  (hydration foundation: context-rehydration-design)
   | platform |
   | plugins  |
   +----------+
```

---

## 1. Position: client, not executor

### 1.1 The divide between the two models

| | Executor model (reference implementation / runlynk) | Collaboration model (this design) |
|---|---|---|
| Who thinks | the platform (server assembles the full prompt) | the runtime itself (agent-led decision loop) |
| Task semantics | "run this prompt to completion" | "this work item is yours; handle it like a colleague" |
| Platform role | scheduler + execution ledger | collaboration substrate (system of record + operation face) |
| What the runtime gets | an assembled context + instructions | credentials + tool face; context hydrated on demand |
| Fits | unattended batch execution | autonomous collaboration, cross-work-item exploration, using platform features |

runlynk keeps its "base hands layer" positioning (the plugin doc §1.2 original ruling stands);
the only possible intersection with this design is **doorbell channel reuse** (§2.4, optional
optimization).

### 1.2 Relationship to the five-piece contract

The five pieces are the extension contract of "platform -> plugin" and "plugin -> platform"; the
sixth face is the operation contract of "client -> platform". A dsh plugin can wrap the platform
MCP tool face into a beautiful local interface; capabilities provided by a platform plugin
(tools/work items/events) treat all clients alike—the two plugin classes meet loosely coupled at
the sixth face, mutually unaware, mutually independent, evolving separately.

### 1.3 Vocabulary (mandatory distinction)

| Term | What it refers to | Where it installs | Author | Platform-governed? |
|---|---|---|---|---|
| **Platform plugin (pack)** | capabilities extending the shared world (service + pack) | server-side, installed into a workspace | capability supplier | yes (provisioning, permission declarations) |
| **Runtime/client plugin** | extensions to one local agent's private experience and UI | the user's machine | users/tool community | no (and ungovernable—it acts inside the user's trust domain with the user's credentials) |
| **External runtime** | the CLI agent itself joining the sixth face (codex/claude/dsh…) | the user's machine | — | attributed only by credential identity |
| **Agent client contract** | the sixth face defined by this doc (§2) | — | — | the contract itself marked internal until v0 freeze |

---

## 2. The agent client contract

### 2.1 Identity and credentials

- **A runtime on the platform = an agent entity** (the agent table gained
  `runtime_kind: engine | external` via `migrations/037`—007's original agent table had no kind
  column, a clean incremental migration【proven: `migrations/007_orchestration.up.sql:1-16`】; the Go
  model `internal/orchestration/app/ports.go` carries the field in sync). Assignment works exactly
  as today: `POST /api/work-items` (assignee_type=agent) or picking in the UI—humans learn
  nothing new.
- **Credential status (2026-10-04 code audit, proven; Phase 2a snapshot below supersedes the
  "only the user form" wording)**: PATs bind strictly to `user_id`
  (`migrations/004_identity.up.sql:39-51`); Source is only session|pat
  (`internal/platform/webx/principal.go:10-13`); all workspace authorization goes through "user
  membership" (`internal/identity/adapters/http/routes.go:129-148`). **No non-user principal
  exists anywhere in the codebase** (grep for service_account/machine token: zero hits)—Phase 2a
  added an `AgentID` field riding the existing user principal (`principal.go:24`), not a new
  principal kind.
- **v0 route (settled, implemented)**: synthetic service user + agent_credential binding table.
  Following the plugin doc §2.2's own "dedicated service account" pattern: each external agent gets
  a synthetic service user (membership machinery reused as-is), plus an `agent_credential` binding
  table (`pat_id` ↔ `agent_id`—no scope-flag columns; scope is hard-coded in the guard layer,
  `migrations/037_external_runtime.up.sql`); after PAT resolution the binding table is checked
  (`internal/identity/app/service_token.go:136-140`), and on a hit the Principal gains the agent
  identity, with write paths deciding scope by binding.
- **v1 route (trigger: a second non-user principal consumer appears, or the Phase 5 scope
  framework kicks off)**: native agent principal—touches the auth middleware
  (`webx/auth.go:118-134` Bearer prefix branch), the Principal struct, cache codec, mint/resolve, CRUD,
  wiring, ~9 places, and needs a parallel membership/authorization story; not worth it in the
  single-consumer era.
- **v0 scope (hard-coded in the guard layer, no general framework)**: read work
  items/tasks/timeline/search within the workspace; write **only tasks assigned to this agent**
  (claim/messages/settle) + comments on related work items; default-deny: creating automations,
  assigning others, deleting any entity (sensitive verbs go through §2.2 v1's separate
  authorization).
- **Attribution**: every write records the PAT owner (the `task_usage` originator machinery
  exists); comments ride the existing `source_task_id` attribution chain.

### 2.2 Hands: the platform MCP tool face (new)

orkome **exposes** (rather than consumes) an MCP server for the first time: a workspace-level
Streamable HTTP endpoint (`/api/mcp/w/{workspaceId}/agent`—workspace bound in the URL path, no
custom client headers), PAT-authenticated. **Zero new
dependencies**【proven: `go.mod:22` mark3labs/mcp-go v0.43.0 already vendors the complete server
side (`vendor/github.com/mark3labs/mcp-go/server/streamable_http.go`); today only its client is
used】. **A same-source thin shell over REST** (the composition root calls repo/service directly,
see open question 1).

Verb catalog v0 as built (read):

| Tool | Semantics | Underlying |
|---|---|---|
| `list_my_work_items` | work items assigned to me (reusing existing filters) | `GET /work-items?assignee=` |
| `get_work_item` | L1 brief in one call (entity + recent timeline + recent task conclusions) | server-side aggregate over the hydration faces (`cmd/orkome-server/wiring_mcp.go`) |
| `get_task` | single-task conclusion/failure/attempt chain | `GET /tasks/{id}` (hydration §6 #9) |
| `get_task_messages` | raw transcript (before_seq descending cursor) | `GET /tasks/{id}/messages` (hydration §6 #6) |

Verb catalog v0 as built (write, bound to the agent identity):

| Tool | Semantics |
|---|---|
| `claim_task` / `report_progress` / `complete_task` / `fail_task` | external execution loop (§2.3) |
| `post_comment` | comment on related work items |

Verb catalog v1 (candidates not yet built): `search_workspace` (federated search exists on the
REST face, not yet unwrapped as a tool), `update_work_item_status`, plus the sensitive set
visible only after separate authorization: `create_automation`, `create_work_item` (spawning
sub-items), `assign_work_item`, `wait_for_event`.

**Naming discipline**: shares one validation set with the plugin tool prefix rules (plugin doc
§2.1 enhancement 2); tool descriptions must state "when to reach for it" (aligned with the skill
spec).

### 2.3 The loop: external execution contract

When an agent's `runtime_kind=external`, the engine **does not take the order** (Dispatcher claim
scope excludes external); the task waits in queued for the client:

```
POST /api/tasks/{id}/claim          # PAT = that task's agent; queued->running (CAS); v0 no lease (open question 3)
POST /api/tasks/{id}/messages       # execution message backflow (seq continues the existing monotonic order; kind same five as today: text/tool_use/tool_result/error/approval)
POST /api/tasks/{id}/complete       # with result -> triggers settlement summary + task_settled projection + events
POST /api/tasks/{id}/fail           # with failure_reason -> same (failures must leave traces)
GET  /api/tasks/{id}/interrupt      # follow-up/interrupt (reusing the existing awaiting_input system)
```

The settlement summary and task_settled projection implementations belong to the hydration design
§5 (settlement products are shared capability).

Reference frame: the reference implementation's daemon callback API
(`/tasks/{id}/start|messages|complete|fail`) proves this
contract shape viable【inferred·isomorphic reference】; the difference is we have no daemon middle
layer—clients connect directly and credentials are the boundary.

**Implementation notes (added in the v0.3 audit; all mandatory fixes or decisions)**:

1. **The Dispatcher exclusion is a one-line change, but must be paired with anti-stranding**【proven】:
   skipping external is written the same way as skipping archived (precedent at
   `internal/orchestration/adapters/workers/dispatcher.go:137-139`; the external skip landed at
   :141-143); but **the whole codebase has
   no age timeout/sweep for the queued status** (dispatched is only recovered at startup)—skipped
   tasks would strand silently. Implement in the same batch: queued-age alarms (or an enqueue
   guard: enqueueing for an external agent emits a notification event immediately).
2. **The existing watchdog would falsely kill external tasks**【proven, fixed in Phase 2a】:
   `LivenessSweeper` death-verdicts running tasks on `agent_runtime` engine-instance heartbeats
   >150s (`internal/orchestration/adapters/pgrepo/repo.go:707-730`)—external runtimes have no
   engine instance and produce no heartbeat; the task entering running gets swept to
   failed(runtime_recovery). **External tasks are excluded from that scan** (Phase 2a:
   FailStaleRunning/FailRunningAtStartup both carry the external exemption), with external
   liveness built: v0 accepts no automatic liveness (manual cancel + over-threshold transition
   into awaiting_input with notification); v1 uses the latest task-message time as heartbeat
   (`report_progress` doubles as it naturally). Initial threshold 24h, final in open question 2.
3. **Task-level token (a Phase 2 design decision point)**: reference—claim mints a short-lived
   task-level token carried by later callbacks, refusing without it (fail-closed). Candidate:
   the claim response returns the token, messages/complete/fail verify it, shrinking the theft
   surface of long-lived service-account PATs【inferred·reference over status quo】. Class-B
   trade-off, decided at Phase 2 kickoff (open question 4).

### 2.4 Eyes: notification and wake

- **v0 = polling**: the client loops `list_my_work_items` (filters exist) + `get_task`, every
  30-60s. Assemblable today with zero platform changes. The auto-wake form is the client kit's
  supervisor loop (§7 Phase 2).
- **v1 = event cursor stream (implemented, Phase 3)**: `GET /api/events?since={cursor}&limit=`
  with cursor `<unixnano>|<id>` (the plugin doc §2.3.4 pull endpoint—**for local clients it is
  the main channel**). **Cursor semantics (2026-10-04 spike correction, decided)**: EventID is a
  random UUID v4, not a monotonic ULID (`outboxx.go:26-29`)—Phase 3 chose the keyset
  `(created_at, id)` cursor over switching the write side to ULIDs (open question 6 records the
  decision). The standalone `work_item.assigned` event type was deliberately **not** added
  (assignment changes are discernible from work_item.updated's data; see the Phase 3 residual
  note).
- **v2 = doorbell (optional optimization)**: reuse runlynk's outbound WS channel to push a "wake"
  signal; clients wake then pull. Explicitly: an optimization, not a premise; the whole system
  works without it.
- **Relation to plugin system Phase 1**: the event source is the same hookup point (the plugin
  doc §2.3.5 outbox-consumer assumption【assumption, verify first when starting】), both sides
  co-building, one verification.

---

## 3. The hydration foundation (reference)

The external runtime's entire read face—the L1 brief, timeline context (incl. task_settled
execution entries), task-layer/transcript drill-down, the event cursor stream—is defined and
implemented first by `context-rehydration-design-2026-10-04.md` (external runtimes are just
one-fourth of its consumer matrix). This doc does not repeat that design; it only agrees on two
points:

1. **§2.2 read verbs = thin shells over the hydration pyramid's layers** (tools and endpoints
   same-source, see open question 1);
2. The hydration design's M1-M3 batches are this contract's Phase 2 **read-side prerequisites**;
   their acceptance floor ("a PAT-only script hydrates a work item in <=3 calls and <8KB
   transferred"【assumed·to be calibrated by M3 measurement】) is simultaneously this contract's
   onboarding-experience floor.

---

## 4. Continuity design: wake -> hydrate -> act -> report

### 4.1 Mechanism

- **Wake**: v0 client self-polls; v1 event stream; sessions may break—each wake re-hydrates,
  context rebuilt from the platform, no dependence on local residue.
- **Continuation pointer**: the engine already has the `session_ref_in/out` system (checkpoint
  machinery in `repo.go`); v1 lets external tasks carry it too (external completes currently
  pass an empty ref), for the runtime to decide its own continuation strategy.
- **Long-task execution log**: `report_progress` streams messages back, visible in real time in
  the platform UI (isomorphic to today's engine-task transcripts, reusing task_message storage).

### 4.2 End-to-end script (acceptance story)

1. A user creates the work item "add RSS subscription to the blog" in orkome, assigned to
   dsh-agent (external) -> the task enqueues, the Dispatcher skips it (with the queued-alarm
   backstop).
2. The local dsh wakes by polling/event -> `get_work_item_brief`: 2-4KB yields the whole picture
   (status, acceptance criteria, last 5 conclusions, active tasks).
3. While executing it doubts a historical decision -> `get_work_item_timeline` pages back ->
   sees the task_settled summary from 3 weeks ago "decided to use sitemap instead of full feed",
   avoiding rework.
4. It decides to add an automation "all sub-items done -> auto-close the parent" ->
   `create_automation` (sensitive verb, first use triggers the authorization flow, §5.2).
5. It changes code and runs tests locally; meanwhile `report_progress` streams the execution
   log, and the user watches live in the platform frontend.
6. `complete_task` (with conclusion) -> the settlement summary persists -> task_settled appears
   in the timeline -> the event fires -> the user's autopilot receives it and chains downstream.
7. Throughout, the dsh side can be a bare terminal or a dashboard the user built with dsh
   plugins—the platform cannot tell the difference.

---

## 5. Security and permissions

### 5.1 New trust-boundary clause (added to the platform trust model)

**External runtimes run with full authority on the user's machine; the isolation boundary = the
OS user running it** (aligned with the reference implementationsecurity-model's frank stance: no sandbox promised). Runtime-side plugins live inside the user's
trust domain acting with the user's credentials—**the platform can only attribute them by
credential identity, and cannot and should not audit runtime plugins themselves** (just as GitHub
does not audit your email client). The platform does not vouch for runtime behavior on the local
machine; the docs recommend running under a dedicated system account/container.

### 5.2 Credentials and authorization

- v0 = synthetic service user + agent_credential binding table + hard-coded scope (§2.1); the
  general scope framework and native agent principal wait for the ecosystem period (aligned with
  the plugin doc Phase 5 cadence, but **the base narrowing is no longer deferred**).
- Sensitive verbs (create_automation / assign_work_item / create_work_item) are default-deny;
  the tool face becomes visible only after explicit authorization by the workspace owner.
- **Task-level token**: whether to introduce it and its granularity, decided at Phase 2 kickoff
  (§2.3 implementation note 3 / open question 4).
- All writes idempotent (clients dedup by event/request ID) + attribution on record.

### 5.3 Outbound

Event push (if v2 goes the runlynk doorbell/webhook route) must pass egressx (the same
network-segment semantics as plugin doc decision 6); this contract's v0/v1 are entirely pull,
adding no new outbound face.

---

## 6. Product discipline: API parity (a bet, written into governance)

- **The object is capabilities, not interface shapes**: every capability the human interface has
  (work-item operations, comments, automations, search, approvals, …) API/MCP must have an
  equivalent; the reverse is not required (API may exceed UI).
- Deep links (4) and slots (5) enhance the platform frontend, naturally bypassed by
  self-interface users—**that is the intended audience split, not a defect**; API must not be
  cut on the grounds that "users can skip our frontend".
- Landing mechanism: when adding a human capability, "API/MCP parity" becomes an acceptance item
  in the task template (the same conventional slot as the route contract test's catalog
  reconciliation).

---

## 7. Phased implementation and acceptance

| Phase | Contents | Acceptance |
|---|---|---|
| **Phase 0 (in place)** | REST+PAT, work-item filtered queries, timeline, task history, task_message persistence, federated search (tool side) | existing e2e goldens |
| **Phase 1 (read-side prerequisite, implemented in the other doc)** | hydration foundation M1-M3: settlement summary, timeline triple-fix, L1 brief, pagination and single-task endpoints | see `context-rehydration-design-2026-10-04.md` §7 (floor: a PAT-only script hydrates a work item in <=3 calls, <8KB) |
| **Phase 2 (the work loop)** | external execution type (runtime_kind migration + Dispatcher exclusion + queued-age alarm + **FailStaleRunning excluding external tasks** + external liveness v0), credentials v0 (synthetic service user + agent_credential binding table + hard-coded scope), platform MCP tool face v0 (mcp-go server side, zero new deps), **minimal client kit** (local supervisor polling loop + AGENTS.md-style brief template + claim-loop reference implementation), (decision point) task-level token | **2a implemented and accepted on the live machine (2026-10-04, walking skeleton first—per §7 discipline, punch through the minimal loop before laying the tool face)**: migration 037 (runtime_kind + agent_credential); ExternalTaskService (Claim/ReportMessage/Complete/Fail, reusing the CompleteRun/FailRun settlement chain—M1 summary and task_settled projection inherited automatically); the four loop endpoints + the issuance endpoint (POST /agents/{id}/runtime-credentials, owner/admin); credentials v0 per the settled route (synthetic service user agent-\<id\>@runtime.internal + ork_ PAT + pat_id<->agent_id binding, ResolvePAT injects Principal.AgentID); write guards (composition root authMw∘guard—the agent subject may write only the loop + comments; the identity domain additionally forbids self-minting PATs); FailStaleRunning/FailRunningAtStartup exclude external; sweeper stranding alarm (60min threshold, log + metric); EnqueueAssignment/LeaderDispatch model fail-fast exempts external. **Full live script passed**: create external agent -> issue credentials -> PUT assign -> task stays queued (engine declines) -> runtime claims -> **still running after one 36s sweeper round (no false kill, proven)** -> messages backflow seq 1-3 -> complete -> summary auto-generated (last text taken) -> timeline top "task completed (dsh-runner): …"; human PAT false-claim 403, unauthorized write 403, PAT minting 403. **Two stock bugs fixed along the way**: PUT-assigning an agent always 500'd (the subscription-table user FK poisoned the transaction); the assignment enqueue cascade fires only on the Updated event (assign-at-create does not trigger—kept as-is; the user flow is "create first, assign after"). **2b implemented and accepted at protocol level (2026-10-04; acceptance runtime = dsh—user's call; the claude/codex live hookups were dropped)**: (1) the platform MCP tool face v0 landed—`/api/mcp/w/{workspaceId}/agent` (Streamable HTTP, workspace bound in the URL path—no custom client headers needed; the mcp-go server zero-new-dep promise cashed), 9 verbs = 4 reads (list_my_work_items / get_work_item L1 brief / get_task / get_task_messages) + 5 writes (claim / report_progress / complete / fail / post_comment), a same-source thin shell over REST (the composition root calls repo/service directly), write guards allowing that path; **real MCP protocol-level verification passed** (initialize handshake -> session -> tools/list -> tools/call, including the claim-report-settle closed loop through the tool face); (2) the client kit (`client-kit/`: README + AGENTS.md work-brief template + orkome-loop.sh supervisor reference—poll -> claim -> brief -> handler -> settle, a verified closed loop on Git Bash/cross-platform); (3) summary semantics fixed: an external complete's result is first landed as the final text message before settling—the summary takes the final conclusion, not intermediate progress (verified live). **The dsh live hookup completed (2026-10-04, accepted)**: a real dsh session natively connected to the MCP tool face (HTTP + Bearer, no middle layer—the "small exe bridge" was formally rejected in review: direct connection proven, a layer is pure burden), all 9 verbs exercised; **and with the platform instance not running, dsh autonomously started the server, wrote its own profile-patch config, and cleared its board (6 completed 1 failed, including two Chinese settlement summaries dsh wrote into the timeline)**—the first real-runtime verification of the autonomous digital colleague's full closed loop. Session evidence (internal record): 759 events, 100 steps. **2b technical remainder**: (decision point) task-level token |
| **Phase 3 (eyes: events)** | `GET /api/events` cursor stream, `work_item.assigned`/`task.settled` into the event catalog, event source co-built with plugin system Phase 1 (the outbox-consumer assumption verified once) | **Implemented and accepted live 2026-10-04 (co-built with plugin Phase 1)**: `GET /api/events?since=<unixnano>|<id>&limit=` keyset cursor reading event_outbox directly (**cursor semantics correction landed**: EventID is a UUID not a monotonic ULID—keyset is the only correct form); workspace isolation + resumed cursors verified; the event source is the outbox itself (the hookup point proven by the M1 spike), no separate projection table. **Residual minor item**: the standalone `work_item.assigned` type not added (the v0 catalog has work_item.updated—assignment changes discernible from its data; add the standalone type only when a real client shows the pain of judging, guarding against catalog bloat); `task.settled` is already carried by the timeline's task_settled entry (pull face), the push face covered by task.completed/failed, no semantic gap |
| **Phase 4 (governance)** | sensitive-verb authorization flow, operation-audit views, (evaluate) runlynk WS doorbell reuse | script §4.2 all 7 steps including automation creation; the audit can answer "whose agent created this automation" |
| **Phase 5 (ecosystem period, conditional start)** | client kit ecosystem polish (multi-runtime templates, docs, examples), the declarative runtime profile contract, API coverage audits made routine, credentials v1 (native agent principal + scope framework, triggers in §2.1), contract v1 freeze | the first third-party runtime plugin (not hand-held by us) appears |

Per-phase gates follow repo conventions: 61-package full tests, route contract test updates, new
e2e goldens.

---

## 8. Risks and open questions

1. **Two-face drift (REST vs the MCP tool face)**: the tool face must be a same-source thin shell
   (same service layer); hand-writing a second semantics is forbidden—otherwise behavioral drift
   becomes permanent support burden. Decide the "tool face auto-generated or thin-wrapped"
   mechanism before laying verbs.
2. **External liveness parameters and the queued timeout threshold** (narrowed in v0.3: direction
   set—exclude false kills + v1 message-as-heartbeat—what remains are parameters and alarm
   shape): the running no-message threshold starts at 24h; the queued alarm threshold and
   destination (whom to notify, whether to auto-transition to awaiting_input) decided in Phase 2.
3. **Concurrent claim semantics**: v0 status CAS (queued->running, reusing the `ClaimNext`
   SKIP LOCKED family semantics) already prevents double claims, but there is no lease—after a
   client crash the task sticks in running. Candidate: port runlynk's lease/fencing six-operation
   protocol (`internal/execution/service.go`) on demand, when multi-instance needs appear.
4. **Task-level token** (§2.3 implementation note 3): leaning to introduce (the reference
   implementation fail-closed reference beats long-lived PAT supremacy); cost
   = one extra thing in the claim response + one verification on the callback side; decided at
   Phase 2 kickoff.
5. **Doorbell channel ownership**: does polling -> event stream -> WS doorbell eventually merge
   into runlynk, or does orkome hold its own lightweight SSE/WS? Affects runlynk's positioning;
   a Phase 4 decision.
6. **Event-source hookup shared with the plugin system**: **verified 2026-10-04 by spike**—
   `event_outbox` rows carry workspace_id and payload jsonb, rows retained after publish (no
   production cleanup, only tests DELETE), the consumption machinery ready (composition root
   Relay.Handler = bus.Emit injection, `outboxx.go:61`; the webhooks/events domains need only
   bus.On-register one Phase to get both the fast path and the crash-backstop replay, at-least-
   once + idempotency contract explicit), write points already covering orchestration
   (Enqueue/Settlement) and work (Catalog/Comment/SourceContext). **Two remaining decisions**:
   (1) EventID is random UUID v4, not a monotonic ULID (`outboxx.go:33-34`, `service.go:759`)—
   either the events API cursor goes keyset `(created_at,id)` or the write side switches to ULID
   (knock-on for the plugin doc §2.3.1 envelope assumption); (2) the outbox has no retention
   cleanup and the table only grows (a pre-existing platform hazard, not introduced by this
   design)—the projection plan is unblocked (the delivery table carries its own retention), but
   `GET /api/events`'s 30-day retention needs its own cleanup.
7. **dsh's MCP support**: **verified 2026-10-04 (binary-evidence level)**—the DeepSeek Harness
   (dsh desktop) app.asar contains 202 `mcpServers` config keys; what remains is end-to-end
   onboarding verification, done at the Phase 2 skeleton day together with claude/codex. If
   end-to-end fails, dsh takes the REST + homemade client-kit path (cost rises, contract
   unchanged).
8. **A direct-conversation entry for runtimes**: casual @runtime chat inside the platform (not
   constituting a task)—the awaiting_input/interrupt system partially covers it; full chat
   @runtime is deferred until the need is verified.
9. **This design's long-term relationship with the executor model**: if "unattended batch
   execution" (the reference implementation's strength) is truly needed
   later, the right path is runlynk adding an agent adapter layer (its refactor docs already
   reserve a "return axis"), coexisting with this contract, not merging—two participant classes
   (executor/colleague), two loops; do not conflate.

---

## 9. Evidence-level appendix (added in v0.3)

**Four levels**:【proven】= code/run evidence (incl. file:line);【inferred】= reference-frame
analogy (reference implementation/Slack/GitHub etc.);【assumed】= unverified,
removable by spike;【bet】= product judgment, not code-verifiable, only governance-izable. **New
key claims must be labeled; old claims get back-labeled as audits proceed.**

| Claim | Level | Status/verification point |
|---|---|---|
| The timeline triple gap (activity shells / execution layer absent / comment double-render) | proven | found at `repo.go` ListTimeline / `wiring_work.go` projection (2026-10-04 audit); **fixed by hydration M1 the same day** |
| Hydration read-path gaps (pagination direction / missing single-task endpoint etc.) | proven | hydration doc §6 list, item-by-item file:line |
| mcp-go ships the server side (Streamable HTTP), zero new deps | proven | `go.mod:22` v0.43.0 + `vendor/.../server/streamable_http.go` (verified 2026-10-04) |
| Dispatcher excluding external = one line (archived precedent exists) | proven | `dispatcher.go:137-139` |
| runtime_kind is a clean incremental migration | proven | `007_orchestration.up.sql:1-16` has no kind column (037 added it) |
| PATs are user-principal only; no non-user principal exists | proven | `004_identity.up.sql:39-51`, `webx/principal.go:10-24` (Phase 2a's AgentID rides the user principal) |
| The existing watchdog would falsely kill external running tasks | proven | `repo.go:707-730` (heartbeat source = the engine instance `agent_runtime`); **fixed in Phase 2a—the scan excludes runtime_kind=external** |
| queued has no age timeout; skipping strands | proven | the sweeper then covered only running/awaiting; **mitigated in Phase 2a—60-min queued stranding alarm (log + metric)** |
| Callback contract shape (claim/messages/complete/fail) viable | inferred | isomorphic with the reference implementation's daemon callback API |
| Pull channels suffice (v0 polling -> cursor events) | inferred (conscious deviation) | the reference implementation/Slack both have outbound WS; we trade simplicity for latency, v2 doorbell backstop |
| Task-level token beats long-lived PAT | inferred | the reference implementation mints at claim + fail-closed reference |
| L1 brief 2-4KB / acceptance <8KB | assumed -> **preliminarily proven** | 2026-10-04 live measurement (synthesized real-size sample: 5-task history / 2 comments / 7 transcripts): detail 652B + timeline 1.1KB + tasks 4.1KB = **3 calls 5.8KB, within budget**; but at ~20 task-history entries the tasks list carrying full result breaks 8KB—hydration #9 (list slimming) is the long-history prerequisite; the M3 gate stands |
| dsh supports MCP | proven (**end-to-end**) | 2026-10-04 dsh live session: natively called all 9 `mcp__orkome__*` verbs (HTTP + Bearer direct, no bridge no exe), autonomously completed 6 tasks and wrote Chinese settlement summaries; session log kept in the internal record |
| acceptance_criteria writable via API | **falsified (new finding from the 2026-10-04 live test)** | the create DTO lacks the field (`work/adapters/http/handlers.go:24-33`), the update DTO too (`:73-89`)—the DB column and model exist and the read path returns it, but the public API has no write path. The first live example of the API-parity discipline (contract doc §6) |
| PAT (ork_) + Bearer + X-Workspace-Id CSRF-free end to end | proven | 2026-10-04 full live chain: GET /config bootstrap CSRF -> register -> PAT minting -> agent/work-item/comment CRUD all via PAT |
| The outbox can host external consumers | proven | **spike held 2026-10-04**: rows carry workspace_id/payload, retained after publish, the Relay Handler injection point ready (details and remaining decisions in open question 6) |
| EventID monotonic and sortable (ULID) | **falsified** | random uuid v4 (`outboxx.go:26-29`); Phase 3 resolved it by adopting the keyset `(created_at,id)` cursor (the envelope now sets EventID explicitly at write points, `service.go:1033-1040`, but it stays a random UUID) |
| API parity buys ecosystem and self-interface audiences | bet | §6 governance-ized; exit condition = if no third-party onboarding intent appears after two first-party verticals, re-evaluate |
| Zero-code (human-driven tier) holds for codex/claude | proven | both officially support external MCP servers (product fact) |
