package http

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

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

func (d Deps) mountAgentLabels(r chi.Router) {
	if d.AgentLabels == nil || !d.AgentLabels.mounted() {
		return
	}
	r.Route("/agents/{agentId}/labels", func(r chi.Router) {
		r.Get("/", d.handleAgentLabelsList)
		r.Post("/", d.handleAgentLabelsAttach)
	})
	r.Delete("/agents/{agentId}/labels/{labelId}", d.handleAgentLabelsDetach)
}

func (d Deps) handleAgentLabelsList(w http.ResponseWriter, r *http.Request) {
	out, err := d.AgentLabels.List(r.Context(), d.ws(r), chi.URLParam(r, "agentId"))
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
	out, err := d.AgentLabels.Attach(r.Context(), d.ws(r), chi.URLParam(r, "agentId"), in.LabelID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) handleAgentLabelsDetach(w http.ResponseWriter, r *http.Request) {
	labelID := chi.URLParam(r, "labelId")
	out, err := d.AgentLabels.Detach(r.Context(), d.ws(r), chi.URLParam(r, "agentId"), labelID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}
