package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"orkome/internal/channels/app"
	"orkome/internal/channels/domain"
	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
)

type Deps struct {
	Titles *app.TitleCallbackService
	Repo   app.Repo
}

func Mount(r chi.Router, d Deps) {
	r.Post("/channels/title-callback", d.handleTitleCallback)
}

type titleCallbackRequest struct {
	InstallationID string `json:"installation_id"`
	ExternalChatID string `json:"external_chat_id"`
	Title          string `json:"title"`
}

func (d Deps) handleTitleCallback(w http.ResponseWriter, r *http.Request) {
	if d.Titles == nil || d.Repo == nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "title callback not wired", nil)
		return
	}
	var in titleCallbackRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	if in.InstallationID == "" || in.ExternalChatID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "installation_id and external_chat_id required", nil)
		return
	}

	proof := r.Header.Get("X-Channel-Config")
	if proof == "" {
		webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "missing channel credential proof", nil)
		return
	}
	inst, err := d.Repo.GetInstallation(r.Context(), in.InstallationID)
	if err != nil {
		if errors.Is(err, app.ErrInstallationNotFound) {

			webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "invalid credential proof", nil)
			return
		}
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
		return
	}
	if domain.CredentialFingerprint(string(inst.Kind), []byte(proof)) != inst.Fingerprint {
		webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "invalid credential proof", nil)
		return
	}

	applied, err := d.Titles.ApplyTitleCallback(r.Context(), in.InstallationID, in.ExternalChatID, in.Title)
	if err != nil {
		if errors.Is(err, app.ErrTitleCallbackRejected) {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid title", nil)
			return
		}
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
		return
	}
	if !applied {

		webx.WriteJSON(w, http.StatusOK, map[string]any{"applied": false})
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"applied": true})
}
