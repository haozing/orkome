package workers

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmigrations "github.com/haozing/ploykit/migrations"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/internal/channels/adapters/pgrepo"
	"orkome/internal/channels/app"
	"orkome/migrations"
)

func newWorkerDB(t *testing.T) (*pgxpool.Pool, string, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_cwkr WITH (FORCE)")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_cwkr")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	pool, err := pgxpool.New(context.Background(),
		strings.Replace(base, "/"+cfg.Database, "/orkome_cwkr", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(context.Background(), 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)

	var wsID, instID string
	require.NoError(t, pool.QueryRow(context.Background(),
		`WITH u AS (INSERT INTO "user" (email, display_name) VALUES ('seed-c-wkr@test.dev','SEED') RETURNING id) INSERT INTO workspace (name, slug, created_by) SELECT 'cwkr','c-wkr', id FROM u RETURNING id::text`).Scan(&wsID))
	require.NoError(t, pool.QueryRow(context.Background(), `
INSERT INTO channel_installation (workspace_id, kind, name, config)
VALUES ($1, 'fake', 'w', '{}'::jsonb) RETURNING id::text`, wsID).Scan(&instID))
	return pool, wsID, instID
}

type fakeDeleter struct {
	mu      sync.Mutex
	deleted []string
	fail    map[string]error
}

func (f *fakeDeleter) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.fail[key]; ok {
		return err
	}
	f.deleted = append(f.deleted, key)
	return nil
}

type fakeAttachments struct{ referenced map[string]bool }

func (f *fakeAttachments) ExistsByStorageKey(_ context.Context, _, key string) (bool, error) {
	return f.referenced[key], nil
}

func TestReconcilerTickReclaimsAndTombstones(t *testing.T) {
	pool, wsID, instID := newWorkerDB(t)
	repo := pgrepo.New(pg.WrapPool(pool))
	deleter := &fakeDeleter{}
	attachments := &fakeAttachments{referenced: map[string]bool{}}
	rec := NewReconciler(repo, attachments, deleter)
	ctx := context.Background()

	past := time.Now().Add(-16 * time.Minute)
	require.NoError(t, repo.InsertMediaIntents(ctx, []app.MediaIntent{
		{StorageKey: "obj-free", WorkspaceID: wsID, InstallationID: instID, State: "pending", NextAttemptAt: past},
		{StorageKey: "obj-kept", WorkspaceID: wsID, InstallationID: instID, State: "pending", NextAttemptAt: past},
		{StorageKey: "obj-young", WorkspaceID: wsID, InstallationID: instID, State: "pending", NextAttemptAt: time.Now().Add(time.Minute)},
	}))
	attachments.referenced["obj-kept"] = true

	rec.Tick(ctx)

	deleter.mu.Lock()
	defer deleter.mu.Unlock()
	assert.Contains(t, deleter.deleted, "obj-free", "unreferenced stale objects deleted (via claim reclamation, never inline delete)")
	assert.NotContains(t, deleter.deleted, "obj-kept", "referenced objects not deleted (binding wins, row settles)")
	assert.NotContains(t, deleter.deleted, "obj-young", "not reclaimed inside the quiet window")

	var state string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM channel_media_pending_object WHERE storage_key='obj-free'`).Scan(&state))
	assert.Equal(t, "tombstoned", state)
	var kept int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM channel_media_pending_object WHERE storage_key='obj-kept'`).Scan(&kept))
	assert.Zero(t, kept, "referenced = binding settles, ledger row cleared")
}

func TestReconcilerDeleteFailureBackoff(t *testing.T) {
	pool, wsID, instID := newWorkerDB(t)
	repo := pgrepo.New(pg.WrapPool(pool))
	deleter := &fakeDeleter{fail: map[string]error{"obj-err": assert.AnError}}
	rec := NewReconciler(repo, &fakeAttachments{referenced: map[string]bool{}}, deleter)
	ctx := context.Background()

	past := time.Now().Add(-16 * time.Minute)
	require.NoError(t, repo.InsertMediaIntents(ctx, []app.MediaIntent{
		{StorageKey: "obj-err", WorkspaceID: wsID, InstallationID: instID, State: "pending", NextAttemptAt: past},
	}))
	rec.Tick(ctx)

	var state string
	var next time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state, next_attempt_at FROM channel_media_pending_object WHERE storage_key='obj-err'`).Scan(&state, &next))
	assert.Equal(t, "pending", state, "failure returns to pending")
	assert.True(t, next.After(time.Now()), "backoff scheduled in the future")
}

func TestReconcilerDeleterUnwiredDegradation(t *testing.T) {
	pool, wsID, instID := newWorkerDB(t)
	repo := pgrepo.New(pg.WrapPool(pool))
	rec := NewReconciler(repo, nil, nil)
	ctx := context.Background()

	past := time.Now().Add(-16 * time.Minute)
	require.NoError(t, repo.InsertMediaIntents(ctx, []app.MediaIntent{
		{StorageKey: "obj-nostore", WorkspaceID: wsID, InstallationID: instID, State: "pending", NextAttemptAt: past},
	}))
	rec.Tick(ctx)
	var state string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state FROM channel_media_pending_object WHERE storage_key='obj-nostore'`).Scan(&state))
	assert.Equal(t, "pending", state, "no storage face: status not advanced (awaiting wiring)")
}

func TestReconcilerSettledByBindFirst(t *testing.T) {
	pool, wsID, instID := newWorkerDB(t)
	repo := pgrepo.New(pg.WrapPool(pool))
	deleter := &fakeDeleter{}
	rec := NewReconciler(repo, &fakeAttachments{referenced: map[string]bool{}}, deleter)
	ctx := context.Background()

	past := time.Now().Add(-16 * time.Minute)
	require.NoError(t, repo.InsertMediaIntents(ctx, []app.MediaIntent{
		{StorageKey: "obj-bound", WorkspaceID: wsID, InstallationID: instID, State: "pending", NextAttemptAt: past},
	}))

	n, err := repo.BindMediaToMessage(ctx, "", []string{"obj-bound"})
	require.NoError(t, err)
	require.Equal(t, 1, n)

	rec.Tick(ctx)
	deleter.mu.Lock()
	defer deleter.mu.Unlock()
	assert.NotContains(t, deleter.deleted, "obj-bound", "bound objects no longer reclaimed")
}

type fakeRewriter struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error
}

func (f *fakeRewriter) RewriteMediaReady(_ context.Context, chatMessageID string, refs []app.MediaMaterializedRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range refs {
		if err, ok := f.fail[chatMessageID]; ok {
			return err
		}
		f.calls = append(f.calls, chatMessageID+"<-"+r.StorageKey+"|"+r.StorageURL)
	}
	return nil
}

func TestReconcilerMaterializesOnSettle(t *testing.T) {
	pool, wsID, instID := newWorkerDB(t)
	repo := pgrepo.New(pg.WrapPool(pool))
	deleter := &fakeDeleter{}
	attachments := &fakeAttachments{referenced: map[string]bool{}}
	rec := NewReconciler(repo, attachments, deleter)
	rewriter := &fakeRewriter{}
	rec.SetMessageRewriter(rewriter)
	ctx := context.Background()

	msgID := "99999999-0000-0000-0000-000000000009"
	past := time.Now().Add(-16 * time.Minute)
	require.NoError(t, repo.InsertMediaIntents(ctx, []app.MediaIntent{
		{StorageKey: "media/a.png", WorkspaceID: wsID, ChatMessageID: msgID,
			StorageURL: "https://cdn.example/obj/a.png", InstallationID: instID,
			State: "pending", NextAttemptAt: past},
	}))
	attachments.referenced["media/a.png"] = true

	rec.Tick(ctx)

	rewriter.mu.Lock()
	calls := append([]string(nil), rewriter.calls...)
	rewriter.mu.Unlock()
	require.Len(t, calls, 1, "ready path materializes exactly one rewrite")
	assert.Equal(t, msgID+"<-media/a.png|https://cdn.example/obj/a.png", calls[0])
	var settled int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM channel_media_pending_object WHERE storage_key='media/a.png'`).Scan(&settled))
	assert.Zero(t, settled, "rows accounted after rewrite success")
	deleter.mu.Lock()
	assert.NotContains(t, deleter.deleted, "media/a.png")
	deleter.mu.Unlock()
}

func TestReconcilerRewriteFailureRetainsRow(t *testing.T) {
	pool, wsID, instID := newWorkerDB(t)
	repo := pgrepo.New(pg.WrapPool(pool))
	deleter := &fakeDeleter{}
	attachments := &fakeAttachments{referenced: map[string]bool{"media/b.jpg": true}}
	rec := NewReconciler(repo, attachments, deleter)
	msgID := "99999999-0000-0000-0000-00000000000a"
	rewriter := &fakeRewriter{fail: map[string]error{msgID: assert.AnError}}
	rec.SetMessageRewriter(rewriter)
	ctx := context.Background()

	past := time.Now().Add(-16 * time.Minute)
	require.NoError(t, repo.InsertMediaIntents(ctx, []app.MediaIntent{
		{StorageKey: "media/b.jpg", WorkspaceID: wsID, ChatMessageID: msgID,
			StorageURL: "https://cdn.example/obj/b.jpg", InstallationID: instID,
			State: "pending", NextAttemptAt: past},
	}))

	rec.Tick(ctx)
	var state string
	var next time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT state, next_attempt_at FROM channel_media_pending_object WHERE storage_key='media/b.jpg'`).Scan(&state, &next))
	assert.Equal(t, "pending", state, "rewrite failure not accounted (rows kept for backoff retry)")
	assert.True(t, next.After(time.Now()), "backoff scheduled in the future")

	_, err := pool.Exec(ctx,
		`UPDATE channel_media_pending_object SET next_attempt_at=now()-interval '1s' WHERE storage_key='media/b.jpg'`)
	require.NoError(t, err)
	rewriter.mu.Lock()
	rewriter.fail = nil
	rewriter.mu.Unlock()
	rec.Tick(ctx)
	var settled int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM channel_media_pending_object WHERE storage_key='media/b.jpg'`).Scan(&settled))
	assert.Zero(t, settled, "accounted after idempotent rewrite retry succeeds")
}

func TestReconcilerRewriterUnwiredDegradation(t *testing.T) {
	pool, wsID, instID := newWorkerDB(t)
	repo := pgrepo.New(pg.WrapPool(pool))
	rec := NewReconciler(repo, &fakeAttachments{referenced: map[string]bool{"media/c.pdf": true}}, &fakeDeleter{})
	ctx := context.Background()

	past := time.Now().Add(-16 * time.Minute)
	require.NoError(t, repo.InsertMediaIntents(ctx, []app.MediaIntent{
		{StorageKey: "media/c.pdf", WorkspaceID: wsID, ChatMessageID: "99999999-0000-0000-0000-00000000000b",
			StorageURL: "https://cdn.example/obj/c.pdf", InstallationID: instID,
			State: "pending", NextAttemptAt: past},
	}))
	rec.Tick(ctx)
	var settled int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM channel_media_pending_object WHERE storage_key='media/c.pdf'`).Scan(&settled))
	assert.Zero(t, settled, "unwired rewrite face: accounted non-blocking")
}
