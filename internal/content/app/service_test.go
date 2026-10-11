package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/storagex"
	"orkome/internal/contract/apierr"
)

type fakeStore struct{ objs map[string]storeObj }

type storeObj struct {
	data  []byte
	ctype string
}

func newFakeStore() *fakeStore { return &fakeStore{objs: map[string]storeObj{}} }

func (f *fakeStore) Put(ctx context.Context, key, contentType string, r io.Reader) (storagex.Object, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return storagex.Object{}, err
	}
	sum := sha256.Sum256(b)
	f.objs[key] = storeObj{data: b, ctype: contentType}
	return storagex.Object{Key: key, ContentType: contentType, Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}, nil
}
func (f *fakeStore) Open(ctx context.Context, key string) (io.ReadCloser, storagex.Object, error) {
	o, ok := f.objs[key]
	if !ok {
		return nil, storagex.Object{}, errors.New("not found")
	}
	sum := sha256.Sum256(o.data)
	return io.NopCloser(bytes.NewReader(o.data)),
		storagex.Object{Key: key, ContentType: o.ctype, Size: int64(len(o.data)), SHA256: hex.EncodeToString(sum[:])}, nil
}
func (f *fakeStore) Delete(ctx context.Context, key string) error { delete(f.objs, key); return nil }
func (f *fakeStore) DeleteMulti(ctx context.Context, keys ...string) error {
	for _, k := range keys {
		delete(f.objs, k)
	}
	return nil
}
func (f *fakeStore) URL(key string) string { return "/uploads/" + key }
func (f *fakeStore) Presign(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "/uploads/" + key, nil
}

type fakeRepo struct {
	rows     map[string]Attachment
	byHost   map[string]HostUsage
	inserted []Attachment
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[string]Attachment{}, byHost: map[string]HostUsage{}}
}

func (f *fakeRepo) InsertAttachment(ctx context.Context, a Attachment) (Attachment, error) {
	a.ID = "att-1"
	f.rows[a.ID] = a
	f.inserted = append(f.inserted, a)
	return a, nil
}
func (f *fakeRepo) GetAttachment(ctx context.Context, ws, id string) (Attachment, bool, error) {
	a, ok := f.rows[id]
	return a, ok && a.WorkspaceID == ws, nil
}
func (f *fakeRepo) GetAttachmentByID(ctx context.Context, id string) (Attachment, bool, error) {
	a, ok := f.rows[id]
	return a, ok, nil
}
func (f *fakeRepo) DeleteAttachment(ctx context.Context, ws, id string) (string, bool, error) {
	a, ok := f.rows[id]
	if !ok {
		return "", false, nil
	}
	delete(f.rows, id)
	return a.StorageKey, true, nil
}
func (f *fakeRepo) ListByHost(ctx context.Context, ws, hostKind, hostID string) ([]Attachment, error) {
	return nil, nil
}
func (f *fakeRepo) HostUsage(ctx context.Context, ws, hostKind, hostID string) (HostUsage, error) {
	return f.byHost[hostKind+"/"+hostID], nil
}

func newSvc(repo Repo, fs Store, gate MemberGate) *AttachmentService {
	s := NewAttachmentService(repo, fs, []byte("test-secret"), nil)
	if gate != nil {
		s.SetMemberGate(gate)
	}
	return s
}

func TestUploadSanitizesMetadata(t *testing.T) {
	repo, fs := newFakeRepo(), newFakeStore()
	s := newSvc(repo, fs, nil)

	a, err := s.Upload(context.Background(), "ws", "work_item", "", "bad\x00name\xff.txt", "text/plain\x00",
		strings.NewReader("body"), "u1")
	require.NoError(t, err)
	assert.NotContains(t, a.Filename, "\x00", "file name NUL stripped")
	assert.NotContains(t, a.Filename, "\xff", "file name invalid UTF-8 corrected")
	assert.Equal(t, "text/plain", a.ContentType, "Content-Type sanitized")
	assert.NotContains(t, a.StorageKey, "\x00", "storage keys built from sanitized file names")
}

func TestUploadHostQuotaCount(t *testing.T) {
	repo, fs := newFakeRepo(), newFakeStore()
	repo.byHost["comment/c1"] = HostUsage{Count: 100, Bytes: 0}
	s := newSvc(repo, fs, nil)

	_, err := s.Upload(context.Background(), "ws", "comment", "c1", "f.txt", "text/plain",
		strings.NewReader("x"), "u1")
	require.Error(t, err)
	ae, ok := err.(*apierr.Error)
	require.True(t, ok)
	assert.Equal(t, apierr.Validation, ae.Code)
	assert.Empty(t, fs.objs, "nothing left behind after quota rejection")
}

func TestUploadHostQuotaBytes(t *testing.T) {
	repo, fs := newFakeRepo(), newFakeStore()
	repo.byHost["comment/c1"] = HostUsage{Count: 0, Bytes: 500<<20 - 2}
	s := newSvc(repo, fs, nil)

	_, err := s.Upload(context.Background(), "ws", "comment", "c1", "f.txt", "text/plain",
		strings.NewReader("abc"), "u1")
	require.Error(t, err)
	assert.Empty(t, fs.objs, "over-quota objects rolled back and cleaned")
}

func TestOpenVerifiesDigest(t *testing.T) {
	repo, fs := newFakeRepo(), newFakeStore()
	s := newSvc(repo, fs, nil)
	a, err := s.Upload(context.Background(), "ws", "work_item", "", "f.txt", "text/plain",
		strings.NewReader("hello"), "u1")
	require.NoError(t, err)

	rc, _, err := s.Open(context.Background(), "ws", a.ID)
	require.NoError(t, err)
	rc.Close()

	o := fs.objs[a.StorageKey]
	o.data[0] = 'X'
	fs.objs[a.StorageKey] = o
	_, _, err = s.Open(context.Background(), "ws", a.ID)
	require.ErrorIs(t, err, ErrDigestMismatch)

	_, _, err = s.OpenSigned(context.Background(), a.ID)
	require.ErrorIs(t, err, ErrDigestMismatch)
}

func TestOpenAllowsLegacyEmptyDigest(t *testing.T) {

	repo, fs := newFakeRepo(), newFakeStore()
	_, err := fs.Put(context.Background(), "ws/k", "text/plain", strings.NewReader("data"))
	require.NoError(t, err)
	repo.rows["legacy"] = Attachment{ID: "legacy", WorkspaceID: "ws", StorageKey: "ws/k", SHA256: ""}
	s := newSvc(repo, fs, nil)
	rc, _, err := s.Open(context.Background(), "ws", "legacy")
	require.NoError(t, err)
	rc.Close()
}

func TestUploadHostCheckNilPortSkips(t *testing.T) {

	repo, fs := newFakeRepo(), newFakeStore()
	s := newSvc(repo, fs, nil)
	a, err := s.Upload(context.Background(), "ws", "comment", "c1", "f.txt", "text/plain",
		strings.NewReader("x"), "u1")
	require.NoError(t, err)
	assert.NotEmpty(t, a.ID)
	assert.Len(t, repo.inserted, 1, "nil port does not block uploads")
}

func TestUploadHostCheckRefusesMissingHost(t *testing.T) {
	repo, fs := newFakeRepo(), newFakeStore()
	s := newSvc(repo, fs, nil)
	var gotKind, gotHost string
	s.SetHostChecker(func(ctx context.Context, ws, hk, hid string) (bool, error) {
		gotKind, gotHost = hk, hid
		return false, nil
	})

	_, err := s.Upload(context.Background(), "ws", "comment", "c-missing", "f.txt", "text/plain",
		strings.NewReader("x"), "u1")
	require.Error(t, err)
	ae, ok := err.(*apierr.Error)
	require.True(t, ok)
	assert.Equal(t, apierr.NotFound, ae.Code, "host does not exist → 404 E_NOT_FOUND (same shape as does-not-exist, I6)")
	assert.Equal(t, "comment", gotKind)
	assert.Equal(t, "c-missing", gotHost, "the port receives host_kind/host_id as-is")
	assert.Empty(t, fs.objs, "rejection before the object lands — no half writes")
	assert.Empty(t, repo.inserted, "rejection precedes the write")
}

func TestUploadHostCheckPassesExistingHost(t *testing.T) {
	repo, fs := newFakeRepo(), newFakeStore()
	s := newSvc(repo, fs, nil)
	s.SetHostChecker(func(ctx context.Context, ws, hk, hid string) (bool, error) { return true, nil })

	a, err := s.Upload(context.Background(), "ws", "comment", "c1", "f.txt", "text/plain",
		strings.NewReader("x"), "u1")
	require.NoError(t, err)
	assert.Equal(t, "c1", a.HostID)
	assert.Len(t, repo.inserted, 1, "host present → persisted as usual")
}

func TestUploadHostCheckSkipsEmptyHostIDPreUpload(t *testing.T) {

	repo, fs := newFakeRepo(), newFakeStore()
	s := newSvc(repo, fs, nil)
	invoked := false
	s.SetHostChecker(func(ctx context.Context, ws, hk, hid string) (bool, error) {
		invoked = true
		return false, nil
	})
	_, err := s.Upload(context.Background(), "ws", "work_item", "", "f.txt", "text/plain",
		strings.NewReader("x"), "u1")
	require.NoError(t, err)
	assert.False(t, invoked, "empty host_id does not trigger host recheck")
}

func TestUploadHostCheckVocabFirst(t *testing.T) {

	repo, fs := newFakeRepo(), newFakeStore()
	s := newSvc(repo, fs, nil)
	invoked := false
	s.SetHostChecker(func(ctx context.Context, ws, hk, hid string) (bool, error) {
		invoked = true
		return true, nil
	})
	_, err := s.Upload(context.Background(), "ws", "bogus", "h1", "f.txt", "text/plain",
		strings.NewReader("x"), "u1")
	require.Error(t, err)
	ae, ok := err.(*apierr.Error)
	require.True(t, ok)
	assert.Equal(t, apierr.Validation, ae.Code)
	assert.False(t, invoked, "out-of-vocabulary values stopped with 400 before the port")
	assert.Empty(t, repo.inserted)
}

func TestSignedURLLifecycle(t *testing.T) {
	repo, fs := newFakeRepo(), newFakeStore()
	s := newSvc(repo, fs, nil)
	a, err := s.Upload(context.Background(), "ws", "work_item", "", "f.txt", "text/plain",
		strings.NewReader("secret-body"), "u1")
	require.NoError(t, err)
	now := time.Unix(1_700_000_000, 0)
	s2 := NewAttachmentService(repo, fs, []byte("k"), func() time.Time { return now })

	_, err = s2.SignedURL(context.Background(), "ws", a.ID, "download", "", time.Minute)
	require.Error(t, err)

	url, err := s2.SignedURL(context.Background(), "ws", a.ID, "download", "m-1", time.Minute)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(url, "/attachments/"+a.ID+"/signed-download?"))
	assert.Contains(t, url, "intent=download")
	assert.Contains(t, url, "&m=m-1", "default bound member")

	q := parseQuery(url)
	assert.True(t, s2.VerifySigned(a.ID, "download", "m-1", q["exp"], q["sig"]), "valid signature passes")
	assert.False(t, s2.VerifySigned(a.ID, "download", "m-2", q["exp"], q["sig"]), "changing member rejected (anti-forwarding)")
	assert.False(t, s2.VerifySigned(a.ID, "content", "m-1", q["exp"], q["sig"]), "changing intent rejected")
	assert.False(t, s2.VerifySigned("other-attachment", "download", "m-1", q["exp"], q["sig"]), "changing attachments rejected")
	assert.False(t, s2.VerifySigned(a.ID, "download", "m-1", q["exp"], "forged"), "forged signature rejected")

	later := NewAttachmentService(repo, fs, []byte("k"), func() time.Time { return now.Add(2 * time.Minute) })
	assert.False(t, later.VerifySigned(a.ID, "download", "m-1", q["exp"], q["sig"]))
}

func TestSignedURLMemberGate(t *testing.T) {
	repo, fs := newFakeRepo(), newFakeStore()
	gate := func(ctx context.Context, ws, uid string) (bool, error) { return uid == "member-ok", nil }
	s := newSvc(repo, fs, gate)
	a, err := s.Upload(context.Background(), "ws", "work_item", "", "f.txt", "text/plain",
		strings.NewReader("x"), "u1")
	require.NoError(t, err)

	_, err = s.SignedURL(context.Background(), "ws", a.ID, "download", "outsider", time.Minute)
	require.Error(t, err)
	ae, ok := err.(*apierr.Error)
	require.True(t, ok)
	assert.Equal(t, apierr.Forbidden, ae.Code, "mint member gate fail-closed 403")

	_, err = s.SignedURL(context.Background(), "ws", a.ID, "download", "member-ok", time.Minute)
	require.NoError(t, err)
}

func TestSignedURLOpenSignedBySignature(t *testing.T) {
	repo, fs := newFakeRepo(), newFakeStore()
	now := time.Unix(1_700_000_000, 0)
	s := NewAttachmentService(repo, fs, []byte("k"), func() time.Time { return now })
	a, err := s.Upload(context.Background(), "ws", "work_item", "", "f.txt", "text/plain",
		strings.NewReader("body-by-sig"), "u1")
	require.NoError(t, err)
	url, err := s.SignedURL(context.Background(), "ws", a.ID, "content", "m-1", time.Minute)
	require.NoError(t, err)
	q := parseQuery(url)
	require.True(t, s.VerifySigned(a.ID, "content", "m-1", q["exp"], q["sig"]))

	rc, att, err := s.OpenSigned(context.Background(), a.ID)
	require.NoError(t, err)
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	assert.Equal(t, "body-by-sig", string(b))
	assert.Equal(t, a.ID, att.ID)
}

func parseQuery(url string) map[string]string {
	out := map[string]string{}
	if i := strings.Index(url, "?"); i >= 0 {
		for _, kv := range strings.Split(url[i+1:], "&") {
			if j := strings.Index(kv, "="); j >= 0 {
				out[kv[:j]] = kv[j+1:]
			}
		}
	}
	return out
}
