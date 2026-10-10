package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

type usageFakeRepo struct {
	orchapp.Repo
	usage map[string]orchapp.WorkItemUsage
	snaps []orchapp.AgentRunSnapshotItem
}

func (f *usageFakeRepo) WorkItemUsage(_ context.Context, _, workItemID string) (orchapp.WorkItemUsage, bool, error) {
	u, ok := f.usage[workItemID]
	return u, ok, nil
}

func (f *usageFakeRepo) ListWorkspaceActiveTasks(_ context.Context, _ string) ([]orchapp.AgentRunSnapshotItem, error) {
	return f.snaps, nil
}

func newUsageRouter(repo orchapp.Repo) http.Handler {
	r := http.NewServeMux()
	Mount(r, Deps{Repo: repo,
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil },
	}, passAuth)
	return r
}

func TestWorkItemUsageEndpoint_ShapeAnd404(t *testing.T) {
	repo := &usageFakeRepo{usage: map[string]orchapp.WorkItemUsage{
		"wi-1": {
			WorkItemID: "wi-1", Tasks: 3,
			TotalInputTokens: 110, TotalOutputTokens: 220, TotalCostUSDTicks: 330,
			ByProvider: []orchapp.UsageByProvider{
				{Provider: "openai", Model: "gpt-4o", InputTokens: 100, OutputTokens: 200, CostUSDTicks: 300},
				{Provider: "anthropic", Model: "claude", InputTokens: 10, OutputTokens: 20, CostUSDTicks: 30},
			},
		},
		"wi-empty": {WorkItemID: "wi-empty", ByProvider: []orchapp.UsageByProvider{}},
	}}
	r := newUsageRouter(repo)

	rec := agentLabelsReq(r, "GET", "/api/work-items/wi-1/usage", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var u orchapp.WorkItemUsage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &u))
	assert.Equal(t, "wi-1", u.WorkItemID)
	assert.EqualValues(t, 3, u.Tasks)
	assert.EqualValues(t, 110, u.TotalInputTokens)
	assert.EqualValues(t, 220, u.TotalOutputTokens)
	assert.EqualValues(t, 330, u.TotalCostUSDTicks)
	require.Len(t, u.ByProvider, 2)
	assert.Equal(t, "openai", u.ByProvider[0].Provider)
	assert.Equal(t, "gpt-4o", u.ByProvider[0].Model)
	assert.EqualValues(t, 300, u.ByProvider[0].CostUSDTicks)

	rec = agentLabelsReq(r, "GET", "/api/work-items/wi-empty/usage", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &u))
	assert.EqualValues(t, 0, u.Tasks)
	assert.Empty(t, u.ByProvider)

	assert.Equal(t, http.StatusNotFound, agentLabelsReq(r, "GET", "/api/work-items/ghost/usage", "").Code)
}

func TestAgentRunSnapshotEndpoint(t *testing.T) {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repo := &usageFakeRepo{snaps: []orchapp.AgentRunSnapshotItem{
		{TaskID: "t-queued", AgentID: "a1", Status: "queued"},
		{TaskID: "t-running", WorkItemID: "wi-1", AgentID: "a1", Status: "running", StartedAt: &started},
	}}
	r := newUsageRouter(repo)

	rec := agentLabelsReq(r, "GET", "/api/agent-run-snapshot", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Items []orchapp.AgentRunSnapshotItem `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Items, 2)
	assert.Nil(t, out.Items[0].StartedAt, "queued with no dispatch/run time → null")
	assert.Empty(t, out.Items[0].WorkItemID)
	require.NotNil(t, out.Items[1].StartedAt)
	assert.True(t, out.Items[1].StartedAt.Equal(started))
	assert.Equal(t, "wi-1", out.Items[1].WorkItemID)

	rec = agentLabelsReq(newUsageRouter(&usageFakeRepo{}), "GET", "/api/agent-run-snapshot", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"items":[]`)
}
