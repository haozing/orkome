package pgrepo

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/work/app"
	"orkome/internal/work/domain"
)

// Project Workflow 定义面集成测试（W1；docs/wf-interfaces-frozen.md §1/§3）。
// 复用 batchEnv 建库 orkome_m85（TEST_DATABASE_URL 存在才跑，跳过模式同
// batch_integration_test.go）。

const wfTestAgentID = "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e6f"

func wfTestProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ws string) string {
	t.Helper()
	var pid string
	err := pool.QueryRow(ctx, `INSERT INTO project (workspace_id, name) VALUES ($1,$2) RETURNING id::text`,
		ws, "wf-proj").Scan(&pid)
	require.NoError(t, err)
	return pid
}

func wfUpsertIn() app.WorkflowUpsertInput {
	return app.WorkflowUpsertInput{
		Name: "主流程", TriggerMode: "auto", EntryStagePosition: 2,
		Stages: []app.WorkflowStageInput{
			{Position: 2, Name: "实现", ExitStatusKey: "done",
				ExecutorType: "agent", ExecutorID: wfTestAgentID, Instruction: "实现它"},
			{Position: 1, Name: "需求澄清", ExitStatusKey: "in_review", ExecutorType: "none"},
		},
	}
}

func TestWorkflowDefinitionLifecycle(t *testing.T) {
	db, pool, ws, wi, uid := batchEnv(t)
	_ = db
	ctx := context.Background()
	r := &Repo{DB: db}
	svc := app.NewWorkflowService(r, nil)
	pid := wfTestProject(t, ctx, pool, ws)

	// 内置 status 种子由 ListStatuses 前的 EnsureStatusSeed 兜底——这里直接补种。
	require.NoError(t, r.EnsureStatusSeed(ctx, ws))

	// 1) 建流程（stages 乱序传入 → 排序落库）。
	def, err := svc.Upsert(ctx, ws, pid, wfUpsertIn(), uid)
	require.NoError(t, err)
	require.NotNil(t, def)
	require.True(t, def.Enabled)
	require.Len(t, def.Stages, 2)
	require.Equal(t, 1, def.Stages[0].Position, "stages returned ascending")
	require.Equal(t, "需求澄清", def.Stages[0].Name)
	require.Equal(t, "", def.Stages[0].ExecutorID, "none stage has empty executor_id")
	require.Equal(t, "实现", def.Stages[1].Name)
	require.Equal(t, wfTestAgentID, def.Stages[1].ExecutorID)
	require.NotEmpty(t, def.Stages[0].ID)

	// 2) 读回一致。
	got, err := svc.Get(ctx, ws, pid)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, def, got, "Get returns the same definition shape")

	// 3) exit_status_key 不存在 → 400 形状错误（Validation）。
	bad := wfUpsertIn()
	bad.Stages[1].ExitStatusKey = "nonexistent"
	_, err = svc.Upsert(ctx, ws, pid, bad, uid)
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, apierr.Validation, ae.Code)

	// closed 类 status 不能作出口。
	bad2 := wfUpsertIn()
	bad2.Stages[1].ExitStatusKey = "cancelled"
	_, err = svc.Upsert(ctx, ws, pid, bad2, uid)
	require.ErrorAs(t, err, &ae)
	require.Equal(t, apierr.Validation, ae.Code)

	// 4) 1:1 约束：同项目第二次 Upsert 是替换（仍是 1 个 workflow、stages 覆盖）。
	up2 := wfUpsertIn()
	up2.Name = "改版流程"
	up2.Stages = []app.WorkflowStageInput{
		{Position: 1, Name: "只验一次", ExitStatusKey: "done", ExecutorType: "none"},
	}
	up2.EntryStagePosition = 1
	def2, err := svc.Upsert(ctx, ws, pid, up2, uid)
	require.NoError(t, err)
	require.Equal(t, 1, len(def2.Stages))
	var nWF, nStages int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM project_workflow WHERE project_id=$1`, pid).Scan(&nWF))
	require.Equal(t, 1, nWF, "1:1: second upsert replaces, never duplicates")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM workflow_stage WHERE workflow_id=$1`, def2.ID).Scan(&nStages))
	require.Equal(t, 1, nStages, "stages fully replaced")

	// 5) 建在途 state → Delete 后 state 落 detached（finished_at 非空）。
	var stageID string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM workflow_stage WHERE workflow_id=$1 LIMIT 1`, def2.ID).Scan(&stageID))
	var stateID string
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO work_item_workflow_state (workspace_id, work_item_id, workflow_id, stage_id)
VALUES ($1,$2,$3,$4) RETURNING id::text`, ws, wi, def2.ID, stageID).Scan(&stateID))

	require.NoError(t, svc.Delete(ctx, ws, pid, uid))
	var finishedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT finished_at FROM work_item_workflow_state WHERE id=$1`, stateID).Scan(&finishedAt))
	require.NotNil(t, finishedAt, "in-flight state detached (finished_at set) on definition delete")

	gone, err := svc.Get(ctx, ws, pid)
	require.NoError(t, err)
	require.Nil(t, gone, "definition gone after delete")

	// 6) SetEnabled 翻转（先重建定义）。
	def3, err := svc.Upsert(ctx, ws, pid, up2, uid)
	require.NoError(t, err)
	require.True(t, def3.Enabled)
	require.NoError(t, svc.SetEnabled(ctx, ws, pid, false, uid))
	got3, err := svc.Get(ctx, ws, pid)
	require.NoError(t, err)
	require.False(t, got3.Enabled)
	require.NoError(t, svc.SetEnabled(ctx, ws, pid, true, uid))
	got3, err = svc.Get(ctx, ws, pid)
	require.NoError(t, err)
	require.True(t, got3.Enabled)
}

func TestWorkflowUpsert_WorkItemProjectFence(t *testing.T) {
	db, pool, ws, _, uid := batchEnv(t)
	ctx := context.Background()
	r := &Repo{DB: db}
	svc := app.NewWorkflowService(r, nil)
	pid := wfTestProject(t, ctx, pool, ws)
	require.NoError(t, r.EnsureStatusSeed(ctx, ws))

	// 不存在的项目 → NotFound（service 层 GetProject 前置）。
	_, err := svc.Upsert(ctx, ws, "00000000-0000-0000-0000-0000000000ff", wfUpsertIn(), uid)
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, apierr.NotFound, ae.Code)

	// 跨 workspace 隔离：另一 workspace 的项目不可见。
	_, _ = pool.Exec(ctx, `DELETE FROM project_workflow WHERE workspace_id=$1`, ws)
	var ws2 string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO workspace (slug, name, created_by) VALUES ('wf-ws2','B2',$1) RETURNING id::text`, uid).Scan(&ws2))
	_, err = svc.Upsert(ctx, ws2, pid, wfUpsertIn(), uid)
	require.ErrorAs(t, err, &ae)
	require.Equal(t, apierr.NotFound, ae.Code)

	// domain 校验断档拒绝。
	bad := wfUpsertIn()
	bad.Stages = []app.WorkflowStageInput{
		{Position: 1, Name: "a", ExitStatusKey: "done", ExecutorType: "none"},
		{Position: 3, Name: "b", ExitStatusKey: "done", ExecutorType: "none"},
	}
	_, err = svc.Upsert(ctx, ws, pid, bad, uid)
	require.ErrorAs(t, err, &ae)
	require.Equal(t, apierr.Validation, ae.Code)
	require.Contains(t, ae.Details[0].Problem, "contiguous")

	// SortWorkflowStages 是保存前规范化的一部分（domain 面冒烟）。
	defs := []domain.WorkflowStageDef{{Position: 2}, {Position: 1}}
	domain.SortWorkflowStages(defs)
	require.Equal(t, 1, defs[0].Position)
}
