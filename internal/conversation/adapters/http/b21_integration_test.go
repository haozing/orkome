package http

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	convapp "orkome/internal/conversation/app"
)

type b4TaskOps struct{ pool *pgxpool.Pool }

func (o b4TaskOps) ListPendingChatTasks(ctx context.Context, ws, userID string) ([]convapp.PendingChatTask, error) {
	rows, err := o.pool.Query(ctx, `
SELECT t.id::text, t.chat_session_id::text, t.status, t.created_at
FROM agent_task_queue t JOIN chat_session s ON s.id = t.chat_session_id
WHERE t.workspace_id=$1 AND s.workspace_id=$1 AND s.creator_id=$2
  AND t.status IN ('queued','dispatched','running')
ORDER BY t.created_at DESC LIMIT 200`, ws, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []convapp.PendingChatTask
	for rows.Next() {
		var p convapp.PendingChatTask
		if err := rows.Scan(&p.TaskID, &p.SessionID, &p.Status, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (o b4TaskOps) HasPendingChatTask(ctx context.Context, ws, userID string) (bool, error) {
	var exists bool
	err := o.pool.QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM agent_task_queue t JOIN chat_session s ON s.id = t.chat_session_id
WHERE t.workspace_id=$1 AND s.workspace_id=$1 AND s.creator_id=$2
  AND t.status IN ('queued','dispatched','running'))`, ws, userID).Scan(&exists)
	return exists, err
}

func (o b4TaskOps) PrioritizeChatTask(ctx context.Context, ws, sessionID, taskID string) (int, bool, error) {
	var priority int
	err := o.pool.QueryRow(ctx, `
UPDATE agent_task_queue t SET priority = (SELECT coalesce(max(priority),0)+1 FROM agent_task_queue WHERE workspace_id=$1)
WHERE t.workspace_id=$1 AND t.id=$2::uuid AND t.chat_session_id=$3::uuid
  AND t.status IN ('queued','dispatched')
RETURNING t.priority`, ws, taskID, sessionID).Scan(&priority)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return priority, true, nil
}

func TestB21_PrioritizeQueuedTask(t *testing.T) {
	h := newB4H(t)

	code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID, "title": "priority"})
	require.Equal(t, 201, code, "create: %v", out)
	sid, _ := out["id"].(string)

	code, out = h.do("POST", "/chat/sessions/"+sid+"/messages", map[string]any{"content": "ping me"})
	require.Equal(t, 201, code, "send: %v", out)
	taskID, _ := out["task_id"].(string)
	require.NotEmpty(t, taskID)

	code, out = h.do("POST", "/chat/sessions/"+sid+"/queued-tasks/"+taskID+"/prioritize", nil)
	require.Equal(t, 200, code, "prioritize: %v", out)
	assert.Equal(t, float64(1), out["priority"])
	code, out = h.do("POST", "/chat/sessions/"+sid+"/queued-tasks/"+taskID+"/prioritize", nil)
	require.Equal(t, 200, code, "re-prioritize: %v", out)
	assert.Equal(t, float64(2), out["priority"], "repeated promotion keeps incrementing")

	code, out = h.do("DELETE", "/chat/sessions/"+sid+"/pending-task", nil)
	require.Equal(t, 200, code, "cancel: %v", out)
	code, out = h.do("POST", "/chat/sessions/"+sid+"/queued-tasks/"+taskID+"/prioritize", nil)
	assert.Equal(t, 404, code, "promoting non-queued tasks should be 404: %v", out)

	code, out = h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID})
	require.Equal(t, 201, code, "create2: %v", out)
	sid2, _ := out["id"].(string)
	code, out = h.do("POST", "/chat/sessions/"+sid2+"/messages", map[string]any{"content": "x"})
	require.Equal(t, 201, code, "send2: %v", out)
	task2, _ := out["task_id"].(string)
	code, out = h.doAs(h.userB, "POST", "/chat/sessions/"+sid2+"/queued-tasks/"+task2+"/prioritize", nil)
	assert.Equal(t, 404, code, "non-creator prioritize should be 404: %v", out)
}

func TestB21_PendingTasksAndHasAny(t *testing.T) {
	h := newB4H(t)

	sids := make([]string, 2)
	for i := 0; i < 2; i++ {
		code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID, "title": "global"})
		require.Equal(t, 201, code, "create: %v", out)
		sids[i], _ = out["id"].(string)
		code, out = h.do("POST", "/chat/sessions/"+sids[i]+"/messages", map[string]any{"content": "task"})
		require.Equal(t, 201, code, "send: %v", out)
	}

	code, out := h.do("GET", "/chat/pending-tasks", nil)
	require.Equal(t, 200, code, "pending-tasks: %v", out)
	list, _ := out["tasks"].([]any)
	require.Len(t, list, 2, "one pending task per session: %v", out)
	got := map[string]bool{}
	for _, it := range list {
		m, _ := it.(map[string]any)
		s, _ := m["session_id"].(string)
		got[s] = true
		assert.Equal(t, "queued", m["status"])
	}
	assert.True(t, got[sids[0]] && got[sids[1]], "both session tasks should appear")

	code, out = h.do("GET", "/chat/pending-tasks/has-any", nil)
	require.Equal(t, 200, code, "has-any: %v", out)
	assert.Equal(t, true, out["has_any"])

	code, out = h.do("DELETE", "/chat/sessions/"+sids[0]+"/pending-task", nil)
	require.Equal(t, 200, code, "cancel: %v", out)
	code, out = h.do("GET", "/chat/pending-tasks/has-any", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, true, out["has_any"], "the remaining task should still be true")
	code, out = h.do("GET", "/chat/pending-tasks", nil)
	require.Equal(t, 200, code)
	list, _ = out["tasks"].([]any)
	assert.Len(t, list, 1, "list has 1 left after cancel")

	code, out = h.do("DELETE", "/chat/sessions/"+sids[1]+"/pending-task", nil)
	require.Equal(t, 200, code, "cancel2: %v", out)
	code, out = h.do("GET", "/chat/pending-tasks/has-any", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, false, out["has_any"], "should be false after all cancelled")
}

func TestB21_PinnedAgentsCRUD(t *testing.T) {
	h := newB4H(t)

	code, out := h.do("GET", "/chat/pinned-agents", nil)
	require.Equal(t, 200, code, "list: %v", out)
	agents, _ := out["agents"].([]any)
	assert.Empty(t, agents, "initial pin list empty (empty array not null)")

	code, out = h.do("POST", "/chat/pinned-agents", map[string]any{"agent_id": h.agentID})
	require.Equal(t, 201, code, "pin: %v", out)
	assert.Equal(t, "pinned", out["result"])

	code, out = h.do("POST", "/chat/pinned-agents", map[string]any{"agent_id": h.agentID})
	require.Equal(t, 201, code, "repeated pin idempotent: %v", out)

	code, out = h.do("GET", "/chat/pinned-agents", nil)
	require.Equal(t, 200, code, "list2: %v", out)
	agents, _ = out["agents"].([]any)
	require.Len(t, agents, 1, "idempotent pin does not duplicate: %v", out)
	a, _ := agents[0].(map[string]any)
	assert.Equal(t, h.agentID, a["agent_id"])
	assert.Equal(t, float64(1), a["position"], "first pin position=1")

	code, out = h.do("DELETE", "/chat/pinned-agents?agent_id="+h.agentID, nil)
	require.Equal(t, 200, code, "unpin: %v", out)
	assert.Equal(t, "unpinned", out["result"])

	code, out = h.do("GET", "/chat/pinned-agents", nil)
	require.Equal(t, 200, code)
	agents, _ = out["agents"].([]any)
	assert.Empty(t, agents, "list empty after cancel")

	code, out = h.do("DELETE", "/chat/pinned-agents?agent_id="+h.agentID, nil)
	assert.Equal(t, 200, code, "unpin idempotent: %v", out)
}

func TestB21_DraftRestoreOwnershipNegative(t *testing.T) {
	h := newB4H(t)

	code, out := h.do("POST", "/chat/sessions", map[string]any{"agent_id": h.agentID, "title": "draft"})
	require.Equal(t, 201, code, "create: %v", out)
	sid, _ := out["id"].(string)

	code, out = h.do("POST", "/chat/sessions/"+sid+"/messages", map[string]any{"content": "private draft"})
	require.Equal(t, 201, code, "send: %v", out)
	code, out = h.do("DELETE", "/chat/sessions/"+sid+"/pending-task", nil)
	require.Equal(t, 200, code, "cancel: %v", out)
	require.Equal(t, "restored", out["outcome"], "empty transcript cancel should keep the draft")

	code, out = h.do("GET", "/chat/sessions/"+sid+"/draft-restores", nil)
	require.Equal(t, 200, code, "owner draft: %v", out)
	draft, _ := out["draft"].(map[string]any)
	require.NotNil(t, draft, "the initiator should retrieve the draft")
	assert.Equal(t, "private draft", draft["content"])

	code, out = h.doAs(h.userB, "GET", "/chat/sessions/"+sid+"/draft-restores", nil)
	assert.Equal(t, 404, code, "non-creator taking the draft should be 404: %v", out)
	code, out = h.doAs(h.userB, "DELETE", "/chat/sessions/"+sid+"/draft-restores", nil)
	assert.Equal(t, 404, code, "non-creator discarding the draft should be 404: %v", out)

	code, out = h.do("GET", "/chat/sessions/"+sid+"/draft-restores", nil)
	require.Equal(t, 200, code, "owner draft again: %v", out)
	draft, _ = out["draft"].(map[string]any)
	assert.NotNil(t, draft, "A's draft unaffected by B's lack of access")
}
