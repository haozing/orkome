package pgrepo

import (
	"context"

	"github.com/jackc/pgx/v5"

	"orkome/internal/work/app"
)

// Project Workflow 定义面（docs/wf-interfaces-frozen.md §1/§2）。
// SQL 只摸 work 域四表（project_workflow / workflow_stage /
// work_item_workflow_state / workflow_transition_log）+ 只读 project /
// work_item_status（出口态校验在 service 层经 ListStatuses），见 arch/sql_allowlist.yml。

func (r *Repo) UpsertWorkflow(ctx context.Context, wsID, projectID string, in app.WorkflowUpsert) (*app.WorkflowDefinition, error) {
	var out *app.WorkflowDefinition
	err := r.DB.Within(ctx, func(ctx context.Context) error {
		// 1) 定义头 upsert（project_id UNIQUE 兜底替换；项目存在性由
		// INSERT..SELECT 的 project 谓词保证——不命中即 not found）。
		var wfID string
		err := r.q(ctx).QueryRow(ctx, `
INSERT INTO project_workflow (workspace_id, project_id, name, trigger_mode, entry_stage_position, created_by)
SELECT $1, p.id, $3, $4, $5, $6 FROM project p WHERE p.workspace_id=$1 AND p.id=$2
ON CONFLICT (project_id) DO UPDATE
SET name = EXCLUDED.name, trigger_mode = EXCLUDED.trigger_mode,
    entry_stage_position = EXCLUDED.entry_stage_position, updated_at = now()
RETURNING id`, wsID, projectID, in.Name, in.TriggerMode, in.EntryStagePosition, in.CreatedBy).Scan(&wfID)
		if noRow(err) {
			return errProjectNotFound
		}
		if err != nil {
			return err
		}

		// 2) 整体替换 stages：删旧插新（UpsertWorkflow 服务层保证单事务）。
		if _, err := r.q(ctx).Exec(ctx,
			`DELETE FROM workflow_stage WHERE workflow_id=$1`, wfID); err != nil {
			return err
		}
		stageIDs := make(map[int]string, len(in.Stages))
		for _, d := range in.Stages {
			var execID *string
			if d.ExecutorID != "" {
				id := d.ExecutorID
				execID = &id
			}
			var stageID string
			if err := r.q(ctx).QueryRow(ctx, `
INSERT INTO workflow_stage (workspace_id, workflow_id, position, name, exit_status_key, executor_type, executor_id, instruction)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING id`, wsID, wfID, d.Position, d.Name, d.ExitStatusKey, d.ExecutorType, execID, d.Instruction).Scan(&stageID); err != nil {
				return err
			}
			stageIDs[d.Position] = stageID
		}

		// 3) 读回完整定义（含时间戳/ID，与 Get 同一形状）。
		def, err := r.getWorkflowByID(ctx, wsID, wfID, projectID)
		if err != nil {
			return err
		}
		out = def
		return nil
	})
	if err == errProjectNotFound {
		return nil, nil
	}
	return out, err
}

func (r *Repo) GetWorkflow(ctx context.Context, wsID, projectID string) (*app.WorkflowDefinition, error) {
	var wfID string
	err := r.q(ctx).QueryRow(ctx,
		`SELECT id FROM project_workflow WHERE workspace_id=$1 AND project_id=$2`,
		wsID, projectID).Scan(&wfID)
	if noRow(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.getWorkflowByID(ctx, wsID, wfID, projectID)
}

// GetWorkflowByID 按定义 ID 直读（W2 状态面：state.workflow_id 软引用回溯；
// 定义已删 → nil,nil）。projectID 回读行值（getWorkflowByID 的入参仅作展示填充）。
func (r *Repo) GetWorkflowByID(ctx context.Context, wsID, workflowID string) (*app.WorkflowDefinition, error) {
	var projectID string
	err := r.q(ctx).QueryRow(ctx,
		`SELECT coalesce(project_id::text,'') FROM project_workflow WHERE id=$1 AND workspace_id=$2`,
		workflowID, wsID).Scan(&projectID)
	if noRow(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.getWorkflowByID(ctx, wsID, workflowID, projectID)
}

func (r *Repo) getWorkflowByID(ctx context.Context, wsID, wfID, projectID string) (*app.WorkflowDefinition, error) {
	var def app.WorkflowDefinition
	var createdBy string
	err := r.q(ctx).QueryRow(ctx, `
SELECT id, project_id, name, trigger_mode, entry_stage_position, enabled, created_by, created_at, updated_at
FROM project_workflow WHERE id=$1 AND workspace_id=$2`, wfID, wsID).Scan(
		&def.ID, &def.ProjectID, &def.Name, &def.TriggerMode, &def.EntryStagePosition,
		&def.Enabled, &createdBy, &def.CreatedAt, &def.UpdatedAt)
	if err != nil {
		return nil, err
	}
	def.WorkspaceID = wsID
	def.CreatedBy = createdBy
	def.ProjectID = projectID

	rows, err := r.q(ctx).Query(ctx, `
SELECT id::text, position, name, exit_status_key, executor_type, coalesce(executor_id::text,''), instruction
FROM workflow_stage WHERE workflow_id=$1 ORDER BY position`, wfID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	def.Stages = []app.WorkflowStage{}
	for rows.Next() {
		var st app.WorkflowStage
		if err := rows.Scan(&st.ID, &st.Position, &st.Name, &st.ExitStatusKey,
			&st.ExecutorType, &st.ExecutorID, &st.Instruction); err != nil {
			return nil, err
		}
		def.Stages = append(def.Stages, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &def, nil
}

func (r *Repo) DeleteWorkflow(ctx context.Context, wsID, projectID string) error {
	// 定义删除（stages 级联）+ 该项目在途 state 全部脱离（note='detached'，
	// 光标冻结）。state 按 workflow_id 关联 project_workflow.project_id 查。
	return r.DB.Within(ctx, func(ctx context.Context) error {
		var wfID string
		err := r.q(ctx).QueryRow(ctx,
			`SELECT id FROM project_workflow WHERE workspace_id=$1 AND project_id=$2`,
			wsID, projectID).Scan(&wfID)
		if noRow(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_workflow_state s SET finished_at = now()
FROM project_workflow w
WHERE s.workflow_id = w.id AND w.id = $1 AND s.finished_at IS NULL`, wfID); err != nil {
			return err
		}
		_, err = r.q(ctx).Exec(ctx,
			`DELETE FROM project_workflow WHERE id=$1 AND workspace_id=$2`, wfID, wsID)
		return err
	})
}

func (r *Repo) SetWorkflowEnabled(ctx context.Context, wsID, projectID string, enabled bool) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE project_workflow SET enabled=$3, updated_at=now()
WHERE workspace_id=$1 AND project_id=$2`, wsID, projectID, enabled)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// errProjectNotFound UpsertWorkflow 内部哨兵：INSERT..SELECT 未命中项目行。
var errProjectNotFound = errSentinel("project not found")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// —— W2 状态面（docs/wf-interfaces-frozen.md §2 + 设计 §2/§4）。
// SQL 全部只摸 work 域四张 workflow 表（arch/sql_allowlist.yml work 段）。

const wfStateCols = `id::text, work_item_id::text, workflow_id::text, stage_id::text,
entered_at, dispatch_count, advance_count, finished_at`

func scanWorkflowState(row pgx.Row) (*app.WorkflowStateRecord, error) {
	var s app.WorkflowStateRecord
	err := row.Scan(&s.ID, &s.WorkItemID, &s.WorkflowID, &s.StageID,
		&s.EnteredAt, &s.DispatchCount, &s.AdvanceCount, &s.FinishedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetWorkflowState 读工单的推进光标（无则 nil,nil）。
func (r *Repo) GetWorkflowState(ctx context.Context, wsID, workItemID string) (*app.WorkflowStateRecord, error) {
	row, err := scanWorkflowState(r.q(ctx).QueryRow(ctx, `
SELECT `+wfStateCols+` FROM work_item_workflow_state
WHERE workspace_id=$1 AND work_item_id=$2::uuid`, wsID, workItemID))
	if noRow(err) {
		return nil, nil
	}
	return row, err
}

// InsertWorkflowState 建光标；UNIQUE(work_item_id) 兜底并发 Start
//（幂等：撞唯一索引时不视为错误，回读现存行 inserted=false）。
func (r *Repo) InsertWorkflowState(ctx context.Context, wsID string, in app.WorkflowStateInsert) (*app.WorkflowStateRecord, bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
INSERT INTO work_item_workflow_state (workspace_id, work_item_id, workflow_id, stage_id)
SELECT $1, $2::uuid, $3::uuid, $4::uuid
ON CONFLICT (work_item_id) DO NOTHING`, wsID, in.WorkItemID, in.WorkflowID, in.StageID)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 0 {
		cur, err := r.GetWorkflowState(ctx, wsID, in.WorkItemID)
		return cur, false, err
	}
	cur, err := r.GetWorkflowState(ctx, wsID, in.WorkItemID)
	return cur, true, err
}

// AdvanceWorkflowStateCAS 推进乐观锁（设计 §4.1）：affected=0 = 并发输家。
// advance_count +1、dispatch_count 清零、entered_at=now 同语句原子完成。
func (r *Repo) AdvanceWorkflowStateCAS(ctx context.Context, wsID, stateID, fromStageID, toStageID string) (*app.WorkflowStateRecord, bool, error) {
	row, err := scanWorkflowState(r.q(ctx).QueryRow(ctx, `
UPDATE work_item_workflow_state SET stage_id=$3::uuid, advance_count=advance_count+1,
       dispatch_count=0, entered_at=now()
WHERE id=$2::uuid AND workspace_id=$1 AND stage_id=$4::uuid AND finished_at IS NULL
RETURNING `+wfStateCols, wsID, stateID, toStageID, fromStageID))
	if noRow(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return row, true, nil
}

// RestartWorkflowState 光标重置回入口阶段（计数清零、复活在途）。
func (r *Repo) RestartWorkflowState(ctx context.Context, wsID, stateID, entryStageID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_workflow_state SET stage_id=$3::uuid, dispatch_count=0, advance_count=0,
       finished_at=NULL, entered_at=now()
WHERE id=$2::uuid AND workspace_id=$1`, wsID, stateID, entryStageID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// FinishWorkflowState 终态化：WHERE finished_at IS NULL——已终态（completed/
// detached）不重复写（幂等）。
func (r *Repo) FinishWorkflowState(ctx context.Context, wsID, stateID string) (bool, error) {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_workflow_state SET finished_at=now()
WHERE id=$2::uuid AND workspace_id=$1 AND finished_at IS NULL`, wsID, stateID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// AdjWorkflowDispatchCount 派发计数增减（派发前置 +1；blocked 回减 -1）。
func (r *Repo) AdjWorkflowDispatchCount(ctx context.Context, wsID, stateID string, delta int) error {
	_, err := r.q(ctx).Exec(ctx, `
UPDATE work_item_workflow_state SET dispatch_count = dispatch_count + $3
WHERE id=$2::uuid AND workspace_id=$1`, wsID, stateID, delta)
	return err
}

// AppendTransitionLog 推进流水一行（from/task_id 空串 = NULL）。
func (r *Repo) AppendTransitionLog(ctx context.Context, wsID string, e app.TransitionLogEntry) error {
	_, err := r.q(ctx).Exec(ctx, `
INSERT INTO workflow_transition_log (workspace_id, state_id, from_stage_id, to_stage_id, actor_type, actor_id, task_id, note)
SELECT $1, $2::uuid, nullif($3,'')::uuid, $4::uuid, $5, $6, nullif($7,'')::uuid, $8`,
		wsID, e.StateID, e.FromStageID, e.ToStageID, e.ActorType, e.ActorID, e.TaskID, e.Note)
	return err
}

// AppendTransitionNote 在该 state 最新一条流水上追加 note（coalesced 标记）。
// state 无流水行（理论上不可达：Start 必写首行）则静默跳过。
func (r *Repo) AppendTransitionNote(ctx context.Context, wsID, stateID, note string) error {
	tag, err := r.q(ctx).Exec(ctx, `
UPDATE workflow_transition_log SET note = CASE WHEN note='' THEN $3 ELSE note || ',' || $3 END
WHERE id = (
  SELECT id FROM workflow_transition_log
  WHERE workspace_id=$1 AND state_id=$2::uuid
  ORDER BY created_at DESC LIMIT 1
)`, wsID, stateID, note)
	if err != nil {
		return err
	}
	_ = tag
	return nil
}
