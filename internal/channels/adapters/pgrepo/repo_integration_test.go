package pgrepo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/internal/channels/app"
	"orkome/migrations"
)

const (
	domain0Settle = 16 * time.Minute
	domain0Lease  = 2 * time.Minute
)

func newChanDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_cchan WITH (FORCE)")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_cchan")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	pool, err := pgxpool.New(context.Background(),
		strings.Replace(base, "/"+cfg.Database, "/orkome_cchan", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)
	return pool
}

type chanFixture struct {
	pool *pgxpool.Pool
	repo *Repo

	wsID, userID, agentID, instID string
}

func newChanFixture(t *testing.T) *chanFixture {
	pool := newChanDB(t)
	f := &chanFixture{pool: pool, repo: New(pg.WrapPool(pool))}
	ctx := context.Background()
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('chan@x.io','CHAN') RETURNING id::text`).Scan(&f.userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ('cchan','c-chan') RETURNING id::text`).Scan(&f.wsID))
	_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, f.wsID, f.userID)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO agent (workspace_id, name, runtime_config)
VALUES ($1, 'chan-agent', '{"provider":"fake","model":"m"}'::jsonb) RETURNING id::text`, f.wsID).Scan(&f.agentID))
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO channel_installation (workspace_id, agent_id, installer_user_id, kind, name, config)
VALUES ($1, $2, $3, 'fake', 'main install', '{"routing_key":"rk-1"}'::jsonb) RETURNING id::text`,
		f.wsID, f.agentID, f.userID).Scan(&f.instID))
	return f
}

func (f *chanFixture) newSession(t *testing.T) string {
	t.Helper()
	var id string
	require.NoError(t, f.pool.QueryRow(context.Background(), `
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status)
VALUES ($1,$2,$3,'channel session','active') RETURNING id::text`, f.wsID, f.agentID, f.userID).Scan(&id))
	return id
}

func TestPGInboundDedupTwoPhase(t *testing.T) {
	f := newChanFixture(t)
	ctx := context.Background()

	tok, err := f.repo.ClaimInbound(ctx, f.instID, "msg-1")
	require.NoError(t, err)
	assert.NotEmpty(t, tok)

	_, err = f.repo.ClaimInbound(ctx, f.instID, "msg-1")
	assert.ErrorIs(t, err, app.ErrDuplicateInbound)

	tok2, err := f.repo.ClaimInbound(ctx, f.instID, "msg-race")
	require.NoError(t, err)
	require.NoError(t, f.repo.ReleaseInbound(ctx, f.instID, "msg-race", tok2))
	var winners atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.repo.ClaimInbound(ctx, f.instID, "msg-race"); err == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(1), winners.Load(), "seen-state concurrent claims win exactly one (single-statement CAS)")

	require.NoError(t, f.repo.MarkInboundProcessed(ctx, f.instID, "msg-1", "00000000-0000-0000-0000-000000000000"))
	var state string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT state FROM channel_inbound_message_dedup WHERE external_id='msg-1'`).Scan(&state))
	assert.Equal(t, "claimed", state, "wrong-token marks must not take effect")
	require.NoError(t, f.repo.MarkInboundProcessed(ctx, f.instID, "msg-1", tok))
	require.NoError(t, f.repo.MarkInboundProcessed(ctx, f.instID, "msg-1", tok))
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT state FROM channel_inbound_message_dedup WHERE external_id='msg-1'`).Scan(&state))
	assert.Equal(t, "processed", state)

	tok3, err := f.repo.ClaimInbound(ctx, f.instID, "msg-2")
	require.NoError(t, err)
	require.NoError(t, f.repo.ReleaseInbound(ctx, f.instID, "msg-2", tok3))
	tok4, err := f.repo.ClaimInbound(ctx, f.instID, "msg-2")
	require.NoError(t, err)
	assert.NotEqual(t, tok3, tok4, "re-claiming issues a new token (owner rotates)")

	n, err := f.repo.CleanStaleInbound(ctx, time.Now().Add(48*time.Hour))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(2))
}

func TestPGLeaseDoubleReplicaExactlyOneWinner(t *testing.T) {
	f := newChanFixture(t)
	ctx := context.Background()
	leases := NewSQLLeaseStore(f.repo)

	const replicas = 8
	var winners atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < replicas; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok := fmt.Sprintf("replica-%d", i)
			<-start
			if err := leases.TryAcquire(ctx, f.instID, tok, time.Minute); err == nil {
				winners.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	assert.Equal(t, int64(1), winners.Load(), "concurrency TryAcquire wins exactly one")

	_, err := f.pool.Exec(ctx, `UPDATE channel_installation SET lease_owner=NULL, lease_expires_at=NULL WHERE id=$1`, f.instID)
	require.NoError(t, err)
	require.NoError(t, leases.TryAcquire(ctx, f.instID, "replica-0", time.Minute))

	assert.ErrorIs(t, leases.Renew(ctx, f.instID, "replica-other", time.Minute), app.ErrLeaseNotAcquired)
	assert.NoError(t, leases.Renew(ctx, f.instID, "replica-0", time.Minute))

	assert.NoError(t, leases.Release(ctx, f.instID, "replica-other"))
	assert.NoError(t, leases.Renew(ctx, f.instID, "replica-0", time.Minute), "someone else's release must not unlock the holder")
	assert.NoError(t, leases.Release(ctx, f.instID, "replica-0"))

	require.NoError(t, leases.TryAcquire(ctx, f.instID, "replica-1", time.Minute))
	_, err = f.pool.Exec(ctx, `UPDATE channel_installation SET lease_expires_at=now()-interval '1s' WHERE id=$1`, f.instID)
	require.NoError(t, err)
	assert.NoError(t, leases.TryAcquire(ctx, f.instID, "replica-2", time.Minute), "expired leases can be taken over")

	held, err := leases.ListHeld(ctx, []string{f.instID})
	require.NoError(t, err)
	assert.Contains(t, held, f.instID)
	_, err = f.pool.Exec(ctx, `UPDATE channel_installation SET lease_expires_at=now()-interval '1s' WHERE id=$1`, f.instID)
	require.NoError(t, err)
	held2, err := leases.ListHeld(ctx, []string{f.instID})
	require.NoError(t, err)
	assert.NotContains(t, held2, f.instID)
}

func TestPGRouteAndGenerations(t *testing.T) {
	f := newChanFixture(t)
	ctx := context.Background()

	sess1 := f.newSession(t)
	rt, err := f.repo.InsertRoute(ctx, app.Route{
		InstallationID: f.instID, WorkspaceID: f.wsID, ExternalChatID: "chat-1",
		ChatType: "p2p", ChatSessionID: sess1,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), rt.Generation)
	assert.Equal(t, int64(1), rt.ContextRevision)

	sessB := f.newSession(t)
	var okCount atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.repo.InsertRoute(ctx, app.Route{
				InstallationID: f.instID, WorkspaceID: f.wsID, ExternalChatID: "chat-1",
				ChatType: "p2p", ChatSessionID: sessB,
			}); err == nil {
				okCount.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(0), okCount.Load(), "with an existing active route, concurrent inserts all conflict (find-or-create converges via re-query)")
	got, ok, err := f.repo.GetRoute(ctx, f.instID, "chat-1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, sess1, got.ChatSessionID, "original route not overwritten")

	rev, err := f.repo.NextGeneration(ctx, sess1, f.userID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), rev)
	var genRows int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT count(*) FROM channel_chat_context_generation WHERE chat_session_id=$1 AND revision=2`, sess1).Scan(&genRows))
	assert.Equal(t, 1, genRows)
	cur, err := f.repo.CurrentGeneration(ctx, sess1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), cur)

	sess2 := f.newSession(t)
	rot, err := f.repo.RotateRoute(ctx, f.instID, "chat-1", sess2)
	require.NoError(t, err)
	assert.Equal(t, int64(2), rot.Generation)
	assert.Equal(t, int64(1), rot.ContextRevision)
	assert.Equal(t, sess2, rot.ChatSessionID)
	got2, ok, err := f.repo.GetRoute(ctx, f.instID, "chat-1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, sess2, got2.ChatSessionID)
}

func TestPGDebounceRecoveryArming(t *testing.T) {
	f := newChanFixture(t)
	ctx := context.Background()
	sess := f.newSession(t)

	snap := app.ArmedPendingContext{
		SessionID: sess, ContextRevision: 1,
		WorkspaceID: f.wsID, AgentID: f.agentID, UserID: f.userID,
		MediaPending: true, BindingID: "route-1", InstallationID: f.instID,
		Kind: "fake", ExternalChatID: "chat-1", ChatType: "p2p",
		ExternalMessageID: "m-1", ExternalThreadID: "th-1", RouteRevision: 3,
	}

	tok, err := f.repo.ArmPendingRun(ctx, snap)
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	armed, err := f.repo.ListArmedPendingContexts(ctx)
	require.NoError(t, err)
	require.Len(t, armed, 1)
	assert.Equal(t, sess, armed[0].SessionID)
	assert.Equal(t, int64(1), armed[0].ContextRevision)
	assert.Equal(t, tok, armed[0].Token)
	assert.Equal(t, snap.UserID, armed[0].UserID)
	assert.Equal(t, snap.AgentID, armed[0].AgentID)
	assert.Equal(t, snap.BindingID, armed[0].BindingID)
	assert.Equal(t, snap.Kind, armed[0].Kind)
	assert.Equal(t, snap.ChatType, armed[0].ChatType)
	assert.Equal(t, snap.ExternalMessageID, armed[0].ExternalMessageID)
	assert.Equal(t, snap.ExternalThreadID, armed[0].ExternalThreadID)
	assert.Equal(t, snap.RouteRevision, armed[0].RouteRevision)
	assert.True(t, armed[0].MediaPending)

	tok2, err := f.repo.ArmPendingRun(ctx, snap)
	require.NoError(t, err)
	assert.NotEqual(t, tok, tok2, "re-arming issues a new fence token")
	require.NoError(t, f.repo.ClearPendingRun(ctx, sess, 1, tok))
	armed, err = f.repo.ListArmedPendingContexts(ctx)
	require.NoError(t, err)
	require.Len(t, armed, 1, "old token clearing must not wrongly clear the new arming marks")
	assert.Equal(t, tok2, armed[0].Token)

	require.NoError(t, f.repo.ClearPendingRun(ctx, sess, 1, tok2))
	armed, err = f.repo.ListArmedPendingContexts(ctx)
	require.NoError(t, err)
	assert.Empty(t, armed)

	_, err = f.repo.InsertRoute(ctx, app.Route{
		InstallationID: f.instID, WorkspaceID: f.wsID, ExternalChatID: "chat-1",
		ChatType: "p2p", ChatSessionID: sess,
	})
	require.NoError(t, err)
	rev, err := f.repo.NextGeneration(ctx, sess, f.userID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), rev)
	snap.ContextRevision = rev
	tok3, err := f.repo.ArmPendingRun(ctx, snap)
	require.NoError(t, err)
	var rows int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT count(*) FROM channel_chat_context_generation WHERE chat_session_id=$1 AND revision=$2`,
		sess, rev).Scan(&rows))
	assert.Equal(t, 1, rows, "arming reuses the existing generation row (no duplicate rows)")
	require.NoError(t, f.repo.ClearPendingRun(ctx, sess, rev, tok3))
}

func TestPGBindingFlow(t *testing.T) {
	f := newChanFixture(t)
	ctx := context.Background()

	_, err := f.repo.ResolveSender(ctx, f.instID, "extu-1")
	assert.ErrorIs(t, err, app.ErrSenderUnbound)

	token, err := f.repo.IssueBindingToken(ctx, f.instID, "extu-1", app.BindingTokenTTL)
	require.NoError(t, err)

	require.NoError(t, f.repo.ConsumeBindingToken(ctx, f.wsID, token, f.userID))
	uid, err := f.repo.ResolveSender(ctx, f.instID, "extu-1")
	require.NoError(t, err)
	assert.Equal(t, f.userID, uid)

	assert.ErrorIs(t, f.repo.ConsumeBindingToken(ctx, f.wsID, token, f.userID), app.ErrBindingTokenInvalid)
	assert.ErrorIs(t, f.repo.ConsumeBindingToken(ctx, f.wsID, "not-a-token", f.userID), app.ErrBindingTokenInvalid)
}

func TestPGMediaLedgerAndBindRace(t *testing.T) {
	f := newChanFixture(t)
	ctx := context.Background()
	past := time.Now().Add(-domain0Settle)

	intents := []app.MediaIntent{
		{StorageKey: "obj-bind", WorkspaceID: f.wsID, State: "pending", NextAttemptAt: past},
		{StorageKey: "obj-claim", WorkspaceID: f.wsID, State: "pending", NextAttemptAt: past},
		{StorageKey: "obj-both", WorkspaceID: f.wsID, State: "pending", NextAttemptAt: past},
	}
	require.NoError(t, f.repo.InsertMediaIntents(ctx, intents))

	n, err := f.repo.BindMediaToMessage(ctx, "", []string{"obj-bind"})
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	won, err := f.repo.ClaimMediaIntent(ctx, "obj-claim", "aaaaaaa1-0000-0000-0000-000000000001", time.Now().Add(domain0Lease))
	require.NoError(t, err)
	require.True(t, won)
	n2, err := f.repo.BindMediaToMessage(ctx, "", []string{"obj-claim"})
	require.NoError(t, err)
	assert.Zero(t, n2, "keys reclaimed after claim cannot be bound (no inline delete, no revival)")

	var bindWin, claimWin atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if n, _ := f.repo.BindMediaToMessage(ctx, "", []string{"obj-both"}); n > 0 {
			bindWin.Add(1)
		}
	}()
	go func() {
		defer wg.Done()
		if won, _ := f.repo.ClaimMediaIntent(ctx, "obj-both", "aaaaaaa2-0000-0000-0000-000000000002", time.Now().Add(domain0Lease)); won {
			claimWin.Add(1)
		}
	}()
	wg.Wait()
	assert.Equal(t, int64(1), bindWin.Load()+claimWin.Load(), "bind vs reclaim race wins exactly one")

	require.NoError(t, f.repo.MarkMediaTombstoned(ctx, "obj-claim", "aaaaaaa1-0000-0000-0000-000000000001", 1,
		time.Now().Add(-time.Second)))
	var state string
	var pass int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT state, tombstone_pass FROM channel_media_pending_object WHERE storage_key='obj-claim'`).Scan(&state, &pass))
	assert.Equal(t, "tombstoned", state)
	assert.Equal(t, 1, pass)

	won2, err := f.repo.ClaimMediaIntent(ctx, "obj-claim", "aaaaaaa3-0000-0000-0000-000000000003", time.Now().Add(domain0Lease))
	require.NoError(t, err)
	require.True(t, won2, "tombstones due (next_attempt injected in the past) reclaimable and re-deletable")
	require.NoError(t, f.repo.ReleaseMediaIntent(ctx, "obj-claim", "aaaaaaa3-0000-0000-0000-000000000003", 2, time.Now().Add(time.Minute), "store 503"))
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT state FROM channel_media_pending_object WHERE storage_key='obj-claim'`).Scan(&state))
	assert.Equal(t, "pending", state)
}

func TestPGMediaClaimFencing(t *testing.T) {
	f := newChanFixture(t)
	ctx := context.Background()
	past := time.Now().Add(-domain0Settle)
	require.NoError(t, f.repo.InsertMediaIntents(ctx, []app.MediaIntent{
		{StorageKey: "obj-f", WorkspaceID: f.wsID, State: "pending", NextAttemptAt: past},
	}))
	won, err := f.repo.ClaimMediaIntent(ctx, "obj-f", "aaaaaaab-0000-0000-0000-00000000000a", time.Now().Add(domain0Lease))
	require.NoError(t, err)
	require.True(t, won)
	won2, err := f.repo.ClaimMediaIntent(ctx, "obj-f", "aaaaaaac-0000-0000-0000-00000000000b", time.Now().Add(domain0Lease))
	require.NoError(t, err)
	assert.False(t, won2, "another party's claim fails while the lease is active")

	_, err = f.pool.Exec(ctx, `UPDATE channel_media_pending_object SET lease_expires_at=now()-interval '1s' WHERE storage_key='obj-f'`)
	require.NoError(t, err)
	won3, err := f.repo.ClaimMediaIntent(ctx, "obj-f", "aaaaaaac-0000-0000-0000-00000000000b", time.Now().Add(domain0Lease))
	require.NoError(t, err)
	assert.True(t, won3)
}

func TestPGTaskDeliveryImmutableStamp(t *testing.T) {
	f := newChanFixture(t)
	ctx := context.Background()
	stamp := app.TaskDelivery{
		TaskID: "44444444-4444-4444-4444-444444444444", BindingID: "bbbbbbbb-0000-0000-0000-00000000000b",
		InstallationID: f.instID, Kind: "fake", ExternalChatID: "chat-1",
		ChatType: "p2p", RouteRevision: 2, ContextRevision: 3, MediaPending: true,
	}
	require.NoError(t, f.repo.InsertTaskDelivery(ctx, stamp))
	require.NoError(t, f.repo.InsertTaskDelivery(ctx, stamp))
	got, ok, err := f.repo.GetTaskDelivery(ctx, stamp.TaskID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, int64(2), got.RouteRevision)
	assert.Equal(t, int64(3), got.ContextRevision)
	assert.True(t, got.MediaPending)
	_, ok, err = f.repo.GetTaskDelivery(ctx, "55555555-5555-5555-5555-555555555555")
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, f.repo.RecordOutboundMessage(ctx, app.OutboundRecord{
		InstallationID: f.instID, Kind: "fake", ExternalMessageID: "plat-9",
		BindingID: "bbbbbbbb-0000-0000-0000-00000000000b", RouteRevision: 2, TaskID: stamp.TaskID, OutboundKind: "reply",
	}))
	require.NoError(t, f.repo.SettleReplyDelivery(ctx, stamp.TaskID, stamp.TaskID, "bbbbbbbb-0000-0000-0000-00000000000b", f.instID, "fake", "chat-1", "plat-9", "delivered"))
	require.NoError(t, f.repo.SettleReplyDelivery(ctx, stamp.TaskID, stamp.TaskID, "bbbbbbbb-0000-0000-0000-00000000000b", f.instID, "fake", "chat-1", "plat-9", "delivered"))
	var phase string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT phase FROM channel_reply_delivery WHERE turn_id=$1`, stamp.TaskID).Scan(&phase))
	assert.Equal(t, "settled", phase)
}

func migrationsAfter023(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("../../../../migrations")
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ver := strings.TrimSuffix(name, filepath.Ext(name))
		if ver > "023_channels.up" {
			seen[ver] = true
		}
	}
	return len(seen)
}

func TestPGMigration023UpDownUp(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_cchan_mig WITH (FORCE)")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_cchan_mig")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	pool, err := pgxpool.New(context.Background(),
		strings.Replace(base, "/"+cfg.Database, "/orkome_cchan_mig", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)

	down, err := m.Down(context.Background(), migrationsAfter023(t)+1)
	require.NoError(t, err)
	require.Len(t, down, migrationsAfter023(t)+1)
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('channel_user_binding','channel_media_pending_object','channel_chat_context_generation')`).Scan(&n))
	assert.Zero(t, n, "after 023 down the newly created tables are all reclaimed")

	up, err := m.Up(context.Background(), 0)
	require.NoError(t, err)
	require.Len(t, up, migrationsAfter023(t)+1)
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.tables WHERE table_name LIKE 'channel_%'`).Scan(&n))
	assert.Equal(t, 12, n, "all 12 tables of the channels family present")
}

func TestPGReplyStreamingTwoPhase(t *testing.T) {
	f := newChanFixture(t)
	ctx := context.Background()
	sess := f.newSession(t)
	const turn = "66666666-6666-6666-6666-666666666666"
	const binding = "77777777-0000-0000-0000-000000000007"

	opened, err := f.repo.BeginReplyStreaming(ctx, turn, turn, binding, f.instID, "fake", "chat-1")
	require.NoError(t, err)
	assert.True(t, opened, "open a placeholder with no rows")

	opened2, err := f.repo.BeginReplyStreaming(ctx, turn, turn, binding, f.instID, "fake", "chat-1")
	require.NoError(t, err)
	assert.True(t, opened2, "streaming phase resumable (takeover retry)")

	require.NoError(t, f.repo.RecordReplyPlaceholder(ctx, turn, "om-100"))
	var sendState, msgID string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT send_state, external_message_id FROM channel_reply_delivery WHERE turn_id=$1`, turn).
		Scan(&sendState, &msgID))
	assert.Equal(t, "known", sendState)
	assert.Equal(t, "om-100", msgID)

	require.NoError(t, f.repo.InsertCardMessage(ctx, app.CardMessage{
		ChatSessionID: sess, TaskID: turn, Kind: "fake", ExternalChatID: "chat-1",
		ExternalCardMessageID: "om-100", Status: "streaming",
	}))
	require.NoError(t, f.repo.InsertCardMessage(ctx, app.CardMessage{
		ChatSessionID: sess, TaskID: turn, Kind: "fake", ExternalChatID: "chat-1",
		ExternalCardMessageID: "om-999", Status: "streaming",
	}))
	var cardCount int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT count(*) FROM channel_outbound_card_message WHERE task_id=$1`, turn).Scan(&cardCount))
	assert.Equal(t, 1, cardCount, "rich-card row unique per task (partial unique index)")

	advanced, err := f.repo.MarkReplyTerminal(ctx, turn, "om-200")
	require.NoError(t, err)
	assert.True(t, advanced)
	var phase string
	var chunks int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT phase, chunks_sent FROM channel_reply_delivery WHERE turn_id=$1`, turn).Scan(&phase, &chunks))
	assert.Equal(t, "terminal", phase)
	assert.Equal(t, 1, chunks, "one final draft delivery = one chunk")

	require.NoError(t, f.repo.FinishCardMessage(ctx, turn))
	require.NoError(t, f.repo.FinishCardMessage(ctx, turn))
	var cardStatus string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status FROM channel_outbound_card_message WHERE task_id=$1`, turn).Scan(&cardStatus))
	assert.Equal(t, "final", cardStatus)

	opened3, err := f.repo.BeginReplyStreaming(ctx, turn, turn, binding, f.instID, "fake", "chat-1")
	require.NoError(t, err)
	assert.False(t, opened3, "placeholders must not reopen after terminal")

	require.NoError(t, f.repo.SettleReplyDelivery(ctx, turn, turn, binding, f.instID, "fake", "chat-1", "om-200", "delivered"))
	require.NoError(t, f.repo.SettleReplyDelivery(ctx, turn, turn, binding, f.instID, "fake", "chat-1", "om-200", "delivered"))
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT phase FROM channel_reply_delivery WHERE turn_id=$1`, turn).Scan(&phase))
	assert.Equal(t, "settled", phase)
	advanced2, err := f.repo.MarkReplyTerminal(ctx, turn, "om-300")
	require.NoError(t, err)
	assert.False(t, advanced2, "advancing the final draft after settled is a no-op")
	opened4, err := f.repo.BeginReplyStreaming(ctx, turn, turn, binding, f.instID, "fake", "chat-1")
	require.NoError(t, err)
	assert.False(t, opened4, "placeholder must not reopen after settled")
}

func TestPGTitleCallback(t *testing.T) {
	f := newChanFixture(t)
	ctx := context.Background()
	sess := f.newSession(t)
	_, err := f.repo.InsertRoute(ctx, app.Route{
		InstallationID: f.instID, WorkspaceID: f.wsID, ExternalChatID: "chat-t",
		ChatType: "p2p", ChatSessionID: sess,
	})
	require.NoError(t, err)

	applied, err := f.repo.UpdateRouteTitle(ctx, f.instID, "chat-t", "LLM polished title", "llm")
	require.NoError(t, err)
	assert.True(t, applied)
	rt, ok, err := f.repo.GetRoute(ctx, f.instID, "chat-t")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "LLM polished title", rt.Title)
	assert.Equal(t, "llm", rt.TitleSource)

	applied, err = f.repo.UpdateRouteTitle(ctx, f.instID, "chat-t", "better title", "llm")
	require.NoError(t, err)
	assert.True(t, applied)

	_, err = f.pool.Exec(ctx, `
UPDATE channel_chat_session_binding SET title_source='explicit', title='user-named'
WHERE channel_installation_id=$1 AND external_chat_id='chat-t'`, f.instID)
	require.NoError(t, err)
	applied, err = f.repo.UpdateRouteTitle(ctx, f.instID, "chat-t", "late LLM title", "llm")
	require.NoError(t, err)
	assert.False(t, applied, "explicit lock: CAS does not overwrite")
	rt, _, err = f.repo.GetRoute(ctx, f.instID, "chat-t")
	require.NoError(t, err)
	assert.Equal(t, "user-named", rt.Title)

	applied, err = f.repo.UpdateRouteTitle(ctx, f.instID, "chat-none", "x", "llm")
	require.NoError(t, err)
	assert.False(t, applied)

	_, err = f.pool.Exec(ctx, `
UPDATE channel_chat_session_binding SET retired_at=now()
WHERE channel_installation_id=$1 AND external_chat_id='chat-t'`, f.instID)
	require.NoError(t, err)
	applied, err = f.repo.UpdateRouteTitle(ctx, f.instID, "chat-t", "y", "llm")
	require.NoError(t, err)
	assert.False(t, applied, "retired historical generations cannot backfill")

	inst, err := f.repo.GetInstallation(ctx, f.instID)
	require.NoError(t, err)
	assert.Equal(t, f.wsID, inst.WorkspaceID)
	_, err = f.repo.GetInstallation(ctx, "88888888-8888-8888-8888-888888888888")
	assert.ErrorIs(t, err, app.ErrInstallationNotFound)
}
