# orkome runtime client kit (v0)

The minimal toolset for external runtimes (dsh / claude / codex / any CLI agent) to join orkome.
The platform hardcodes no adapter for any runtime—onboarding = credential + MCP URL + this kit.

## Three-step onboarding

### 1. An admin prepares on the orkome side (once)

```bash
# Create an external-kind agent (runtime_kind=external is the key—the engine will not take the
# order; it waits for the client to claim)
curl -X POST $ORKOME/api/agents -H "$AUTH" -H "$WS" -d   '{"name":"dsh-runner","visibility":"public","runtime_config":{},"runtime_kind":"external"}'

# Issue the runtime credential (the token is shown only once—store it well)
curl -X POST $ORKOME/api/agents/<agentId>/runtime-credentials -H "$AUTH" -H "$WS" -d '{"name":"dsh-main"}'
# -> {"token":"ork_..."}   <- this is the runtime's entire credential
```

### 2. Configure the MCP server on the runtime side

Any MCP-capable client (dsh / claude / codex), add a Streamable HTTP server:

```json
{
  "mcpServers": {
    "orkome": {
      "url": "http://<orkome-host>:8080/api/mcp/w/<workspaceId>/agent",
      "headers": { "Authorization": "Bearer ork_..." }
    }
  }
}
```

The workspace binding lives in the URL path—no extra custom headers needed.
In production the host is `https://orkome.com`
(e.g. `https://orkome.com/api/mcp/w/<workspaceId>/agent`).

#### How to add it in dsh (DeepSeek Harness)—verified 2026-10-04

The dsh desktop has **no dedicated MCP settings panel**; MCP servers mount as "config-style
bundles", and tools appear as `mcp__orkome__<tool>` once effective (same naming shape as
claude/codex).

The bundle directory needs just two files:

```yaml
# cordis.patch.yml
- insert:
    - id: orkome-mcp
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: orkome
        transport: streamable-http
        url: http://<orkome-host>:8080/api/mcp/w/<workspaceId>/agent
        headers:
          Authorization: !!js '`Bearer ork_...`'
        failOnStartupError: true
```

```json
// package.json
{ "name": "@local/orkome-mcp", "version": "1.0.0", "private": true,
  "type": "module", "dsh": { "bundle": { "patch": "./cordis.patch.yml" } } }
```

Two equivalent installs—pick either:

1. **Plugins page**: sidebar "Plugins" → "Add plugin" → paste the bundle directory's **absolute
   path** → install and enable.
2. **Profile patch**: append the whole `- insert:` block above to the end of
   `~/.dsh/profiles/desktop/cordis.patch.yml` (that file is exactly the user-editable patch
   layer), reload the profile and it takes effect; `!!js` expressions are supported there.

Gotcha: the `url` host/port must be the address the instance **actually listens on**. Port 8080
locally is often taken by another container and the instance may run on a different port (e.g.
18080)—copying the doc's default port yields a 404 while dsh only reports a connection failure.

### 3. Give the agent an AGENTS.md

Drop this directory's `AGENTS.md` template into the runtime's working directory (or paste it into
the session), and the agent knows who it is, where work comes from, and how to report back.

## Tool face (9 verbs)

| Tool | When to use |
|---|---|
| `list_my_work_items` | Check the board first after waking: what work is assigned to me |
| `get_work_item` | Full picture before/after claiming (entity + context + recent conclusions in one call) |
| `get_task` / `get_task_messages` | Drill down when full text is needed |
| `claim_task` | Claim before starting work |
| `report_progress` | Report at any time while working (doubles as heartbeat) |
| `complete_task` / `fail_task` | Settle (the conclusion lands on the work item timeline) |
| `post_comment` | Communicate with humans, explain decisions |

## Auto-wake (optional)

`orkome-loop.sh` is a reference supervisor implementation: it polls work items assigned to you,
writes a brief when a new task appears and calls your designated handler.
The human-driven mode does not need it—open the runtime and ask "what work do I have on the
platform".

```bash
ORKOME=http://localhost:8080 \        # production: https://orkome.com
ORKOME_WS=<workspaceId> ORKOME_TOKEN=ork_... ORKOME_AGENT=<agentId> HANDLER='cat'   # replace with your runtime invocation, e.g. a dsh/claude headless command
./orkome-loop.sh
```

## Security boundaries (already enforced platform-side)

- The runtime credential = the agent identity: it can only operate on tasks **assigned to it**;
- Write-face fence: every write outside the loop and comments is rejected (403);
- Claim/messages/settle are closed to human PATs (403 runtime credential required);
- Full-authority execution on the local machine (no sandbox)—running the runtime under a
  dedicated system account is recommended.
