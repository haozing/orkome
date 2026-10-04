package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"orkome/internal/content/domain"
	"orkome/internal/contract/apierr"
	"orkome/internal/platform/ids"
	"orkome/internal/platform/redactx"
	"orkome/internal/platform/storagex"
)

const MaxAttachmentBytes = 100 << 20

var ErrDigestMismatch = errors.New("attachment digest mismatch")

const (
	IntentDownload = domain.IntentDownload
	IntentContent  = domain.IntentContent
)

type Attachment struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	HostKind    string    `json:"host_kind"`
	HostID      string    `json:"host_id,omitempty"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	StorageKey  string    `json:"-"`
	SHA256      string    `json:"sha256"`
	CreatedBy   string    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (a Attachment) Size() int64 { return a.SizeBytes }

type HostUsage struct {
	Count int
	Bytes int64
}

type Repo interface {
	InsertAttachment(ctx context.Context, a Attachment) (Attachment, error)
	GetAttachment(ctx context.Context, workspaceID, id string) (Attachment, bool, error)

	GetAttachmentByID(ctx context.Context, id string) (Attachment, bool, error)
	DeleteAttachment(ctx context.Context, workspaceID, id string) (storageKey string, ok bool, err error)
	ListByHost(ctx context.Context, workspaceID, hostKind, hostID string) ([]Attachment, error)

	HostUsage(ctx context.Context, workspaceID, hostKind, hostID string) (HostUsage, error)
}

type Store = storagex.Store

type MemberGate func(ctx context.Context, workspaceID, userID string) (bool, error)

type HostChecker func(ctx context.Context, workspaceID, hostKind, hostID string) (bool, error)

type AttachmentService struct {
	repo       Repo
	store      Store
	sign       domain.DownloadSigner
	now        func() time.Time
	memberGate MemberGate
	hostCheck  HostChecker
}

func NewAttachmentService(repo Repo, store Store, secret []byte, now func() time.Time) *AttachmentService {
	if now == nil {
		now = time.Now
	}

	return &AttachmentService{repo: repo, store: store, sign: domain.NewDownloadSigner(secret), now: now}
}

func (s *AttachmentService) SetMemberGate(gate MemberGate) { s.memberGate = gate }

func (s *AttachmentService) SetHostChecker(hc HostChecker) { s.hostCheck = hc }

var validHosts = map[string]bool{"work_item": true, "comment": true, "chat_session": true, "chat_message": true, "task": true}

func sanitizeFilename(name string) string {
	name = redactx.Sanitize(name)
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > 255 {
		out = out[len(out)-255:]
	}
	if strings.TrimSpace(out) == "" {
		out = "file"
	}
	return out
}

func contentTypeFor(filename, given string) string {
	given = redactx.Sanitize(given)
	if given != "" && given != "application/octet-stream" {
		return given
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".txt":
		return "text/plain"
	case ".pdf":
		return "application/pdf"
	}
	return "application/octet-stream"
}

func (s *AttachmentService) Upload(ctx context.Context, workspaceID, hostKind, hostID, filename, contentType string, r io.Reader, userID string) (*Attachment, error) {
	if !validHosts[hostKind] {
		return nil, apierr.NewValidation("invalid host_kind")
	}
	if hostKind != "work_item" && hostID == "" {
		return nil, apierr.NewValidation("host_id required for this host_kind")
	}

	if s.hostCheck != nil && hostID != "" {
		ok, err := s.hostCheck(ctx, workspaceID, hostKind, hostID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, apierr.New(apierr.NotFound, "host not found")
		}
	}

	filename = sanitizeFilename(filename)
	contentType = contentTypeFor(filename, contentType)

	usage, err := s.repo.HostUsage(ctx, workspaceID, hostKind, hostID)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateHostQuota(usage.Count, usage.Bytes, 0); err != nil {
		return nil, err
	}

	lr := io.LimitReader(r, MaxAttachmentBytes+1)
	key := workspaceID + "/" + s.now().UTC().Format("20060102/") + ids.NewV4().String() + "/" + filename
	obj, err := s.store.Put(ctx, key, contentType, lr)
	if err != nil {
		return nil, err
	}
	if obj.Size > MaxAttachmentBytes {
		_ = s.store.Delete(ctx, key)
		return nil, apierr.NewValidation("file exceeds 100MiB limit")
	}
	if err := domain.ValidateHostQuota(usage.Count, usage.Bytes, obj.Size); err != nil {
		_ = s.store.Delete(ctx, key)
		return nil, err
	}
	row, err := s.repo.InsertAttachment(ctx, Attachment{
		WorkspaceID: workspaceID, HostKind: hostKind, HostID: hostID,
		Filename: filename, ContentType: obj.ContentType, SizeBytes: obj.Size,
		StorageKey: key, SHA256: obj.SHA256, CreatedBy: userID,
	})
	if err != nil {
		_ = s.store.Delete(ctx, key)
		return nil, err
	}
	return &row, nil
}

func (s *AttachmentService) Get(ctx context.Context, workspaceID, id string) (*Attachment, error) {
	a, ok, err := s.repo.GetAttachment(ctx, workspaceID, id)
	if err != nil || !ok {
		return nil, apierr.New(apierr.NotFound, "attachment not found")
	}
	return &a, nil
}

func (s *AttachmentService) verifiedOpen(ctx context.Context, a Attachment) (io.ReadCloser, error) {
	rc, _, err := s.store.Open(ctx, a.StorageKey)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, rc)
	rc.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if a.SHA256 != "" && hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return nil, ErrDigestMismatch
	}
	rc2, _, err := s.store.Open(ctx, a.StorageKey)
	return rc2, err
}

func (s *AttachmentService) Open(ctx context.Context, workspaceID, id string) (io.ReadCloser, Attachment, error) {
	a, err := s.Get(ctx, workspaceID, id)
	if err != nil {
		return nil, Attachment{}, err
	}
	rc, err := s.verifiedOpen(ctx, *a)
	return rc, *a, err
}

func (s *AttachmentService) OpenSigned(ctx context.Context, id string) (io.ReadCloser, Attachment, error) {
	a, ok, err := s.repo.GetAttachmentByID(ctx, id)
	if err != nil || !ok {
		return nil, Attachment{}, apierr.New(apierr.NotFound, "attachment not found")
	}
	rc, err := s.verifiedOpen(ctx, a)
	return rc, a, err
}

func (s *AttachmentService) SignedURL(ctx context.Context, workspaceID, id, intent, memberID string, ttl time.Duration) (string, error) {
	if _, err := s.Get(ctx, workspaceID, id); err != nil {
		return "", err
	}
	if s.sign.BindMember && memberID == "" {
		return "", apierr.New(apierr.Forbidden, "signed url minting requires member identity")
	}
	if s.memberGate != nil && memberID != "" {
		ok, err := s.memberGate(ctx, workspaceID, memberID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", apierr.New(apierr.Forbidden, "not a member of this workspace")
		}
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return s.sign.Sign(id, intent, memberID, ttl, s.now()), nil
}

func (s *AttachmentService) VerifySigned(attachmentID, intent, memberID, exp, sig string) bool {
	return s.sign.Verify(attachmentID, intent, memberID, exp, sig, s.now())
}

func (s *AttachmentService) BindsMember() bool { return s.sign.BindMember }

func (s *AttachmentService) Delete(ctx context.Context, workspaceID, id string) error {
	key, ok, err := s.repo.DeleteAttachment(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.New(apierr.NotFound, "attachment not found")
	}
	return s.store.Delete(ctx, key)
}

func (s *AttachmentService) ListByHost(ctx context.Context, workspaceID, hostKind, hostID string) ([]Attachment, error) {
	if !validHosts[hostKind] {
		return nil, apierr.NewValidation("invalid host_kind")
	}
	return s.repo.ListByHost(ctx, workspaceID, hostKind, hostID)
}

type AvatarService struct {
	sign  domain.Signer
	store Store
	now   func() time.Time
}

func NewAvatarService(store Store, secret []byte, now func() time.Time) *AvatarService {
	if now == nil {
		now = time.Now
	}
	return &AvatarService{store: store, sign: domain.Signer{Secret: secret}, now: now}
}

func (s *AvatarService) Sign(key string, ttl time.Duration) string {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	exp := s.now().Add(ttl).Unix()
	return "/avatars/" + key + "?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + s.sign.Sig(key, exp)
}

func (s *AvatarService) Verify(key, exp, sig string) bool {
	return s.sign.Verify(key, exp, sig, s.now())
}

func (s *AvatarService) Open(ctx context.Context, key string) (io.ReadCloser, storagex.Object, error) {
	return s.store.Open(ctx, key)
}
