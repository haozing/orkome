package http

import (
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	orchapp "orkome/internal/orchestration/app"
)

// pack 工具 schema 存档（迁移 1035）的 HTTP 面：
// GET 只读给前端/调试兜底；PUT/DELETE 由 pack-provision 调用（workspace admin）。
// 与 ui-entries 同构：workspace 取 X-Workspace-Id header，pack 取路径段。
// 接口冻结见 docs/interfaces-job-handle-2026-10-11.md §7。

func (d Deps) handlePackToolSchemaList(w http.ResponseWriter, r *http.Request) {
	items, err := d.PackToolSchemas.ListPackToolSchemas(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []orchapp.PackToolSchema{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d Deps) handlePackToolSchemaReplace(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	in, ok := decode[struct {
		Items []orchapp.PackToolSchema `json:"items"`
	}](w, r)
	if !ok {
		return
	}
	if err := d.PackToolSchemas.Replace(r.Context(), d.ws(r), r.PathValue("pack"), in.Items); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "replaced"})
}

func (d Deps) handlePackToolSchemaDelete(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	if err := d.PackToolSchemas.Delete(r.Context(), d.ws(r), r.PathValue("pack")); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "deleted"})
}
