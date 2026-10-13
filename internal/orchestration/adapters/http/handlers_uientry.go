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
	// slots 已在 repo 层 COALESCE 为 []——一个端点两用：深链入口与 slot 宿主
	// 都读 GET /api/ui-entries，slots 随行返回（前端设计 §1.2，无新路由）。
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d Deps) handleUIEntryUpsert(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	in, ok := decode[struct {
		Pack        string               `json:"pack"`
		PackID      string               `json:"pack_id"`
		Label       string               `json:"label"`
		URLTemplate string               `json:"url_template"`
		Slots       []orchapp.PackUISlot `json:"slots"`
	}](w, r)
	if !ok {
		return
	}
	if err := d.PackUIEntries.UpsertWithSlots(r.Context(), d.ws(r), in.Pack, in.PackID, in.Label, in.URLTemplate, in.Slots); err != nil {
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
