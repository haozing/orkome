package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"github.com/haozing/ploykit/platform/storagex"
	"github.com/haozing/ploykit/platform/webx"
	contentpgrepo "orkome/internal/content/adapters/pgrepo"
	contentapp "orkome/internal/content/app"
	"orkome/internal/contract/apierr"
	"orkome/migrations"
)

type b7h struct {
	t       *testing.T
	handler http.Handler
	att     *contentapp.AttachmentService
	av      *contentapp.AvatarService
	local   *storagex.LocalBackend
	pool    *pgxpool.Pool
	wsID    string
	userID  string
}

func newB7H(t *testing.T) *b7h {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_b7h")
	admin.Close(context.Background())
	pool, err := pgxpool.New(context.Background(), strings.Replace(base, "/orkome", "/orkome_b7h", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `TRUNCATE attachment, member, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	ctx := context.Background()
	var userID, wsID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('b7@x.io','B7') RETURNING id::text`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ('b7','b7-h') RETURNING id::text`).Scan(&wsID))
	_, err = pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, wsID, userID)
	require.NoError(t, err)

	secret := []byte("b7-signing-secret-32-bytes-ok!!!!!!")
	local, err := storagex.NewLocal(t.TempDir())
	require.NoError(t, err)
	repo := contentpgrepo.New(pg.WrapPool(pool))
	att := contentapp.NewAttachmentService(repo, local, secret, nil)
	av := contentapp.NewAvatarService(local, secret, nil)

	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid := r.Header.Get("X-Test-User")
			if uid == "" {
				uid = userID
			}
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(), &webx.Principal{
				UserID: uid, Source: webx.SourceSession,
			})))
		})
	}
	memberCheck := func(ctx context.Context, ws, uid string) (bool, error) {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM member WHERE workspace_id=$1 AND user_id=$2)`, ws, uid).Scan(&exists)
		return exists, err
	}

	roleOf := func(ctx context.Context, ws, uid string) (string, error) {
		var role string
		err := pool.QueryRow(ctx,
			`SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2`, ws, uid).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return role, err
	}

	hostCheck := func(ctx context.Context, ws, kind, hid string) (bool, error) {
		table := map[string]string{"work_item": "work_item", "comment": "comment",
			"chat_session": "chat_session", "chat_message": "chat_message",
			"task": "agent_task_queue"}[kind]
		if table == "" {
			return true, nil
		}
		var exists bool
		err := pool.QueryRow(ctx, fmt.Sprintf(
			`SELECT EXISTS(SELECT 1 FROM %s WHERE workspace_id=$1 AND id=$2)`, table), ws, hid).Scan(&exists)
		return exists, err
	}
	att.SetHostChecker(hostCheck)
	r := http.NewServeMux()
	MountAPI(r, Deps{Attachments: att, Avatars: av, Local: local,
		MemberCheck: memberCheck, RoleOf: roleOf}, auth)
	MountUploads(r, Deps{Local: local})
	return &b7h{t: t, handler: r, att: att, av: av, local: local, pool: pool, wsID: wsID, userID: userID}
}

func (h *b7h) req(method, path string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	rd := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range hdr {
		rd.Header.Set(k, v)
	}
	rd.Header.Set("X-Workspace-Id", h.wsID)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, rd)
	return w
}

func (h *b7h) upload(t *testing.T, filename, body string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write([]byte(body))
	_ = mw.WriteField("host_kind", "work_item")
	_ = mw.Close()
	w := h.req("POST", "/api/upload-file", buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()})
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestB7_AttachmentLifecycle(t *testing.T) {
	h := newB7H(t)

	code, out := h.upload(t, "notes.txt", "hello attachment")
	require.Equal(t, 201, code, "upload: %v", out)
	id, _ := out["id"].(string)
	require.NotEmpty(t, id)
	assert.Equal(t, int64(16), toI64(out["size_bytes"]))
	assert.Len(t, out["sha256"], 64)
	assert.NotContains(t, out, "storage_key", "storage key absent from the response")

	w := h.req("GET", "/api/attachments/"+id, nil, nil)
	require.Equal(t, 200, w.Code)
	var meta map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &meta)
	assert.Equal(t, true, meta["inline"])

	w = h.req("GET", "/api/attachments/"+id+"/download", nil, nil)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "hello attachment", w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")

	w = h.req("GET", "/api/attachments/"+id+"/content", nil, nil)
	require.Equal(t, 200, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "inline")

	ctx := context.Background()
	url, err := h.att.SignedURL(ctx, h.wsID, id, "download", h.userID, 0)
	require.NoError(t, err)
	w = h.req("GET", "/api"+url, nil, nil)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "hello attachment", w.Body.String())
	w = h.req("GET", "/api/attachments/"+id+"/signed-download?exp=99999999999&intent=download&sig=forged", nil, nil)
	assert.Equal(t, 403, w.Code)

	w = h.req("GET", "/uploads/", nil, nil)
	assert.Equal(t, 404, w.Code)
	w = h.req("GET", "/uploads/../secret", nil, nil)
	// stdlib ServeMux canonicalizes dot-dot segments with a redirect before
	// matching (chi answered 404 here); either outcome keeps storage unreachable.
	assert.Contains(t, []int{http.StatusMovedPermanently, http.StatusNotFound}, w.Code)

	w = h.req("DELETE", "/api/attachments/"+id, nil, nil)
	require.Equal(t, 200, w.Code)
	w = h.req("GET", "/api/attachments/"+id, nil, nil)
	assert.Equal(t, 404, w.Code)
}

func TestB7_SVGForcedDownload(t *testing.T) {
	h := newB7H(t)
	code, out := h.upload(t, "evil.svg", "<svg onload=alert(1)>")
	require.Equal(t, 201, code, "upload: %v", out)
	id, _ := out["id"].(string)

	w := h.req("GET", "/api/attachments/"+id, nil, nil)
	var meta map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &meta)
	assert.Equal(t, false, meta["inline"], "SVG auto mode should judge non-inline")

	w = h.req("GET", "/api/attachments/"+id+"/content", nil, nil)
	require.Equal(t, 200, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment", "SVG always downloads")
}

func TestB7_AvatarSignedURL(t *testing.T) {
	h := newB7H(t)
	ctx := context.Background()

	_, err := h.local.Put(ctx, "avatars/u1.png", "image/png", strings.NewReader("pngdata"))
	require.NoError(t, err)

	u := h.av.Sign("avatars/u1.png", 0)
	require.True(t, strings.HasPrefix(u, "/avatars/avatars/u1.png?"), "signed URL form: %s", u)
	w := h.req("GET", "/api"+u, nil, nil)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "pngdata", w.Body.String())

	w = h.req("GET", "/api/avatars/avatars/u1.png?exp=99999999999&sig=deadbeef", nil, nil)
	assert.Equal(t, 403, w.Code)
}

func TestB7_UploadValidation(t *testing.T) {
	h := newB7H(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "x.txt")
	_, _ = fw.Write([]byte("x"))
	_ = mw.WriteField("host_kind", "bogus")
	_ = mw.Close()
	w := h.req("POST", "/api/upload-file", buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()})
	assert.Equal(t, 400, w.Code)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	assert.Equal(t, apierr.Validation, out["error"])
}

func toI64(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

func TestB21_RangeSingleSegment(t *testing.T) {
	h := newB7H(t)
	code, out := h.upload(t, "range.bin", "0123456789abcdefghij")
	require.Equal(t, 201, code, "upload: %v", out)
	id, _ := out["id"].(string)

	rd := func(path, rng string) (*httptest.ResponseRecorder, int) {
		req := httptest.NewRequest("GET", path, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		req.Header.Set("X-Workspace-Id", h.wsID)
		w := httptest.NewRecorder()
		h.handler.ServeHTTP(w, req)
		n := 0
		for _, c := range w.Body.String() {
			_ = c
			n++
		}
		return w, n
	}

	w, _ := rd("/api/attachments/"+id+"/download", "bytes=2-5")
	assert.Equal(t, 206, w.Code)
	assert.Equal(t, "2345", w.Body.String(), "bytes=2-5 means subscripts 2..5")
	assert.Equal(t, "bytes 2-5/20", w.Header().Get("Content-Range"))

	w, _ = rd("/api/attachments/"+id+"/download", "bytes=10-")
	assert.Equal(t, 206, w.Code)
	assert.Equal(t, "abcdefghij", w.Body.String())

	w, _ = rd("/api/attachments/"+id+"/download", "bytes=-4")
	assert.Equal(t, 206, w.Code)
	assert.Equal(t, "ghij", w.Body.String())

	w, _ = rd("/api/attachments/"+id+"/download", "bytes=0-99")
	assert.Equal(t, 200, w.Code)
	assert.Len(t, w.Body.String(), 20)

	w, _ = rd("/api/attachments/"+id+"/download", "bytes=0-1,3-4")
	assert.Equal(t, 200, w.Code)
}

func TestB7_DeletePermissionGrading(t *testing.T) {
	h := newB7H(t)
	ctx := context.Background()

	code, out := h.upload(t, "graded.txt", "body")
	require.Equal(t, 201, code, "upload: %v", out)
	id, _ := out["id"].(string)
	newUser := func(email, role string) string {
		var uid string
		require.NoError(t, h.pool.QueryRow(ctx,
			`INSERT INTO "user" (email, display_name) VALUES ($1,$1) RETURNING id::text`, email).Scan(&uid))
		_, err := h.pool.Exec(ctx,
			`INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,$3)`, h.wsID, uid, role)
		require.NoError(t, err)
		return uid
	}
	member2 := newUser("member2@x.io", "member")
	admin2 := newUser("admin2@x.io", "admin")

	w := h.req("DELETE", "/api/attachments/"+id, nil, map[string]string{"X-Test-User": member2})
	require.Equal(t, 403, w.Code)
	assert.Contains(t, w.Body.String(), apierr.Forbidden)
	w = h.req("GET", "/api/attachments/"+id, nil, nil)
	require.Equal(t, 200, w.Code, "row not deleted after member 403")

	w = h.req("DELETE", "/api/attachments/"+id, nil, map[string]string{"X-Test-User": admin2})
	require.Equal(t, 200, w.Code, "admin tiered allowance")
	w = h.req("GET", "/api/attachments/"+id, nil, nil)
	assert.Equal(t, 404, w.Code, "invisible after deletion")

	_, err := h.pool.Exec(ctx, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, h.wsID, admin2)
	require.NoError(t, err)
	w = h.req("DELETE", "/api/attachments/"+id, nil, map[string]string{"X-Test-User": admin2})
	assert.Equal(t, 403, w.Code, "memberMW rejects after removal from the workspace")
}

func TestB7_UploadHostCheckMissingHost(t *testing.T) {
	h := newB7H(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "x.txt")
	_, _ = fw.Write([]byte("x"))
	_ = mw.WriteField("host_kind", "work_item")
	_ = mw.WriteField("host_id", "00000000-0000-0000-0000-000000000001")
	_ = mw.Close()
	w := h.req("POST", "/api/upload-file", buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()})
	require.Equal(t, 404, w.Code, "host does not exist, propagation rejected")
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	assert.Equal(t, apierr.NotFound, out["error"])

	code, out := h.upload(t, "pre.txt", "x")
	require.Equal(t, 201, code, "pre-upload skips the recheck: %v", out)

	var wiID string
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`INSERT INTO work_item (workspace_id, number, title, status_key, creator_id)
		 VALUES ($1, 1, 'host-wi', 'todo', $2) RETURNING id::text`,
		h.wsID, h.userID).Scan(&wiID))
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	fw, _ = mw.CreateFormFile("file", "on-host.txt")
	_, _ = fw.Write([]byte("x"))
	_ = mw.WriteField("host_kind", "work_item")
	_ = mw.WriteField("host_id", wiID)
	_ = mw.Close()
	w = h.req("POST", "/upload-file", buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType()})
	require.Equal(t, 201, w.Code, "host present, allowed")
}
