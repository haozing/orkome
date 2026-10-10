package pgrepo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/egressx"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/internal/contract/events"
	webhookworkers "orkome/internal/webhooks/adapters/workers"
	"orkome/internal/webhooks/app"
	"orkome/migrations"

	idpgrepo "orkome/internal/identity/adapters/pgrepo"
)

func webhooksEnv(t *testing.T) (*app.Service, *pgxpool.Pool, string, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_wh")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_wh")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_wh", 1)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)

	idr := idpgrepo.New(pg.WrapPool(pool))
	u, err := idr.UpsertUserByEmail(ctx, "wh@example.com")
	require.NoError(t, err)
	wsFull, _, err := idr.CreateWorkspace(ctx, "W", "wh-ws", u.ID)
	require.NoError(t, err)

	guard, err := egressx.NewGuard([]string{"127.0.0.1/8"})
	require.NoError(t, err)
	svc := app.New(&Repo{DB: pg.WrapPool(pool)}, guard)
	return svc, pool, wsFull.ID, u.ID
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakePlugin struct {
	mu     sync.Mutex
	bodies []string
	sigs   []string
	tss    []string
	status int
}

func (f *fakePlugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, r.ContentLength)
	_, _ = r.Body.Read(b)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(b))
	f.sigs = append(f.sigs, r.Header.Get("X-Signature"))
	f.tss = append(f.tss, r.Header.Get("X-Timestamp"))
	st := f.status
	f.mu.Unlock()
	w.WriteHeader(st)
}

func TestWebhookSignedDeliveryLifecycle(t *testing.T) {
	svc, _, ws, user := webhooksEnv(t)
	ctx := context.Background()

	fp := &fakePlugin{status: 200}
	srv := httptest.NewServer(fp)
	t.Cleanup(srv.Close)

	res, err := svc.Create(ctx, ws, srv.URL, []string{events.TaskCompleted}, "test subscription", user)
	require.NoError(t, err)
	require.NotEmpty(t, res.Secret, "secret shown once at creation")
	assert.True(t, strings.HasPrefix(res.Secret, "webk_"))

	env := events.Envelope{EventID: "evt-test-0001", Event: events.TaskCompleted,
		WorkspaceID: ws, OccurredAt: time.Now().UTC(),
		Payload: map[string]any{"result": "summary: task completed"}}
	svc.HandleEvent(ctx, env)
	svc.HandleEvent(ctx, env)

	dels, err := svc.Deliveries(ctx, ws, res.Subscription.ID, "", 10)
	require.NoError(t, err)
	require.Len(t, dels, 1, "same event_id double counting absorbed by UNIQUE")

	w := &webhookworkers.DeliveryWorker{Service: svc, Log: quietLogger()}
	n, err := w.RunOnce(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	fp.mu.Lock()
	require.Len(t, fp.bodies, 1)
	body, sigHeader, tsHeader := fp.bodies[0], fp.sigs[0], fp.tss[0]
	fp.mu.Unlock()

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	assert.Equal(t, events.TaskCompleted, got["type"])
	assert.Equal(t, "evt-test-0001", got["id"])

	mac := hmac.New(sha256.New, []byte(res.Secret))
	mac.Write([]byte(tsHeader + "." + body))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	assert.Equal(t, want, sigHeader, "HMAC(ts.body) verifiable")

	dels, err = svc.Deliveries(ctx, ws, res.Subscription.ID, "", 10)
	require.NoError(t, err)
	require.Len(t, dels, 1)
	assert.Equal(t, "succeeded", dels[0].Status)
	assert.NotNil(t, dels[0].DeliveredAt)
}

func TestWebhookRetryDeadReplay(t *testing.T) {
	svc, pool, ws, user := webhooksEnv(t)
	ctx := context.Background()

	fp := &fakePlugin{status: 500}
	srv := httptest.NewServer(fp)
	t.Cleanup(srv.Close)

	res, err := svc.Create(ctx, ws, srv.URL, nil, "", user)
	require.NoError(t, err)
	svc.HandleEvent(ctx, events.Envelope{EventID: "evt-retry-0001", Event: events.TaskFailed,
		WorkspaceID: ws, OccurredAt: time.Now().UTC()})

	w := &webhookworkers.DeliveryWorker{Service: svc, Log: quietLogger()}
	_, err = w.RunOnce(ctx)
	require.NoError(t, err)
	dels, err := svc.Deliveries(ctx, ws, res.Subscription.ID, "", 10)
	require.NoError(t, err)
	require.Len(t, dels, 1)
	assert.Equal(t, "pending", dels[0].Status)
	assert.EqualValues(t, 1, dels[0].Attempts)
	assert.True(t, dels[0].NextAttemptAt.After(time.Now()), "backoff shifted back")

	_, err = pool.Exec(ctx,
		`UPDATE event_delivery SET attempts=$1, next_attempt_at=now() WHERE event_id='evt-retry-0001'`,
		app.MaxAttempts())
	require.NoError(t, err)
	_, err = w.RunOnce(ctx)
	require.NoError(t, err)
	dead, err := svc.Deliveries(ctx, ws, res.Subscription.ID, "dead", 10)
	require.NoError(t, err)
	require.Len(t, dead, 1, "attempts beyond backoff table → dead letter")

	fp.status = 200
	require.NoError(t, svc.Replay(ctx, ws, res.Subscription.ID, dead[0].ID))
	_, err = w.RunOnce(ctx)
	require.NoError(t, err)
	ok, err := svc.Deliveries(ctx, ws, res.Subscription.ID, "succeeded", 10)
	require.NoError(t, err)
	require.Len(t, ok, 1, "succeeds after replay")
}

func TestEventsKeysetFallback(t *testing.T) {
	svc, pool, ws, _ := webhooksEnv(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, err := pool.Exec(ctx, `
INSERT INTO event_outbox (id, workspace_id, event_type, payload)
VALUES ($1, $2, 'task.completed', '{"n":1}')`,
			fmt.Sprintf("00000000-0000-4000-8000-%012d", i), ws)
		require.NoError(t, err)
	}
	_, err := pool.Exec(ctx, `
INSERT INTO event_outbox (id, workspace_id, event_type, payload) VALUES ($1, $2, 'task.failed', '{}')`,
		"99999999-9999-4999-8999-999999999999", "88888888-8888-4888-8888-888888888888")
	require.NoError(t, err)

	page1, next, err := svc.Events(ctx, ws, time.Time{}, "", 3)
	require.NoError(t, err)
	require.Len(t, page1, 3)
	require.NotEmpty(t, next)
	for _, e := range page1 {
		assert.Equal(t, ws, e.WorkspaceID, "workspace isolation")
	}

	parts := strings.SplitN(next, "|", 2)
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	require.NoError(t, err)
	page2, next2, err := svc.Events(ctx, ws, time.Unix(0, ns).UTC(), parts[1], 3)
	require.NoError(t, err)
	require.Len(t, page2, 2, "remaining 2")
	assert.Empty(t, next2, "partial pages have no cursor")
	assert.Len(t, append(page1, page2...), 5, "two pages sum = full set")
}
