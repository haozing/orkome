package pgrepo

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmigrations "github.com/haozing/ploykit/migrations"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"

	"orkome/internal/identity/app"
	"orkome/migrations"
)

var uitSecret = []byte("test-secret-0123456789abcdef0123456789")

func uitokenEnv(t *testing.T) (app.UITokenRepo, string, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_uit")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_uit")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_uit", 1)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(ctx, 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)
	repo := New(pg.WrapPool(pool))

	// seed against the framework-owned tables (migrations 002/003)
	var userID, wsID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (email, display_name) VALUES ($1, 'Uit') RETURNING id`,
		"uit@example.com").Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO workspace (slug, name, created_by) VALUES ('uit-ws', 'U', $1) RETURNING id`,
		userID).Scan(&wsID))
	_, err = pool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, wsID, userID)
	require.NoError(t, err)
	return repo, wsID, userID
}

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

func TestUITokenMintConsumeReplay(t *testing.T) {
	repo, ws, user := uitokenEnv(t)
	svc := app.NewUITokenService(repo, uitSecret, time.Now)
	ctx := context.Background()

	token, err := svc.Mint(ctx, ws, user, "fakeblog", "")
	require.NoError(t, err)

	claims, err := svc.Consume(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, user, claims.Sub)
	assert.Equal(t, ws, claims.WorkspaceID)
	assert.Equal(t, "fakeblog", claims.Plugin)

	_, err = svc.Consume(ctx, token)
	require.Error(t, err, "replay rejected (jti single consumption)")

	_, err = svc.Consume(ctx, token+"x")
	require.Error(t, err, "wrong key / tampered tickets rejected")
}

func TestUITokenExpiry(t *testing.T) {
	repo, ws, user := uitokenEnv(t)
	ctx := context.Background()
	now := time.Now()
	clock := &fakeClock{t: now}
	svc := app.NewUITokenService(repo, uitSecret, clock.Now)

	token, err := svc.Mint(ctx, ws, user, "fakeblog", "")
	require.NoError(t, err)

	clock.t = now.Add(61 * time.Second)
	_, err = svc.Consume(ctx, token)
	require.Error(t, err, "expired ticket rejected (both JWT exp and recorded expires_at deny)")
}

func TestUITokenWrongSecretRejected(t *testing.T) {
	repo, ws, user := uitokenEnv(t)
	ctx := context.Background()
	minter := app.NewUITokenService(repo, uitSecret, time.Now)
	verifier := app.NewUITokenService(repo, []byte("another-secret-entirely-1234"), time.Now)

	token, err := minter.Mint(ctx, ws, user, "fakeblog", "")
	require.NoError(t, err)
	_, err = verifier.Consume(ctx, token)
	require.Error(t, err, "tickets signed by a different key must not verify")
}
