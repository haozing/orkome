package http

import (
	"context"
	"encoding/json"
	"net/http"

	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
)

type SkillLabelView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type SkillLabelEndpoints struct {
	List   func(ctx context.Context, workspaceID, skillID string) ([]SkillLabelView, error)
	Attach func(ctx context.Context, workspaceID, skillID, labelID string) ([]SkillLabelView, error)
	Detach func(ctx context.Context, workspaceID, skillID, labelID string) ([]SkillLabelView, error)
}

func (e SkillLabelEndpoints) mounted() bool {
	return e.List != nil && e.Attach != nil && e.Detach != nil
}

func (d Deps) mountSkillLabels(reg func(string, http.HandlerFunc)) {
	if !d.SkillLabels.mounted() {
		return
	}
	reg("GET /api/skills/{skillId}/labels", d.handleSkillLabelsList)
	reg("POST /api/skills/{skillId}/labels", d.handleSkillLabelsAttach)
	reg("DELETE /api/skills/{skillId}/labels/{labelId}", d.handleSkillLabelsDetach)
}

func (d Deps) handleSkillLabelsList(w http.ResponseWriter, r *http.Request) {
	out, err := d.SkillLabels.List(r.Context(), d.ws(r), r.PathValue("skillId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if out == nil {
		out = []SkillLabelView{}
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) handleSkillLabelsAttach(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LabelID string `json:"label_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil || in.LabelID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "label_id required", nil)
		return
	}
	out, err := d.SkillLabels.Attach(r.Context(), d.ws(r), r.PathValue("skillId"), in.LabelID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) handleSkillLabelsDetach(w http.ResponseWriter, r *http.Request) {
	labelID := r.PathValue("labelId")
	out, err := d.SkillLabels.Detach(r.Context(), d.ws(r), r.PathValue("skillId"), labelID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}
