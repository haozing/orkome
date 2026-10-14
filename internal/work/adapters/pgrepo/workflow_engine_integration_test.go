package pgrepo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/contract/events"
	"orkome/internal/work/app"
	"orkome/internal/work/domain"
)

// W2 推进引擎集成测试（真 DB；docs/wf-interfaces-frozen.md §2 + 设计 §2/§4/§5
// 覆盖矩阵）。复用 batchEnv（建库 orkome_m85，TEST_DATABASE_URL 才跑）。
// 覆盖矩阵每条一个 Test 函数，命名 TestWorkflowEngine_*；任务书条目编号见各
// 函数头注释。派发走 wiring 同构 SQL 落 agent_task_queue（跨域读/写在 cmd 层
// 的接缝在本包以等价注入复现），coalesced/already_active 分流在 service 单测
// 覆盖（任务书 E-13）。

// wfEngineEnv 每用例独立 workspace + 项目 + 工单 + 两 agent + 两阶段流程
//（s1: in_review/agent1 → s2: done/agent2）。
type wfEngineEnv struct {
	pool   *pgxpool.Pool
	ws     string
	wi     string
	uid    string
	agent1 string
	agent2 string
	svc    *app.WorkflowService
	repo   *Repo
	disp   *wfFakeDisp
}

// wfFakeDisp 集成侧派发假体：不真正入队（断言落库任务的用例手工 recTask）。
type wfFakeDisp struct{ calls int }

func (d *wfFakeDisp) dispatch(context.Context, string, string, app.StageExecInfo) error {
	d.calls++
	return nil
}

const wfAgent1ID = "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e6f"
const wfAgent2ID = "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e6e"

func wfEngineEnvOf(t *testing.T) *wfEngineEnv {
	t.Helper()
	db, pool, ws, wi, uid := batchEnv(t)
	ctx := context.Background()
	r := &Repo{DB: db}
	require.NoError(t, r.EnsureStatusSeed(ctx, ws))

	// 两个真 agent（agent_task_queue.agent_id FK 需要）。
	for _, a := range []struct{ id, name string }{{wfAgent1ID, "wf-agent-1"}, {wfAgent2ID, "wf-agent-2"}} {
		_, err := pool.Exec(ctx, `
INSERT INTO agent (id, workspace_id, name, runtime_config) VALUES ($1,$2,$3,'{}'::jsonb)
ON CONFLICT (id) DO NOTHING`, a.id, ws, a.name)
		require.NoError(t, err)
	}

	// 项目 + 工单挂项目。
	var pid string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, name) VALUES ($1,'wf-engine') RETURNING id::text`, ws).Scan(&pid))
	_, err := pool.Exec(ctx,
		`UPDATE work_item SET project_id=$2::uuid WHERE id=$1::uuid AND workspace_id=$3`, wi, pid, ws)
	require.NoError(t, err)
	// 流程定义（经 service Upsert：校验+排序全走真路径）。
	def, err := r.UpsertWorkflow(ctx, ws, pid, app.WorkflowUpsert{
		Name: "两阶段", TriggerMode: "manual", EntryStagePosition: 1, CreatedBy: uid,
		Stages: []domain.WorkflowStageDef{
			{Position: 1, Name: "阶段一", ExitStatusKey: "in_review", ExecutorType: "agent", ExecutorID: wfAgent1ID, Instruction: "做第一阶段 {{title}}"},
			{Position: 2, Name: "阶段二", ExitStatusKey: "done", ExecutorType: "agent", ExecutorID: wfAgent2ID, Instruction: "做第二阶段"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, def)

	d := &wfFakeDisp{}
	app.SetDispatcher(d.dispatch)
	app.SetStageTaskTerminal(func(ctx context.Context, ws, wiID, stageID string) (bool, error) {
		// wiring 同构谓词（真 SQL：context->>'stage_id' 命中且任务终态）。
		var exists bool
		err := pool.QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM agent_task_queue
  WHERE work_item_id=$1::uuid AND context->>'stage_id'=$2
    AND status IN ('completed','failed','cancelled'))`, wiID, stageID).Scan(&exists)
		return exists, err
	})
	t.Cleanup(func() {
		app.SetDispatcher(nil)
		app.SetStageTaskTerminal(nil)
	})

	svc := app.NewWorkflowService(r, nil)
	return &wfEngineEnv{pool: pool, ws: ws, wi: wi, uid: uid, agent1: wfAgent1ID, agent2: wfAgent2ID,
		svc: svc, repo: r, disp: d}
}

// statusOf 读工单现 status。
func (e *wfEngineEnv) statusOf(t *testing.T, ctx context.Context) string {
	t.Helper()
	var st string
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT status_key FROM work_item WHERE id=$1::uuid`, e.wi).Scan(&st))
	return st
}

// setStatus 直改工单 status（模拟执行者 update_work_item_status / 人工拖单）。
func (e *wfEngineEnv) setStatus(t *testing.T, ctx context.Context, key string) {
	t.Helper()
	_, err := e.pool.Exec(ctx,
		`UPDATE work_item SET status_key=$2 WHERE id=$1::uuid`, e.wi, key)
	require.NoError(t, err)
}

// recStageTask 落一个带 stage_id context 的阶段任务（模拟派发产物）。
func (e *wfEngineEnv) recStageTask(t *testing.T, ctx context.Context, agentID, stageID, status string) string {
	t.Helper()
	var id string
	require.NoError(t, e.pool.QueryRow(ctx, `
INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status, max_attempts, context,
  originator_user_id, accountable_user_id, originator_source, trigger_evidence_kind,
  completed_at)
VALUES ($1,$2,$3::uuid,$4,2, jsonb_build_object('trigger','workflow_stage','stage_id',$5::text),
  $6,$6,'direct_human','assignment', CASE WHEN $4 IN ('completed','failed','cancelled') THEN now() END)
RETURNING id::text`,
		e.ws, agentID, e.wi, status, stageID, e.uid).Scan(&id))
	return id
}

// countStageTasks 统计带 stage_id context 的任务数。
func (e *wfEngineEnv) countStageTasks(t *testing.T, ctx context.Context, stageID string) int {
	t.Helper()
	var n int
	require.NoError(t, e.pool.QueryRow(ctx, `
SELECT count(*) FROM agent_task_queue WHERE work_item_id=$1::uuid AND context->>'stage_id'=$2`,
		e.wi, stageID).Scan(&n))
	return n
}

// logNotes 读该工单 state 的流水 notes（按时间升序）。
func (e *wfEngineEnv) logNotes(t *testing.T, ctx context.Context) []string {
	t.Helper()
	rows, err := e.pool.Query(ctx, `
SELECT l.note FROM workflow_transition_log l
JOIN work_item_workflow_state s ON s.id=l.state_id
WHERE s.work_item_id=$1::uuid ORDER BY l.created_at, l.id`, e.wi)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		out = append(out, n)
	}
	require.NoError(t, rows.Err())
	return out
}

// —— 1) Start：state 建立 + assignee 投影 + 第一棒任务落库 + transition_log 一行 ——

func TestWorkflowEngine_Start_BuildsStateProjectsAssigneeAndDispatches(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	// real dispatcher（wiring 同构 SQL）：真落 agent_task_queue。
	app.SetDispatcher(func(ctx context.Context, ws, wiID string, st app.StageExecInfo) error {
		_, err := e.pool.Exec(ctx, `
INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status, max_attempts, context,
  originator_user_id, accountable_user_id, originator_source, trigger_evidence_kind)
SELECT $1,$2,$3::uuid,'queued',2, jsonb_build_object(
  'trigger','workflow_stage','workflow_id',$4::text,'stage_id',$5::text,'stage_name',$6::text,
  'stage_position',$7::int,'stage_exit_status',$8::text,'instruction',$9::text,'work_item_title',$10::text),
  $11,$11,'direct_human','assignment'
WHERE EXISTS (SELECT 1 FROM work_item w WHERE w.id=$3::uuid AND w.workspace_id=$1)
  AND EXISTS (SELECT 1 FROM agent a WHERE a.id=$2::uuid AND a.workspace_id=$1)`,
			ws, st.ExecutorID, wiID, st.WorkflowID, st.StageID, st.StageName,
			st.StagePosition, st.ExitStatusKey, st.Instruction, "batch host", st.OriginatorUserID)
		return err
	})
	t.Cleanup(func() { app.SetDispatcher(nil) })

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	require.NotNil(t, st)
	// entry stage = position 1（阶段一，agent1 执行）。
	var s1 string
	require.NoError(t, e.pool.QueryRow(ctx, `
SELECT s.id::text FROM workflow_stage s JOIN project_workflow w ON w.id=s.workflow_id
WHERE w.project_id = (SELECT project_id FROM work_item WHERE id=$1::uuid) AND s.position=1`,
		e.wi).Scan(&s1))
	require.Equal(t, s1, st.StageID)

	// assignee 投影到第一棒。
	var atype, aid string
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT assignee_type, coalesce(assignee_id::text,'') FROM work_item WHERE id=$1::uuid`, e.wi).Scan(&atype, &aid))
	require.Equal(t, "agent", atype)
	require.Equal(t, e.agent1, aid)

	// 第一棒任务落 agent_task_queue，context.trigger=workflow_stage + §5 形状。
	var trigger, wfID, stageID, exitKey string
	var pos int
	var instr, title string
	require.NoError(t, e.pool.QueryRow(ctx, `
SELECT context->>'trigger', context->>'workflow_id', context->>'stage_id',
       context->>'stage_exit_status', (context->>'stage_position')::int,
       context->>'instruction', context->>'work_item_title'
FROM agent_task_queue WHERE work_item_id=$1::uuid AND status='queued'`, e.wi).Scan(
		&trigger, &wfID, &stageID, &exitKey, &pos, &instr, &title))
	require.Equal(t, "workflow_stage", trigger)
	require.NotEmpty(t, wfID)
	require.NotEmpty(t, stageID)
	require.Equal(t, "in_review", exitKey)
	require.Equal(t, 1, pos)
	// instruction 原样透传（{{title}} 占位符的渲染归 engine 侧 W3，不在派发层）。
	require.Equal(t, "做第一阶段 {{title}}", instr)
	require.Equal(t, "batch host", title)

	// transition_log 一行：from=NULL、actor=user/uid。
	require.Equal(t, []string{""}, e.logNotes(t, ctx))
}

// —— 2) Start 幂等：二次 Start 同一 state、无新任务 ——

func TestWorkflowEngine_Start_IdempotentSecondCall(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	first, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	n := e.countStageTasks(t, ctx, first.StageID)
	require.Equal(t, 0, n, "fake dispatcher doesn't enqueue; assert via state instead")

	second, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	require.Equal(t, first.StageID, second.StageID)
	require.Equal(t, first.WorkItemID, second.WorkItemID)
	require.Equal(t, first.EnteredAt.Unix(), second.EnteredAt.Unix(), "same state row")

	// 无第二行流水。
	require.Len(t, e.logNotes(t, ctx), 1)
}

// —— 3) 快乐路径推进：任务 completed + status 出口态 → stage 前进 + 换人 + 两行 log ——

func TestWorkflowEngine_HappyPath_AdvancesStageAndReassigns(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	s1 := st.StageID

	// 执行者跑完：任务置 completed（带 stage_id）+ 工单打到出口态。
	e.recStageTask(t, ctx, e.agent1, s1, "completed")
	e.setStatus(t, ctx, "in_review")

	st2, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.NotNil(t, st2)
	require.NotEqual(t, s1, st2.StageID, "stage advanced")
	require.Equal(t, "阶段二", st2.StageName)

	// assignee 换人。
	var atype, aid string
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT assignee_type, coalesce(assignee_id::text,'') FROM work_item WHERE id=$1::uuid`, e.wi).Scan(&atype, &aid))
	require.Equal(t, "agent", atype)
	require.Equal(t, e.agent2, aid)

	// 两行 log：start 行 + advance 行（advance 行 note 空）。
	notes := e.logNotes(t, ctx)
	require.Len(t, notes, 2)
	require.Equal(t, "", notes[1])

	// 工单 status 不被流程改写（推进的因不是果）。
	require.Equal(t, "in_review", e.statusOf(t, ctx))
}

// —— 4) 未达出口态不推进 ——

func TestWorkflowEngine_StatusNotAtExit_NoAdvance(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	e.recStageTask(t, ctx, e.agent1, st.StageID, "completed") // 任务终态了
	e.setStatus(t, ctx, "todo")                               // 但 status 没到出口态

	got, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.Nil(t, got)
}

// —— 5) 无终态任务不推进；none 执行者例外分支可推进 ——

func TestWorkflowEngine_NoTerminalTask_NoAdvance_NoneSkips(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	e.setStatus(t, ctx, "in_review") // 出口态但没有任务背书

	got, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.Nil(t, got, "exit status without terminal stage task → no advance")

	// none 阶段：无任务也能推进（人工拖单=人工背书，§4.3）。
	// 重定义流程为 none 执行者。
	var pid string
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT project_id::text FROM project_workflow WHERE id=$1::uuid`, st.WorkflowID).Scan(&pid))
	_, err = e.repo.UpsertWorkflow(ctx, e.ws, pid, app.WorkflowUpsert{
		Name: "两阶段", TriggerMode: "manual", EntryStagePosition: 1, CreatedBy: e.uid,
		Stages: []domain.WorkflowStageDef{
			{Position: 1, Name: "人工一", ExitStatusKey: "in_review", ExecutorType: "none"},
			{Position: 2, Name: "人工二", ExitStatusKey: "done", ExecutorType: "none"},
		},
	})
	require.NoError(t, err)
	// state.stage_id 已随定义替换失效（软引用孤儿）→ 冻结不推进；需重建 state。
	_, err = e.pool.Exec(ctx, `DELETE FROM work_item_workflow_state WHERE work_item_id=$1::uuid`, e.wi)
	require.NoError(t, err)
	st2, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	got2, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.NotNil(t, got2, "none stage advances on exit status alone")
	require.NotEqual(t, st2.StageID, got2.StageID)
}

// —— 6) CAS 幂等：同工单两次 TryAdvance 只成功一次 ——

func TestWorkflowEngine_CAS_IdempotentDoubleAdvance(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	e.recStageTask(t, ctx, e.agent1, st.StageID, "completed")
	e.setStatus(t, ctx, "in_review")

	first, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.NotNil(t, first)

	// 顺序模拟并发第二次：stage 已 s2、status ≠ s2 出口 → nil（光标未重复推进）。
	second, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.Nil(t, second)

	// advance_count 恰好 1。
	var ac int
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT advance_count FROM work_item_workflow_state WHERE work_item_id=$1::uuid`, e.wi).Scan(&ac))
	require.Equal(t, 1, ac)
}

// —— 7) 终阶段：最后一阶段出口态 → finished_at + note=completed，无新任务 ——

func TestWorkflowEngine_FinalStage_Completes(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	e.recStageTask(t, ctx, e.agent1, st.StageID, "completed")
	e.setStatus(t, ctx, "in_review")
	st2, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.NotNil(t, st2)

	// 第二棒跑完打到 done（终阶段出口）。
	e.recStageTask(t, ctx, e.agent2, st2.StageID, "completed")
	e.setStatus(t, ctx, "done")
	final, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.NotNil(t, final)
	require.NotNil(t, final.FinishedAt, "finished_at set")

	notes := e.logNotes(t, ctx)
	require.Len(t, notes, 3)
	require.Equal(t, "completed", notes[2])

	// 无新任务（已完成的那条是第二棒自己的结算任务，不算新派发）。
	var queued int
	require.NoError(t, e.pool.QueryRow(ctx, `
SELECT count(*) FROM agent_task_queue
WHERE work_item_id=$1::uuid AND context->>'stage_id'=$2 AND status IN ('queued','dispatched','running')`,
		e.wi, st2.StageID).Scan(&queued))
	require.Equal(t, 0, queued, "no new task dispatched after completion")
}

// —— 8) Detach 后不再推进 ——

func TestWorkflowEngine_Detach_StopsAdvancing(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	require.NoError(t, e.svc.Detach(ctx, e.ws, e.wi, e.uid))

	// detached note 双写。
	notes := e.logNotes(t, ctx)
	require.Len(t, notes, 2)
	require.Equal(t, "detached", notes[1])

	e.recStageTask(t, ctx, e.agent1, st.StageID, "completed")
	e.setStatus(t, ctx, "in_review")
	got, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.Nil(t, got, "detached → frozen")

	// 再 Detach 幂等（不重复写流水）。
	require.NoError(t, e.svc.Detach(ctx, e.ws, e.wi, e.uid))
	require.Len(t, e.logNotes(t, ctx), 2)
}

// —— 9) workflow disabled 后 TryAdvance nil ——

func TestWorkflowEngine_Disabled_NoAdvance(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	e.recStageTask(t, ctx, e.agent1, st.StageID, "completed")
	e.setStatus(t, ctx, "in_review")

	var pid string
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT project_id::text FROM project_workflow WHERE id=$1::uuid`, st.WorkflowID).Scan(&pid))
	require.NoError(t, e.svc.SetEnabled(ctx, e.ws, pid, false, e.uid))

	got, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.Nil(t, got, "disabled workflow → no advance")

	// 重新启用后同一 state 可继续推进（§4.8 逃生门语义）。
	require.NoError(t, e.svc.SetEnabled(ctx, e.ws, pid, true, e.uid))
	got, err = e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.NotNil(t, got)
}

// —— 10) dispatch_count 超限不派发 ——

func TestWorkflowEngine_DispatchCountOverLimit_NoAdvance(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	// 手工推高 dispatch_count 到 4（>3）。
	require.NoError(t, e.repo.AdjWorkflowDispatchCount(ctx, e.ws, stateIDOf(t, e, ctx), 4))

	e.recStageTask(t, ctx, e.agent1, st.StageID, "completed")
	e.setStatus(t, ctx, "in_review")
	got, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.Nil(t, got, "dispatch_count > MaxDispatchPerStage → no advance")
}

// —— 11) advance_count 超限返回可识别错误（wiring 停用路径的 service 侧）——

func TestWorkflowEngine_AdvanceCountOverLimit_RunawayError(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	// uow 接真 db.Within：TryAdvance 的 advance 是单事务，runaway 必须整体回滚。
	e.svc.SetOutbox(captureOutbox{sink: func([]events.Envelope) {}}, e.repo.DB.Within)

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	// 阶段数×3 = 6；当前 advance_count=6 → 本次推进到 7 触发熔断。
	_, err = e.pool.Exec(ctx,
		`UPDATE work_item_workflow_state SET advance_count=6 WHERE work_item_id=$1::uuid`, e.wi)
	require.NoError(t, err)

	e.recStageTask(t, ctx, e.agent1, st.StageID, "completed")
	e.setStatus(t, ctx, "in_review")

	_, err = e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.ErrorIs(t, err, app.ErrAdvanceRunaway)
	// 事务回滚：光标仍在 s1、无 advance 行。
	var stageNow string
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT stage_id::text FROM work_item_workflow_state WHERE work_item_id=$1::uuid`, e.wi).Scan(&stageNow))
	require.Equal(t, st.StageID, stageNow)
	require.Len(t, e.logNotes(t, ctx), 1, "no advance log row (rolled back)")
}

// —— 12) Restart：回 entry、计数清零、重新派发 ——

func TestWorkflowEngine_Restart_ResetsCursorAndRedispatches(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	st, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	e.recStageTask(t, ctx, e.agent1, st.StageID, "completed")
	e.setStatus(t, ctx, "in_review")
	st2, err := e.svc.TryAdvance(ctx, e.ws, e.wi)
	require.NoError(t, err)
	require.NotNil(t, st2)

	rst, err := e.svc.Restart(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	require.Equal(t, st.StageID, rst.StageID, "back to entry stage")
	require.Equal(t, 0, rst.DispatchCount)
	require.Equal(t, 0, rst.AdvanceCount)
	require.Nil(t, rst.FinishedAt)

	// DB 计数确实清零 + assignee 回第一棒。
	var dc, ac int
	var aid string
	require.NoError(t, e.pool.QueryRow(ctx, `
SELECT dispatch_count, advance_count FROM work_item_workflow_state WHERE work_item_id=$1::uuid`, e.wi).Scan(&dc, &ac))
	require.Equal(t, 0, dc)
	require.Equal(t, 0, ac)
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT coalesce(assignee_id::text,'') FROM work_item WHERE id=$1::uuid`, e.wi).Scan(&aid))
	require.Equal(t, e.agent1, aid)

	// restarted note 落流水。
	require.Equal(t, "restarted", lastNote(t, e, ctx))
}

// —— 13) coalesced 路径：already_active 分流在 service 单测覆盖（任务书 E-13：
// MergeIntoPending 的 SQL 依赖 orchestration 表、真 dispatcher 在 wiring 层）。
// 本集成面只锚定"投影 work_item.updated 事件负载"这一 Start 侧 evidence——
// main.go assignee 派发器共存语义（设计 §3.1 #2）的锚点。

func TestWorkflowEngine_StartAssigneeProjection_EmitsWorkItemUpdated(t *testing.T) {
	e := wfEngineEnvOf(t)
	ctx := context.Background()

	var envs []events.Envelope
	rec := captureOutbox{sink: func(list []events.Envelope) { envs = append(envs, list...) }}
	e.svc.SetOutbox(rec, func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })

	_, err := e.svc.Start(ctx, e.ws, e.wi, e.uid)
	require.NoError(t, err)
	require.NotEmpty(t, envs)
	last := envs[len(envs)-1]
	require.Equal(t, events.WorkItemUpdated, last.Event, "projection event is work_item.updated (assignee dispatcher consumes this)")
	p, ok := last.Payload.(events.WorkItemPayload)
	require.True(t, ok)
	require.Equal(t, "agent", p.AssigneeType)
	require.Equal(t, e.agent1, p.AssigneeID)
}

// —— helpers ——

func stateIDOf(t *testing.T, e *wfEngineEnv, ctx context.Context) string {
	t.Helper()
	var id string
	require.NoError(t, e.pool.QueryRow(ctx,
		`SELECT id::text FROM work_item_workflow_state WHERE work_item_id=$1::uuid`, e.wi).Scan(&id))
	return id
}

func lastNote(t *testing.T, e *wfEngineEnv, ctx context.Context) string {
	t.Helper()
	notes := e.logNotes(t, ctx)
	require.NotEmpty(t, notes)
	return notes[len(notes)-1]
}

// captureOutbox 适配 OutboxRecorder（sink 引用外层切片）。
type captureOutbox struct{ sink func([]events.Envelope) }

func (c captureOutbox) Append(ctx context.Context, envs ...events.Envelope) error {
	c.sink(envs)
	return nil
}
func (c captureOutbox) MarkPublished(context.Context, ...string) error { return nil }

// 编译期锚：pg / time / errors 引用防误删（集成文件共享依赖）。
var (
	_ = pg.WrapPool
	_ = time.Now
	_ = errors.New
)
