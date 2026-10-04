package http

import (
	"encoding/json"
	"net/http"

	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
)

func (d Deps) handleUITokenMint(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	if p == nil {
		webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "authentication required", nil)
		return
	}
	var in struct {
		Plugin      string `json:"plugin"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil ||
		in.Plugin == "" || in.WorkspaceID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "plugin and workspace_id required", nil)
		return
	}

	if _, ok, err := d.Repo.GetMember(r.Context(), in.WorkspaceID, p.UserID); err != nil || !ok {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "workspace membership required", nil)
		return
	}
	token, err := d.UITokens.Mint(r.Context(), in.WorkspaceID, p.UserID, in.Plugin)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{"token": token, "expires_in": 60})
}

func (d Deps) handleUITokenConsume(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil || in.Token == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "token required", nil)
		return
	}
	claims, err := d.UITokens.Consume(r.Context(), in.Token)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id": claims.Sub, "workspace_id": claims.WorkspaceID, "plugin": claims.Plugin,
	})
}
