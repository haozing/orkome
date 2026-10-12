package http

import (
	"encoding/json"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
)

func (d Deps) listCollaborators(w http.ResponseWriter, r *http.Request) {
	out, err := d.Autopilots.ListCollaborators(r.Context(), wsOf(r), r.PathValue("autopilotId"))
	if err != nil {
		writeErrReq(r, w, err)
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
	out, err := d.Autopilots.AddCollaborator(r.Context(), wsOf(r), r.PathValue("autopilotId"), in.UserID, grantedBy)
	if err != nil {
		writeErrReq(r, w, err)
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
	if err := d.Autopilots.RemoveCollaborator(r.Context(), wsOf(r), r.PathValue("autopilotId"), userID); err != nil {
		writeErrReq(r, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
