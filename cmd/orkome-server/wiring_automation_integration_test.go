package main

// W2-2 回归（真 PG 门控）：system wakeup 全链——
//   EnsureChildDoneRule 播种（created_by 恒 NULL）→ 子工单进终态 → fire 链
//   → wakeupEnqueuer 落 agent_task_queue（originator/accountable 为 NULL，
//   不再 orZeroUUID 补零撞 agent_task_queue_originator_user_id_fkey）
//   → FireWakeup 推进（fire_count / revision / last_task_id / condition_state）。
//
// 修复前该链在 enqueue 处必炸 23503：InsertTask 整体失败 → fireOneSteps 返回
// false → 事务回滚 → next_fire_at 永不推进、due 扫描每 tick 重试同一失败
// （FP-13 P0-2 同族遗留）。fake repo 不经 pgx/PG 约束、且真正的补零逻辑在
// cmd 装配层（wakeupEnqueuer），internal 包的测试够不着——必须在本包真 PG 验证。
//
// 对照组：有主唤醒（created_by 非空）fire 后 originator 保持真实用户 +
// source=trigger_owner，证明去补零只影响"无主"分支，不回归归属链。

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	pkgmigrations "github.com/haozing/ploykit/migrations"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	autoppgrepo "orkome/internal/automation/adapters/pgrepo"
	autopapp "orkome/internal/automation/app"
	"orkome/internal/automation/domain"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	"orkome/migrations"
)

type wakeChainFixture struct {
	pool *pgxpool.Pool
	db   *pg.DB
	ctx  context.Context
	svc  *autopapp.WakeupService

	ws, agent, parent, child string
}

func newWakeChainFixture(t *testing.T) *wakeChainFixture {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_wake2")
	_ = admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	pool, err := pgxpool.New(context.Background(), strings.Replace(base, "/"+cfg.Database, "/orkome_wake2", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(context.Background(), 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `TRUNCATE agent_task_queue, work_item_wakeup_receipt,
		work_item_wakeup, system_wakeup_setting, work_item_status, work_item, agent,
		autopilot_run, autopilot_quota_reservation, autopilot_trigger, autopilot,
		automation_webhook_delivery, autopilot_rule_version, autopilot_collaborator,
		workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	f := &wakeChainFixture{pool: pool, db: pg.WrapPool(pool), ctx: context.Background()}
	db := f.db

	// seed：user → workspace → agent → 状态目录（done 终态）→ 父子工单。
	require.NoError(t, pool.QueryRow(f.ctx,
		`WITH u AS (INSERT INTO "user" (email, display_name) VALUES ('wake2@test.dev','W2') RETURNING id)
		 INSERT INTO workspace (name, slug, created_by) SELECT 'wake2-ws','wake2-ws', id FROM u RETURNING id::text`).
		Scan(&f.ws))
	require.NoError(t, pool.QueryRow(f.ctx,
		`INSERT INTO agent (workspace_id, name) VALUES ($1,'wake2-agent') RETURNING id::text`, f.ws).
		Scan(&f.agent))
	_, err = pool.Exec(f.ctx,
		`INSERT INTO work_item_status (workspace_id, key, name, category) VALUES ($1,'done','Done','done')`, f.ws)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(f.ctx,
		`INSERT INTO work_item (workspace_id, number, title, status_key, creator_id, assignee_type, assignee_id)
		 VALUES ($1,1,'wake2-parent','todo',gen_random_uuid(),'agent',$2) RETURNING id::text`, f.ws, f.agent).
		Scan(&f.parent))
	require.NoError(t, pool.QueryRow(f.ctx,
		`INSERT INTO work_item (workspace_id, number, title, status_key, creator_id, parent_work_item_id)
		 VALUES ($1,2,'wake2-child','todo',gen_random_uuid(),$2) RETURNING id::text`, f.ws, f.parent).
		Scan(&f.child))

	// 与 wiringAutomation 完全一致的装配：真 repo + 真 wakeupEnqueuer + 真 factsReader。
	enq := wakeupEnqueuer{repo: orchpgrepo.New(db)}
	f.svc = autopapp.NewWakeupService(autoppgrepo.New(db), enq, factsReader{db: db, wRepo: &workpgrepo.Repo{DB: db}})
	f.svc.SetUow(db.Within)
	return f
}

func (f *wakeChainFixture) taskAttribution(t *testing.T, taskID string) (orig, acct interface{}, source string) {
	t.Helper()
	require.NoError(t, f.pool.QueryRow(f.ctx, `
SELECT originator_user_id::text, accountable_user_id::text, originator_source
FROM agent_task_queue WHERE id=$1::uuid`, taskID).Scan(&orig, &acct, &source))
	return
}

// 主链：播种（created_by NULL）→ 子工单未完成不 fire → 子工单终态 → fire 成功，
// 任务落库 originator NULL + unattributed，wakeup 状态推进。
func TestSystemWakeup_FireChain_NullOriginator_DB(t *testing.T) {
	f := newWakeChainFixture(t)

	// 1) 播种：created_by 有意不写、落库为 NULL（W2-2 裁决）。
	w, err := f.svc.EnsureChildDoneRule(f.ctx, f.ws, f.parent)
	require.NoError(t, err)
	require.Empty(t, w.CreatedBy)
	var createdBy interface{}
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT created_by::text FROM work_item_wakeup WHERE id=$1::uuid`, w.ID).Scan(&createdBy))
	require.Nil(t, createdBy, "system wakeup created_by must land as NULL (platform-seeded, unattributed)")

	// 播种幂等：重复 Ensure 命中 ON CONFLICT，不重建。
	again, err := f.svc.EnsureChildDoneRule(f.ctx, f.ws, f.parent)
	require.NoError(t, err)
	require.Equal(t, w.ID, again.ID)

	// 2) 子工单未终态：不 fire、不留任务、不报错。
	require.NoError(t, f.svc.NotifyChildrenChanged(f.ctx, f.ws, f.child))
	var tasks int
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM agent_task_queue WHERE workspace_id=$1::uuid`, f.ws).Scan(&tasks))
	require.Zero(t, tasks, "children not terminal yet: no task may be enqueued")

	// 3) 子工单进终态 → fire 链走通（修复前此处 23503 回滚整个事务）。
	_, err = f.pool.Exec(f.ctx,
		`UPDATE work_item SET status_key='done' WHERE id=$1::uuid`, f.child)
	require.NoError(t, err)
	require.NoError(t, f.svc.NotifyChildrenChanged(f.ctx, f.ws, f.child))

	// 4) 任务落库且归属为"系统发起"：originator/accountable NULL + unattributed。
	var taskID string
	require.NoError(t, f.pool.QueryRow(f.ctx, `
SELECT id::text FROM agent_task_queue
WHERE workspace_id=$1::uuid AND context->>'wakeup_id'=$2`, f.ws, w.ID).Scan(&taskID),
		"fire must enqueue an agent task (pre-fix: FK 23503 rolled back the whole tx)")
	orig, acct, source := f.taskAttribution(t, taskID)
	require.Nil(t, orig, "originator must be NULL for system-initiated tasks (no zero-UUID sentinel)")
	require.Nil(t, acct, "accountable must be NULL alongside (InsertTask binds both to the same param)")
	require.Equal(t, "unattributed", source)

	// 5) fire 推进：last_task_id/revision/condition_state 落定。
	// 注意：fire_count 不能当真实 fire 计数——审计 P2-1（独立既有缺陷，非本波
	// 范围）的 advanceCondition 未命中路径也会空写 FireWakeup 使 fire_count+1
	// （步骤 2 的"未终态不 fire"正是一次这样的空写）。真实 fire 以 last_task_id
	// 落定为准。
	var fireCount int
	var revision int64
	var lastTask *string
	var condState *string
	require.NoError(t, f.pool.QueryRow(f.ctx, `
SELECT fire_count, revision, last_task_id::text, condition_state
FROM work_item_wakeup WHERE id=$1::uuid`, w.ID).Scan(&fireCount, &revision, &lastTask, &condState))
	require.GreaterOrEqual(t, fireCount, 1)
	require.NotNil(t, lastTask)
	require.Equal(t, taskID, *lastTask)
	require.NotNil(t, condState)
	require.Equal(t, "all", *condState, "children-done fingerprint must persist as the edge state")
	require.Greater(t, revision, w.Revision, "revision must advance (optimistic-lock CAS succeeded)")

	// 6) 无重试洪水（P0-2 症状锁）：边沿已消费，重复评估不得再产生新任务。
	require.NoError(t, f.svc.NotifyChildrenChanged(f.ctx, f.ws, f.child))
	var taskCount int
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM agent_task_queue WHERE context->>'wakeup_id'=$1`, w.ID).
		Scan(&taskCount))
	require.Equal(t, 1, taskCount, "edge consumed: re-evaluation must not enqueue again (pre-fix: every tick retried the failed enqueue)")
}

// 对照组：有主唤醒（API 路径形态，created_by=真实用户）fire 后归属不回归。
func TestWakeup_OwnedWakeup_KeepsTriggerOwnerAttribution_DB(t *testing.T) {
	f := newWakeChainFixture(t)

	var uid string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT id::text FROM "user" WHERE email='wake2@test.dev'`).Scan(&uid))

	w, err := f.svc.Create(f.ctx, autopapp.Wakeup{
		WorkspaceID: f.ws, WorkItemID: f.parent, AgentID: f.agent, Kind: domain.WakeupTime,
		Instruction: "owned wakeup", IntervalSeconds: 3600, CreatedBy: uid})
	require.NoError(t, err, "created_by present: must pass the service 400 gate")
	// due 手工拨到过去，等 TickOnce 认领。
	_, err = f.pool.Exec(f.ctx,
		`UPDATE work_item_wakeup SET next_fire_at = now() - interval '1 minute' WHERE id=$1::uuid`, w.ID)
	require.NoError(t, err)

	n, err := f.svc.TickOnce(f.ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 1)

	var taskID string
	require.NoError(t, f.pool.QueryRow(f.ctx, `
SELECT id::text FROM agent_task_queue
WHERE workspace_id=$1::uuid AND context->>'wakeup_id'=$2`, f.ws, w.ID).Scan(&taskID))
	orig, _, source := f.taskAttribution(t, taskID)
	require.NotNil(t, orig)
	require.Equal(t, uid, orig, "originator must stay the real creator")
	require.Equal(t, "trigger_owner", source)
}

// 遗留形态回归：created_by 为 NULL 的 time 唤醒（历史行/手工行）due 后必须能 fire，
// next_fire_at 推进（FP-13 P0-2 的"永不推进"症状不复存在）。
func TestWakeup_LegacyNullCreatedBy_TimeKind_FiresAndAdvances_DB(t *testing.T) {
	f := newWakeChainFixture(t)

	var wid string
	var revBefore int64
	require.NoError(t, f.pool.QueryRow(f.ctx, `
INSERT INTO work_item_wakeup (workspace_id, work_item_id, agent_id, created_by, kind, instruction,
  interval_seconds, next_fire_at, enabled)
VALUES ($1,$2,$3,NULL,'time','legacy null-originator wakeup',3600, now() - interval '1 minute', true)
RETURNING id::text, revision`, f.ws, f.parent, f.agent).Scan(&wid, &revBefore))

	n, err := f.svc.TickOnce(f.ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 1)

	var taskID string
	require.NoError(t, f.pool.QueryRow(f.ctx, `
SELECT id::text FROM agent_task_queue
WHERE workspace_id=$1::uuid AND context->>'wakeup_id'=$2`, f.ws, wid).Scan(&taskID))
	orig, _, source := f.taskAttribution(t, taskID)
	require.Nil(t, orig, "legacy NULL created_by must enqueue as unattributed, not zero-UUID")
	require.Equal(t, "unattributed", source)

	var fireCount int
	var nextFire time.Time
	require.NoError(t, f.pool.QueryRow(f.ctx, `
SELECT fire_count, next_fire_at FROM work_item_wakeup WHERE id=$1::uuid`, wid).Scan(&fireCount, &nextFire))
	require.Equal(t, 1, fireCount)
	require.True(t, time.Until(nextFire) > 55*time.Minute,
		"next_fire_at must advance by the interval (pre-fix it froze at the due moment and retried every tick)")
}
