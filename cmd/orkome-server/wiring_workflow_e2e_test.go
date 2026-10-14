package main

// W3 真链路 E2E（机制层，非真模型；TEST_DATABASE_URL 门禁同款 skip 模式）。
//
// 与 W2 集成测试（internal/work/adapters/pgrepo/workflow_engine_integration_
// test.go 直调 TryAdvance）的分工：本测试补 **eventbus 事件驱动路径的装配
// 证据**——
//
//	建 workspace/user/project/agent×2/workflow（2 阶段 agent1→agent2，出口态
//	用内置种子态 in_progress/done）→ Start → 断言第一棒任务落库且 context
//	逐字段 == 契约 §5 → 手动置任务 completed + 工单 status=出口态 →
//	**bus.Emit(work_item.updated{WorkItemPayload})**（真实 eventbus +
//	真实 wiringWorkflow 双门铃订阅，relay.DecodePayload 解码产物同型）→
//	断言 stage 2 光标 + agent2 新任务 + assignee 投影。
//
// 派发侧用 W2 集成 Test 1 锚定的"§5 context 组装"SQL 落 agent_task_queue
//（wiring agent 分支的生产实现经 EnqueueAssignment，context 包装缺口见
// docs/wf-w3-engine-notes.md §装配缺口上报）。

import (
	"context"
	"encoding/json"
	"log/slog"
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

	"orkome/internal/contract/events"
	// engine 包 import 激活 modelmeta 注册表（init 注册 "openai" factory）——
	// 生产 dispatcher 的模型配置校验（复刻 EnqueueAssignment 语义）要求
	// internal agent 的 runtime_config 有合法 provider/model。
	_ "orkome/internal/orchestration/adapters/engine"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
	"orkome/migrations"
)

const (
	e2eAgent1ID = "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e7d"
	e2eAgent2ID = "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e7e"
)

type wfE2EFixture struct {
	pool *pgxpool.Pool
	db   *pg.DB
	bus  *eventbus.Bus
	wf   *workapp.WorkflowService
	ctx  context.Context

	ws, uid, wi, agent1, agent2 string
}

func newWfE2EFixture(t *testing.T) *wfE2EFixture {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_w3e2e")
	_ = admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	pool, err := pgxpool.New(context.Background(), strings.Replace(base, "/"+cfg.Database, "/orkome_w3e2e", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	ctx := context.Background()
	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(ctx, 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)
	// 逐表 TRUNCATE（只列本链涉及的表；硬引用 CASCADE 自动带走弱依赖行）。
	_, err = pool.Exec(ctx, `
TRUNCATE agent_task_queue, notification, work_item_workflow_state,
	workflow_transition_log, workflow_stage, project_workflow,
	work_item_wakeup_receipt, work_item_wakeup, system_wakeup_setting,
	work_item_status, work_item, project, agent, workspace, "user"
	RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	f := &wfE2EFixture{pool: pool, db: pg.WrapPool(pool), bus: eventbus.New(slog.Default()), ctx: ctx}
	f.uid = mustQuery(t, pool, `INSERT INTO "user" (email, display_name) VALUES ('w3e2e@test.dev','W3') RETURNING id::text`)
	f.ws = mustQuery(t, pool, `INSERT INTO workspace (name, slug, created_by) VALUES ('w3e2e-ws','w3e2e-ws',$1) RETURNING id::text`, f.uid)
	f.agent1 = mustQuery(t, pool, `INSERT INTO agent (id, workspace_id, name, runtime_config) VALUES ($1,$2,'w3-agent-1','{"provider":"openai","model":"gpt-4o-mini"}'::jsonb) RETURNING id::text`, e2eAgent1ID, f.ws)
	f.agent2 = mustQuery(t, pool, `INSERT INTO agent (id, workspace_id, name, runtime_config) VALUES ($1,$2,'w3-agent-2','{"provider":"openai","model":"gpt-4o-mini"}'::jsonb) RETURNING id::text`, e2eAgent2ID, f.ws)
	require.NoError(t, (&pgrepo.Repo{DB: f.db}).EnsureStatusSeed(ctx, f.ws))

	// 项目 + 工单挂项目。
	var pid string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO project (workspace_id, name) VALUES ($1,'w3-e2e') RETURNING id::text`, f.ws).Scan(&pid))
	f.wi = mustQuery(t, pool, `INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id, project_id)
VALUES ($1,1,'w3-e2e 工单','todo','user',$2,$3) RETURNING id::text`, f.ws, f.uid, pid)

	// WorkflowService（生产构造：emit=bus、outbox/uow 与 wiring_work.go 同款）。
	wRepo := &pgrepo.Repo{DB: f.db}
	f.wf = workapp.NewWorkflowService(wRepo, f.bus)
	f.wf.SetOutbox(wfE2EOutbox{}, f.db.Within)

	// 定义面经 Upsert 真路径（exit_status 校验/排序全走生产代码）。
	_, err = f.wf.Upsert(ctx, f.ws, pid, workapp.WorkflowUpsertInput{
		Name: "两阶段", TriggerMode: "manual", EntryStagePosition: 1,
		Stages: []workapp.WorkflowStageInput{
			{Position: 1, Name: "阶段一", ExitStatusKey: "in_progress", ExecutorType: "agent", ExecutorID: f.agent1, Instruction: "做第一阶段"},
			{Position: 2, Name: "阶段二", ExitStatusKey: "done", ExecutorType: "agent", ExecutorID: f.agent2, Instruction: "做第二阶段"},
		},
	}, f.uid)
	require.NoError(t, err)

	// —— 复刻 wiringWorkflow 的装配（生产同构）——
	orepo := orchpgrepo.New(f.db)
	enq := orchapp.NewEnqueueService(orepo, f.bus)
	enq.SetOutbox(wfE2EOutbox{}, f.db.Within)
	workapp.SetStageTaskTerminal(func(ctx context.Context, ws, workItemID, stageID string) (bool, error) {
		var exists bool
		err := f.db.Tx(ctx).QueryRow(ctx, `
SELECT EXISTS(
  SELECT 1 FROM agent_task_queue
  WHERE work_item_id=$1::uuid AND context->>'stage_id'=$2
    AND status IN ('completed','failed','cancelled')
)`, workItemID, stageID).Scan(&exists)
		return exists, err
	})
	t.Cleanup(func() {
		workapp.SetDispatcher(nil)
		workapp.SetStageTaskTerminal(nil)
	})

	// wiringWorkflow 一行装配（与 main.go:92 同款；真实 eventbus 挂双门铃）。
	// 注意：wiringWorkflow 内部自带 SetDispatcher（会覆盖任何先前注入）。
	// ★ 生产派发路径原样受测：W3 装配缺口（EnqueueAssignment context 硬编码
	// trigger=assignment 丢 §5 stage 字段）已在 wiring_workflow.go 修复——agent
	// 分支直插 InsertTask 带 §5 context。本测试因此成为缺口的第一消费者：
	// 下方断言（context 逐字段 == §5）直接锚定生产 dispatcher 的输出。
	wiringWorkflow(slog.Default(), f.db, f.bus, f.wf, enq)

	return f
}

func mustQuery(t *testing.T, pool *pgxpool.Pool, q string, args ...any) string {
	t.Helper()
	var out string
	require.NoError(t, pool.QueryRow(context.Background(), q, args...).Scan(&out))
	return out
}

// wfE2EOutbox outbox 假体：main.go 生产是 event_outbox 表 + relay 重放回
// bus.Emit；本测试的 WorkflowService/EnqueueService 直接以 bus 为 Emitter
//（emitCommitted 出站口），relay 只是 bus.Emit 的重试投递器——监听器消费
// 的 Envelope/Payload 形状一致，事件驱动路径等价。
type wfE2EOutbox struct{}

func (wfE2EOutbox) Append(context.Context, ...events.Envelope) error { return nil }
func (wfE2EOutbox) MarkPublished(context.Context, ...string) error   { return nil }

// —— E2E 主链：Start → 第一棒落库(§5) → 终态+出口态 → bus.Emit 推进 → 第二棒 ——
func TestWorkflowE2E(t *testing.T) {
	f := newWfE2EFixture(t)
	ctx := f.ctx

	// 1) Start：state 建立 + 第一棒任务落库。
	st, err := f.wf.Start(ctx, f.ws, f.wi, f.uid)
	require.NoError(t, err)
	require.NotNil(t, st)
	require.Equal(t, 1, st.StagePosition)
	require.Equal(t, "阶段一", st.StageName)

	// 第一棒任务：agent1、queued、context 逐字段 == 契约 §5。
	var trigger, wfID, stageID, stageName, exitKey, instr, title string
	var pos int
	require.NoError(t, f.pool.QueryRow(ctx, `
SELECT context->>'trigger', context->>'workflow_id', context->>'stage_id',
       context->>'stage_name', (context->>'stage_position')::int,
       context->>'stage_exit_status', context->>'instruction', context->>'work_item_title'
FROM agent_task_queue WHERE work_item_id=$1::uuid AND status='queued'`, f.wi).Scan(
		&trigger, &wfID, &stageID, &stageName, &pos, &exitKey, &instr, &title))
	require.Equal(t, "workflow_stage", trigger)
	require.Equal(t, st.WorkflowID, wfID)
	require.Equal(t, st.StageID, stageID)
	require.Equal(t, "阶段一", stageName)
	require.Equal(t, 1, pos)
	require.Equal(t, "in_progress", exitKey, "stage 1 exit = in_progress（内置种子态）")
	require.Equal(t, "做第一阶段", instr)
	require.Equal(t, "w3-e2e 工单", title)

	// assignee 投影到第一棒。
	var atype, aid string
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT assignee_type, coalesce(assignee_id::text,'') FROM work_item WHERE id=$1::uuid`, f.wi).Scan(&atype, &aid))
	require.Equal(t, "agent", atype)
	require.Equal(t, f.agent1, aid)

	// relay 解码面证据：decodeOutboxPayload 对 work_item.updated 出
	// WorkItemPayload（非 nil）——双门铃 p.ID 消费面的端到端前提（§4.1 注记）。
	decoded := decodeOutboxPayload(events.WorkItemUpdated, mustJSON(t,
		events.WorkItemPayload{ID: f.wi, WorkspaceID: f.ws}))
	dp, ok := decoded.(events.WorkItemPayload)
	require.True(t, ok, "decodeOutboxPayload must yield WorkItemPayload for work_item.updated")
	require.Equal(t, f.wi, dp.ID)

	// 2) 执行者干完：任务置 completed（真实 SQL）。
	require.NoError(t, f.pool.QueryRow(ctx, `
UPDATE agent_task_queue SET status='completed', completed_at=now()
WHERE work_item_id=$1::uuid AND context->>'stage_id'=$2 RETURNING id::text`,
		f.wi, st.StageID).Scan(new(string)))
	// 工单打到出口态（执行者 update_work_item_status / 人工拖单的等价动作）。
	_, err = f.pool.Exec(ctx, `UPDATE work_item SET status_key='in_progress' WHERE id=$1::uuid`, f.wi)
	require.NoError(t, err)

	// 3) **bus.Emit 真实事件**（wiringWorkflow 双门铃裁决；W2 直调 TryAdvance
	// 之外的事件驱动路径证据）。
	f.bus.Emit(ctx, events.Envelope{
		EventID: "e2e-manual-1", Event: events.WorkItemUpdated, WorkspaceID: f.ws,
		OccurredAt: time.Now(), Payload: events.WorkItemPayload{ID: f.wi, WorkspaceID: f.ws, Status: "in_progress"},
	})

	// 4) 断言：stage 2 光标 + agent2 新任务 + assignee 投影。
	st2, err := f.wf.StateOf(ctx, f.ws, f.wi)
	require.NoError(t, err)
	require.NotNil(t, st2)
	require.Equal(t, 2, st2.StagePosition)
	require.Equal(t, "阶段二", st2.StageName)
	require.Equal(t, 1, st2.AdvanceCount)
	require.Nil(t, st2.FinishedAt)

	var agent2 string
	require.NoError(t, f.pool.QueryRow(ctx, `
SELECT agent_id::text FROM agent_task_queue
WHERE work_item_id=$1::uuid AND status='queued'`, f.wi).Scan(&agent2))
	require.Equal(t, f.agent2, agent2, "stage-2 task enqueued for agent2 via eventbus advance")

	// agent2 任务的 context 同为 §5 形状（stage_position=2）。
	var pos2 int
	var trig2 string
	require.NoError(t, f.pool.QueryRow(ctx, `
SELECT (context->>'stage_position')::int, context->>'trigger' FROM agent_task_queue
WHERE work_item_id=$1::uuid AND status='queued'`, f.wi).Scan(&pos2, &trig2))
	require.Equal(t, 2, pos2)
	require.Equal(t, "workflow_stage", trig2)

	// assignee 投影换人（advance 事务内投影）。
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT assignee_type, coalesce(assignee_id::text,'') FROM work_item WHERE id=$1::uuid`, f.wi).Scan(&atype, &aid))
	require.Equal(t, "agent", atype)
	require.Equal(t, f.agent2, aid)

	// 工单 status 不被流程改写（推进的因不是果，设计 §3.2）。
	var statusNow string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status_key FROM work_item WHERE id=$1::uuid`, f.wi).Scan(&statusNow))
	require.Equal(t, "in_progress", statusNow)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
