package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contentapp "orkome/internal/content/app"
	"github.com/haozing/ploykit/platform/storagex"
	"orkome/internal/platform/webx"
)

type sdStore struct{ objs map[string][]byte }

func (f *sdStore) Put(_ context.Context, key, ct string, r io.Reader) (storagex.Object, error) {
	b, _ := io.ReadAll(r)
	f.objs[key] = b
	sum := sha256.Sum256(b)
	return storagex.Object{Key: key, ContentType: ct, Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}, nil
}
func (f *sdStore) Open(_ context.Context, key string) (io.ReadCloser, storagex.Object, error) {
	b, ok := f.objs[key]
	if !ok {
		return nil, storagex.Object{}, errors.New("not found")
	}
	sum := sha256.Sum256(b)
	return io.NopCloser(bytes.NewReader(b)),
		storagex.Object{Key: key, Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}, nil
}
func (f *sdStore) Delete(_ context.Context, key string) error { delete(f.objs, key); return nil }

func (f *sdStore) DeleteMulti(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(f.objs, k)
	}
	return nil
}
func (f *sdStore) URL(key string) string { return key }
func (f *sdStore) Presign(_ context.Context, key string, ttl time.Duration) (string, error) {
	return key, nil
}

type sdRepo struct{ row *contentapp.Attachment }

func (r *sdRepo) InsertAttachment(_ context.Context, a contentapp.Attachment) (contentapp.Attachment, error) {
	return a, nil
}
func (r *sdRepo) GetAttachment(_ context.Context, ws, id string) (contentapp.Attachment, bool, error) {
	if r.row != nil && r.row.WorkspaceID == ws && r.row.ID == id {
		return *r.row, true, nil
	}
	return contentapp.Attachment{}, false, nil
}
func (r *sdRepo) GetAttachmentByID(_ context.Context, id string) (contentapp.Attachment, bool, error) {
	if r.row != nil && r.row.ID == id {
		return *r.row, true, nil
	}
	return contentapp.Attachment{}, false, nil
}
func (r *sdRepo) DeleteAttachment(_ context.Context, ws, id string) (string, bool, error) {
	return "", false, nil
}
func (r *sdRepo) ListByHost(_ context.Context, ws, hk, hid string) ([]contentapp.Attachment, error) {
	return nil, nil
}
func (r *sdRepo) HostUsage(_ context.Context, ws, hk, hid string) (contentapp.HostUsage, error) {
	return contentapp.HostUsage{}, nil
}

func authStub(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uid := r.Header.Get("X-Test-User"); uid != "" {
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(), &webx.Principal{UserID: uid})))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newSDFixture(body string) (*sdRepo, *sdStore) {
	fs := &sdStore{objs: map[string][]byte{}}
	_, _ = fs.Put(context.Background(), "ws/k1", "text/plain", strings.NewReader(body))
	sum := sha256.Sum256([]byte(body))
	return &sdRepo{row: &contentapp.Attachment{
		ID: "att-1", WorkspaceID: "ws", StorageKey: "ws/k1",
		ContentType: "text/plain", Filename: "f.txt", SizeBytes: int64(len(body)),
		SHA256: hex.EncodeToString(sum[:]),
	}}, fs
}

func newSDSvc(repo *sdRepo, fs *sdStore, now func() time.Time) *contentapp.AttachmentService {
	return contentapp.NewAttachmentService(repo, fs, []byte("sd-secret"), now)
}

func TestSignedDownloadEndpoint(t *testing.T) {
	repo, fs := newSDFixture("signed-body")
	now := time.Unix(1_700_000_000, 0)
	svc := newSDSvc(repo, fs, func() time.Time { return now })
	h := mountSD(Deps{Attachments: svc})

	url, err := svc.SignedURL(context.Background(), "ws", "att-1", "download", "m-1", time.Minute)
	require.NoError(t, err)
	url = "/api" + url // MountAPI registers the endpoint under /api

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "signed-body", w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment", "intent=download")

	past := newSDSvc(repo, fs, func() time.Time { return now.Add(-2 * time.Hour) })
	expiredURL, err := past.SignedURL(context.Background(), "ws", "att-1", "download", "m-1", time.Minute)
	require.NoError(t, err)
	expiredURL = "/api" + expiredURL
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", expiredURL, nil))
	assert.Equal(t, 403, w.Code)

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", strings.Replace(url, "intent=download", "intent=content", 1), nil))
	assert.Equal(t, 403, w.Code)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", url+"x", nil))
	assert.Equal(t, 403, w.Code)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", strings.Replace(url, "m=m-1", "m=m-2", 1), nil))
	assert.Equal(t, 403, w.Code)

	w = httptest.NewRecorder()
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("X-Test-User", "someone-else")
	h.ServeHTTP(w, req)
	assert.Equal(t, 403, w.Code, "forwarding to other authenticated members rejected")

	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", url, nil)
	req.Header.Set("X-Test-User", "m-1")
	h.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	contentURL, err := svc.SignedURL(context.Background(), "ws", "att-1", "content", "m-1", time.Minute)
	require.NoError(t, err)
	contentURL = "/api" + contentURL
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", contentURL, nil))
	require.Equal(t, 200, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "inline")
}

func TestSignedDownloadDigestTamperRefused(t *testing.T) {
	repo, fs := newSDFixture("clean")
	svc := newSDSvc(repo, fs, nil)
	h := mountSD(Deps{Attachments: svc})
	url, err := svc.SignedURL(context.Background(), "ws", "att-1", "download", "m-1", time.Minute)
	require.NoError(t, err)
	url = "/api" + url // MountAPI registers the endpoint under /api

	fs.objs["ws/k1"] = []byte("tlean")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "tlean", "tampered content must not be served")
}

func mountSD(d Deps) http.Handler {
	r := http.NewServeMux()
	MountAPI(r, d, authStub)
	return r
}
