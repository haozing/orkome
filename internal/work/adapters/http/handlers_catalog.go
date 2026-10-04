package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"orkome/internal/platform/webx"
	workapp "orkome/internal/work/app"
)

func (d Deps) handleGetLabel(w http.ResponseWriter, r *http.Request) {
	l, err := d.Catalog.GetLabel(r.Context(), wsID(r), chi.URLParam(r, "labelId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, l)
}

func (d Deps) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Name     *string `json:"name"`
		Icon     *string `json:"icon"`
		Category *string `json:"category"`
	}](w, r)
	if !ok {
		return
	}
	def, err := d.Catalog.UpdateStatus(r.Context(), wsID(r), chi.URLParam(r, "statusKey"), workapp.StatusDefPatch{
		Name: in.Name, Icon: in.Icon, Category: in.Category,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, def)
}

func (d Deps) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := d.Catalog.GetProject(r.Context(), wsID(r), chi.URLParam(r, "projectId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, p)
}

func (d Deps) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}](w, r)
	if !ok {
		return
	}
	p, err := d.Catalog.UpdateProject(r.Context(), wsID(r), chi.URLParam(r, "projectId"), in.Name, in.Description)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, p)
}

func (d Deps) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if err := d.Catalog.DeleteProject(r.Context(), wsID(r), chi.URLParam(r, "projectId")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleSearchProjects(w http.ResponseWriter, r *http.Request) {
	items, err := d.Catalog.SearchProjects(r.Context(), wsID(r), r.URL.Query().Get("q"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d Deps) handleListProjectResources(w http.ResponseWriter, r *http.Request) {
	items, err := d.Catalog.ListProjectResources(r.Context(), wsID(r), chi.URLParam(r, "projectId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d Deps) handleCreateProjectResource(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Kind string `json:"kind"`
		URI  string `json:"uri"`
	}](w, r)
	if !ok {
		return
	}
	res, err := d.Catalog.CreateProjectResource(r.Context(), wsID(r), chi.URLParam(r, "projectId"), in.Kind, in.URI)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, res)
}

func (d Deps) handleUpdateProjectResource(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Kind string `json:"kind"`
		URI  string `json:"uri"`
	}](w, r)
	if !ok {
		return
	}
	res, err := d.Catalog.UpdateProjectResource(r.Context(), wsID(r), chi.URLParam(r, "projectId"),
		chi.URLParam(r, "resourceId"), in.Kind, in.URI)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, res)
}

func (d Deps) handleDeleteProjectResource(w http.ResponseWriter, r *http.Request) {
	if err := d.Catalog.DeleteProjectResource(r.Context(), wsID(r), chi.URLParam(r, "projectId"),
		chi.URLParam(r, "resourceId")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleGetPropertyDef(w http.ResponseWriter, r *http.Request) {
	def, err := d.Catalog.GetPropertyDef(r.Context(), wsID(r), chi.URLParam(r, "key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, def)
}

func (d Deps) handleUpdatePropertyDef(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Name        *string   `json:"name"`
		Description *string   `json:"description"`
		Options     *[]string `json:"options"`
	}](w, r)
	if !ok {
		return
	}
	patch := workapp.PropertyDefPatch{Name: in.Name, Description: in.Description}
	if in.Options != nil {
		patch.Options = *in.Options
	}
	def, err := d.Catalog.UpdatePropertyDef(r.Context(), wsID(r), chi.URLParam(r, "key"), patch)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, def)
}

func (d Deps) handleDeletePropertyDef(w http.ResponseWriter, r *http.Request) {
	if err := d.Catalog.DeletePropertyDef(r.Context(), wsID(r), chi.URLParam(r, "key")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
