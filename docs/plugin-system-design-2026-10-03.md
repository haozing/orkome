# Plugin System Design: an MCP-native plugin ecosystem (the five-piece interaction contract)

**Version**: v0.3.4 (2026-10-04. v0.3.2: Phase 1+2 platform mechanisms complete; v0.3.3:
Phase 3 deep-link UI complete—ui-tokens ticket exchange/consume (60s one-time JWT), the
pack_ui_entry entry registry, the frontend PackEntries renderer, full live chain incl. replay
rejection; piece 4 of the five landed, only 5 (slots) remains (conditional start—build only when
a real pain appears); v0.3.4: doc-audit pass—§2.3 event catalog/envelope/cursor aligned to the
as-built webhooks domain, §3 provisioning aligned to the install|uninstall as-built shape,
stale line refs refreshed)
**Decision basis** (settled in the 2026-10-03 positioning series, each item traceable):
1. **Platform positioning = substrate + plugin ecosystem**. orkome owns and evolves the
   substrate; first-party verticals (agentblog blogging, product selection, video production)
   land first as "first-party plugins"; third-party onboarding waits for the ecosystem period.
   Flagship verticals first, plugin mechanisms maturing behind the flagship—building a market
   first and waiting for sellers is a dead end.
2. **The only plugin shape = standalone service + pack config (MCP-native)**. A plugin is an
   independently deployed system (own system of record + MCP tool face) joining via a declarative
   pack (agents/skills/MCP registrations/UI/permissions/event subscriptions). **First-party and
   third-party go through the same door; privileged channels never open** (compile-time
   embedding/InMemoryTransport only as a future transport optimization, changing no contract and
   serving as no integration method).
3. **The domain-zero rule**: orkome never takes domain code, never takes domain data (no
   vertical's schema enters orkome's PG). Plugins bring only "tools + agents + skills +
   declarations"; data always lives in the plugin's own system.
4. **The five interaction pieces**: MCP (platform->plugin, synchronous) + API (plugin->platform,
   synchronous) + **event push (platform->plugin, asynchronous—direct push + query pull)** +
   deep-link UI (user -> plugin interface) + partial page slots (plugin interface inside the
   platform).
5. **Direct event push** (revised from "pull first, push later"): rationale a) this ecosystem
   already has a reference implementation (runlynk's webhook delivery: HMAC/retry/dead
   letter/replay); b) webhook push is table stakes for an application platform—build it sooner,
   use the real shape sooner. A read-only event query endpoint stays alongside push (the Stripe
   pattern: push and pull coexist).
6. **A small egressx change**: the https-only check at `internal/platform/egressx/egressx.go:69-71`
   becomes "allow http when the target IP is within `ENGINE_EGRESS_EXTRA_ALLOW_CIDRS`",
   uniformly covering the three outbound sites: agent base_url, MCP endpoints, webhook callback
   URLs. ADR 0007 accompanies the implementation.
7. **Contract freeze timing**: the pack format and the event catalog freeze into a public
   contract (v1) only **after the third first-party plugin lands**; until then everything is
   marked v0 internal, free to break.
8. **Explicitly excluded**: WASM/sandboxes (no hot-path need), micro-frontends, dynamic library
   loading (infeasible on Windows and ABI hell), shared databases (the WordPress anti-pattern),
   subprocess plugins (go-plugin style—duplicating the resident-service shape).

**Companion doc**: the first plugin's (agentblog) implementation plan §9, in the agentblog repo
(`doc/qu-agent-shishi-fangan.md`).

**Discipline**: events inform, never orchestrate; slots are platform-defined scarce resources;
permissions visible at install; all consumption idempotent; all outbound passes the SSRF gate.

---

## 0. One-sentence target shape

How third parties (and first parties) build professional systems on orkome: **deploy your own
service, expose an MCP tool face, submit a pack**. orkome handles assembly (agents/skills/
autopilots/UI entries/event subscriptions); users work in the orkome frontend/back office, agents
call the plugin via MCP to do work, the plugin reports back via API, receives event pushes, and
owns summary slots and jump entries inside the platform.

```
              (1) MCP (sync)  agent calls tools
   +------------+ ------------------------------> +----------------+
   | orkome     |                                 | plugin          |
   |  (substrate)| <------------------------------ | (standalone     |
   |            |  (2) API (sync) create items/   |  service + own  |
   |            | -------------->       query     |  data)          |
   |            |  (3) event push (async,          +--------+-------+
   | front/back |      webhook + query pull)              |
   |  office    |  (4) deep link (user jump, token handoff) | own UI
   +------------+  (5) slots (plugin summary embedded)      v
                                                 plugin console
```

---

## 1. Plugin definition and boundaries

### 1.1 A plugin = standalone service + pack

- **Standalone service**: the plugin deploys itself (any language/framework), with its own
  database and console UI, its own system of record. It exposes at least one MCP server (tool
  face), optionally deep-link entries and slot summary pages.
- **pack**: a declarative catalog (see §5) containing MCP registrations, agent definitions,
  skills, autopilots, event subscriptions, UI declarations, permission declarations. The
  provisioning tool installs all of it idempotently into a workspace.

### 1.2 What is not a plugin

- **A pure MCP server** (no pack, no stateful system): just a tool library—registrable and
  usable, but not a "plugin" (no ecosystem slot, no UI, no events).
- **runlynk**: positioned as the **substrate's hands layer** (local reach + execution ledger),
  registered into orkome as a workspace MCP—a substrate component, not a plugin.
- **Domain logic**: any proposal to "put vertical business code/table structures into orkome"
  directly violates the domain-zero rule; historically rejected in three shapes: the one-big
  backend migration (no), code domains moving in (no), compile-time pluginization (first-party
  only, demoted to an in-house framework—no).

### 1.3 Trust boundary

Plugin code never runs inside the orkome process/frontend runtime. A breached plugin != a
breached platform; a dead plugin only fails its own work items. The platform's trust in a plugin
extends only to: the data its MCP tools return (treated as ordinary tool results) and its PAT's
scope.

---

## 2. The five-piece interaction contract

### 2.1 (1) MCP: platform -> plugin (synchronous; agents call tools)

**Status (in place)**:
- Workspace-level registration: `POST /api/workspaces/{id}/mcp-servers` (DTO `mcpWire`:
  name/transport/url/credentials.headers—credentials injected into HTTP headers only, never in
  responses); agent-level same-name override: `ResolveForEngine`
  (`internal/orchestration/app/ports.go:552`).
- Per-task engine assembly: `buildMCPTools` (defined at
  `internal/orchestration/adapters/engine/tools_mcp.go:104`, call site engine.go:254)—live
  Resolve -> ValidateURL -> mcp-go client; Start+Initialize+ListTools on a 10s budget, 32-tool
  cap per server, cross-server name collisions dropped, the assembly snapshot persisted into
  `task.context.mcp_servers`.

**Enhancements in this design (Phase 1-2)**:
1. **Tool catalog cache** (implemented in Phase 2, ADR 0011): a process-level persistent-client
   cache keyed by (transport, URL, credential headers), idle 5min lazy reclaim—a hit skips the
   entire Start/Initialize/ListTools handshake; config changes re-key and re-handshake
   immediately.
2. **Naming discipline**: tools registered by a pack must carry the plugin-name prefix
   (`agentblog_get_post`); pack-provision validates the `<server-name>_*` prefix at install
   time, failing the install on violation (the assembly-time best-effort drop stays—tasks are
   never blocked by plugin naming); `--no-prefix-check` exempts servers that don't follow the
   prefix convention.
3. **Credential rotation**: credentials.headers support no-downtime updates (the PUT endpoint
   exists; a rotation guide doc is still owed).

### 2.2 (2) API: plugin -> platform (synchronous)

**Status (in place, nothing new needed)**: REST + PAT (`Authorization: Bearer ork_...`, PATs
CSRF-exempt) + `X-Workspace-Id`. The plugin-side minimal set all exists:
- Create/assign work items: `POST /api/work-items` (assignee_type=agent enqueues immediately),
  `PUT /api/work-items/{id}`
- Query tasks and results: `GET /api/work-items/{id}/active-task`,
  `GET /api/work-items/{id}/tasks`, `GET /api/tasks/{id}/messages`,
  `GET /api/work-items/{id}/usage`
- Rerun/cancel: `POST /api/work-items/{id}/rerun`, `POST /api/tasks/{id}/cancel`
- Event query pull: see §2.3

**Enhancement (open question, Phase 1 decision)**: today's PAT carries full workspace power.
Plugin onboarding recommendation: one dedicated PAT per plugin (owned by a dedicated service
account), revocable, usage attributable (task_usage already records the originator per task).
**PAT scope subdivision** (read-only / work-items-only / events-only) is a long-term enhancement,
not blocking—govern the first-party period by "dedicated account + dedicated PAT".

### 2.3 (3) Event push: platform -> plugin (asynchronous—direct push + query pull)

**This is the plan's largest new subsystem.**

#### 2.3.1 Event catalog v0 (the contract's core)

Unified envelope (as built, `internal/contract/events` CatalogEvent; entity IDs ride inside
`data`—there is no separate `related` object):

```json
{
  "id": "b1e4c7d2-...-9af3",
  "type": "task.completed",
  "occurred_at": "2026-10-03T08:00:00Z",
  "workspace_id": "...",
  "data": { "task_id": "...", "work_item_id": "...", ... }
}
```

Event catalog v0 as built (five types; enough is enough, prefer missing over bloat; new types
ride minor versions with a recorded rationale):

| type | data highlights |
|---|---|
| task.queued / task.dispatch | contract constants exist; **not in the push catalog** (only terminal states push) |
| **task.completed** | full task result (a <=1KB summary slice is the planned tightening), usage totals |
| **task.failed** | failure_reason, error, attempt |
| work_item.created / work_item.updated | status, assignment changes |
| comment.created | comment summary |

**Rules**: event payloads are summaries, not bodies (<=1KB); large content always "take the ID,
call the API". **Events inform, never orchestrate**—reactions to events are always recorded in
orkome's automation (autopilot/wakeup) or pack declarations; implicit chains of "plugin A's event
directly triggers plugin B's action" are forbidden.

**Data-schema definition vehicle**: Go types in `internal/contract/` define each type's data
structure (single source of truth); external JSON Schema docs are generated from them, preventing
doc drift.

#### 2.3.2 Subscription API (new)

```
POST   /api/webhook-subscriptions        {url, events: ["task.completed",...], description}
GET    /api/webhook-subscriptions
DELETE /api/webhook-subscriptions/{id}
POST   /api/webhook-subscriptions/{id}/test          # send a ping event to verify connectivity
GET    /api/webhook-subscriptions/{id}/deliveries?status=failed&limit=...   # limit default 50, max 200
POST   /api/webhook-subscriptions/{id}/deliveries/{deliveryId}/replay
```

- The subscription URL **must pass egressx validation** at creation (the same allowed-segment
  semantics as decision 6); each POST creates a new subscription row—idempotent installs are
  pack-provision's job (it skips by URL).
- Creating/deleting subscriptions requires the workspace **owner/admin** role—subscriptions are a
  sensitive data-egress face, on par with `POST /api/credentials`.
- The secret is platform-generated, used only for signing, returned exactly once at creation,
  never echoed in GET responses.

#### 2.3.3 Delivery protocol (runlynk/Stripe homework)

- **Signature headers**: `X-Signature: sha256=HMAC(secret, timestamp + "." + body)`,
  `X-Timestamp` (±5min anti-replay window), `X-Delivery-Id`, `X-Event-Id`.
- **Semantics**: at-least-once. First attempt + 4 retries (backoff 5m/30m/2h/6h—claiming
  increments attempts, so the backoff table's first slot never applies), then dead letter; dead
  letters queryable and replayable (the deliveries/replay endpoints above).
- **Response convention**: plugin 2xx = success; timeout 10s.
- **Consumption discipline** (first line of the onboarding doc): dedup idempotently by
  `X-Event-Id` (i.e. envelope.id).
- **Storm protection (v0 shape)**: delivery runs on a single serial worker per replica—a slow
  callback back up only its own subscription's queue, never other subscriptions'; per-subscription
  concurrency caps and a global rate cap are deferred until a throughput need appears.
- **Delivery implementation**: a resident worker scanning the delivery table (SKIP LOCKED
  claiming, multi-replica safe—the same pattern as the Dispatcher).

#### 2.3.4 Query pull (push and pull coexist)

```
GET /api/events?since={cursor}&limit=     # cursor = "<unixnano>|<id>" keyset (created_at, id), workspace-scoped
```

For debugging, missed-push catch-up, reconciliation. As built there is no `GET /api/events/{id}`
single-event fetch, and the outbox has **no retention cleanup yet** (rows accumulate; a retention
policy/cleanup worker is an open item).

#### 2.3.5 Code landing spots

- **New domain `internal/webhooks/`** (app/adapters{http,pgrepo,workers} three layers, obeying
  the depguard rules): subscription CRUD, delivery worker, dead letter/replay.
- **The event catalog (type list and envelope types) lands in `internal/contract/`** (the
  cross-domain vocabulary layer).
- **The event source (implemented)**: event_outbox reuse, verified by the 2026-10-04 spike—rows
  carry workspace_id and payload and are retained after publish; the webhooks domain
  bus.On-registers the PhaseProject phase (the relay's Handler = bus.Emit injection point), and
  `GET /api/events` reads event_outbox directly with a keyset cursor. The design-time alternate
  route (orchestration emitting standard events at settlement) was not needed.

### 2.4 (4) Deep-link UI: the user jumps from the platform to the plugin interface

**Flow**:
1. The orkome frontend calls `POST /api/ui-tokens {plugin: "agentblog", workspace_id}` -> a
   one-time token (JWT, exp 60s, jti single-use, claims user/workspace/plugin).
2. Render the jump per the pack-declared `ui.entry.urlTemplate` (template variables:
   `{uiToken}`, `{workspaceId}`, optional context variables like `{workItemId}`).
3. **The plugin verifies the ticket**: the plugin backend calls orkome
   `POST /api/ui-tokens/consume {token}` -> `{user_id, workspace_id, plugin}` (consume is
   one-time, anti-replay; expiry is enforced inside the consume, not echoed). The plugin plants
   its own session and 302s to the target page.
4. The plugin manages its own session; orkome shares no cookies.
5. **Template-variable safety**: context variables ({workItemId} etc.) must be URL-encoded and
   pass a character whitelist before injection into the URL—anti-injection.

**Code landing spots**: `internal/identity/` (new ui-tokens endpoints—essentially a
cross-system assertion of user identity); the frontend entry rendering reads the pack registry.
**Optimization (long-term)**: publish JWKS for local plugin verification, saving the consume
round trip; v0 uses the consume endpoint (simple, revocable).

### 2.5 (5) Partial page slots: plugin summaries embedded in platform pages

**Slot catalog v1 (only two, platform-defined, plugins apply to occupy)**:

| Slot | Location | Content convention |
|---|---|---|
| `workitem.detail.tab` | a new tab on the work item detail page | plugin views related to that work item (e.g. "blog post preview") |
| `workspace.dashboard.card` | a workspace dashboard card | site-level summaries (e.g. "3 pending review · QA avg 0.82") |

**Rules**:
- Slots = **summary views**: read-only, lightweight, within one screen; heavy interaction always
  goes through an "open full console" button via (4) deep links.
- Rendering: an iframe embedding the pack-declared slot URL + a one-time ui-token (reusing §2.4
  ticket exchange). iframe `sandbox="allow-scripts allow-same-origin allow-popups"`; the plugin
  side must set `Content-Security-Policy: frame-ancestors 'https://<orkome domain>'`—at slot
  registration the platform GETs the slot URL to probe the response header, **verifying the
  value contains the orkome domain** (mere "header exists" does not pass—'none' passes that too);
  non-compliance rejects listing (anti-phishing abuse).
- Size protocol: after loading, the plugin
  `postMessage({type:"pack.resize", height}, targetOrigin)`; the parent listens and adjusts. On
  a 3s timeout without a message, the declared height is used.
- Once a slot has third-party use, the catalog freezes into the v1 contract (same as decision 7).
**Code landing spots**: the frontend (web/) gains slot-rendering components + registry reads; no
new backend domain (the pack registry stores slot declarations).

---

## 3. pack format v0 (internal use; freezes to v1 after the third first-party plugin)

```
agentblog-pack/
  pack.yaml
  agents/
    writer.yaml        # name/description (system prompt)/runtime_config (credential placeholder refs)/max_concurrent
    curator.yaml
    reviewer.yaml
  skills/
    generation/SKILL.md    # generation methodology (frontmatter: name/description—description must state when to use)
    quality/SKILL.md       # E-E-A-T rubric
  autopilots.yaml       # optional: scheduled rules
```

`pack.yaml`:

```yaml
apiVersion: pack/v0
name: agentblog
version: 0.1.0
credentials:                  # placeholders the installer fills
  - key: AGENTBLOG_MCP_TOKEN
    description: agentblog MCP Bearer Token
mcp:
  - name: agentblog
    transport: http
    url: http://agentblog:8080/mcp/
    credentialRef: AGENTBLOG_MCP_TOKEN
events:                       # declarative subscriptions (provisioning creates webhook-subscriptions)
  - url: http://agentblog:8080/internal/v1/orkome/webhook
    types: [task.completed, task.failed]
ui:
  entry:
    label: Blog system
    urlTemplate: https://blog.example.com/console?token={uiToken}&ws={workspaceId}
  slots: []                   # enabled after Phase 4
permissions:                  # shown to the user at install (permissions upfront)
  tools: [12-tool list]
  events: [subscription list]
  data: [posts read/write, plan read/write, metering notes]
```

**Provisioning tool** (Phase 2, `cmd/pack-provision`—a pure API client, not a domain):
`pack-provision install|uninstall <pack-dir> --workspace <id> --pat <token>` (no upgrade
subcommand—an entity that already exists is skipped, so re-running install reconciles; a real
replace/upgrade path waits for a live need).
Idempotent install = calling existing APIs in order: MCP registration (credential headers
injected via `--credential KEY=VALUE` flags, not through `POST /api/credentials`) -> tool-prefix
check -> agents -> skills (`POST /api/skills` + files + bindings) -> webhook subscriptions ->
ui-entry registration.
Uninstall cleans in reverse—webhook subscriptions and skills deleted, **agents archived not
hard-deleted** (preserving task-history/usage FK semantics), **MCP registrations hard-deleted**,
ui-entry removed. (A version-compatibility matrix is deliberately absent from packSpec v0—no
compatibility field is parsed or validated at install; revisit when the pack format freezes to
v1.)

**Cross-pack naming discipline**: entity names inside a pack must carry the pack prefix (agent
`agentblog_writer`, skill `agentblog_generation`, MCP server `agentblog`); cross-pack name
collisions detected at registration are rejected outright, preventing two packs overwriting each
other. **No general tooling abstraction before the second vertical—write the agentblog-specific
script first.**

---

## 4. Permission and security model

1. **Permissions upfront (two steps)**: the pack's `permissions` section **is declaration-only
   for now** (which tools registered, which events subscribed, which slots occupied, how
   metered—zero cost, constrains the pack format, auditable, fully revoked on uninstall); the
   "install-confirmation page shown to users" is a marketplace-period thing (its beneficiary is
   the non-author installer, who does not exist before the third-party ecosystem), built with
   Phase 5, risk-tiered display in the same batch (see the §6 Phase 5 memo).
2. **Identity**: one dedicated service account + dedicated PAT per plugin; credentials (LLM keys
   etc.) via `POST /api/credentials` (AES-256-GCM sealed, owner/admin). Never send platform
   credentials to plugins; the future shape of plugins "borrowing model capability" goes through
   MCP sampling reverse requests (LSP-style bidirectional), never API keys.
3. **Unified outbound SSRF**: agent base_url, MCP URLs, and webhook callback URLs all pass
   egressx (including this plan's small change); private networks must be explicitly allowed
   ranges.
4. **Signature and anti-replay**: webhook HMAC + time window; ui-tokens one-time + short-lived +
   burn-on-consume.
5. **Isolation**: iframe sandbox + the plugin-side frame-ancestors whitelist + separate domains
   (no cookie sharing).

---

## 5. Data and consistency

- **No cross-system transactions**: all cross-boundary writes follow "idempotency key +
  compensation". The reference is agentblog itself: confirming a plan item reserves quota -> a
  generation-failure event -> refundSweeper refunds per failed run.
- **Large content never rides events**: events are <=1KB summaries + ID references; bodies are
  pulled via API.
- **orkome never creates tables for plugins**: plugin data lives in the plugin's database;
  orkome keeps only its own books (tasks, subscriptions, deliveries, usage).

---

## 6. Phased implementation and acceptance

| Phase | Contents | Acceptance |
|---|---|---|
| **Phase 0 (achieved)** | (1) MCP + (2) API | production paths exist (G8 goldens etc.) |
| **Phase 1** | **(3) Event push**: the egressx change (+ADR 0007), the `internal/webhooks` domain (subscriptions/delivery/HMAC/retry/dead letter/replay), event catalog v0 into contract, the `GET /api/events` query endpoint, task.completed/failed first | **Implemented and accepted live 2026-10-04**: the egressx http narrow gate (all addresses must hit exception ranges, fail-closed, ADR 0007); the webhooks domain landed (subscription CRUD owner/admin + one-time secret echo, HMAC-SHA256(ts.body) signature headers, SKIP LOCKED delivery worker, backoff 1m/5m/30m/2h/6h -> dead letter, replay, test ping); **table-name correction**: the delivery table is `event_delivery` (`webhook_delivery` already taken by the automation domain); `GET /api/events` keyset `(created_at,id)` cursor reading event_outbox directly (**§2.3.4
correction**: EventID is UUID v4, not a monotonic ULID—cursors go keyset; the envelope
monotonic-sort assumption is void); event catalog v0 = task.completed/failed + work_item.created/updated + comment.created (task:message never enters push). Acceptance evidence: httptest fake-plugin signatures verifiable (HMAC recompute compare), retry/dead-letter/replay full-chain INT tests + **live full chain** (comment-triggered task -> engine failure -> task.failed -> the fake plugin at 127.0.0.1 receives the signed POST—http through the ADR 0007 narrow gate) |
| **Phase 2** | **pack v0 + provisioning** (agentblog-specific script), the MCP tool catalog cache, tool-name prefix validation | **Platform mechanisms implemented and accepted live 2026-10-04 (fake plugin first; the real agentblog pack swaps URLs once its side's endpoints are ready)**: (1) catalog cache—a process-level persistent-client cache in `tools_mcp.go`, key=(transport+URL+credential headers), a hit skipping Start/Initialize/ListTools, idle 5min lazy reclaim (no background goroutine); real-MCP fake-service test: two assemblies share one Initialize; config change (new key) re-handshakes. (2) Prefix validation—`pack-provision` probes the tool list via an mcp-go client at install, enforcing the `<server-name>_*` prefix; violation or unreachability fails the install (§2.1 enhancement 2's "explicit error" lands at install time—the assembly-time best-effort drop stays, tasks not blocked by plugin naming); `--no-prefix-check` exempts servers that don't follow the prefix convention. (3) `cmd/pack-provision`—a pure API client (imports no domain): pack.yaml v0 + agents/*.yaml + skills/*/SKILL.md (frontmatter); install chain = MCP registration (idempotent skip) -> prefix check -> agents -> skills (SKILL.md upload + agent binding) -> webhook subscription; uninstall in reverse (subscriptions/skills deleted, agents archived not hard-deleted, MCP removed). **e2e against a real service**: install -> second install fully idempotent (exactly one of each entity) -> uninstall cleans correctly. Evidence: `cmd/pack-provision/provision_test.go` (always-run unit tests + ORCH_E2E_*-gated e2e) |
| **Phase 3** | **(4) Deep-link UI**: `POST /api/ui-tokens`(+consume), pack ui.entry rendering | **Implemented and accepted live 2026-10-04**: identity domain `UITokenService`—60s JWT (mechanism in webx.SignUIToken/VerifyUIToken, rules 2/3: the app layer holds no third-party libs) + jti persisted with CAS single consumption (anti-replay) + on-record/ticket claim consistency validation; the two endpoints `POST /api/ui-tokens` (member ticket exchange) and `/api/ui-tokens/consume` (authentication suffices—the plugin backend calls with its PAT, itself verifying the plugin claim is its own). The entry registry `pack_ui_entry` (orchestration domain; pack-provision writes from pack.yaml ui.entry, idempotent upsert) + `GET /api/ui-entries` (the frontend rendering source) + the frontend sidebar PackEntries component (renders nothing without packs; click -> ticket exchange -> template render {uiToken}/{workspaceId} (URL-encoded) -> new-window jump). **Live full chain**: register a fake plugin entry -> exchange -> consume (returns user/workspace/plugin claims) -> **replay 401** -> template-rendered jump lands with the full token. Integration tests cover replay-reject/expiry-reject (clock adjustable). JWKS local verification (saving the consume round trip) stays long-term as written |
| **Phase 4 (conditional start)** | **(5) Slots v1**: the two slots, the iframe protocol, resize, frame-ancestors validation. **Trigger**: operations actually producing the "deep-link jumping is annoying, want summaries inside the platform" pain (beneficiary test: at N=1 the deep link already suffices—if no beneficiary exists, don't build) | agentblog's "post preview tab" and "dashboard card" usable in the orkome frontend |
| **Phase 5 (long-term, only with external pull)** | Registry/marketplace/review, PAT scopes, JWKS local verification, pack v1 freeze, **the permission confirmation page with risk-tiered display, slot-contract semver, degraded three-state** (all three benefit third-party ecosystem participants—memo'd here, not designed early) | starts after the third first-party plugin lands |

Per-phase gates follow repo conventions: 61-package full tests, route contract test updates
(route catalog reconciliation), new e2e goldens.

---

## 7. agentblog as the first plugin—integration mapping (reference)

| Five pieces | agentblog landing |
|---|---|
| (1) MCP | tool-face expansion: reads (get_post/serp_metrics/get_link_candidates), writes (create_post_draft/set_*/submit_quality_judgment/submit_for_review); QuotaGuard stays inside the tool implementations. Retrieval/material tools (search_knowledge/get_source_docs/submit_documents) were dropped along with agentblog's knowledge-base removal (its plan v1.2 §4.5); future RAG is plugin-side |
| (2) API | full-server's OrkomeExecutor: PAT creates work items, assigns writer agents |
| (3) Events | subscribe task.completed/failed -> replacing the polling watcher: FinishRun/refund/alerting |
| (4) Deep links | console entry registered, pack ui.entry |
| (5) Slots | the work-item "post preview tab", the dashboard "pending review/QA" card |

See the agentblog repo's implementation plan §9, `doc/qu-agent-shishi-fangan.md` (full MCP
tool-face list and the orchestration mapping).

---

## 8. Risks and open questions

1. **Event-catalog bloat**: uncontrolled new types would ruin the contract—in the v0 era each
   added type needs an ADR-level recorded rationale.
2. **Delivery storms**: task message-level events (task:message) **never enter** the push catalog
   (high volume and WS already has the realtime channel); only task terminal states and
   work-item-level events are pushed.
3. **task messages have no cursor** (today limit+seq): the event query endpoint must carry a ULID
   cursor—do not reuse messages pagination semantics.
4. **The dual-track temptation**: the moment first-party plugins get an
   "embedded/shared-library" backdoor, this design is declared dead—the discipline goes into an
   ADR.
5. **Slots are real estate**: the dashboard card count needs a cap (e.g. 8 per workspace),
   trimmed by recency beyond that.
6. **The webhook vs automation boundary**: subscriptions "notify the plugin"; autopilot/wakeup is
   the "platform's own reaction"—the two can coexist, but the docs must state clearly who owns
   what, preventing users from building half on each side.
7. **The missing external-signal direction** (the orchestration home of connector plugins): in
   the five pieces, events are one-way "platform->plugin" only, and the discipline says "events
   inform, never orchestrate". When connector-type plugins appear—GitHub PR integration being the
   likeliest first (PR merged -> auto-close the work item; checks all green -> wake
   downstream)—plugins can only each hard-code a private "receive external webhook -> call the
   API to change state/properties" orchestration, and today's automation predicates support only
   status/priority eq/ne—"merge after checks are green" cannot be expressed. Candidate solution:
   the platform provides `POST /api/external-signals` (idempotency key + subscriber validation)
   feeding wakeup predicates—plugins only report facts, reactions stay declared in orkome
   automation, breaking no "events inform" discipline. **Trigger**: decided when the first
   connector plugin (GitHub/Slack etc.) kicks off; this item must be re-examined before the v0
   event catalog freezes into the v1 contract (decision 7), preventing private orchestration
   layers growing in each connector beyond convergence.
