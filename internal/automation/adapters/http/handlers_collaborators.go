package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
)

func (d Deps) listCollaborators(w http.ResponseWriter, r *http.Request) {
	out, err := d.Autopilots.ListCollaborators(r.Context(), wsOf(r), chi.URLParam(r, "autopilotId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"collaborators": out})
}

func (d Deps) addCollaborator(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&in); err != nil || in.UserID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "user_id required", nil)
		return
	}
	p := webx.PrincipalFrom(r.Context())
	grantedBy := ""
	if p != nil {
		grantedBy = p.UserID
	}
	out, err := d.Autopilots.AddCollaborator(r.Context(), wsOf(r), chi.URLParam(r, "autopilotId"), in.UserID, grantedBy)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, out)
}

func (d Deps) removeCollaborator(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "user_id required", nil)
		return
	}
	if err := d.Autopilots.RemoveCollaborator(r.Context(), wsOf(r), chi.URLParam(r, "autopilotId"), userID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
