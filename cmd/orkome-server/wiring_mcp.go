package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"orkome/internal/contract/apierr"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/pg"
	webx "orkome/internal/platform/webx"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
)

type agentMCPDeps struct {
	orchRepo *orchpgrepo.Repo
	wRepo    *workpgrepo.Repo
	wSvc     *workapp.WorkItemService
	external *orchapp.ExternalTaskService
}

func mountAgentMCP(api chi.Router, auth func(http.Handler) http.Handler,
	db *pg.DB, bus *eventbus.Bus, external *orchapp.ExternalTaskService,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)) {
	orchRepo := orchpgrepo.New(db)
	wRepo := &workpgrepo.Repo{DB: db}
	d := &agentMCPDeps{
		orchRepo: orchRepo, wRepo: wRepo,
		wSvc:     workapp.NewWorkItemService(wRepo, bus, nil),
		external: external,
	}

	s := server.NewMCPServer("orkome-agent", "0.1.0")
	d.addReadTools(s)
	d.addLoopTools(s)
	handler := server.NewStreamableHTTPServer(s)

	api.Group(func(r chi.Router) {
		r.Use(auth, webx.RequireAuth)
		r.Use(d.workspaceMW(memberCheck))

		r.Post("/mcp/w/{workspaceId}/agent", handler.ServeHTTP)
		r.Get("/mcp/w/{workspaceId}/agent", handler.ServeHTTP)
	})
}

func (d *agentMCPDeps) workspaceMW(memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := webx.PrincipalFrom(r.Context())
			ws := chi.URLParam(r, "workspaceId")
			ok, err := memberCheck(r.Context(), ws, p.UserID)
			if err != nil || !ok {
				webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "workspace membership required", nil)
				return
			}
			p.WorkspaceID = ws
			next.ServeHTTP(w, r)
		})
	}
}

func (d *agentMCPDeps) agentOf(ctx context.Context) (ws, agentID string, err error) {
	p := webx.PrincipalFrom(ctx)
	if p == nil || p.AgentID == "" || p.WorkspaceID == "" {
		return "", "", apierr.New(apierr.Forbidden, "runtime credential required (agent-bound MCP endpoint)")
	}
	return p.WorkspaceID, p.AgentID, nil
}

func jsonText(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(b)), nil
}

func errResult(err error) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(err.Error()), nil
}

func (d *agentMCPDeps) addReadTools(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("list_my_work_items",
		mcp.WithDescription("list work items assigned to the current runtime agent (L0 board: call this first after waking to see the work)"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, agentID, err := d.agentOf(ctx)
		if err != nil {
			return errResult(err)
		}
		items, _, err := d.wSvc.List(ctx, workapp.ListQuery{WorkspaceID: ws, AssigneeID: agentID, Limit: 20})
		if err != nil {
			return errResult(err)
		}
		type row struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		}
		out := make([]row, 0, len(items))
		for _, wi := range items {
			out = append(out, row{ID: wi.ID, Title: wi.Title, Status: wi.StatusKey})
		}
		return jsonText(map[string]any{"work_items": out})
	})

	s.AddTool(mcp.NewTool("get_work_item",
		mcp.WithDescription("work item brief (L1: entity + recent timeline + recent task conclusions; use this before/after claiming instead of assembling endpoint by endpoint)"),
		mcp.WithString("work_item_id", mcp.Required(), mcp.Description("work item ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := d.agentOf(ctx)
		if err != nil {
			return errResult(err)
		}
		wi, err := d.wSvc.Get(ctx, ws, req.GetString("work_item_id", ""))
		if err != nil {
			return errResult(err)
		}
		tl, err := d.wRepo.ListTimeline(ctx, wi.ID, 20, "")
		if err != nil {
			return errResult(err)
		}
		tasks, err := d.orchRepo.ListTasksByWorkItem(ctx, wi.ID, true)
		if err != nil {
			return errResult(err)
		}

		if len(tasks) > 5 {
			tasks = tasks[:5]
		}
		type taskRow struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Attempt int    `json:"attempt"`
			Summary string `json:"summary"`
		}
		tr := make([]taskRow, 0, len(tasks))
		for _, t := range tasks {
			tr = append(tr, taskRow{ID: t.ID, Status: t.Status, Attempt: t.Attempt, Summary: t.Summary})
		}
		return jsonText(map[string]any{
			"work_item": map[string]any{
				"id": wi.ID, "title": wi.Title, "description": wi.Description,
				"status": wi.StatusKey, "priority": wi.Priority,
			},
			"recent_tasks": tr,
			"timeline":     tl,
		})
	})

	s.AddTool(mcp.NewTool("get_task",
		mcp.WithDescription("single task detail (full conclusion / failure reason / attempt chain; drill down from the brief when you need full text)"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task  ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := d.agentOf(ctx)
		if err != nil {
			return errResult(err)
		}
		t, ok, err := d.orchRepo.GetTask(ctx, ws, req.GetString("task_id", ""))
		if err != nil {
			return errResult(err)
		}
		if !ok {
			return mcp.NewToolResultError("task not found"), nil
		}
		return jsonText(t)
	})

	s.AddTool(mcp.NewTool("get_task_messages",
		mcp.WithDescription("task execution message stream (ascending by time; use to review run details of your own or others' tasks)"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task  ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := d.agentOf(ctx)
		if err != nil {
			return errResult(err)
		}
		msgs, err := d.orchRepo.ListTaskMessages(ctx, ws, req.GetString("task_id", ""), orchapp.TaskMessagesQuery{})
		if err != nil {
			return errResult(err)
		}
		return jsonText(map[string]any{"messages": msgs})
	})
}

func (d *agentMCPDeps) addLoopTools(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("claim_task",
		mcp.WithDescription("claim the task (queued→running; call this before starting work you saw in the list)"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task  ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, agentID, err := d.agentOf(ctx)
		if err != nil {
			return errResult(err)
		}
		t, err := d.external.Claim(ctx, ws, req.GetString("task_id", ""), agentID)
		if err != nil {
			return errResult(err)
		}
		return jsonText(map[string]any{"claimed": true, "task_id": t.ID, "status": t.Status})
	})

	s.AddTool(mcp.NewTool("report_progress",
		mcp.WithDescription("report execution progress (text messages streamed back; doubles as heartbeat. Call anytime while working)"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task  ID")),
		mcp.WithString("content", mcp.Required(), mcp.Description("progress content")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, agentID, err := d.agentOf(ctx)
		if err != nil {
			return errResult(err)
		}
		seq, err := d.external.ReportMessage(ctx, ws, req.GetString("task_id", ""), agentID,
			"text", "", req.GetString("content", ""))
		if err != nil {
			return errResult(err)
		}
		return jsonText(map[string]any{"seq": seq})
	})

	s.AddTool(mcp.NewTool("complete_task",
		mcp.WithDescription("complete the task (call this when done; result feeds the conclusion summary on the work item timeline)"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task  ID")),
		mcp.WithString("result", mcp.Required(), mcp.Description("conclusion (what was done and the outcome)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, agentID, err := d.agentOf(ctx)
		if err != nil {
			return errResult(err)
		}
		if err := d.external.Complete(ctx, ws, req.GetString("task_id", ""), agentID, req.GetString("result", "")); err != nil {
			return errResult(err)
		}
		return jsonText(map[string]any{"result": "completed"})
	})

	s.AddTool(mcp.NewTool("fail_task",
		mcp.WithDescription("fail the task (call this when the work cannot be done, with a reason; failures are recorded on the work item timeline)"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("Task  ID")),
		mcp.WithString("reason", mcp.Required(), mcp.Description("failure reason (short)")),
		mcp.WithString("detail", mcp.Description("failure detail")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, agentID, err := d.agentOf(ctx)
		if err != nil {
			return errResult(err)
		}
		if err := d.external.Fail(ctx, ws, req.GetString("task_id", ""), agentID,
			req.GetString("reason", ""), req.GetString("detail", "")); err != nil {
			return errResult(err)
		}
		return jsonText(map[string]any{"result": "failed"})
	})

	s.AddTool(mcp.NewTool("post_comment",
		mcp.WithDescription("post a comment on the work item (as the agent; use when communicating with humans or explaining decisions)"),
		mcp.WithString("work_item_id", mcp.Required(), mcp.Description("work item ID")),
		mcp.WithString("content", mcp.Required(), mcp.Description("comment content")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, agentID, err := d.agentOf(ctx)
		if err != nil {
			return errResult(err)
		}
		if _, err := d.wRepo.CreateComment(ctx, workapp.NewComment{
			WorkItemID: req.GetString("work_item_id", ""), WorkspaceID: ws,
			AuthorType: "agent", AuthorID: agentID, Content: req.GetString("content", ""),
		}); err != nil {
			return errResult(err)
		}
		return jsonText(map[string]any{"posted": true})
	})
}
