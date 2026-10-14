package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	"orkome/internal/work/domain"
)

// WorkflowService = 定义面 CRUD（W1）+ 状态面推进引擎（W2：Start/StateOf/
// Restart/Detach/TryAdvance）。派发、跨域存在性检查、通知不 import
// orchestration（iron law），经包级注入位（SetDispatcher/SetStageTaskTerminal）
// 由 cmd 层 wiring_workflow.go 装配；W1 定义面代码零改动。契约：§2。
type WorkflowService struct {
	repo Repo
	emit Emitter

	outbox OutboxRecorder
	uow    Within
}

func NewWorkflowService(repo Repo, emit Emitter) *WorkflowService {
	return &WorkflowService{repo: repo, emit: emit}
}

func (s *WorkflowService) SetOutbox(rec OutboxRecorder, uow Within) {
	s.outbox, s.uow = rec, uow
}

func (s *WorkflowService) emitCommitted(ctx context.Context, env *events.Envelope, write func(context.Context) error) error {
	return emitCommitted(ctx, s.emit, s.outbox, s.uow, env, write)
}

func (s *WorkflowService) GetProject(ctx context.Context, ws, projectID string) (*Project, error) {
	p, ok, err := s.repo.GetProject(ctx, ws, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "project not found")
	}
	return &p, nil
}

// WorkflowUpsertInput 整体替换式保存入口（PUT 语义：stages 全量覆盖）。
type WorkflowUpsertInput struct {
	Name               string               `json:"name"`
	TriggerMode        string               `json:"trigger_mode"`
	EntryStagePosition int                  `json:"entry_stage_position"`
	Stages             []WorkflowStageInput `json:"stages"`
}

// WorkflowStageInput 阶段输入（HTTP JSON 形状逐字段冻结于契约 §3）。
type WorkflowStageInput struct {
	Position      int    `json:"position"`
	Name          string `json:"name"`
	ExitStatusKey string `json:"exit_status_key"`
	ExecutorType  string `json:"executor_type"` // agent|squad|none
	ExecutorID    string `json:"executor_id,omitempty"`
	Instruction   string `json:"instruction"`
}

// Upsert 创建或整体替换项目的流程定义（单事务：删旧 stages 插新的）。
// exit_status_key 应用层校验（设计 §1.1：软引用，无 FK）——key 必须存在于
// work_item_status 且 category ∈ {started, done}（closed 是流程外的废弃态）。
func (s *WorkflowService) Upsert(ctx context.Context, ws, projectID string, in WorkflowUpsertInput, actorUserID string) (*WorkflowDefinition, error) {
	if _, err := s.GetProject(ctx, ws, projectID); err != nil {
		return nil, err
	}
	// name 可选（契约 §3 请求体无 name 字段，W1 前端按契约不传）：
	// 传入时 1-100 字符；缺省回落「项目名 + 流程」由调用方展示层补语境。
	if len(in.Name) > 100 {
		return nil, apierr.NewValidation("invalid workflow name",
			apierr.FieldError{Field: "name", Problem: "at most 100 chars"})
	}
	if in.Name == "" {
		in.Name = "Workflow"
	}
	if in.TriggerMode == "" {
		in.TriggerMode = "auto"
	}
	if in.TriggerMode != "auto" && in.TriggerMode != "manual" {
		return nil, apierr.NewValidation("trigger_mode must be auto|manual",
			apierr.FieldError{Field: "trigger_mode", Problem: "auto|manual"})
	}
	if len(in.Stages) == 0 {
		return nil, apierr.NewValidation("workflow requires at least one stage",
			apierr.FieldError{Field: "stages", Problem: "at least one stage"})
	}
	if len(in.Stages) > 20 {
		return nil, apierr.NewValidation("too many stages",
			apierr.FieldError{Field: "stages", Problem: "at most 20"})
	}

	defs := make([]domain.WorkflowStageDef, 0, len(in.Stages))
	for _, st := range in.Stages {
		defs = append(defs, domain.WorkflowStageDef{
			Position: st.Position, Name: st.Name, ExitStatusKey: st.ExitStatusKey,
			ExecutorType: st.ExecutorType, ExecutorID: st.ExecutorID, Instruction: st.Instruction,
		})
	}
	// 规范化：按 position 排序后再校验连续性/唯一性（可乱序传入，保存时排序）。
	domain.SortWorkflowStages(defs)
	if err := domain.ValidateWorkflowStages(defs); err != nil {
		return nil, apierr.NewValidation("invalid workflow stages",
			apierr.FieldError{Field: "stages", Problem: err.Error()})
	}

	// exit_status_key 存在性 + category 校验（软引用的应用层守门，设计 §1.1）。
	statuses, err := s.repo.ListStatuses(ctx, ws)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]string, len(statuses))
	for _, d := range statuses {
		byKey[d.Key] = d.Category
	}
	for _, d := range defs {
		cat, ok := byKey[d.ExitStatusKey]
		if !ok {
			return nil, apierr.NewValidation("unknown exit_status_key",
				apierr.FieldError{Field: "stages", Problem: "exit_status_key " + d.ExitStatusKey + " does not exist in this workspace"})
		}
		if cat != domain.CatStarted && cat != domain.CatDone {
			return nil, apierr.NewValidation("exit_status_key category must be started|done",
				apierr.FieldError{Field: "stages", Problem: "exit_status_key " + d.ExitStatusKey + " is category " + cat + " (closed statuses cannot be stage exits)"})
		}
	}

	hit := false
	for _, d := range defs {
		if d.Position == in.EntryStagePosition {
			hit = true
			break
		}
	}
	if !hit {
		return nil, apierr.NewValidation("entry_stage_position does not match any stage",
			apierr.FieldError{Field: "entry_stage_position", Problem: "no stage at this position"})
	}

	env := ev("project.workflow_updated", ws, actorUserID, map[string]any{"project_id": projectID})
	var out *WorkflowDefinition
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		def, err := s.repo.UpsertWorkflow(ctx, ws, projectID, WorkflowUpsert{
			Name: in.Name, TriggerMode: in.TriggerMode,
			EntryStagePosition: in.EntryStagePosition, CreatedBy: actorUserID,
			Stages: defs,
		})
		if err != nil {
			return err
		}
		out = def
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// Get 无定义返回 (nil, nil)——HTTP 层转 {"workflow":null}（契约 §3）。
func (s *WorkflowService) Get(ctx context.Context, ws, projectID string) (*WorkflowDefinition, error) {
	return s.repo.GetWorkflow(ctx, ws, projectID)
}

// Delete 删除定义；该项目的在途 state 全部 finished_at=now（note='detached'），
// state 表按 workflow_id 关联查询（单子光标冻结，设计 §4.6 detach 语义）。
func (s *WorkflowService) Delete(ctx context.Context, ws, projectID, actorUserID string) error {
	if _, err := s.GetProject(ctx, ws, projectID); err != nil {
		return err
	}
	env := ev("project.workflow_updated", ws, actorUserID, map[string]any{"project_id": projectID, "deleted": true})
	return s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		return s.repo.DeleteWorkflow(ctx, ws, projectID)
	})
}

// SetEnabled 只翻 enabled 位（启停逃生门，设计 §4.8）。
func (s *WorkflowService) SetEnabled(ctx context.Context, ws, projectID string, enabled bool, actorUserID string) error {
	if _, err := s.GetProject(ctx, ws, projectID); err != nil {
		return err
	}
	env := ev("project.workflow_updated", ws, actorUserID, map[string]any{"project_id": projectID, "enabled": enabled})
	return s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		ok, err := s.repo.SetWorkflowEnabled(ctx, ws, projectID, enabled)
		if err != nil {
			return err
		}
		if !ok {
			return apierr.New(apierr.NotFound, "workflow not found")
		}
		return nil
	})
}

// —— W2：状态面。签名冻结于契约 §2；实现依据设计 §2（推进链）、§3（派发
// 接缝）、§4（撞车防御）。派发/跨域存在性检查/通知不 import orchestration
//（iron law），经 service 上的函数注入位由 cmd 层 wiring 装配
//（wiring_workflow.go），单测用 fake 断言。 ——

// WorkflowState 工作项的流程位置（W2 状态面读面；字段契约 §2 冻结）。
type WorkflowState struct {
	WorkItemID    string          `json:"work_item_id"`
	WorkflowID    string          `json:"workflow_id"`
	ProjectID     string          `json:"project_id"`
	StageID       string          `json:"stage_id"`
	StagePosition int             `json:"stage_position"`
	StageName     string          `json:"stage_name"`
	ExecutorType  string          `json:"executor_type"`
	ExecutorID    string          `json:"executor_id"`
	EntryStatusKey string         `json:"entry_status_key"` // 进入本阶段时 status
	ExitStatusKey  string         `json:"exit_status_key"`
	DispatchCount int             `json:"dispatch_count"`
	AdvanceCount  int             `json:"advance_count"`
	EnteredAt     time.Time       `json:"entered_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	Stages        []WorkflowStage `json:"stages"`
}

// StageExecInfo 阶段执行信息（派发矩阵的单棒载荷；dispatcher 注入实现按
// ExecutorType 分流，Context 按 契约 §5 形状注入任务 context——engine W3 消费）。
type StageExecInfo struct {
	StageID       string
	StageName     string
	StagePosition int
	ExitStatusKey string
	ExecutorType  string // agent|squad|none
	ExecutorID    string
	Instruction   string
	WorkflowID    string
	// OriginatorUserID 流程创建者（project_workflow.created_by）——任务的
	// 人类归属（originator/accountable），通知收件人（设计 §3）。
	OriginatorUserID string
}

// 防失控上限（设计 §4.5/§5.2）。
const (
	MaxDispatchPerStage = 3
	maxAdvanceFactor    = 3
)

// ErrAdvanceRunaway advance_count 超过 阶段数×3 的可识别错误：调用方
//（wiring）负责停用 workflow + 通知创建者（设计 §5.2 的 service 侧部分）。
var ErrAdvanceRunaway = errors.New("workflow: advance_count exceeded runaway limit")

// SetStageTaskTerminal 跨域存在性检查谓词（设计 §4.3 条件 2）：该工单是否存在
// context->>'stage_id'=stageID 且 status ∈ 终态的任务。实现归 wiring 层内联
// SQL（agent_task_queue 归 orchestration 域，iron law 禁止 work service import）。
var setStageTaskTerminal func(ctx context.Context, ws, workItemID, stageID string) (bool, error)

func SetStageTaskTerminal(f func(ctx context.Context, ws, workItemID, stageID string) (bool, error)) {
	setStageTaskTerminal = f
}

// stageTaskTerminal nil-safe 包装（谓词未装配时视为"无背书任务"——单测与
// 极简装配下的退化路径）。
func (s *WorkflowService) stageTaskTerminal(ctx context.Context, ws, workItemID, stageID string) bool {
	if setStageTaskTerminal == nil {
		return false
	}
	ok, err := setStageTaskTerminal(ctx, ws, workItemID, stageID)
	if err != nil {
		return false
	}
	return ok
}

// Dispatcher 派发矩阵注入位（契约 §5）：wiring 层实现按 ExecutorType 调
// EnqueueService 入口，不新造入队路径。派发计数的前置+1 在 service 侧
// dispatchStage 完成，dispatcher 回调不再动计数。
var setDispatcher func(ctx context.Context, ws, workItemID string, st StageExecInfo) error

func SetDispatcher(f func(ctx context.Context, ws, workItemID string, st StageExecInfo) error) {
	setDispatcher = f
}

func (s *WorkflowService) dispatchStage(ctx context.Context, ws, workItemID string, st StageExecInfo) error {
	if setDispatcher == nil {
		return nil
	}
	return setDispatcher(ctx, ws, workItemID, st)
}

// AppendTransitionNote 在最新 transition 行上追加 note（W2 追加段；契约 §2
// 冻结面之外的干净扩展——coalesced 标记走这里，不另开 SQL 接缝）。
func (s *WorkflowService) AppendTransitionNote(ctx context.Context, ws, workItemID, note string) error {
	st, err := s.repo.GetWorkflowState(ctx, ws, workItemID)
	if err != nil || st == nil {
		return err
	}
	return s.repo.AppendTransitionNote(ctx, ws, st.ID, note)
}

// loadWorkflowFor 读工单所属项目的 enabled workflow（TryAdvance/Start 共用）。
// 返回 (nil, nil) = 无流程可推进。
func (s *WorkflowService) loadWorkflowFor(ctx context.Context, ws, workItemID string) (*WorkflowDefinition, error) {
	wi, ok, err := s.repo.GetWorkItem(ctx, ws, workItemID)
	if err != nil {
		return nil, err
	}
	if !ok || wi.ProjectID == "" {
		return nil, nil
	}
	def, err := s.repo.GetWorkflow(ctx, ws, wi.ProjectID)
	if err != nil || def == nil {
		return nil, err
	}
	if !def.Enabled {
		return nil, nil
	}
	return def, nil
}

// stateWithDef 把 state 光标 + 定义组装成契约 §2 的 WorkflowState 读面
//（联查当前 stage + stages 升序 + entry_status 推导：transition_log 首行无
// from → entry_status = workflow 的入口阶段出口语义不成立，取 work_item 现
// status（W1 实施注记：state 表没有 entry_status 列；lightweight 推导见
// docs/wf-w2-notes.md §entry_status））。
func (s *WorkflowService) stateWithDef(ctx context.Context, ws string, st *WorkflowStateRecord, def *WorkflowDefinition, wiStatus string) *WorkflowState {
	out := &WorkflowState{
		WorkItemID:    st.WorkItemID,
		WorkflowID:    st.WorkflowID,
		ProjectID:     def.ProjectID,
		StageID:       st.StageID,
		DispatchCount: st.DispatchCount,
		AdvanceCount:  st.AdvanceCount,
		EnteredAt:     st.EnteredAt,
		FinishedAt:    st.FinishedAt,
		Stages:        def.Stages,
		EntryStatusKey: wiStatus,
	}
	for i := range def.Stages {
		g := def.Stages[i]
		if g.ID == st.StageID {
			out.StagePosition = g.Position
			out.StageName = g.Name
			out.ExecutorType = g.ExecutorType
			out.ExecutorID = g.ExecutorID
			out.ExitStatusKey = g.ExitStatusKey
			break
		}
	}
	return out
}

// Start 进入流程（契约 §2）：幂等——已在途/已完成直接返回现 state。
// 单事务：建 state + transition_log(from=NULL, actor=user) + assignee 投影
//（emitCommitted 发 work_item.updated，assignee 派发器/前端消费）+ 派第一棒
//（派发在事务内：EnqueueService 走同一 uow/outbox，commit 时一起落）。
func (s *WorkflowService) Start(ctx context.Context, ws, workItemID, actorUserID string) (*WorkflowState, error) {
	wi, ok, err := s.repo.GetWorkItem(ctx, ws, workItemID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "work item not found")
	}
	// 幂等：已有 state（在途/已完成）原样返回（契约 §2：已在途/已完成返回现 state）。
	if cur, err := s.repo.GetWorkflowState(ctx, ws, workItemID); err != nil {
		return nil, err
	} else if cur != nil {
		return s.readState(ctx, ws, cur, workItemID)
	}
	if wi.ProjectID == "" {
		return nil, apierr.NewValidation("work item is not attached to a project",
			apierr.FieldError{Field: "project_id", Problem: "workflow start requires a project"})
	}
	def, err := s.repo.GetWorkflow(ctx, ws, wi.ProjectID)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, apierr.New(apierr.NotFound, "project has no workflow")
	}
	entry := stageAtPosition(def.Stages, def.EntryStagePosition)
	if entry == nil {
		return nil, apierr.NewValidation("entry stage not found",
			apierr.FieldError{Field: "entry_stage_position", Problem: "no stage at this position"})
	}

	var state *WorkflowState
	env := ev(events.WorkItemUpdated, ws, actorUserID, nil)
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		rec, _, err := s.repo.InsertWorkflowState(ctx, ws, WorkflowStateInsert{
			WorkItemID: workItemID, WorkflowID: def.ID, StageID: entry.ID,
		})
		if err != nil {
			return err
		}
		if err := s.repo.AppendTransitionLog(ctx, ws, TransitionLogEntry{
			StateID: rec.ID, ToStageID: entry.ID, ActorType: "user", ActorID: actorUserID,
		}); err != nil {
			return err
		}
		// assignee 投影（光标移动的投影，设计 §2.1 动作 3）+ work_item.updated 事件
		//（事件链防环：投影只动 assignee 不动 status，§5.3）。
		updated, err := s.projectAssignee(ctx, ws, workItemID, entry.ExecutorType, entry.ExecutorID)
		if err != nil {
			return err
		}
		env.Payload = wiEvent(updated)
		// 派第一棒（事务内；EnqueueService 的 emitCommitted 嵌套 Within 复用外层 tx）。
		if err := s.dispatchStage(ctx, ws, workItemID, s.stageExec(def, entry)); err != nil {
			return err
		}
		state = s.stateWithDef(ctx, ws, rec, def, wi.StatusKey)
		return nil
	}); err != nil {
		return nil, err
	}
	return state, nil
}

// stageAtPosition 定位入口阶段。
func stageAtPosition(stages []WorkflowStage, position int) *WorkflowStage {
	for i := range stages {
		if stages[i].Position == position {
			return &stages[i]
		}
	}
	return nil
}

// stageExec 组装派发载荷（StageExecInfo；def.CreatedBy = 流程创建者 =
// 任务的 originator/通知收件人，设计 §3）。
func (s *WorkflowService) stageExec(def *WorkflowDefinition, stage *WorkflowStage) StageExecInfo {
	return StageExecInfo{
		StageID: stage.ID, StageName: stage.Name, StagePosition: stage.Position,
		ExitStatusKey: stage.ExitStatusKey, ExecutorType: stage.ExecutorType,
		ExecutorID: stage.ExecutorID, Instruction: stage.Instruction,
		WorkflowID: def.ID, OriginatorUserID: def.CreatedBy,
	}
}

// MaybeAutoStart auto 模式的自动进入（设计 §4.6）：work_item.created（或挂载
// 项目的 PATCH）后由 wiring 层调用。非流程单/无定义/未启用/manual 模式一律
// 静默返回 nil——调用方无需预判。Start 自身幂等（已有 state 原样返回），
// 事件重复投递安全。actorUserID 传 e.ActorUserID（创建者）；系统事件缺省
// 为空时回落流程 created_by（Start 落 transition_log 需要非空 actor）。
func (s *WorkflowService) MaybeAutoStart(ctx context.Context, ws, workItemID, actorUserID string) error {
	wi, ok, err := s.repo.GetWorkItem(ctx, ws, workItemID)
	if err != nil || !ok || wi.ProjectID == "" {
		return err
	}
	def, err := s.repo.GetWorkflow(ctx, ws, wi.ProjectID)
	if err != nil || def == nil || !def.Enabled || def.TriggerMode != "auto" {
		return err
	}
	actor := actorUserID
	if actor == "" {
		actor = def.CreatedBy
	}
	_, err = s.Start(ctx, ws, workItemID, actor)
	return err
}

// HasActiveWorkflowState 该工单是否存在未完结的流程光标（finished_at IS NULL）。
// 供 main.go 的 resetIdleWorkItem 让位判断（§3.1 #3 二次修订）：流程内工单的
// 状态回落由流程自己的返工语义管，idle reset 不得触碰——否则出口态恰为
// in_progress 时，结算 cascade 里 reset 先于推进门铃消费，出口态被打回 todo。
func (s *WorkflowService) HasActiveWorkflowState(ctx context.Context, ws, workItemID string) bool {
	st, err := s.repo.GetWorkflowState(ctx, ws, workItemID)
	return err == nil && st != nil && st.FinishedAt == nil
}

// projectAssignee assignee 投影：agent→'agent'、squad→'squad'、none→'none'+清 id。
// expectedRevision=0（流程是权威写侧，不做版本博弈——并发由 state CAS 仲裁）。
// 返回投影后的工单（供 wiEvent 事件负载）。
func (s *WorkflowService) projectAssignee(ctx context.Context, ws, workItemID, executorType, executorID string) (WorkItem, error) {
	at, aid := "none", ""
	switch executorType {
	case "agent", "squad":
		at, aid = executorType, executorID
	}
	return s.repo.UpdateWorkItem(ctx, ws, workItemID, WorkItemPatch{
		AssigneeType: &at, AssigneeID: &aid,
	}, 0)
}

// StateOf 读工单流程状态（契约 §2：无则 nil,nil）。孤儿 state（定义已删）
// 返回带空 stages 的裸形——前端按 finished_at 判 detached。
func (s *WorkflowService) StateOf(ctx context.Context, ws, workItemID string) (*WorkflowState, error) {
	st, err := s.repo.GetWorkflowState(ctx, ws, workItemID)
	if err != nil || st == nil {
		return nil, err
	}
	return s.readState(ctx, ws, st, workItemID)
}

// readState 光标 + 定义 + 工单现 status 组装契约 §2 形状（孤儿 state 降级裸形）。
func (s *WorkflowService) readState(ctx context.Context, ws string, st *WorkflowStateRecord, workItemID string) (*WorkflowState, error) {
	def, err := s.repo.GetWorkflowByID(ctx, ws, st.WorkflowID)
	if err != nil {
		return nil, err
	}
	wiStatus := ""
	if wi, ok, err := s.repo.GetWorkItem(ctx, ws, workItemID); err != nil {
		return nil, err
	} else if ok {
		wiStatus = wi.StatusKey
	}
	if def == nil {
		return s.stateWithDef(ctx, ws, st, &WorkflowDefinition{Stages: []WorkflowStage{}}, wiStatus), nil
	}
	return s.stateWithDef(ctx, ws, st, def, wiStatus), nil
}

// Restart 人工回退（设计 §4.5）：回 entry stage、计数清零、finished_at=NULL、
// transition_log(note='restarted')，重新投影 + 派发（单事务，同 Start）。
func (s *WorkflowService) Restart(ctx context.Context, ws, workItemID, actorUserID string) (*WorkflowState, error) {
	st, err := s.repo.GetWorkflowState(ctx, ws, workItemID)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, apierr.New(apierr.NotFound, "work item has no workflow state")
	}
	def, err := s.repo.GetWorkflowByID(ctx, ws, st.WorkflowID)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, apierr.New(apierr.NotFound, "workflow definition no longer exists")
	}
	entry := stageAtPosition(def.Stages, def.EntryStagePosition)
	if entry == nil {
		return nil, apierr.NewValidation("entry stage not found",
			apierr.FieldError{Field: "entry_stage_position", Problem: "no stage at this position"})
	}
	wi, ok, err := s.repo.GetWorkItem(ctx, ws, workItemID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "work item not found")
	}
	var state *WorkflowState
	env := ev(events.WorkItemUpdated, ws, actorUserID, nil)
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		ok, err := s.repo.RestartWorkflowState(ctx, ws, st.ID, entry.ID)
		if err != nil {
			return err
		}
		if !ok {
			return apierr.New(apierr.NotFound, "work item has no workflow state")
		}
		if err := s.repo.AppendTransitionLog(ctx, ws, TransitionLogEntry{
			StateID: st.ID, FromStageID: st.StageID, ToStageID: entry.ID,
			ActorType: "user", ActorID: actorUserID, Note: "restarted",
		}); err != nil {
			return err
		}
		updated, err := s.projectAssignee(ctx, ws, workItemID, entry.ExecutorType, entry.ExecutorID)
		if err != nil {
			return err
		}
		env.Payload = wiEvent(updated)
		if err := s.dispatchStage(ctx, ws, workItemID, s.stageExec(def, entry)); err != nil {
			return err
		}
		fresh, err := s.repo.GetWorkflowState(ctx, ws, workItemID)
		if err != nil {
			return err
		}
		state = s.stateWithDef(ctx, ws, fresh, def, wi.StatusKey)
		return nil
	}); err != nil {
		return nil, err
	}
	return state, nil
}

// Detach 脱离流程（契约 §2 + W1 注记双写裁决）：finished_at=now +
// transition_log(note='detached')。已终态幂等（不重复写流水）。
func (s *WorkflowService) Detach(ctx context.Context, ws, workItemID, actorUserID string) error {
	st, err := s.repo.GetWorkflowState(ctx, ws, workItemID)
	if err != nil {
		return err
	}
	if st == nil {
		return apierr.New(apierr.NotFound, "work item has no workflow state")
	}
	env := ev(events.WorkItemUpdated, ws, actorUserID, nil)
	return s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		ok, err := s.repo.FinishWorkflowState(ctx, ws, st.ID)
		if err != nil || !ok {
			return err // 已终态：幂等空转
		}
		if wi, ok, err := s.repo.GetWorkItem(ctx, ws, workItemID); err == nil && ok {
			env.Payload = wiEvent(wi)
		}
		return s.repo.AppendTransitionLog(ctx, ws, TransitionLogEntry{
			StateID: st.ID, FromStageID: st.StageID, ToStageID: st.StageID,
			ActorType: "user", ActorID: actorUserID, Note: "detached",
		})
	})
}

// TryAdvance 推进内核（契约 §2 / 设计 §2.1 三条件 + §4.3 存在性检查 + §4.1 CAS）。
// 返回 (nil, nil) = 未推进（任一条件不满足/并发输家）；推进成功返回新 state。
// 不改 status——status 是推进的因不是果（设计 §3.2 不变量）。
// advance_count 超 阶段数×3 返回包 ErrAdvanceRunaway 的错误（wiring 停用路径）。
func (s *WorkflowService) TryAdvance(ctx context.Context, ws, workItemID string) (*WorkflowState, error) {
	// 1) 读 state（无/已终态/workflow disabled → 未推进）。
	st, err := s.repo.GetWorkflowState(ctx, ws, workItemID)
	if err != nil || st == nil {
		return nil, err
	}
	if st.FinishedAt != nil {
		return nil, nil
	}
	def, err := s.repo.GetWorkflowByID(ctx, ws, st.WorkflowID)
	if err != nil {
		return nil, err
	}
	if def == nil || !def.Enabled {
		return nil, nil
	}
	cur, next := stageAround(def.Stages, st.StageID)
	if cur == nil {
		return nil, nil // 孤儿光标（阶段被重定义冲掉）：冻结不推进
	}
	// 2) 工单现 status ≠ 当前阶段出口态 → 未推进。
	wi, ok, err := s.repo.GetWorkItem(ctx, ws, workItemID)
	if err != nil {
		return nil, err
	}
	if !ok || wi.StatusKey != cur.ExitStatusKey {
		return nil, nil
	}
	// 3) 存在性检查（§4.3）：该阶段有终态任务背书（谓词注入；none 阶段无任务
	// → 退化为条件 1+3，人工拖单 = 人工背书）。
	if cur.ExecutorType != "none" && !s.stageTaskTerminal(ctx, ws, workItemID, cur.ID) {
		return nil, nil
	}
	// 4) dispatch_count 上限（§4.5）：超限不推进不派发（等人工 restart）。
	if st.DispatchCount > MaxDispatchPerStage {
		return nil, nil
	}
	// 5) 下一阶段：无 → 终态（§4.7：finished_at + note=completed，不再派任务）。
	if next == nil {
		env := ev(events.WorkItemUpdated, ws, "", nil)
		if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
			ok, err := s.repo.FinishWorkflowState(ctx, ws, st.ID)
			if err != nil || !ok {
				return err // 并发输家/已终态：幂等
			}
			if wi2, ok2, err2 := s.repo.GetWorkItem(ctx, ws, workItemID); err2 == nil && ok2 {
				env.Payload = wiEvent(wi2)
			}
			return s.repo.AppendTransitionLog(ctx, ws, TransitionLogEntry{
				StateID: st.ID, FromStageID: cur.ID, ToStageID: cur.ID,
				ActorType: "system", Note: "completed",
			})
		}); err != nil {
			return nil, err
		}
		fin, err := s.repo.GetWorkflowState(ctx, ws, workItemID)
		if err != nil {
			return nil, err
		}
		return s.stateWithDef(ctx, ws, fin, def, wi.StatusKey), nil
	}
	// 6) CAS 推进（affected=0 = 并发输家 → nil,nil）+ 7) log/投影 + 8) 防失控 +
	// 9) 派下一棒，单事务（设计 §2.1 "advance 动作（单事务）"）。
	env := ev(events.WorkItemUpdated, ws, "", nil)
	var state *WorkflowState
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		advanced, _, err := s.repo.AdvanceWorkflowStateCAS(ctx, ws, st.ID, cur.ID, next.ID)
		if err != nil {
			return err
		}
		if advanced == nil {
			state = nil // 并发输家：静默放弃（§4.1）
			return nil
		}
		if err := s.repo.AppendTransitionLog(ctx, ws, TransitionLogEntry{
			StateID: st.ID, FromStageID: cur.ID, ToStageID: next.ID, ActorType: "system",
		}); err != nil {
			return err
		}
		updated, err := s.projectAssignee(ctx, ws, workItemID, next.ExecutorType, next.ExecutorID)
		if err != nil {
			return err
		}
		env.Payload = wiEvent(updated)
		// 防失控总量（§5.2）：advance_count 超 阶段数×3 → 可识别错误，
		// wiring 层停用 workflow + 通知创建者（事务回滚：异常循环不落库）。
		if advanced.AdvanceCount > len(def.Stages)*maxAdvanceFactor {
			return fmt.Errorf("%w: advance_count=%d stages=%d", ErrAdvanceRunaway, advanced.AdvanceCount, len(def.Stages))
		}
		// 派发前置计数 +1（§4.5：dispatch_count 在成功派发路径上递增；
		// blocked 未派出去由 dispatcher 侧决定是否回减——wiring 层）。
		if err := s.repo.AdjWorkflowDispatchCount(ctx, ws, st.ID, +1); err != nil {
			return err
		}
		if err := s.dispatchStage(ctx, ws, workItemID, s.stageExec(def, next)); err != nil {
			return err
		}
		state = s.stateWithDef(ctx, ws, advanced, def, wi.StatusKey)
		return nil
	}); err != nil {
		return nil, err
	}
	return state, nil
}

// stageAround 在升序 stages 里定位 current 与 position+1 的下一阶段。
func stageAround(stages []WorkflowStage, stageID string) (cur, next *WorkflowStage) {
	for i := range stages {
		if stages[i].ID == stageID {
			cur = &stages[i]
			if i+1 < len(stages) {
				next = &stages[i+1]
			}
			break
		}
	}
	return cur, next
}

// PauseWorkflowOnRunaway 防失控熔断的 service 侧（设计 §5.2）：TryAdvance 返回
// ErrAdvanceRunaway 后由 wiring 调用——停用该工单所属 workflow（enabled=false，
// §4.8 逃生门）并回传定义（wiring 据此通知 created_by）。事务推进已回滚，
// 这里只翻开关；通知由 wiring 层做（跨 notification 域）。
func (s *WorkflowService) PauseWorkflowOnRunaway(ctx context.Context, ws, workItemID string, st *WorkflowState, cause error) *WorkflowDefinition {
	if st == nil || st.WorkflowID == "" {
		return nil
	}
	def, err := s.repo.GetWorkflowByID(ctx, ws, st.WorkflowID)
	if err != nil || def == nil {
		return nil
	}
	if _, err := s.repo.SetWorkflowEnabled(ctx, ws, def.ProjectID, false); err != nil {
		return nil
	}
	return def
}
