# External Runtime Onboarding Guide (dsh / claude / codex / any MCP client)

> Audience: operators who want a local CLI agent to become a "digital colleague" in an orkome
> workspace. Companion design: `agent-client-contract-design-2026-10-04.md`; tooling:
> `client-kit/`.

## Prerequisites

- The orkome server running (hereafter `$ORKOME`; e.g. `http://localhost:8080` locally—the
  default port, adjust to the instance's actual port—`https://orkome.com` in production)
- A workspace (hereafter `$WS`) and one of its owner/admin PATs (hereafter `$ADMIN_PAT`)
- Your runtime (dsh/claude/codex…) supports external MCP servers (Streamable HTTP + Bearer header)

## Three-step onboarding

### 1. Create an external agent + issue a runtime credential (admin, once)

```bash
# Create the agent (runtime_kind=external is the key—the engine will not take the order;
# it waits for your client to claim)
curl -X POST $ORKOME/api/agents   -H "Authorization: Bearer $ADMIN_PAT" -H "X-Workspace-Id: $WS"   -H "Content-Type: application/json"   -d '{"name":"dsh-runner","visibility":"public","runtime_config":{},"runtime_kind":"external"}'

# Issue the credential (the token is shown only once—store it)
curl -X POST $ORKOME/api/agents/<agentId>/runtime-credentials   -H "Authorization: Bearer $ADMIN_PAT" -H "X-Workspace-Id: $WS"   -H "Content-Type: application/json" -d '{"name":"main"}'
# -> {"token":"ork_..."}   <- this is the runtime's entire credential
```

### 2. Configure the MCP server in your runtime

Standard mcpServers config (the workspace binding lives in the URL path—nothing beyond the auth
header is needed):

```json
{
  "mcpServers": {
    "orkome": {
      "url": "$ORKOME/api/mcp/w/$WS/agent",
      "headers": { "Authorization": "Bearer ork_..." }
    }
  }
}
```

Use `http://localhost:8080` (or the instance's actual port) for `$ORKOME` locally,
`https://orkome.com` in production.

dsh: the config lives in `~/.dsh/profiles/desktop/cordis.patch.yml` (or the in-app MCP settings).
Verified 2026-10-04: dsh connects natively with no bridging layer.

### 3. Give the agent a work brief

Drop `client-kit/AGENTS.md` into the runtime's working directory (or paste it into the session),
and it will know the loop: board (`list_my_work_items`) -> full picture (`get_work_item`) ->
claim (`claim_task`) -> progress (`report_progress`) -> settle (`complete_task`/`fail_task`) ->
communicate (`post_comment`).

Then say to it: "**check what work I have on the platform**".

## Tool face (9 verbs)

| Tool | When to use |
|---|---|
| `list_my_work_items` | L0 board: check the board first after waking |
| `get_work_item` | L1 brief: entity + context + recent task conclusions in one call |
| `get_task` / `get_task_messages` | L2/L3 drill-down: full text / transcript |
| `claim_task` | Claim (queued->running, CAS prevents duplicates) |
| `report_progress` | In-progress reporting (doubles as heartbeat) |
| `complete_task` / `fail_task` | Settle (conclusions land in the timeline; failures leave traces) |
| `post_comment` | Communicate with humans |

REST equivalents (thin shells over the same service layer):
`POST /api/tasks/{id}/claim|messages|complete|fail`.

## Assigning work to humans / agents

Work items are **created first, assigned after** (the PUT assignment triggers enqueue—assigning
at creation does not, known semantics): once assigned to an external agent the task stays queued,
waiting for the client's `claim_task`. Stranding alarm: queued beyond 60 minutes is logged and
counted server-side.

## Security boundaries (already enforced by the platform)

- The runtime credential = the agent identity: it can only operate on tasks **assigned to it** +
  comments;
- Write guards: every write outside the loop and comments is 403; self-minting a PAT is 403;
- Claim/settle are closed to human PATs (403 runtime credential required);
- Full execution rights on the local machine (no sandbox)—running the runtime under a dedicated
  system account is recommended.

## Auto-wake (optional)

`client-kit/orkome-loop.sh`: poll assignments -> claim -> brief -> call your handler -> settle.
The human-driven mode does not need it.

## Event stream (an upgrade over polling, optional)

`GET /api/events?since=<unixnano>|<id>&limit=` (PAT, keyset cursor catch-up semantics)—a more
precise "eye" than polling the work-item list. On reconnect, resume from the last cursor without
losing events.
