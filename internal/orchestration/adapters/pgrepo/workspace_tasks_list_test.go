package pgrepo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/orchestration/app"
	"orkome/internal/orchestration/domain"
)

func TestListWorkspaceTasksPG(t *testing.T) {
	e := newDeferredEnv(t)
	agent := e.mkAgent(t, "tasks-list-agent")
	wi := e.mkWorkItem(t)
	queued := e.mkQueued(t, agent.ID, wi, map[string]any{"trigger": "assignment"})

	rows, next, err := e.repo.ListWorkspaceTasks(e.ctx, e.ws, app.TaskListFilter{Limit: 50})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, queued.ID, rows[0].ID)
	assert.Equal(t, "tasks-list-agent", rows[0].AgentName)
	assert.Equal(t, "WI", rows[0].WorkItemTitle)
	assert.Empty(t, next)

	rows, _, err = e.repo.ListWorkspaceTasks(e.ctx, e.ws, app.TaskListFilter{Statuses: []string{domain.Queued}, Limit: 50})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	rows, _, err = e.repo.ListWorkspaceTasks(e.ctx, e.ws, app.TaskListFilter{
		Statuses: []string{domain.Queued, domain.Dispatched, domain.Running, "awaiting_input"}, Limit: 50})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	rows, _, err = e.repo.ListWorkspaceTasks(e.ctx, e.ws, app.TaskListFilter{Statuses: []string{domain.Failed}, Limit: 50})
	require.NoError(t, err)
	assert.Empty(t, rows)

	wi2 := e.mkWorkItem(t)
	extra := e.mkQueued(t, agent.ID, wi2, map[string]any{"trigger": "assignment"})
	page1, next, err := e.repo.ListWorkspaceTasks(e.ctx, e.ws, app.TaskListFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, page1, 1)
	assert.NotEmpty(t, next)
	page2, next2, err := e.repo.ListWorkspaceTasks(e.ctx, e.ws, app.TaskListFilter{Cursor: next, Limit: 1})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Empty(t, next2)
	seen := map[string]bool{page1[0].ID: true, page2[0].ID: true}
	assert.True(t, seen[queued.ID] && seen[extra.ID], "two pages should cover both tasks")

	claimed, ok, err := e.repo.ClaimNext(e.ctx, agent.ID, 6)
	require.NoError(t, err)
	require.True(t, ok)
	started, err := e.repo.StartTask(e.ctx, claimed.ID, "inst-list", "eino")
	require.NoError(t, err)
	err = e.repo.ParkTask(e.ctx, started.ID, app.InterruptInfo{TargetID: "t1", Kind: "approval", Payload: "{}"})
	require.NoError(t, err)

	rows, _, err = e.repo.ListWorkspaceTasks(e.ctx, e.ws, app.TaskListFilter{Statuses: []string{"awaiting_input"}, Limit: 50})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "awaiting_input", rows[0].Status)

	rows, _, err = e.repo.ListWorkspaceTasks(e.ctx, e.ws, app.TaskListFilter{
		Statuses: []string{domain.Queued, domain.Dispatched, domain.Running, "awaiting_input"}, Limit: 50})
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}
