package http

import (
	"net/http"

	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
	"orkome/internal/work/app"
	"orkome/internal/work/domain"
)

func (d Deps) handleSourcePreview(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	out, err := d.SourceCtx.Preview(r.Context(), wsID(r), r.PathValue("commentId"), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) handleSourceCreate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		CaptureToken string `json:"capture_token"`
		Mode         string `json:"mode"`
		Title        string `json:"title"`
	}](w, r)
	if !ok {
		return
	}
	if in.Mode == "" {
		in.Mode = "manual"
	}
	if in.Mode != "manual" && in.Mode != "agent" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "mode must be manual|agent", nil)
		return
	}
	p := webx.PrincipalFrom(r.Context())
	child, err := d.SourceCtx.CreateFromComment(r.Context(), wsID(r), r.PathValue("commentId"),
		p.UserID, in.CaptureToken, in.Mode, in.Title)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, child)
}

func (d Deps) handleQuickCreate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		SourceDigest string `json:"source_digest"`
		ParentID     string `json:"parent_work_item_id"`
	}](w, r)
	if !ok {
		return
	}
	p := webx.PrincipalFrom(r.Context())
	var (
		child *app.WorkItem
		err   error
	)
	if in.SourceDigest != "" {
		child, err = d.SourceCtx.CreateFromDigest(r.Context(), wsID(r), in.SourceDigest, p.UserID, in.Title)
	} else {
		child, err = d.WorkItems.Create(r.Context(), app.NewWorkItem{
			WorkspaceID: wsID(r), Title: in.Title, Description: in.Description, StatusKey: domain.StatusTodo,
			CreatorType: "user", CreatorID: p.UserID, ParentID: in.ParentID,
			OriginType: app.OriginQuickCreate,
		}, p.UserID)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, child)
}
