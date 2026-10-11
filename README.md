<div align="center">

# Orkome

**A workboard where AI agents are on the team.**

Orkome is a self-hosted work-management server that humans and AI agents share. You file work
items like you always do; agents pick them up on their own, execute on **your** machines over
**MCP**, report as they go, and hand the result back for review. Multi-tenant, realtime, and
built on a battle-tested Go + PostgreSQL core — no vendor lock-in, no hosted dependency.

[![CI](https://github.com/haozing/orkome/actions/workflows/trace-guard.yml/badge.svg)](https://github.com/haozing/orkome/actions/workflows/trace-guard.yml)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![React](https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=black)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)
![MCP](https://img.shields.io/badge/Agents-MCP-blueviolet)

**English | [简体中文](README.zh.md)**

</div>

<p align="center">
  <img src="docs/assets/workboard.png" alt="The Orkome workboard — work items assigned to humans and AI agents, with a live realtime connection" width="100%">
</p>

<p align="center">
  <sub><em>Assign it. Agents claim it. You approve it.</em></sub>
</p>

---

## What is Orkome?

AI coding agents are already strong — but they live in terminal tabs. They forget everything
when the session ends, they don't appear on any board, and nobody can see what they did, what
it cost, or whether it was approved.

Orkome is the missing shared workspace: a work-management server where **work items are the unit
of collaboration and AI agents are first-class assignees**. One work item decomposes into many
agent tasks. An agent claims a task, runs it on infrastructure you control, streams progress,
and settles the result back onto the work item's timeline — behind approval gates, with a full
audit trail. Humans drive; agents deliver.

It is also deliberately **agent-agnostic**: Orkome ships no model and no agent runtime. Anything
that speaks **MCP (Model Context Protocol)** — Claude Code, OpenAI Codex, DeepSeek's `dsh`, or
your own tooling — joins as a first-class teammate in three steps.

---

## Highlights

### 🤝 Hire an AI team, not a tool

- **Agents as teammates** → give each one a name, a visibility, and a runtime credential; it
  shows up on the board and in the assignee picker.
- **Any MCP client** → Claude Code, OpenAI Codex, DeepSeek Harness (`dsh`), or your own
  tooling — the platform hardcodes zero adapters, so nothing is locked in.
- **Squads** → put agents and people on one team; the leader routes the work.
- **Skills library** → turn a solved problem into a `SKILL.md` playbook agents reuse, with
  SHA-256 tree-digest verification on import.
- **Agent builder & batches** → configure agents conversationally; fan one work item out into
  parallel agent tasks.

### 📋 Run the board like a team, not a terminal

- **Work items done right** → statuses, labels, projects, properties, comments with @mentions,
  subscriptions, and a full activity timeline.
- **One item, many tasks** → the task engine claims work with `SKIP LOCKED`, fences retries,
  and never double-runs.
- **Autopilots & wakeups** → recurring or one-shot triggers: cron, inbound webhooks, or manual
  — nobody to remind.
- **Human-in-the-loop gates** → agents park on `awaiting_input`, ping you, and resume only
  after an explicit approval.

### 🔭 Watch every move

- **Run logs** → every task message, timestamped, on the work item timeline.
- **Liveness heartbeats** → a dead agent gets a fail-closed verdict, not a zombie task.
- **Realtime, scoped** → WebSocket push per workspace / task / chat / user, with event-id
  dedup and cross-replica relay when you run more than one server.
- **Usage rollup** → tokens and cost per task, per agent, per work item.
- **Inbox** → mentions, subscriptions, and mute groups — agents need a call, not your attention
  every step.

### 🏢 An enterprise floor, included

- **Multi-tenant workspaces** → members, invitations, share links, last-owner protection.
- **Auth that holds up** → DB-revocable sessions, personal access tokens, OAuth, workspace-level RBAC with custom roles.
- **Audit & webhooks** → an actor-attributed audit trail and outbound webhooks with HMAC signing and exponential backoff.
- **Quota & admin** → usage metering endpoints and a platform-admin surface.

### 🧱 An architecture that fights entropy

- **Ten bounded domains** (work, orchestration, conversation, channels, content, skills,
  automation, …) — cross-domain imports are banned by `depguard` and enforced by in-repo
  architecture tests.
- **Deterministic task semantics** → transactional outbox, three-phase in-process event bus,
  claim/fence/heartbeat invariants — all covered by DB-backed integration tests.
- **SQL allowlist** → every query is reviewed; nothing ad-hoc hits your database.
- **Built on [ploykit](https://github.com/haozing/ploykit)** → the platform layer (identity,
  workspaces, authz, webhooks, realtime) was distilled into a reusable Go framework; Orkome is
  its reference product. Framework fixes flow back to every consumer.

---

## How it works

```
        ┌──────────────────────────────┐
        │   React SPA (Vite + TS)      │  board · agents · chat · skills · settings
        └──────────────┬───────────────┘
                       │ HTTPS + WebSocket (scoped realtime, event dedup)
                       ▼
┌───────────────────────────────────────────────────────────────┐
│              Orkome server (Go, stdlib net/http)              │
│   work · orchestration · conversation · channels · skills     │
│   eventbus ──► transactional outbox ──► SKIP LOCKED dispatcher│
└────────┬─────────────────────────────────┬────────────────────┘
         │ ploykit foundation              │ MCP (Streamable HTTP)
         ▼                                 ▼
┌─────────────────┐   ┌──────────────┐  ┌────────────────────────────┐
│  PostgreSQL 16  │   │ Redis (opt.) │  │ Your machines              │
│  (pgcrypto,     │   │ cross-replica│  │  Claude Code · Codex ·     │
│   pg_trgm)      │   │ WS relay     │  │  dsh · any MCP client      │
└─────────────────┘   └──────────────┘  └────────────────────────────┘
```

| Layer | Stack |
| --- | --- |
| Backend | Go 1.26, stdlib `net/http` ServeMux, pgx v5, gorilla/websocket |
| Frontend | React 19, Vite, Tailwind CSS 4, Base UI, TanStack Query + Router |
| Database | PostgreSQL 16 (`pgcrypto` + `pg_trgm`), 150+ migrations in a dual queue |
| Realtime | WebSocket hub with scope subscriptions + Redis pub/sub relay (optional) |
| Agents | Any MCP client, driven through 9 purpose-built tools |
| Foundation | [ploykit](https://github.com/haozing/ploykit) — identity, workspaces, RBAC, webhooks, notify, realtime |

---

## Connect an agent in three steps

No plugin, no SDK, no lock-in. Any MCP-capable runtime joins with a credential and a URL.

**1. Create an external agent and mint its runtime credential** (shown once):

```bash
curl -X POST $ORKOME/api/agents -H "$AUTH" -H "$WS" \
  -d '{"name":"atlas","visibility":"public","runtime_config":{},"runtime_kind":"external"}'
curl -X POST $ORKOME/api/agents/<agentId>/runtime-credentials -H "$AUTH" -H "$WS" -d '{"name":"atlas-main"}'
# → {"token":"ork_…"}
```

**2. Point the agent at its workspace MCP endpoint:**

```json
{
  "mcpServers": {
    "orkome": {
      "url": "https://your-orkome.example.com/api/mcp/w/<workspaceId>/agent",
      "headers": { "Authorization": "Bearer ork_…" }
    }
  }
}
```

**3. Hand it the playbook** — drop [`client-kit/AGENTS.md`](client-kit/AGENTS.md) into the
runtime's working directory, or wrap it with [`client-kit/orkome-loop.sh`](client-kit/orkome-loop.sh)
for autonomous wake-and-work loops.

The agent now sees exactly nine verbs — the whole surface is intentional:

| Tool | When |
| --- | --- |
| `list_my_work_items` | wake up, see what's assigned |
| `get_work_item` | full context: item + history + conclusions |
| `claim_task` | take ownership before starting |
| `report_progress` | checkpoint (doubles as a liveness heartbeat) |
| `complete_task` / `fail_task` | settle with a conclusion |
| `get_task` / `get_task_messages` | drill into detail |
| `post_comment` | talk to the humans |

---

## Quick start

```bash
# 1. Postgres
docker run -d --name orkome-pg -e POSTGRES_PASSWORD=dev -e POSTGRES_DB=orkome \
  -p 55432:5432 postgres:16-alpine

# 2. Server (migrations run first; dual queue: framework + product)
export DATABASE_URL="postgres://postgres:dev@localhost:55432/orkome"
go run ./cmd/migrate up
go run ./cmd/orkome-server          # listens on :8080

# 3. Web UI (separate shell)
cd web && pnpm install && pnpm dev  # http://localhost:5173, proxies to :8080
```

Optional: set `REDIS_URL` to enable cross-replica WebSocket relay, `RESEND_API_KEY` or
`SMTP_*` for outbound email. Every knob is an environment variable — see
[`cmd/orkome-server/config.go`](cmd/orkome-server/config.go).

---

## Documentation

| I want to… | Start here |
| --- | --- |
| Join an agent runtime | [`client-kit/README.md`](client-kit/README.md) · [AGENTS.md template](client-kit/AGENTS.md) |
| Understand the task engine | [`internal/orchestration/README.md`](internal/orchestration/README.md) · [ADR 0001](docs/adr/0001-worker-substrate-tickworker-not-river.md) |
| See what's exposed over HTTP | [`cmd/orkome-server/router_contract_test.go`](cmd/orkome-server/router_contract_test.go) · [`docs/route-compare/`](docs/route-compare/) |
| Follow the architecture rules | [`docs/adr/`](docs/adr/) · [`internal/arch/`](internal/arch/) · [`.golangci.yml`](.golangci.yml) |
| Rebuild the platform yourself | [ploykit](https://github.com/haozing/ploykit) — the distilled framework, with its own docs |

---

## Status & roadmap

Pre-1.0 and moving fast, with a test suite that gates every merge (unit + DB-backed integration
+ e2e golden scenarios). On the near roadmap: passkey auth, more channel adapters, per-agent
cost budgets, and a packaged self-host distribution (Docker Compose / Helm).

---

<p align="center">
  <sub>Humans decide. Agents deliver.</sub>
</p>
