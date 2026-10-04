package workers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"orkome/internal/channels/app"
	"orkome/internal/channels/domain"
)

type Reconciler struct {
	repo        app.Repo
	attachments app.AttachmentReferenceChecker
	deleter     app.ObjectDeleter
	rewriter    app.MessageRewriter
	interval    time.Duration
	batch       int
	logger      *slog.Logger
}

func NewReconciler(repo app.Repo, attachments app.AttachmentReferenceChecker, deleter app.ObjectDeleter) *Reconciler {
	return &Reconciler{
		repo:        repo,
		attachments: attachments,
		deleter:     deleter,
		interval:    domain.ReconcileInterval,
		batch:       100,
		logger:      slog.Default(),
	}
}

func (r *Reconciler) SetMessageRewriter(w app.MessageRewriter) { r.rewriter = w }

func (r *Reconciler) SetInterval(d time.Duration) { r.interval = d }

func (r *Reconciler) Run(ctx context.Context) error {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			r.Tick(context.WithoutCancel(ctx))
		}
	}
}

func (r *Reconciler) Tick(ctx context.Context) {
	n, err := r.repo.CleanStaleInbound(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		r.logger.Warn("channels reconciler: stale inbound cleanup failed", "error", err)
	}
	due, err := r.repo.ListDueMedia(ctx, time.Now(), r.batch)
	if err != nil {
		r.logger.Warn("channels reconciler: list due media failed", "error", err)
		return
	}
	for _, in := range due {
		if ctx.Err() != nil {
			return
		}
		r.reconcileOne(ctx, in)
	}
	_ = n
}

func (r *Reconciler) reconcileOne(ctx context.Context, in app.MediaIntent) {
	lease := newLeaseToken()
	if won, err := r.repo.ClaimMediaIntent(ctx, in.StorageKey, lease, time.Now().Add(domain.MediaReconcileLease)); err != nil || !won {
		return
	}

	if r.attachments != nil {
		referenced, err := r.attachments.ExistsByStorageKey(ctx, in.WorkspaceID, in.StorageKey)
		if err == nil && referenced {

			if r.rewriter != nil && in.ChatMessageID != "" && in.StorageURL != "" {
				if rerr := r.rewriter.RewriteMediaReady(ctx, in.ChatMessageID, []app.MediaMaterializedRef{{
					Kind:       mediaKindOf(in.StorageKey),
					StorageKey: in.StorageKey,
					StorageURL: in.StorageURL,
				}}); rerr != nil {
					r.logger.Warn("channels reconciler: media materialize rewrite failed",
						"storage_key", in.StorageKey, "error", rerr)
					_ = r.repo.ReleaseMediaIntent(ctx, in.StorageKey, lease, in.Attempt,
						time.Now().Add(domain.MediaRetryDelay(in.Attempt+1)), "message rewrite failed: "+rerr.Error())
					return
				}
			}
			if err := r.repo.SettleMediaIntent(ctx, in.StorageKey, lease); err == nil {
				return
			}
		}
	}

	if r.deleter == nil {

		_ = r.repo.ReleaseMediaIntent(ctx, in.StorageKey, lease, in.Attempt,
			time.Now().Add(domain.MediaReconcileLease), "object deleter not wired")
		return
	}

	if err := r.deleter.Delete(ctx, in.StorageKey); err != nil {

		_ = r.repo.ReleaseMediaIntent(ctx, in.StorageKey, lease, in.Attempt+1,
			time.Now().Add(domain.MediaRetryDelay(in.Attempt+1)), err.Error())
		return
	}

	nextPass := in.TombstonePass + 1
	_ = r.repo.MarkMediaTombstoned(ctx, in.StorageKey, lease, nextPass,
		time.Now().Add(domain.TombstoneNextDelay(in.TombstonePass)))
}

func newLeaseToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buf)
}

func mediaKindOf(storageKey string) string {
	switch {
	case containsExt(storageKey, ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"):
		return "image"
	case containsExt(storageKey, ".mp4", ".mov", ".webm", ".avi"):
		return "video"
	case containsExt(storageKey, ".mp3", ".wav", ".ogg", ".m4a", ".aac"):
		return "audio"
	default:
		return "file"
	}
}

func containsExt(key string, exts ...string) bool {
	lower := strings.ToLower(key)
	for _, e := range exts {
		if strings.HasSuffix(lower, e) {
			return true
		}
	}
	return false
}
