package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contentapp "orkome/internal/content/app"
	"orkome/internal/platform/webx"
)

type delRepo struct {
	rows map[string]contentapp.Attachment
}

func (r *delRepo) InsertAttachment(_ context.Context, a contentapp.Attachment) (contentapp.Attachment, error) {
	a.ID = "gen-" + a.Filename
	r.rows[a.ID] = a
	return a, nil
}
func (r *delRepo) GetAttachment(_ context.Context, ws, id string) (contentapp.Attachment, bool, error) {
	a, ok := r.rows[id]
	return a, ok && a.WorkspaceID == ws, nil
}
func (r *delRepo) GetAttachmentByID(_ context.Context, id string) (contentapp.Attachment, bool, error) {
	a, ok := r.rows[id]
	return a, ok, nil
}
func (r *delRepo) DeleteAttachment(_ context.Context, ws, id string) (string, bool, error) {
	a, ok := r.rows[id]
	if !ok || a.WorkspaceID != ws {
		return "", false, nil
	}
	delete(r.rows, id)
	return a.StorageKey, true, nil
}
func (r *delRepo) ListByHost(_ context.Context, ws, hk, hid string) ([]contentapp.Attachment, error) {
	return nil, nil
}
func (r *delRepo) HostUsage(_ context.Context, ws, hk, hid string) (contentapp.HostUsage, error) {
	return contentapp.HostUsage{}, nil
}

func mountDel(t *testing.T, d Deps) http.Handler {
	t.Helper()
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(), &webx.Principal{
				UserID: r.Header.Get("X-Test-User"),
				Role:   r.Header.Get("X-Test-Role"),
				Source: webx.SourceSession,
			})))
		})
	}
	r := http.NewServeMux()
	MountAPI(r, d, auth)
	return r
}

func newDelFixture(t *testing.T, roleOf func(context.Context, string, string) (string, error)) (*delRepo, *sdStore, http.Handler) {
	t.Helper()
	fs := &sdStore{objs: map[string][]byte{}}
	_, _ = fs.Put(context.Background(), "ws/k1", "text/plain", strings.NewReader("body"))
	repo := &delRepo{rows: map[string]contentapp.Attachment{
		"att-1": {ID: "att-1", WorkspaceID: "ws", StorageKey: "ws/k1", CreatedBy: "uploader-1"},
	}}
	svc := contentapp.NewAttachmentService(repo, fs, []byte("del-secret"), nil)
	d := Deps{Attachments: svc, RoleOf: roleOf}
	return repo, fs, mountDel(t, d)
}

func delReq(h http.Handler, uid, role, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("DELETE", "/api/attachments/"+id, nil)
	req.Header.Set("X-Workspace-Id", "ws")
	if uid != "" {
		req.Header.Set("X-Test-User", uid)
	}
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestDeleteAuthzUploaderOK(t *testing.T) {
	repo, fs, h := newDelFixture(t, nil)
	w := delReq(h, "uploader-1", "", "att-1")
	require.Equal(t, 200, w.Code, "uploader's own deletion allowed")
	assert.NotContains(t, repo.rows, "att-1", "row deleted")
	assert.NotContains(t, fs.objs, "ws/k1", "objects cascade-cleaned")
}

func TestDeleteAuthzOtherMemberForbidden(t *testing.T) {
	repo, fs, h := newDelFixture(t, func(context.Context, string, string) (string, error) {
		return "member", nil
	})
	w := delReq(h, "member-2", "", "att-1")
	require.Equal(t, 403, w.Code, "non-uploader member rejected (any-member-deletes is too broad → tightened)")
	assert.Contains(t, w.Body.String(), "E_FORBIDDEN")
	assert.Contains(t, repo.rows, "att-1", "rows kept")
	assert.Contains(t, fs.objs, "ws/k1", "object kept")
}

func TestDeleteAuthzOwnerOK(t *testing.T) {
	repo, _, h := newDelFixture(t, func(context.Context, string, string) (string, error) {
		return "owner", nil
	})
	w := delReq(h, "the-owner", "", "att-1")
	require.Equal(t, 200, w.Code, "owner allowed (role injected via RoleOf bypass)")
	assert.NotContains(t, repo.rows, "att-1")
}

func TestDeleteAuthzAdminOK(t *testing.T) {
	repo, _, h := newDelFixture(t, func(context.Context, string, string) (string, error) {
		return "admin", nil
	})
	w := delReq(h, "the-admin", "", "att-1")
	require.Equal(t, 200, w.Code, "admin allowed")
	assert.NotContains(t, repo.rows, "att-1")
}

func TestDeleteAuthzUpstreamRoleHonored(t *testing.T) {

	_, _, h := newDelFixture(t, nil)
	w := delReq(h, "the-admin", "admin", "att-1")
	require.Equal(t, 200, w.Code, "upstream-injected admin role allowed (RoleOf=nil unaffected)")
}

func TestDeleteAuthzNotFound(t *testing.T) {
	_, _, h := newDelFixture(t, nil)
	w := delReq(h, "uploader-1", "", "no-such-id")
	assert.Equal(t, 404, w.Code, "does not exist → 404")
}

func TestDeleteAuthzCrossWorkspaceIs404(t *testing.T) {

	_, _, h := newDelFixture(t, nil)
	req := httptest.NewRequest("DELETE", "/api/attachments/att-1", nil)
	req.Header.Set("X-Workspace-Id", "other-ws")
	req.Header.Set("X-Test-User", "uploader-1")
	req.Header.Set("X-Test-Role", "owner")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, 404, w.Code, "cross-workspace → 404 (not 403)")
}

func TestDeleteAuthzRoleOfNilNonUploaderForbidden(t *testing.T) {

	repo, _, h := newDelFixture(t, nil)
	w := delReq(h, "someone-else", "", "att-1")
	require.Equal(t, 403, w.Code)
	assert.Contains(t, repo.rows, "att-1", "rows kept")
}
