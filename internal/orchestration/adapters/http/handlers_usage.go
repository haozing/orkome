package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/webx"
)

func (d Deps) handleWorkItemUsage(w http.ResponseWriter, r *http.Request) {
	u, ok, err := d.Repo.WorkItemUsage(r.Context(), d.ws(r), chi.URLParam(r, "workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		writeErr(w, apierr.New(apierr.NotFound, "work item not found"))
		return
	}
	webx.WriteJSON(w, http.StatusOK, u)
}

func (d Deps) handleAgentRunSnapshot(w http.ResponseWriter, r *http.Request) {
	items, err := d.Repo.ListWorkspaceActiveTasks(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []orchapp.AgentRunSnapshotItem{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}
