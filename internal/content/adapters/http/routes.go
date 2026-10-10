package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"orkome/internal/content/app"
	"orkome/internal/contract/apierr"
	"github.com/haozing/ploykit/platform/storagex"
	"orkome/internal/platform/webx"
)

type Deps struct {
	Attachments *app.AttachmentService
	Avatars     *app.AvatarService

	Local *storagex.LocalBackend

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)

	RoleOf func(ctx context.Context, workspaceID, userID string) (string, error)
}

func writeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, app.ErrDigestMismatch) {
		slog.Error("content: attachment digest mismatch (tampered object refused)",
			"err", err.Error())
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "content unavailable", nil)
		return
	}
	var ae *apierr.Error
	if errors.As(err, &ae) {
		status := map[string]int{apierr.NotFound: 404, apierr.Validation: 400, apierr.Forbidden: 403}[ae.Code]
		if status == 0 {
			status = 500
		}
		webx.WriteError(w, status, ae.Code, ae.Message, nil)
		return
	}
	webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
}

func MountAPI(mux *http.ServeMux, d Deps, auth func(http.Handler) http.Handler) {
	if d.Attachments == nil {
		return
	}
	reg := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(webx.RequireAuth(d.memberMW(h))))
	}
	reg("POST /api/upload-file", d.handleUpload)

	reg("GET /api/work-items/{workItemId}/attachments", d.handleListByWorkItem)
	reg("GET /api/attachments/{attachmentId}", d.handleGet)
	reg("GET /api/attachments/{attachmentId}/download", d.handleDownload)
	reg("GET /api/attachments/{attachmentId}/content", d.handleContent)
	reg("DELETE /api/attachments/{attachmentId}", d.handleDelete)

	mux.Handle("GET /api/attachments/{attachmentId}/signed-download", auth(http.HandlerFunc(d.handleSignedDownload)))

	if d.Avatars != nil {
		mux.HandleFunc("GET /api/avatars/{rest...}", d.handleAvatar)
	}
}

func MountUploads(mux *http.ServeMux, d Deps) {
	if d.Local == nil {
		return
	}
	h := http.StripPrefix("/uploads/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if !storagex.ValidKey(key) {
			http.NotFound(w, r)
			return
		}
		rc, obj, err := d.Local.Open(r.Context(), key)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer rc.Close()

		disp := "inline"
		if m, err := d.Local.LoadMeta(key); err == nil && m.Disposition == "attachment" {
			disp = "attachment"
		}
		w.Header().Set("Content-Type", obj.ContentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", disp+`; filename="`+sanitizeQuote(baseOf(key))+`"`)
		if obj.Size > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
		}
		_, _ = io.Copy(w, rc)
	}))
	mux.Handle("GET /uploads/{rest...}", h)
	mux.Handle("HEAD /uploads/{rest...}", h)
}

func baseOf(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}

func sanitizeQuote(s string) string { return strings.ReplaceAll(s, `"`, `'`) }

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

		if p.Role == "" && d.RoleOf != nil {
			role, err := d.RoleOf(r.Context(), ws, p.UserID)
			if err != nil {
				webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "role lookup failed", nil)
				return
			}
			p.Role = role
		}
		p.WorkspaceID = ws
		next.ServeHTTP(w, r)
	})
}

func (d Deps) ws(r *http.Request) string { return webx.PrincipalFrom(r.Context()).WorkspaceID }

func (d Deps) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid multipart", nil)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "file field required", nil)
		return
	}
	defer file.Close()
	p := webx.PrincipalFrom(r.Context())
	a, err := d.Attachments.Upload(r.Context(), d.ws(r),
		r.FormValue("host_kind"), r.FormValue("host_id"),
		hdr.Filename, hdr.Header.Get("Content-Type"), file, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, a)
}

func (d Deps) handleGet(w http.ResponseWriter, r *http.Request) {
	a, err := d.Attachments.Get(r.Context(), d.ws(r), r.PathValue("attachmentId"))
	if err != nil {
		writeErr(w, err)
		return
	}

	mode := "inline"
	if strings.Contains(a.ContentType, "svg") {
		mode = "attachment"
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"attachment": a, "inline": mode == "inline"})
}

func (d Deps) handleListByWorkItem(w http.ResponseWriter, r *http.Request) {
	list, err := d.Attachments.ListByHost(r.Context(), d.ws(r), "work_item", r.PathValue("workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (d Deps) streamOut(w http.ResponseWriter, r *http.Request, disp string, rc io.ReadCloser, a app.Attachment) {
	defer rc.Close()
	if strings.Contains(a.ContentType, "svg") {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", disp+`; filename="`+sanitizeQuote(a.Filename)+`"`)
	w.Header().Set("ETag", `"`+a.SHA256+`"`)
	if rng := parseRange(r.Header.Get("Range"), a.Size()); rng != nil {
		w.Header().Set("Content-Range",
			fmt.Sprintf("bytes %d-%d/%d", rng[0], rng[1], a.Size()))
		w.WriteHeader(http.StatusPartialContent)
		if sk, ok := rc.(io.ReadSeeker); ok {
			if _, err := sk.Seek(rng[0], io.SeekStart); err == nil {
				_, _ = io.CopyN(w, rc, rng[1]-rng[0]+1)
			}
		} else {
			_, _ = io.CopyN(io.Discard, rc, rng[0])
			_, _ = io.CopyN(w, rc, rng[1]-rng[0]+1)
		}
		return
	}
	_, _ = io.Copy(w, rc)
}

func parseRange(h string, size int64) *[2]int64 {
	if !strings.HasPrefix(h, "bytes=") {
		return nil
	}
	spec := strings.TrimPrefix(h, "bytes=")
	if strings.Contains(spec, ",") {
		return nil
	}
	dash := strings.Index(spec, "-")
	if dash < 0 {
		return nil
	}
	startStr, endStr := strings.TrimSpace(spec[:dash]), strings.TrimSpace(spec[dash+1:])
	var start, end int64
	if startStr == "" {
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n <= 0 || size <= 0 {
			return nil
		}
		if n > size {
			n = size
		}
		return &[2]int64{size - n, size - 1}
	}
	var err error
	if start, err = strconv.ParseInt(startStr, 10, 64); err != nil || start < 0 || start >= size {
		return nil
	}
	if endStr == "" {
		end = size - 1
	} else if end, err = strconv.ParseInt(endStr, 10, 64); err != nil || end < start || end >= size {
		return nil
	}
	return &[2]int64{start, end}
}

func (d Deps) handleDownload(w http.ResponseWriter, r *http.Request) {
	d.openStream(w, r, "attachment")
}

func (d Deps) handleContent(w http.ResponseWriter, r *http.Request) {
	d.openStream(w, r, "inline")
}

func (d Deps) openStream(w http.ResponseWriter, r *http.Request, disp string) {
	rc, a, err := d.Attachments.Open(r.Context(), d.ws(r), r.PathValue("attachmentId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	d.streamOut(w, r, disp, rc, a)
}

func (d Deps) handleSignedDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("attachmentId")
	q := r.URL.Query()
	intent := q.Get("intent")
	if intent == "" {
		intent = app.IntentDownload
	}
	member := q.Get("m")
	if !d.Attachments.VerifySigned(id, intent, member, q.Get("exp"), q.Get("sig")) {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "invalid or expired signature", nil)
		return
	}

	if p := webx.PrincipalFrom(r.Context()); p != nil && d.Attachments.BindsMember() && p.UserID != member {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "signature bound to another member", nil)
		return
	}
	rc, a, err := d.Attachments.OpenSigned(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	disp := "attachment"
	if intent == app.IntentContent {
		disp = "inline"
	}
	d.streamOut(w, r, disp, rc, a)
}

func (d Deps) handleDelete(w http.ResponseWriter, r *http.Request) {
	a, err := d.Attachments.Get(r.Context(), d.ws(r), r.PathValue("attachmentId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	p := webx.PrincipalFrom(r.Context())
	if a.CreatedBy != p.UserID && p.Role != "owner" && p.Role != "admin" {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden,
			"only the uploader or a workspace admin can delete this attachment", nil)
		return
	}
	if err := d.Attachments.Delete(r.Context(), d.ws(r), a.ID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}

func (d Deps) handleAvatar(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("rest")
	if key == "" || !storagex.ValidKey(key) {
		http.NotFound(w, r)
		return
	}
	if !d.Avatars.Verify(key, r.URL.Query().Get("exp"), r.URL.Query().Get("sig")) {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "invalid or expired signature", nil)
		return
	}
	rc, obj, err := d.Avatars.Open(r.Context(), key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	_, _ = io.Copy(w, rc)
}
