package pgrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	autopapp "orkome/internal/automation/app"
	"orkome/internal/automation/domain"
	"orkome/migrations"
)

type quotaFixture struct {
	pool *pgxpool.Pool
	repo *Repo
	db   *pg.DB
	wsID string
	apID string
	trig autopapp.Trigger
	ctx  context.Context
}

func newQuotaFixture(t *testing.T) *quotaFixture {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_auto")
	_ = admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	pool, err := pgxpool.New(context.Background(), strings.Replace(base, "/"+cfg.Database, "/orkome_auto", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `TRUNCATE autopilot_run, autopilot_quota_reservation,
		autopilot_trigger, autopilot, webhook_delivery, work_item_wakeup,
		autopilot_rule_version, autopilot_collaborator, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	f := &quotaFixture{pool: pool, repo: New(pg.WrapPool(pool)), db: pg.WrapPool(pool), ctx: context.Background()}
	require.NoError(t, pool.QueryRow(f.ctx,
		`INSERT INTO workspace (name, slug) VALUES ('w','auto-quota') RETURNING id::text`).Scan(&f.wsID))
	ap, err := f.repo.CreateAutopilot(f.ctx, autopapp.Autopilot{
		WorkspaceID: f.wsID, Name: "ap", ExecMode: "task_only", DailyLimit: 3, Enabled: true,
		Template: map[string]any{}})
	require.NoError(t, err)
	f.apID = ap.ID
	f.trig, err = f.repo.CreateTrigger(f.ctx, autopapp.Trigger{
		AutopilotID: ap.ID, Kind: domain.TriggerWebhook, Enabled: true,
		WebhookToken: "tok-integration", SigningSecret: strings.Repeat("s", 64)})
	require.NoError(t, err)
	return f
}

func TestReserveQuota_ConcurrentSerialized(t *testing.T) {
	f := newQuotaFixture(t)
	ws, we := domain.QuotaWindow(time.Now())

	const n, limit = 10, 3
	var mu sync.Mutex
	granted, denied := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.repo.ReserveQuota(f.ctx, f.apID, ws, we, limit)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				granted++
			case errors.Is(err, domain.ErrQuotaExceeded):
				denied++
			default:
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, limit, granted, "successful reservations must equal the window cap exactly")
	require.Equal(t, n-limit, denied)

	var reserved, committed int
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT reserved, committed FROM autopilot_quota_reservation WHERE autopilot_id=$1`, f.apID).
		Scan(&reserved, &committed))
	require.Equal(t, limit, reserved)
	require.Equal(t, 0, committed)
}

func TestSetRunStatusByTask_CommitsReservation(t *testing.T) {
	f := newQuotaFixture(t)
	ws, we := domain.QuotaWindow(time.Now())
	resID, err := f.repo.ReserveQuota(f.ctx, f.apID, ws, we, 3)
	require.NoError(t, err)
	run, err := f.repo.InsertRun(f.ctx, autopapp.Run{
		AutopilotID: f.apID, TriggerID: f.trig.ID, Source: "webhook",
		Status: domain.RunRunning, PlannedAt: time.Now(), QuotaReservationID: resID})
	require.NoError(t, err)
	require.NoError(t, f.repo.SetRunTask(f.ctx, run.ID, "11111111-1111-1111-1111-111111111111", ""))

	require.NoError(t, f.repo.SetRunStatusByTask(f.ctx, "11111111-1111-1111-1111-111111111111", domain.RunCompleted))
	var status string
	var reserved, committed int
	require.NoError(t, f.pool.QueryRow(f.ctx, `SELECT status FROM autopilot_run WHERE id=$1`, run.ID).Scan(&status))
	require.Equal(t, domain.RunCompleted, status)
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT reserved, committed FROM autopilot_quota_reservation WHERE id=$1`, resID).Scan(&reserved, &committed))
	require.Equal(t, 0, reserved)
	require.Equal(t, 1, committed)

	require.NoError(t, f.repo.SetRunStatusByTask(f.ctx, "11111111-1111-1111-1111-111111111111", domain.RunCompleted))
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT committed FROM autopilot_quota_reservation WHERE id=$1`, resID).Scan(&committed))
	require.Equal(t, 1, committed)
}

func TestSetDispatchLockTimeout_TxLocal(t *testing.T) {
	f := newQuotaFixture(t)
	err := f.db.Within(f.ctx, func(ctx context.Context) error {
		if err := f.repo.SetDispatchLockTimeout(ctx); err != nil {
			return err
		}
		var ms string
		if err := f.repo.DB.Tx(ctx).QueryRow(ctx, `SHOW lock_timeout`).Scan(&ms); err != nil {
			return err
		}
		if ms != "50ms" {
			return fmt.Errorf("lock_timeout = %q, want 50ms", ms)
		}
		return nil
	})
	require.NoError(t, err)
}

func TestSetTriggerWebhookToken_RotatesLookup(t *testing.T) {
	f := newQuotaFixture(t)
	ok, err := f.repo.SetTriggerWebhookToken(f.ctx, f.wsID, f.apID, f.trig.ID, "tok-new")
	require.NoError(t, err)
	require.True(t, ok)

	_, ok, err = f.repo.GetTriggerByToken(f.ctx, "tok-integration")
	require.NoError(t, err)
	require.False(t, ok, "old tokens must be invalidated immediately")

	got, ok, err := f.repo.GetTriggerByToken(f.ctx, "tok-new")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, f.trig.ID, got.ID)

	ok, err = f.repo.SetTriggerWebhookToken(f.ctx, "00000000-0000-0000-0000-000000000001", f.apID, f.trig.ID, "tok-x")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestSetTriggerSigningSecret_Predicate(t *testing.T) {
	f := newQuotaFixture(t)
	secret := strings.Repeat("k", 64)
	ok, err := f.repo.SetTriggerSigningSecret(f.ctx, f.wsID, f.apID, f.trig.ID, secret)
	require.NoError(t, err)
	require.True(t, ok)
	got, _, err := f.repo.GetTriggerByToken(f.ctx, "tok-integration")
	require.NoError(t, err)
	require.Equal(t, secret, got.SigningSecret)

	ok, err = f.repo.SetTriggerSigningSecret(f.ctx, "00000000-0000-0000-0000-000000000002", f.apID, f.trig.ID, secret)
	require.NoError(t, err)
	require.False(t, ok, "cross-workspace must be invisible (E_NOT_FOUND semantics)")
}

type scanFixture struct {
	*quotaFixture
	wiID, agentID string
}

func newScanFixture(t *testing.T) *scanFixture {
	f := &scanFixture{quotaFixture: newQuotaFixture(t)}
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO agent (workspace_id, name) VALUES ($1,'scan-agent') RETURNING id::text`, f.wsID).Scan(&f.agentID))

	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO work_item (workspace_id, number, title, status_key, creator_id)
		 VALUES ($1, 1, 'wi', 'todo', gen_random_uuid()) RETURNING id::text`,
		f.wsID).Scan(&f.wiID))
	return f
}

func TestListDueWakeups_FourPathUnion(t *testing.T) {
	f := newScanFixture(t)
	now := time.Now()
	due, err := f.repo.CreateWakeup(f.ctx, autopapp.Wakeup{
		WorkspaceID: f.wsID, WorkItemID: f.wiID, AgentID: f.agentID, Kind: domain.WakeupTime,
		Instruction: "due", IntervalSeconds: 3600, NextFireAt: &now})
	require.NoError(t, err)
	future := now.Add(time.Hour)
	cond, err := f.repo.CreateWakeup(f.ctx, autopapp.Wakeup{
		WorkspaceID: f.wsID, WorkItemID: f.wiID, AgentID: f.agentID, Kind: domain.WakeupCondition,
		Instruction: "cond", Condition: map[string]any{"field": "status", "op": "eq", "value": "done"}})
	require.NoError(t, err)
	ev, err := f.repo.CreateWakeup(f.ctx, autopapp.Wakeup{
		WorkspaceID: f.wsID, WorkItemID: f.wiID, AgentID: f.agentID, Kind: domain.WakeupEvent,
		Instruction: "ev", EventTypes: []string{"comment.created"}})
	require.NoError(t, err)
	evStored, _, err := f.repo.GetWakeup(f.ctx, f.wsID, ev.ID)
	require.NoError(t, err)
	expiring, err := f.repo.CreateWakeup(f.ctx, autopapp.Wakeup{
		WorkspaceID: f.wsID, WorkItemID: f.wiID, AgentID: f.agentID, Kind: domain.WakeupTime,
		Instruction: "exp", IntervalSeconds: 3600, NextFireAt: &future, ExpiresAt: &now})
	require.NoError(t, err)

	require.NoError(t, f.repo.InsertReceipt(f.ctx, autopapp.Receipt{
		WakeupID: ev.ID, Revision: evStored.Revision, EventKey: "evt-1", WorkspaceID: f.wsID, CapturedAt: now}))

	processed, err := f.repo.CreateWakeup(f.ctx, autopapp.Wakeup{
		WorkspaceID: f.wsID, WorkItemID: f.wiID, AgentID: f.agentID, Kind: domain.WakeupEvent,
		Instruction: "ev-done", EventTypes: []string{"comment.created"}})
	require.NoError(t, err)
	require.NoError(t, f.repo.InsertReceipt(f.ctx, autopapp.Receipt{
		WakeupID: processed.ID, Revision: processed.Revision, EventKey: "evt-done", WorkspaceID: f.wsID, CapturedAt: now}))
	var rcID string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`UPDATE work_item_wakeup_receipt SET processed_at=now() WHERE event_key='evt-done' RETURNING id::text`).Scan(&rcID))

	got, err := f.repo.ListDueWakeups(f.ctx, now, 100)
	require.NoError(t, err)
	bySrc := map[string][]autopapp.DueWakeup{}
	for _, d := range got {
		bySrc[d.Source] = append(bySrc[d.Source], d)
	}
	require.Len(t, bySrc[autopapp.ScanDue], 1)
	require.Equal(t, due.ID, bySrc[autopapp.ScanDue][0].Wakeup.ID)
	require.Len(t, bySrc[autopapp.ScanExpire], 1)
	require.Equal(t, expiring.ID, bySrc[autopapp.ScanExpire][0].Wakeup.ID)
	require.Len(t, bySrc[autopapp.ScanCondition], 1, "system/condition candidates (expiring does not enter the condition path twice)")
	require.Equal(t, cond.ID, bySrc[autopapp.ScanCondition][0].Wakeup.ID)
	require.Len(t, bySrc[autopapp.ScanReceipt], 1)
	rc := bySrc[autopapp.ScanReceipt][0]
	require.Equal(t, ev.ID, rc.Wakeup.ID)
	require.Equal(t, "evt-1", rc.EventKey)
	require.Equal(t, evStored.Revision, rc.ReceiptRev, "receipts carry the captured revision (consistency gate input)")
	for _, d := range got {
		require.NotEqual(t, processed.ID, d.Wakeup.ID, "rules with processed receipts no longer enter scan for that receipt")
	}

	limited, err := f.repo.ListDueWakeups(f.ctx, now, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)

	require.NoError(t, f.repo.InsertReceipt(f.ctx, autopapp.Receipt{
		WakeupID: ev.ID, Revision: evStored.Revision, EventKey: "evt-1", WorkspaceID: f.wsID}))
	var n int
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM work_item_wakeup_receipt WHERE wakeup_id=$1 AND event_key='evt-1'`, ev.ID).Scan(&n))
	require.Equal(t, 1, n)
}

func TestScanEventMatch_LockTaskOwner_MarkExpired(t *testing.T) {
	f := newScanFixture(t)
	now := time.Now()
	matching, err := f.repo.CreateWakeup(f.ctx, autopapp.Wakeup{
		WorkspaceID: f.wsID, WorkItemID: f.wiID, AgentID: f.agentID, Kind: domain.WakeupEvent,
		Instruction: "m", EventTypes: []string{"comment.created", "task.completed"}})
	require.NoError(t, err)
	_, err = f.repo.CreateWakeup(f.ctx, autopapp.Wakeup{
		WorkspaceID: f.wsID, WorkItemID: f.wiID, AgentID: f.agentID, Kind: domain.WakeupEvent,
		Instruction: "other", EventTypes: []string{"task.failed"}})
	require.NoError(t, err)

	got, err := f.repo.ListEventWakeups(f.ctx, f.wsID, "comment.created")
	require.NoError(t, err)
	require.Len(t, got, 1, "subscription match = event_types array contains the event name")
	require.Equal(t, matching.ID, got[0].ID)

	err = f.db.Within(f.ctx, func(ctx context.Context) error {
		if err := f.repo.LockTaskOwner(ctx, f.wiID); err != nil {
			return err
		}
		return f.repo.LockTaskOwner(ctx, f.wiID)
	})
	require.NoError(t, err)

	expiring, err := f.repo.CreateWakeup(f.ctx, autopapp.Wakeup{
		WorkspaceID: f.wsID, WorkItemID: f.wiID, AgentID: f.agentID, Kind: domain.WakeupTime,
		Instruction: "e2", IntervalSeconds: 3600, ExpiresAt: &now})
	require.NoError(t, err)
	stored, ok2, err := f.repo.GetWakeup(f.ctx, f.wsID, expiring.ID)
	require.NoError(t, err)
	require.True(t, ok2)
	ok, err := f.repo.MarkWakeupExpired(f.ctx, stored.ID, stored.Revision)
	require.NoError(t, err)
	require.True(t, ok)
	var paused string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT paused_reason FROM work_item_wakeup WHERE id=$1`, expiring.ID).Scan(&paused))
	require.Equal(t, "expired", paused)
	ok, err = f.repo.MarkWakeupExpired(f.ctx, stored.ID, stored.Revision)
	require.NoError(t, err)
	require.False(t, ok, "CAS miss (already changed) idempotently returns false")
}
