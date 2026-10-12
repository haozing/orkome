package pgrepo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/orchestration/app"
)

func TestCompleteRunWritesRetryFailoverColumns(t *testing.T) {
	e := newDeferredEnv(t)
	agent := e.mkAgent(t, "usage-agent")
	wi := e.mkWorkItem(t)
	task := e.mkQueued(t, agent.ID, wi, map[string]any{"trigger": "assignment"})

	claimed, ok, err := e.repo.ClaimNext(e.ctx, agent.ID, 6)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, task.ID, claimed.ID)
	started, err := e.repo.StartTask(e.ctx, claimed.ID, "inst-usage", "eino")
	require.NoError(t, err)

	u := app.UsageReport{Provider: "openai", Model: "gpt-5",
		PromptTokens: 5, CompletionTokens: 7, ModelRetries: 2, ModelFailovers: 1}
	require.NoError(t, e.repo.CompleteRun(e.ctx, started.ID, "conclusion", "session:ulid1", "eino", "", u))

	var retries, failovers, prompt, completion int
	var label string
	err = e.pool.QueryRow(e.ctx, `
SELECT model_retries, model_failovers, prompt_tokens, completion_tokens, agent_label
FROM task_usage WHERE task_id=$1`, started.ID).
		Scan(&retries, &failovers, &prompt, &completion, &label)
	require.NoError(t, err, "terminal transactions must land task_usage rows")
	assert.Equal(t, 2, retries, "model_retries records engine-reported values")
	assert.Equal(t, 1, failovers, "model_failovers records engine-reported values")
	assert.Equal(t, 5, prompt)
	assert.Equal(t, 7, completion)
	assert.Equal(t, "primary", label)
}

func TestCompleteRunZeroRetryColumnsBackwardCompatible(t *testing.T) {
	e := newDeferredEnv(t)
	agent := e.mkAgent(t, "usage-agent2")
	wi := e.mkWorkItem(t)
	e.mkQueued(t, agent.ID, wi, map[string]any{"trigger": "assignment"})
	claimed, ok, err := e.repo.ClaimNext(e.ctx, agent.ID, 6)
	require.NoError(t, err)
	require.True(t, ok)
	started, err := e.repo.StartTask(e.ctx, claimed.ID, "inst-usage", "eino")
	require.NoError(t, err)

	u := app.UsageReport{Provider: "fake", Model: "m", PromptTokens: 1}
	require.NoError(t, e.repo.CompleteRun(e.ctx, started.ID, "ok", "", "eino", "", u))

	var retries, failovers int
	err = e.pool.QueryRow(e.ctx,
		`SELECT model_retries, model_failovers FROM task_usage WHERE task_id=$1`, started.ID).
		Scan(&retries, &failovers)
	require.NoError(t, err)
	assert.Zero(t, retries)
	assert.Zero(t, failovers)
}

func TestInsertAuxUsageWritesRetryColumns(t *testing.T) {
	e := newDeferredEnv(t)
	require.NoError(t, e.repo.InsertAuxUsage(e.ctx, e.ws, app.UsageReport{
		Provider: "openai", Model: "gpt-5", PromptTokens: 2, CompletionTokens: 3,
		AgentLabel: "eval", ModelRetries: 1, ModelFailovers: 0}))

	var retries, failovers int
	var taskID, agentID *string
	err := e.pool.QueryRow(e.ctx, `
SELECT model_retries, model_failovers, task_id, agent_id
FROM task_usage WHERE workspace_id=$1 AND agent_label='eval'`, e.ws).
		Scan(&retries, &failovers, &taskID, &agentID)
	require.NoError(t, err)
	assert.Equal(t, 1, retries)
	assert.Zero(t, failovers)
	assert.Nil(t, taskID, "auxiliary rows have task_id NULL (internal archive 09 §12)")
	assert.Nil(t, agentID, "auxiliary rows have agent_id NULL (internal archive 09 §12)")
}
