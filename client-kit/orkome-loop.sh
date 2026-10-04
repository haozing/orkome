#!/usr/bin/env bash
set -euo pipefail

ORKOME="${ORKOME:-http://localhost:8080}"
WS="${ORKOME_WS:?ORKOME_WS required (workspace id)}"
TOK="${ORKOME_TOKEN:?ORKOME_TOKEN required (runtime credential ork_...)}"
AGENT="${ORKOME_AGENT:?ORKOME_AGENT required (agent id)}"
HANDLER="${HANDLER:-cat}"
POLL="${POLL_SECS:-30}"

AUTH="Authorization: Bearer $TOK"
HWS="X-Workspace-Id: $WS"

jget() { # jget <json> <pyexpr over d>
  printf '%s' "$1" | python -c "import json,sys; d=json.load(sys.stdin); print($2)" 2>/dev/null || echo ""
}

jstr() { python -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1"; }

echo "[loop] watching assignments for agent $AGENT at $ORKOME (ws $WS), poll ${POLL}s"
while true; do
  items=$(curl -s "$ORKOME/api/work-items?assignee_type=agent&assignee_id=$AGENT&limit=20" \
    -H "$AUTH" -H "$HWS" || echo '')
  ids=$(jget "$items" "[i['id'] for i in (d.get('items') or [])]" | tr -d "[]',")
  for wi in $ids; do
    qtid=$(curl -s "$ORKOME/api/work-items/$wi/active-task" \
      -H "$AUTH" -H "$HWS" | python -c "import json,sys; t=json.load(sys.stdin).get('task') or {}; print(t.get('id','') if t.get('status')=='queued' else '')" 2>/dev/null || echo "")
    [ -z "$qtid" ] && continue
    echo "[loop] queued task $qtid on work item $wi — claiming"
    brief="+ orkome work item $wi\n"
    curl -s "$ORKOME/api/work-items/$wi" -H "$AUTH" -H "$HWS" >> /tmp/orkome-brief.json
    printf '\n--- timeline ---\n' >> /tmp/orkome-brief.json
    curl -s "$ORKOME/api/work-items/$wi/timeline?limit=20" -H "$AUTH" -H "$HWS" >> /tmp/orkome-brief.json
    if ! curl -sf -X POST "$ORKOME/api/tasks/$qtid/claim" -H "$AUTH" -H "$HWS" >/dev/null; then
      echo "[loop] claim raced, skipping"; continue
    fi
    out=$(printf '%b' "$brief" | cat - /tmp/orkome-brief.json | eval "$HANDLER" 2>&1 || echo "[handler failed]")
    if [ -n "$out" ] && ! echo "$out" | grep -q "handler failed"; then
      curl -s -X POST "$ORKOME/api/tasks/$qtid/complete" -H "$AUTH" -H "$HWS" \
        -H "Content-Type: application/json" \
        -d "{\"result\":$(jstr "${out:0:4000}")}" >/dev/null
      echo "[loop] completed $qtid"
    else
      curl -s -X POST "$ORKOME/api/tasks/$qtid/fail" -H "$AUTH" -H "$HWS" \
        -H "Content-Type: application/json" -d '{"reason":"handler_failed","detail":"client-kit handler exit"}' >/dev/null
      echo "[loop] failed $qtid (handler error)"
    fi
    rm -f /tmp/orkome-brief.json
  done
  sleep "$POLL"
done
