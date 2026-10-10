package http

import (
	"context"
	"encoding/json"
	"net/http"

	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
)

type AgentLabelView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type AgentLabelEndpoints struct {
	List   func(ctx context.Context, workspaceID, agentID string) ([]AgentLabelView, error)
	Attach func(ctx context.Context, workspaceID, agentID, labelID string) ([]AgentLabelView, error)
	Detach func(ctx context.Context, workspaceID, agentID, labelID string) ([]AgentLabelView, error)
}

func (e AgentLabelEndpoints) mounted() bool {
	return e.List != nil && e.Attach != nil && e.Detach != nil
}

func (d Deps) mountAgentLabels(reg func(pattern string, h http.HandlerFunc)) {
	if d.AgentLabels == nil || !d.AgentLabels.mounted() {
		return
	}
	reg("GET /api/agents/{agentId}/labels", d.handleAgentLabelsList)
	reg("POST /api/agents/{agentId}/labels", d.handleAgentLabelsAttach)
	reg("DELETE /api/agents/{agentId}/labels/{labelId}", d.handleAgentLabelsDetach)
}

func (d Deps) handleAgentLabelsList(w http.ResponseWriter, r *http.Request) {
	out, err := d.AgentLabels.List(r.Context(), d.ws(r), r.PathValue("agentId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if out == nil {
		out = []AgentLabelView{}
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) handleAgentLabelsAttach(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LabelID string `json:"label_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil || in.LabelID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "label_id required", nil)
		return
	}
	out, err := d.AgentLabels.Attach(r.Context(), d.ws(r), r.PathValue("agentId"), in.LabelID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) handleAgentLabelsDetach(w http.ResponseWriter, r *http.Request) {
	labelID := r.PathValue("labelId")
	out, err := d.AgentLabels.Detach(r.Context(), d.ws(r), r.PathValue("agentId"), labelID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}
