package http

import (
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	workapp "orkome/internal/work/app"
)

// Project Workflow 定义面（docs/wf-interfaces-frozen.md §3；W1 范围 = 项目侧
// CRUD 五端点。工作项侧 /work-items/{id}/workflow×4 是 W2 状态面，届时追加）。

// PUT /api/projects/{projectId}/workflow
func (d Deps) handleUpsertWorkflow(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Name               string `json:"name"`
		TriggerMode        string `json:"trigger_mode"`
		EntryStagePosition int    `json:"entry_stage_position"`
		Stages             []struct {
			Position      int    `json:"position"`
			Name          string `json:"name"`
			ExitStatusKey string `json:"exit_status_key"`
			ExecutorType  string `json:"executor_type"`
			ExecutorID    string `json:"executor_id"`
			Instruction   string `json:"instruction"`
		} `json:"stages"`
	}](w, r)
	if !ok {
		return
	}
	input := workapp.WorkflowUpsertInput{
		Name: in.Name, TriggerMode: in.TriggerMode, EntryStagePosition: in.EntryStagePosition,
	}
	for _, st := range in.Stages {
		input.Stages = append(input.Stages, workapp.WorkflowStageInput{
			Position: st.Position, Name: st.Name, ExitStatusKey: st.ExitStatusKey,
			ExecutorType: st.ExecutorType, ExecutorID: st.ExecutorID, Instruction: st.Instruction,
		})
	}
	def, err := d.Workflow.Upsert(r.Context(), wsID(r), r.PathValue("projectId"), input, principal(r).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"workflow": def})
}

// GET /api/projects/{projectId}/workflow → {"workflow":{...}|null}
func (d Deps) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	def, err := d.Workflow.Get(r.Context(), wsID(r), r.PathValue("projectId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"workflow": def})
}

// DELETE /api/projects/{projectId}/workflow → 204
func (d Deps) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if err := d.Workflow.Delete(r.Context(), wsID(r), r.PathValue("projectId"), principal(r).UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) setWorkflowEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := d.Workflow.SetEnabled(r.Context(), wsID(r), r.PathValue("projectId"), enabled, principal(r).UserID); err != nil {
			writeErr(w, err)
			return
		}
		def, err := d.Workflow.Get(r.Context(), wsID(r), r.PathValue("projectId"))
		if err != nil {
			writeErr(w, err)
			return
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{"workflow": def})
	}
}

// —— W2 状态面：工作项侧（契约 §3 工作项×4；GET state 走内部消费，v0 前端
// 联调评估后再挂，见 docs/wf-w2-notes.md §给 W3）——

// POST /api/work-items/{workItemId}/workflow/start → {"state":{...}}
func (d Deps) handleStartWorkflow(w http.ResponseWriter, r *http.Request) {
	st, err := d.Workflow.Start(r.Context(), wsID(r), r.PathValue("workItemId"), principal(r).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"state": st})
}

// POST /api/work-items/{workItemId}/workflow/restart → {"state":{...}}
func (d Deps) handleRestartWorkflow(w http.ResponseWriter, r *http.Request) {
	st, err := d.Workflow.Restart(r.Context(), wsID(r), r.PathValue("workItemId"), principal(r).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"state": st})
}

// DELETE /api/work-items/{workItemId}/workflow → 204（脱离流程 = detached）
func (d Deps) handleDetachWorkflow(w http.ResponseWriter, r *http.Request) {
	if err := d.Workflow.Detach(r.Context(), wsID(r), r.PathValue("workItemId"), principal(r).UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
