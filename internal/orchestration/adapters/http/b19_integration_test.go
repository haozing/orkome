package http

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

func TestB19_TaskViewsAndSupplements(t *testing.T) {
	h := newB2H(t)

	h2 := h
	mountB19(h, h2)

	wi := createWIForAgent(t, h)
	agent, err := h.agents.Create(context.Background(), orchapp.Agent{
		WorkspaceID: h.wsID, Name: "b19-agent",
		RuntimeConfig: map[string]any{"provider": "fake", "model": "m"},
	})
	require.NoError(t, err)
	task, _, err := h.enq.EnqueueAssignment(context.Background(), h.wsID, wi, agent.ID, h.userID)
	require.NoError(t, err)

	code, out := h.do("POST", "/api/work-items/"+wi+"/tasks/"+task.ID+"/supplements",
		map[string]any{"content": "additional instruction"})
	require.Equal(t, 200, code, "supplement: %v", out)
	assert.Equal(t, true, out["accepted"])
	var supp any
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT context->'supplement' FROM agent_task_queue WHERE id=$1`, task.ID).Scan(&supp))
	assert.Equal(t, "additional instruction", supp)

	code, out = h.do("GET", "/api/work-items/"+wi+"/active-task", nil)
	require.Equal(t, 200, code)
	taskObj, _ := out["task"].(map[string]any)
	require.NotNil(t, taskObj)
	assert.Equal(t, task.ID, taskObj["id"])

	code, out = h.do("POST", "/api/work-items/"+wi+"/tasks/"+task.ID+"/cancel", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, true, out["cancelled"])

	code, out = h.do("POST", "/api/work-items/"+wi+"/rerun", map[string]any{})
	require.Equal(t, 201, code, "rerun: %v", out)
	assert.Equal(t, "rerun", out["trigger_evidence_kind"])

	code, out = h.do("GET", "/api/agents/"+agent.ID+"/tasks", nil)
	require.Equal(t, 200, code)
	code, out = h.do("GET", "/api/working-agents", nil)
	require.Equal(t, 200, code)
	code, out = h.do("GET", "/api/agent-run-counts", nil)
	require.Equal(t, 200, code)
	counts, _ := out["counts"].([]any)
	require.Len(t, counts, 1)
	code, _ = h.do("GET", "/api/agent-activity-30d", nil)
	assert.Equal(t, 200, code)
}

func TestB19_Squads(t *testing.T) {
	h := newB2H(t)
	mountB19(h, h)

	a1, err := h.agents.Create(context.Background(), orchapp.Agent{
		WorkspaceID: h.wsID, Name: "sq-a",
		RuntimeConfig: map[string]any{"provider": "fake", "model": "m"},
	})
	require.NoError(t, err)

	code, out := h.do("POST", "/api/squads", map[string]any{
		"name": "review group", "instructions": "two perspectives", "leader_agent_id": a1.ID,
	})
	require.Equal(t, 201, code, "create squad: %v", out)
	squadID, _ := out["id"].(string)
	require.NotEmpty(t, squadID)

	code, out2 := h.do("POST", "/api/squads/"+squadID+"/members",
		map[string]any{"agent_id": a1.ID, "role": "leader"})
	require.Equal(t, 200, code, "add member: %v", out2)

	code, out = h.do("GET", "/api/squads", nil)
	require.Equal(t, 200, code)
	squads, _ := out["squads"].([]any)
	require.Len(t, squads, 1)
	first, _ := squads[0].(map[string]any)
	assert.Equal(t, "review group", first["name"])

	code, _ = h.do("DELETE", "/api/squads/"+squadID+"/members/"+a1.ID, nil)
	require.Equal(t, 200, code)
}

func mountB19(h *b2h, _ *b2h) {
	r := http.NewServeMux()
	Mount(r, Deps{
		Settle: orchapp.NewSettlementService(h.repo, h.bus, nil, nil, nil),
		Agents: h.agents, Repo: h.repo,
		MemberCheck: h.memberCheck,
	}, h.auth)
	h.handler = r
}
