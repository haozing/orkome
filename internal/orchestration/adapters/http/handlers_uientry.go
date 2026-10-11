package http

import (
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	orchapp "orkome/internal/orchestration/app"
)

func (d Deps) handleUIEntryList(w http.ResponseWriter, r *http.Request) {
	items, err := d.PackUIEntries.List(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []orchapp.PackUIEntry{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d Deps) handleUIEntryUpsert(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	in, ok := decode[struct {
		Pack        string `json:"pack"`
		Label       string `json:"label"`
		URLTemplate string `json:"url_template"`
	}](w, r)
	if !ok {
		return
	}
	if err := d.PackUIEntries.Upsert(r.Context(), d.ws(r), in.Pack, in.Label, in.URLTemplate); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{"result": "upserted"})
}

func (d Deps) handleUIEntryDelete(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	if err := d.PackUIEntries.Delete(r.Context(), d.ws(r), r.PathValue("pack")); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "deleted"})
}
