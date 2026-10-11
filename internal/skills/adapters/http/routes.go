package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	"orkome/internal/skills/app"
	"orkome/internal/skills/domain"
)

type Deps struct {
	Skills *app.SkillService
	Import *app.ImportService

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)

	SkillLabels SkillLabelEndpoints
}

func writeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrManifestMismatch) {
		slog.Error("skills: manifest mismatch on read (tampered content refused)")
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "skill content unavailable", nil)
		return
	}
	var ae *apierr.Error
	if errors.As(err, &ae) {
		status := map[string]int{
			apierr.NotFound: 404, apierr.Validation: 400, apierr.Conflict: 409,
		}[ae.Code]
		if status == 0 {
			status = 500
		}
		webx.WriteError(w, status, ae.Code, ae.Message, nil)
		return
	}
	webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
}

func Mount(mux *http.ServeMux, d Deps, auth func(http.Handler) http.Handler) {
	if d.Skills == nil {
		return
	}
	reg := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(webx.RequireAuth(d.memberMW(h))))
	}
	reg("GET /api/skills", d.handleList)
	reg("POST /api/skills", d.handleCreate)
	reg("GET /api/skills/search", d.handleSearch)
	if d.Import != nil {
		reg("POST /api/skills/import", d.handleImport)
	}
	reg("GET /api/skills/{skillId}", d.handleGet)
	reg("PUT /api/skills/{skillId}", d.handleUpdate)
	reg("DELETE /api/skills/{skillId}", d.handleDelete)
	reg("POST /api/skills/{skillId}/refresh", d.handleRefresh)
	reg("GET /api/skills/{skillId}/files", d.handleFiles)
	reg("PUT /api/skills/{skillId}/files", d.handleUpsertFile)
	reg("DELETE /api/skills/{skillId}/files/{fileId}", d.handleDeleteFile)

	reg("GET /api/skills/{skillId}/agents", d.handleSkillAgents)

	reg("GET /api/agents/{agentId}/skills", d.handleAgentSkills)
	reg("PUT /api/agents/{agentId}/skills", d.handleSetAgentSkills)
	reg("POST /api/agents/{agentId}/skills/add", d.handleAddAgentSkill)
	reg("PUT /api/agents/{agentId}/skills/{skillId}/enabled", d.handleSetSkillEnabled)
	reg("DELETE /api/agents/{agentId}/skills/{skillId}", d.handleRemoveAgentSkill)

	d.mountSkillLabels(reg)
}

func (d Deps) memberMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := webx.PrincipalFrom(r.Context())
		ws := r.Header.Get("X-Workspace-Id")
		if ws == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
			return
		}
		if d.MemberCheck != nil {
			ok, err := d.MemberCheck(r.Context(), ws, p.UserID)
			if err != nil || !ok {
				webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "not a member of this workspace", nil)
				return
			}
		}
		p.WorkspaceID = ws
		next.ServeHTTP(w, r)
	})
}

func (d Deps) ws(r *http.Request) string { return webx.PrincipalFrom(r.Context()).WorkspaceID }

func (d Deps) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := d.Skills.List(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []app.Skill{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"skills": list})
}

func (d Deps) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in app.Skill
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	in.WorkspaceID = d.ws(r)
	in.CreatedBy = webx.PrincipalFrom(r.Context()).UserID
	in.Origin = "manual"
	sk, err := d.Skills.Create(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, sk)
}

func (d Deps) handleGet(w http.ResponseWriter, r *http.Request) {
	sk, err := d.Skills.Get(r.Context(), d.ws(r), r.PathValue("skillId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, sk)
}

func (d Deps) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := d.Skills.Delete(r.Context(), d.ws(r), r.PathValue("skillId")); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}

func (d Deps) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	sk, err := d.Skills.Update(r.Context(), d.ws(r), r.PathValue("skillId"), in.Name, in.Description)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, sk)
}

func (d Deps) handleUpsertFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&in); err != nil || in.Path == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "path and content required", nil)
		return
	}
	f, manifest, err := d.Skills.UpsertFile(r.Context(), d.ws(r), r.PathValue("skillId"), in.Path, in.Content)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"file": f, "manifest_sha256": manifest})
}

func (d Deps) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	manifest, err := d.Skills.DeleteFile(r.Context(), d.ws(r),
		r.PathValue("skillId"), r.PathValue("fileId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "deleted", "manifest_sha256": manifest})
}

func (d Deps) handleSearch(w http.ResponseWriter, r *http.Request) {
	list, err := d.Skills.Search(r.Context(), d.ws(r), r.URL.Query().Get("q"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []app.Skill{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"skills": list})
}

func (d Deps) handleFiles(w http.ResponseWriter, r *http.Request) {
	files, manifest, err := d.Skills.Files(r.Context(), d.ws(r), r.PathValue("skillId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if files == nil {
		files = []app.SkillFile{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"files": files, "manifest_sha256": manifest})
}

func (d Deps) handleImport(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var in struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil || in.URL == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "url required", nil)
			return
		}
		p := webx.PrincipalFrom(r.Context())
		sk, err := d.Import.ImportURL(r.Context(), d.ws(r), p.UserID, in.URL)
		if err != nil {
			writeErr(w, err)
			return
		}
		webx.WriteJSON(w, http.StatusCreated, sk)
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid multipart", nil)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "file field required", nil)
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 50<<20+1))
	if err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "unreadable file", nil)
		return
	}
	p := webx.PrincipalFrom(r.Context())
	sk, err := d.Import.ImportZip(r.Context(), d.ws(r), p.UserID, b, hdr.Filename)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, sk)
}

func (d Deps) handleRefresh(w http.ResponseWriter, r *http.Request) {
	n, err := d.Import.Refresh(r.Context(), d.ws(r), r.PathValue("skillId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"refreshed": n})
}

func (d Deps) handleSkillAgents(w http.ResponseWriter, r *http.Request) {
	list, err := d.Skills.ListSkillAgents(r.Context(), d.ws(r), r.PathValue("skillId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"agents": list})
}

func (d Deps) handleAgentSkills(w http.ResponseWriter, r *http.Request) {
	list, err := d.Skills.ListAgentSkills(r.Context(), d.ws(r), r.PathValue("agentId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []app.AgentSkill{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"skills": list})
}

func (d Deps) handleSetAgentSkills(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SkillIDs []string `json:"skill_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	if err := d.Skills.SetAgentSkills(r.Context(), d.ws(r), r.PathValue("agentId"), in.SkillIDs); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"set": len(in.SkillIDs)})
}

func (d Deps) handleAddAgentSkill(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SkillID string `json:"skill_id"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in)
	if in.SkillID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "skill_id required", nil)
		return
	}
	if err := d.Skills.AddAgentSkill(r.Context(), d.ws(r), r.PathValue("agentId"), in.SkillID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "added"})
}

func (d Deps) handleSetSkillEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in)
	if err := d.Skills.SetAgentSkillEnabled(r.Context(), d.ws(r),
		r.PathValue("agentId"), r.PathValue("skillId"), in.Enabled); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]bool{"enabled": in.Enabled})
}

func (d Deps) handleRemoveAgentSkill(w http.ResponseWriter, r *http.Request) {
	if err := d.Skills.RemoveAgentSkill(r.Context(), d.ws(r),
		r.PathValue("agentId"), r.PathValue("skillId")); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "removed"})
}
