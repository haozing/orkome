package http

import (
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
)

func requireWorkspaceAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := webx.PrincipalFrom(r.Context())
	if p.Role != "owner" && p.Role != "admin" {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "workspace owner or admin role required", nil)
		return false
	}
	return true
}

func (d Deps) handleCredentialList(w http.ResponseWriter, r *http.Request) {
	list, err := d.Credentials.List(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"credentials": list})
}

func (d Deps) handleCredentialCreate(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	in, ok := decode[struct {
		Provider string `json:"provider"`
		Secret   string `json:"secret"`
	}](w, r)
	if !ok {
		return
	}
	p := webx.PrincipalFrom(r.Context())
	c, err := d.Credentials.Create(r.Context(), d.ws(r), in.Provider, in.Secret, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, c)
}

func (d Deps) handleCredentialDelete(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	if err := d.Credentials.Delete(r.Context(), d.ws(r), r.PathValue("credentialId")); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}
