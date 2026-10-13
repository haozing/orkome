package http

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestB20_SquadSingleCRUD(t *testing.T) {
	e := newSqhEnv(t)
	ctx := context.Background()
	leader := e.mkAgent("b20-leader")
	outsider := e.mkAgent("b20-outsider")

	code, out := e.do("POST", "/api/squads", map[string]any{
		"name": "b20-squad", "instructions": "first version", "leader_agent_id": leader})
	require.Equal(t, http.StatusCreated, code, out)
	squadID := out["id"].(string)

	code, out = e.do("GET", "/api/squads/"+squadID, nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "b20-squad", out["name"])
	assert.Equal(t, leader, out["leader_agent_id"])
	assert.Equal(t, "first version", out["instructions"])

	code, _ = e.do("GET", "/api/squads/00000000-0000-0000-0000-000000000001", nil)
	assert.Equal(t, http.StatusNotFound, code)

	code, out = e.do("PUT", "/api/squads/"+squadID, map[string]any{
		"name": "b20-  change", "instructions": "new version", "leader_agent_id": leader})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "b20-  change", out["name"])
	assert.NotContains(t, out, "choreography")

	code, _ = e.do("PUT", "/api/squads/"+squadID, map[string]any{"name": " "})
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = e.do("PUT", "/api/squads/00000000-0000-0000-0000-000000000001", map[string]any{"name": "x"})
	assert.Equal(t, http.StatusNotFound, code)

	code, out = e.do("PUT", "/api/squads/"+squadID, map[string]any{"name": "b20-  change", "leader_agent_id": outsider})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, outsider, out["leader_agent_id"])
	require.Contains(t, out["members"], outsider, "non-member new leader joins in the same transaction")
	var role string
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT role FROM squad_member WHERE squad_id=$1 AND agent_id=$2`, squadID, outsider).Scan(&role))
	assert.Equal(t, "leader", role)

	code, _ = e.do("PUT", "/api/squads/"+squadID,
		map[string]any{"name": "b20-  change", "leader_agent_id": "00000000-0000-0000-0000-000000000009"})
	assert.Equal(t, http.StatusBadRequest, code)

	code, out = e.do("POST", "/api/squads", map[string]any{"name": "b20-conflict"})
	require.Equal(t, http.StatusCreated, code, out)
	code, _ = e.do("PUT", "/api/squads/"+squadID, map[string]any{"name": "b20-conflict"})
	assert.Equal(t, http.StatusConflict, code)

	code, _ = e.do("DELETE", "/api/squads/"+squadID, nil)
	assert.Equal(t, http.StatusConflict, code)

	code, _ = e.do("DELETE", "/api/squads/"+squadID+"/members/"+outsider, nil)
	require.Equal(t, http.StatusOK, code)
	code, out = e.do("DELETE", "/api/squads/"+squadID, nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "deleted", out["result"])
	code, _ = e.do("GET", "/api/squads/"+squadID, nil)
	assert.Equal(t, http.StatusNotFound, code)

	code, _ = e.do("DELETE", "/api/squads/00000000-0000-0000-0000-000000000002", nil)
	assert.Equal(t, http.StatusNotFound, code)

	_, out = e.do("POST", "/api/squads", map[string]any{"name": "b20-active"})
	sqActive := out["id"].(string)
	actor := e.mkAgent("b20-actor")
	e.mkTask(actor, "queued", true, sqActive)
	code, _ = e.do("DELETE", "/api/squads/"+sqActive, nil)
	assert.Equal(t, http.StatusConflict, code)

	_, out = e.do("POST", "/api/squads", map[string]any{"name": "b20-history"})
	sqHist := out["id"].(string)
	hist := e.mkTask(actor, "completed", true, sqHist)
	code, _ = e.do("DELETE", "/api/squads/"+sqHist, nil)
	assert.Equal(t, http.StatusOK, code)
	var nulled bool
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT squad_id IS NULL FROM agent_task_queue WHERE id=$1`, hist).Scan(&nulled))
	assert.True(t, nulled, "terminal tasks' historical squad_id references released")
}

func TestB20_WorkItemUsage(t *testing.T) {
	e := newSqhEnv(t)
	ctx := context.Background()
	agent := e.mkAgent("b20-usage")

	var wi string
	require.NoError(t, e.pool.QueryRow(ctx, `
INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1, 1, 'usage carrier', 'todo', 'user', $2) RETURNING id::text`, e.wsID, e.wsUserID).Scan(&wi))
	mkWITask := func() string {
		id := e.mkTask(agent, "completed", false, "")
		_, err := e.pool.Exec(ctx,
			`UPDATE agent_task_queue SET work_item_id=$1 WHERE id=$2`, wi, id)
		require.NoError(t, err)
		return id
	}
	t1, t2 := mkWITask(), mkWITask()

	ins := `INSERT INTO task_usage (workspace_id, task_id, agent_id, provider, model, prompt_tokens, completion_tokens, cost_usd_ticks)
VALUES ($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8)`
	for _, r := range []struct{ task, provider, model string }{
		{t1, "openai", "gpt-4o"}, {t1, "openai", "gpt-4o-mini"},
		{t2, "anthropic", "claude"},
	} {
		_, err := e.pool.Exec(ctx, ins, e.wsID, r.task, agent, r.provider, r.model, 100, 200, 300)
		require.NoError(t, err)
	}
	_, err := e.pool.Exec(ctx,
		`INSERT INTO task_usage (workspace_id, provider, model, prompt_tokens, completion_tokens, cost_usd_ticks)
VALUES ($1,'openai','aux',999,999,999)`, e.wsID)
	require.NoError(t, err)

	code, out := e.do("GET", "/api/work-items/"+wi+"/usage", nil)
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, wi, out["work_item_id"])
	assert.EqualValues(t, 2, out["tasks"], "dedup task count (auxiliary rows not counted)")
	assert.EqualValues(t, 300, out["total_input_tokens"])
	assert.EqualValues(t, 600, out["total_output_tokens"])
	assert.EqualValues(t, 900, out["total_cost_usd_ticks"])
	byProvider, _ := out["by_provider"].([]any)
	require.Len(t, byProvider, 3, "GROUP BY provider,model")
	p0, _ := byProvider[0].(map[string]any)
	assert.Equal(t, "anthropic", p0["provider"], "ORDER BY provider,model")
	assert.Equal(t, "claude", p0["model"])
	assert.EqualValues(t, 100, p0["input_tokens"])
	p1, _ := byProvider[1].(map[string]any)
	assert.Equal(t, "openai", p1["provider"])
	assert.Equal(t, "gpt-4o", p1["model"])

	code, _ = e.do("GET", "/api/work-items/00000000-0000-0000-0000-000000000003/usage", nil)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestB20_AgentRunSnapshot(t *testing.T) {
	e := newSqhEnv(t)
	ctx := context.Background()
	a1 := e.mkAgent("b20-snap-a")
	a2 := e.mkAgent("b20-snap-b")

	queued := e.mkTask(a1, "queued", false, "")
	dispatched := e.mkTask(a1, "dispatched", false, "")
	running := e.mkTask(a2, "running", false, "")
	e.mkTask(a2, "completed", false, "")

	_, err := e.pool.Exec(ctx,
		`UPDATE agent_task_queue SET dispatched_at=now()-interval '5 minutes' WHERE id=$1`, dispatched)
	require.NoError(t, err)
	_, err = e.pool.Exec(ctx,
		`UPDATE agent_task_queue SET started_at=now()-interval '2 minutes' WHERE id=$1`, running)
	require.NoError(t, err)
	var wi string
	require.NoError(t, e.pool.QueryRow(ctx, `
INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1, 2, 'snapshot carrier', 'todo', 'user', $2) RETURNING id::text`, e.wsID, e.wsUserID).Scan(&wi))
	_, err = e.pool.Exec(ctx,
		`UPDATE agent_task_queue SET work_item_id=$1 WHERE id=$2`, wi, running)
	require.NoError(t, err)

	code, out := e.do("GET", "/api/agent-run-snapshot", nil)
	require.Equal(t, http.StatusOK, code, out)
	items, _ := out["items"].([]any)
	require.Len(t, items, 3, "only queued/dispatched/running")

	byID := map[string]map[string]any{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		byID[m["task_id"].(string)] = m
	}
	assert.Nil(t, byID[queued]["started_at"], "queued with no dispatch/run time → null")
	require.NotNil(t, byID[dispatched]["started_at"], "dispatched takes dispatched_at")
	require.NotNil(t, byID[running]["started_at"], "running takes started_at")
	assert.Equal(t, wi, byID[running]["work_item_id"])
	assert.Equal(t, "running", byID[running]["status"])
}
