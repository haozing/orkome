package workers

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"

	"orkome/internal/orchestration/adapters/engine"
	orchapp "orkome/internal/orchestration/app"
)

func TestB11_SearchWorkspaceTool(t *testing.T) {
	e := newEnv(t)

	e.engine.inner.WorkspaceSearch = func(_ context.Context, _, q string) ([]orchapp.SearchHit, error) {
		if !strings.Contains(q, "pricing") {
			return nil, nil
		}
		return []orchapp.SearchHit{
			{WorkItemID: "wi-1", Title: "competitor pricing research", Snippet: "price-band analysis of three competitors", Source: "title"},
			{WorkItemID: "wi-2", Title: "pricing strategy memo", Snippet: "", Source: "comment"},
		}, nil
	}

	user := e.createUser(t, "b11@example.com")
	ws := e.createWS(t, user, "b11")
	wi := e.createWorkItem(t, ws, user, "retrieval task")
	a := e.createAgent(t, ws, "searcher")

	e.setToolsScript(func(hist []*schema.Message) (engine.FakeStep, schema.TokenUsage, error) {
		for _, m := range hist {
			if m.Role == schema.Tool {
				return engine.FakeStep{Text: "done based on retrieval results: competitor pricing research and pricing strategy memo."}, schema.TokenUsage{}, nil
			}
		}
		return engine.FakeStep{ToolName: "search_workspace",
			ToolArgs: `{"query":"pricing"}`}, schema.TokenUsage{}, nil
	})

	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	go d.Run(dctx)
	defer cancel()

	task, _, _ := e.enq.EnqueueAssignment(ctx0(), ws, wi, a.ID, user)
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, task.ID)
		return ok && tt.Status == "completed"
	}, "task completed")

	tt, _, _ := e.repo.GetTask(ctx0(), ws, task.ID)
	assert.Contains(t, tt.Result, "competitor pricing research", "retrieval hits should flow back into the final draft")
	assert.Contains(t, tt.Result, "pricing strategy memo")
}
