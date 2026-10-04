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

	"orkome/internal/identity/app"
	"orkome/internal/platform/pg"
	"orkome/internal/platform/pgmigrate"
	"orkome/internal/platform/webx"
)

var uitAuth = &webx.AuthConfig{Secret: []byte("test-secret-0123456789abcdef0123456789")}

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
	m := pgmigrate.New(pool, "../../../../migrations")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)
	repo := New(pg.WrapPool(pool))
	u, err := repo.UpsertUserByEmail(ctx, "uit@example.com")
	require.NoError(t, err)
	wsFull, _, err := repo.CreateWorkspace(ctx, "U", "uit-ws", u.ID)
	require.NoError(t, err)
	return repo, wsFull.ID, u.ID
}

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

func TestUITokenMintConsumeReplay(t *testing.T) {
	repo, ws, user := uitokenEnv(t)
	svc := app.NewUITokenService(repo, uitAuth, time.Now)
	ctx := context.Background()

	token, err := svc.Mint(ctx, ws, user, "fakeblog")
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
	svc := app.NewUITokenService(repo, uitAuth, clock.Now)

	token, err := svc.Mint(ctx, ws, user, "fakeblog")
	require.NoError(t, err)

	clock.t = now.Add(61 * time.Second)
	_, err = svc.Consume(ctx, token)
	require.Error(t, err, "expired ticket rejected (both JWT exp and recorded expires_at deny)")
}
