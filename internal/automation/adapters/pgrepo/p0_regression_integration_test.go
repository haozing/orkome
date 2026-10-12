package pgrepo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	autopapp "orkome/internal/automation/app"
	"orkome/internal/automation/domain"
)

// 回归（FP-13 P0-1）：手动触发 autopilot 走 StartRun(apID, "", "manual")，
// trigger_id 是空串。修复前 $2 直接入参，PG 报
// `22P02 invalid input syntax for type uuid: ""` → dispatchRun 整个事务回滚 →
// 前端「立即触发」100% 必败（routes.go 曾同时不打日志，根因被吞）。
// fake repo 不做 uuid 类型断言（正是既有 45+ 单测漏网的原因），所以本用例
// 必须跑真 PG：无 TEST_DATABASE_URL 时 skip，是设计上的集成门控，不是遗漏。
func TestInsertRun_EmptyTriggerIDStoredAsNull(t *testing.T) {
	f := newQuotaFixture(t)
	run, err := f.repo.InsertRun(f.ctx, autopapp.Run{
		AutopilotID: f.apID, TriggerID: "", Source: "manual",
		Status: domain.RunPending, PlannedAt: time.Now()})
	require.NoError(t, err, "manual trigger (empty trigger_id) must insert as NULL, not 22P02")
	require.NotEmpty(t, run.ID)

	var trigID *string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT trigger_id::text FROM autopilot_run WHERE id=$1`, run.ID).Scan(&trigID))
	require.Nil(t, trigID, "empty trigger_id must land as NULL (column is nullable, 1006:77)")
}

// 非空 trigger_id（schedule/webhook 路径）不受 nullif 影响，仍正常落库。
func TestInsertRun_RealTriggerIDRoundTrips(t *testing.T) {
	f := newQuotaFixture(t)
	run, err := f.repo.InsertRun(f.ctx, autopapp.Run{
		AutopilotID: f.apID, TriggerID: f.trig.ID, Source: "webhook",
		Status: domain.RunPending, PlannedAt: time.Now()})
	require.NoError(t, err)
	var got string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT trigger_id::text FROM autopilot_run WHERE id=$1`, run.ID).Scan(&got))
	require.Equal(t, f.trig.ID, got)
}

// 回归（FP-13 P0-3）：创建 autopilot 必产一条 enabled 的空 cron 兜底 trigger
// （service_autopilot.go Create cronExpr=="" 分支，无 next_run_at，永不触发）。
// 旧删除守卫只看 enabled → DELETE 必 409，前端无 trigger 管理入口 → 删除闭环全断。
// 修复后守卫只挡"会真正触发的 trigger"（webhook 或带 cron 的 schedule）。
func TestDeleteAutopilot_InertFallbackTriggerDoesNotBlock(t *testing.T) {
	f := newQuotaFixture(t)
	var uid string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('ap-fallback@test.dev','AP') RETURNING id::text`).Scan(&uid),
		"autopilot.created_by is an FK to the user table, seed a real user first")
	svc := autopapp.NewAutopilotService(f.repo, nil, nil)
	ap, _, err := svc.Create(f.ctx, autopapp.Autopilot{
		WorkspaceID: f.wsID, Name: "ap-fallback", ExecMode: "task_only",
		Template: map[string]any{}, CreatedBy: uid}, "", "")
	require.NoError(t, err, "empty cron must succeed via the fallback trigger path")

	out, err := f.repo.DeleteAutopilot(f.ctx, f.wsID, ap.ID)
	require.NoError(t, err)
	require.True(t, out.Found)
	require.False(t, out.BlockedTriggers,
		"enabled fallback trigger (empty cron, never fires) must not block DELETE")
	require.True(t, out.Deleted, "autopilot delete must not be blocked by its own default trigger")
}

// 对照组：会真正触发的 trigger（enabled webhook / 带 cron 的 schedule）仍然挡删除。
func TestDeleteAutopilot_ActiveTriggerStillBlocks(t *testing.T) {
	f := newQuotaFixture(t) // fixture 已给 apID 挂了一条 enabled webhook trigger

	out, err := f.repo.DeleteAutopilot(f.ctx, f.wsID, f.apID)
	require.NoError(t, err)
	require.True(t, out.Found)
	require.True(t, out.BlockedTriggers, "enabled webhook trigger must still block DELETE")
	require.False(t, out.Deleted)

	// 带 cron 的 enabled schedule trigger 同样挡。
	var uid2 string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('ap-cron@test.dev','AP2') RETURNING id::text`).Scan(&uid2))
	ap2, _, err := autopapp.NewAutopilotService(f.repo, nil, nil).Create(f.ctx, autopapp.Autopilot{
		WorkspaceID: f.wsID, Name: "ap-cron", ExecMode: "task_only",
		Template: map[string]any{}, CreatedBy: uid2}, "0 9 * * *", "")
	require.NoError(t, err)
	out, err = f.repo.DeleteAutopilot(f.ctx, f.wsID, ap2.ID)
	require.NoError(t, err)
	require.True(t, out.BlockedTriggers, "enabled schedule trigger with a cron must still block DELETE")
	require.False(t, out.Deleted)
}
