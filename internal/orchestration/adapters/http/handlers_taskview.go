package http

import (
	"net/http"
	"strconv"
	"strings"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/orchestration/domain"
	"orkome/internal/platform/webx"
)

type TaskViewDeps struct {
	Repo   orchapp.Repo
	Settle *orchapp.SettlementService
}

func MountTaskViews(mux *http.ServeMux, d Deps, auth func(http.Handler) http.Handler) {
	if d.Repo == nil {
		return
	}
	reg := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(webx.RequireAuth(d.memberMW(h))))
	}

	reg("GET /api/agents/{agentId}/tasks", d.handleAgentTasks)
	reg("GET /api/working-agents", d.handleWorkingAgents)
	reg("GET /api/agent-run-counts", d.handleRunCounts)
	reg("GET /api/agent-activity-30d", d.handleActivity30d)

	reg("GET /api/agent-run-snapshot", d.handleAgentRunSnapshot)

	reg("GET /api/tasks", d.handleWorkspaceTasks)

	reg("GET /api/tasks/{taskId}", d.handleTaskGet)

	reg("GET /api/squads", d.handleListSquads)
	reg("POST /api/squads", d.handleCreateSquad)
	reg("GET /api/squads/{squadId}", d.handleGetSquad)
	reg("PUT /api/squads/{squadId}", d.handleUpdateSquad)
	reg("DELETE /api/squads/{squadId}", d.handleDeleteSquad)
	reg("POST /api/squads/{squadId}/members", d.handleAddSquadMember)
	reg("GET /api/squads/{squadId}/members", d.handleListSquadMembers)
	reg("GET /api/squads/{squadId}/members/status", d.handleSquadMemberStatus)
	reg("PATCH /api/squads/{squadId}/members/{agentId}", d.handleUpdateSquadMemberRole)
	reg("DELETE /api/squads/{squadId}/members/{agentId}", d.handleRemoveSquadMember)

	reg("POST /api/work-items/{workItemId}/squad-evaluated", d.handleSquadEvaluated)

	reg("GET /api/work-items/{workItemId}/active-task", d.handleActiveTask)
	reg("GET /api/work-items/{workItemId}/tasks", d.handleWorkItemTasks)

	reg("GET /api/work-items/{workItemId}/usage", d.handleWorkItemUsage)
	reg("POST /api/work-items/{workItemId}/tasks/{taskId}/cancel", d.handleTaskCancel)
	reg("POST /api/work-items/{workItemId}/rerun", d.handleRerun)

	reg("POST /api/work-items/{workItemId}/tasks/{taskId}/supplements", webx.RequireHuman(d.handleSupplement))

	reg("POST /api/work-items/{workItemId}/tasks/{taskId}/supplements/{commentId}/retry", webx.RequireHuman(d.handleSupplementRetry))
	reg("POST /api/tasks/{taskId}/cancel", d.handleTaskCancelTop)
}

func (d Deps) handleAgentTasks(w http.ResponseWriter, r *http.Request) {
	list, err := d.Repo.ListTasksByAgent(r.Context(), d.ws(r), r.PathValue("agentId"), 20)
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []orchapp.Task{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"tasks": list})
}

func parseTaskStatusFilter(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	known := map[string]bool{
		domain.Queued: true, domain.Dispatched: true, domain.Running: true,
		domain.Completed: true, domain.Failed: true, domain.Cancelled: true, domain.Deferred: true,
		"awaiting_input": true,
		"all":            true,
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		if s == "all" {
			continue
		}
		if s == "active" {
			for _, v := range []string{domain.Queued, domain.Dispatched, domain.Running, "awaiting_input"} {
				if !seen[v] {
					seen[v] = true
					out = append(out, v)
				}
			}
			continue
		}
		if !known[s] {
			return nil, apierr.NewValidation("unknown task status: " + s)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func (d Deps) handleWorkspaceTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	statuses, err := parseTaskStatusFilter(q.Get("status"))
	if err != nil {
		writeErr(w, err)
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, next, err := d.Repo.ListWorkspaceTasks(r.Context(), d.ws(r), orchapp.TaskListFilter{
		Statuses: statuses,
		AgentID:  strings.TrimSpace(q.Get("agent_id")),
		Cursor:   strings.TrimSpace(q.Get("cursor")),
		Limit:    limit,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []orchapp.TaskListRow{}
	}
	if q.Get("fields") == "slim" {
		for i := range list {
			list[i].Result = ""
			list[i].Error = ""
			list[i].Context = nil
		}
	}
	if next == "" {
		webx.WriteJSON(w, http.StatusOK, map[string]any{"tasks": list, "next_cursor": nil})
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"tasks": list, "next_cursor": next})
}

func (d Deps) handleWorkingAgents(w http.ResponseWriter, r *http.Request) {
	out, err := d.Repo.InsightWorkingAgents(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if out == nil {
		out = []map[string]any{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (d Deps) handleRunCounts(w http.ResponseWriter, r *http.Request) {
	out, err := d.Repo.InsightRunCounts(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if out == nil {
		out = []map[string]any{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"counts": out})
}

func (d Deps) handleActivity30d(w http.ResponseWriter, r *http.Request) {
	out, err := d.Repo.InsightActivity30d(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if out == nil {
		out = []map[string]any{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"activity": out})
}

func (d Deps) handleActiveTask(w http.ResponseWriter, r *http.Request) {
	t, ok, err := d.Repo.GetActiveTask(r.Context(), d.ws(r), r.PathValue("workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		webx.WriteJSON(w, http.StatusOK, map[string]any{"task": nil})
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"task": t})
}

func (d Deps) handleWorkItemTasks(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	if limit > 100 {
		limit = 100
	}
	newestFirst := r.URL.Query().Get("order") == "desc"
	slim := r.URL.Query().Get("fields") == "slim"
	list, err := d.Repo.ListTasksByWorkItem(r.Context(), r.PathValue("workItemId"), newestFirst)
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []orchapp.Task{}
	}
	if len(list) > limit {
		list = list[:limit]
	}
	if slim {
		for i := range list {
			list[i].Result = ""
			list[i].Error = ""
			list[i].Context = nil
		}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"tasks": list})
}

func (d Deps) handleTaskGet(w http.ResponseWriter, r *http.Request) {
	t, ok, err := d.Repo.GetTask(r.Context(), d.ws(r), r.PathValue("taskId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		webx.WriteError(w, http.StatusNotFound, apierr.NotFound, "task not found", nil)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"task": t})
}

func (d Deps) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	d.cancelTask(w, r, r.PathValue("taskId"))
}

func (d Deps) handleTaskCancelTop(w http.ResponseWriter, r *http.Request) {
	d.cancelTask(w, r, r.PathValue("taskId"))
}

func (d Deps) cancelTask(w http.ResponseWriter, r *http.Request, taskID string) {
	ok, err := d.Settle.Cancel(r.Context(), d.ws(r), taskID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"cancelled": ok})
}

func (d Deps) handleRerun(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		SourceTaskID string `json:"source_task_id"`
	}](w, r)
	if !ok {
		return
	}
	ws := d.ws(r)
	wi := r.PathValue("workItemId")

	agentID := ""
	if in.SourceTaskID != "" {
		t, ok2, err := d.Repo.GetTask(r.Context(), ws, in.SourceTaskID)
		if err != nil || !ok2 {
			writeErr(w, apierr.New(apierr.NotFound, "source task not found"))
			return
		}
		agentID = t.AgentID
	} else if list, err := d.Repo.ListTasksByWorkItem(r.Context(), wi, false); err == nil && len(list) > 0 {
		agentID = list[0].AgentID
	}
	if agentID == "" {
		writeErr(w, apierr.New(apierr.Conflict, "no prior task to rerun"))
		return
	}
	if d.Trigger != nil {
		task, err := d.Trigger.Rerun(r.Context(), ws, wi, agentID, webx.PrincipalFrom(r.Context()).UserID)
		if err != nil {
			writeErr(w, err)
			return
		}
		webx.WriteJSON(w, http.StatusCreated, task)
		return
	}

	task, err := d.Repo.InsertTask(r.Context(), orchapp.Task{
		WorkspaceID: ws, AgentID: agentID, WorkItemID: wi, Status: domain.Queued, MaxAttempts: 1,
		Context:             map[string]any{"trigger": "rerun", "force_fresh_session": true},
		OriginatorUserID:    webx.PrincipalFrom(r.Context()).UserID,
		TriggerEvidenceKind: domain.EvidenceRerun,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, task)
}

func (d Deps) handleSupplement(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Content string `json:"content"`
	}](w, r)
	if !ok {
		return
	}
	if in.Content == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "content required", nil)
		return
	}
	ws := d.ws(r)
	wi := r.PathValue("workItemId")
	taskID := r.PathValue("taskId")
	t, ok2, err := d.Repo.GetTask(r.Context(), ws, taskID)
	if err != nil || !ok2 {
		writeErr(w, apierr.New(apierr.NotFound, "task not found"))
		return
	}
	accepted, err := d.Repo.InsertSupplementForPending(r.Context(), ws, wi, t.AgentID,
		in.Content, webx.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]bool{"accepted": accepted})
}

func (d Deps) handleSupplementRetry(w http.ResponseWriter, r *http.Request) {
	ws := d.ws(r)
	taskID := r.PathValue("taskId")
	supplementID := r.PathValue("commentId")
	t, ok2, err := d.Repo.GetTask(r.Context(), ws, taskID)
	if err != nil || !ok2 {
		writeErr(w, apierr.New(apierr.NotFound, "task not found"))
		return
	}
	reset, err := d.Repo.ResetSupplementToPending(r.Context(), ws, t.ID, supplementID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !reset {
		writeErr(w, apierr.New(apierr.NotFound, "failed supplement not found"))
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]bool{"accepted": true})
}

func (d Deps) handleListSquads(w http.ResponseWriter, r *http.Request) {
	out, err := d.Repo.ListSquads(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if out == nil {
		out = []map[string]any{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"squads": out})
}

func (d Deps) handleCreateSquad(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Name          string `json:"name"`
		Instructions  string `json:"instructions"`
		LeaderAgentID string `json:"leader_agent_id"`
		Choreography  bool   `json:"choreography"`
	}](w, r)
	if !ok || in.Name == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "name required", nil)
		return
	}

	if taken, err := d.Repo.SquadNameTaken(r.Context(), d.ws(r), in.Name, ""); err == nil && taken {
		webx.WriteError(w, http.StatusConflict, apierr.Conflict, "squad name already taken", nil)
		return
	}
	out, err := d.Repo.CreateSquad(r.Context(), d.ws(r), in.Name, in.Instructions,
		in.LeaderAgentID, in.Choreography)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, out)
}

func (d Deps) handleAddSquadMember(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		AgentID string `json:"agent_id"`
		Role    string `json:"role"`
	}](w, r)
	if !ok || in.AgentID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "agent_id required", nil)
		return
	}
	if d.Squads != nil {
		if err := d.Squads.AddMember(r.Context(), d.ws(r), r.PathValue("squadId"), in.AgentID, in.Role); err != nil {
			writeErr(w, err)
			return
		}
	} else if err := d.Repo.AddSquadMember(r.Context(), d.ws(r), r.PathValue("squadId"), in.AgentID, in.Role); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "added"})
}

func (d Deps) handleRemoveSquadMember(w http.ResponseWriter, r *http.Request) {
	if err := d.Repo.RemoveSquadMember(r.Context(), d.ws(r), r.PathValue("squadId"), r.PathValue("agentId")); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "removed"})
}

func (d Deps) handleListSquadMembers(w http.ResponseWriter, r *http.Request) {
	out, err := d.Repo.ListSquadMembers(r.Context(), d.ws(r), r.PathValue("squadId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if out == nil {
		out = []orchapp.SquadMemberRow{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (d Deps) handleSquadMemberStatus(w http.ResponseWriter, r *http.Request) {
	if d.Squads == nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "squad service not wired", nil)
		return
	}
	out, err := d.Squads.MembersStatus(r.Context(), d.ws(r), r.PathValue("squadId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if out == nil {
		out = []orchapp.SquadMemberView{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (d Deps) handleUpdateSquadMemberRole(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Role string `json:"role"`
	}](w, r)
	if !ok {
		return
	}
	if d.Squads != nil {
		if err := d.Squads.SetMemberRole(r.Context(), d.ws(r), r.PathValue("squadId"),
			r.PathValue("agentId"), in.Role); err != nil {
			writeErr(w, err)
			return
		}
	} else {
		role := strings.TrimSpace(in.Role)
		if role == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "role required", nil)
			return
		}
		ok2, err := d.Repo.UpdateSquadMemberRole(r.Context(), d.ws(r), r.PathValue("squadId"),
			r.PathValue("agentId"), role)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !ok2 {
			writeErr(w, apierr.New(apierr.NotFound, "squad member not found"))
			return
		}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "updated"})
}

func (d Deps) handleSquadEvaluated(w http.ResponseWriter, r *http.Request) {
	if d.Squads == nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "squad service not wired", nil)
		return
	}
	in, ok := decode[orchapp.SquadEvalReport](w, r)
	if !ok {
		return
	}
	if err := d.Squads.RecordEvaluation(r.Context(), d.ws(r), r.PathValue("workItemId"), *in); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]string{"result": "recorded"})
}

func (d Deps) handleAgentEnvGet(w http.ResponseWriter, r *http.Request) {
	a, err := d.Agents.Get(r.Context(), d.ws(r), r.PathValue("agentId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	env, _ := a.RuntimeConfig["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"env": env})
}

func (d Deps) handleAgentEnvPut(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Env map[string]string `json:"env"`
	}](w, r)
	if !ok {
		return
	}
	a, err := d.Agents.Get(r.Context(), d.ws(r), r.PathValue("agentId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	rc := map[string]any{}
	for k, v := range a.RuntimeConfig {
		rc[k] = v
	}
	rc["env"] = in.Env
	out, err := d.Agents.Update(r.Context(), orchapp.Agent{
		ID: a.ID, WorkspaceID: a.WorkspaceID, Name: a.Name, Description: a.Description,
		RuntimeConfig: rc, MaxConcurrent: a.MaxConcurrent, Visibility: a.Visibility,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out.Sanitized())
}
