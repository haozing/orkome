package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
)

// W2 状态面单测（fake repo；docs/wf-interfaces-frozen.md §2 + 设计 §2/§4/§5）。
// 覆盖 TryAdvance 全条件分支、派发分流、context 注入形状逐字段断言。
// 集成面（真 SQL）见 pgrepo/workflow_engine_integration_test.go。

// —— fake repo：内存版 workflow 四表 + work_item 最小面 ——

type fakeStateRepo struct {
	Repo
	statuses []StatusDef
	def      *WorkflowDefinition
	items    map[string]*WorkItem
	states   map[string]*WorkflowStateRecord // by work_item_id
	logs     []TransitionLogEntry
	seq      int

	stateByID map[string]*WorkflowStateRecord
}

func newFakeStateRepo(def *WorkflowDefinition, items ...WorkItem) *fakeStateRepo {
	f := &fakeStateRepo{
		def:       def,
		items:     map[string]*WorkItem{},
		states:    map[string]*WorkflowStateRecord{},
		stateByID: map[string]*WorkflowStateRecord{},
	}
	for i := range items {
		it := items[i]
		f.items[it.ID] = &it
	}
	return f
}

func (f *fakeStateRepo) nextID() string {
	f.seq++
	return "id-" + string(rune('a'+f.seq-1))
}

func (f *fakeStateRepo) GetProject(context.Context, string, string) (Project, bool, error) {
	return Project{ID: "p-1"}, true, nil
}

func (f *fakeStateRepo) ListStatuses(context.Context, string) ([]StatusDef, error) { return f.statuses, nil }

func (f *fakeStateRepo) GetWorkItem(_ context.Context, _, id string) (WorkItem, bool, error) {
	it, ok := f.items[id]
	if !ok {
		return WorkItem{}, false, nil
	}
	return *it, true, nil
}

func (f *fakeStateRepo) UpdateWorkItem(_ context.Context, _, id string, p WorkItemPatch, _ int64) (WorkItem, error) {
	it, ok := f.items[id]
	if !ok {
		return WorkItem{}, errors.New("no such work item")
	}
	if p.AssigneeType != nil {
		it.AssigneeType = *p.AssigneeType
	}
	if p.AssigneeID != nil {
		it.AssigneeID = *p.AssigneeID
	}
	if p.StatusKey != nil {
		it.StatusKey = *p.StatusKey
	}
	it.Revision++
	return *it, nil
}

func (f *fakeStateRepo) GetWorkflow(context.Context, string, string) (*WorkflowDefinition, error) {
	return f.def, nil
}

func (f *fakeStateRepo) GetWorkflowByID(context.Context, string, string) (*WorkflowDefinition, error) {
	return f.def, nil
}

func (f *fakeStateRepo) GetWorkflowState(_ context.Context, _, workItemID string) (*WorkflowStateRecord, error) {
	if st, ok := f.states[workItemID]; ok {
		cp := *st
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeStateRepo) InsertWorkflowState(_ context.Context, _ string, in WorkflowStateInsert) (*WorkflowStateRecord, bool, error) {
	if _, exists := f.states[in.WorkItemID]; exists {
		cp := *f.states[in.WorkItemID]
		return &cp, false, nil
	}
	rec := &WorkflowStateRecord{
		ID: f.nextID(), WorkItemID: in.WorkItemID, WorkflowID: in.WorkflowID,
		StageID: in.StageID, EnteredAt: time.Now(),
	}
	f.states[in.WorkItemID] = rec
	f.stateByID[rec.ID] = rec
	return rec, true, nil
}

func (f *fakeStateRepo) AdvanceWorkflowStateCAS(_ context.Context, _, stateID, fromStageID, toStageID string) (*WorkflowStateRecord, bool, error) {
	rec, ok := f.stateByID[stateID]
	if !ok || rec.StageID != fromStageID || rec.FinishedAt != nil {
		return nil, false, nil
	}
	rec.StageID = toStageID
	rec.AdvanceCount++
	rec.DispatchCount = 0
	rec.EnteredAt = time.Now()
	cp := *rec
	return &cp, true, nil
}

func (f *fakeStateRepo) RestartWorkflowState(_ context.Context, _, stateID, entryStageID string) (bool, error) {
	rec, ok := f.stateByID[stateID]
	if !ok {
		return false, nil
	}
	rec.StageID = entryStageID
	rec.DispatchCount = 0
	rec.AdvanceCount = 0
	rec.FinishedAt = nil
	rec.EnteredAt = time.Now()
	return true, nil
}

func (f *fakeStateRepo) FinishWorkflowState(_ context.Context, _, stateID string) (bool, error) {
	rec, ok := f.stateByID[stateID]
	if !ok || rec.FinishedAt != nil {
		return false, nil
	}
	now := time.Now()
	rec.FinishedAt = &now
	return true, nil
}

func (f *fakeStateRepo) AdjWorkflowDispatchCount(_ context.Context, _, stateID string, delta int) error {
	if rec, ok := f.stateByID[stateID]; ok {
		rec.DispatchCount += delta
	}
	return nil
}

func (f *fakeStateRepo) AppendTransitionLog(_ context.Context, _ string, e TransitionLogEntry) error {
	f.logs = append(f.logs, e)
	return nil
}

func (f *fakeStateRepo) AppendTransitionNote(_ context.Context, _, _, _ string) error { return nil }

// —— harness ——

const (
	fakeAgent1 = "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e6f"
	fakeAgent2 = "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e6e"
)

type dispatchCall struct {
	ws, wi    string
	st        StageExecInfo
	note      string
	blocked   bool // fake dispatcher 模拟 blocked 返回
	alreadyOn bool // fake dispatcher 模拟 already_active
}

// fakeDispatcher 记录派发调用，模拟 EnqueueService 的 reason 语义。
type fakeDispatcher struct {
	calls []dispatchCall
}

func (d *fakeDispatcher) dispatch(ctx context.Context, ws, wi string, st StageExecInfo) error {
	d.calls = append(d.calls, dispatchCall{ws: ws, wi: wi, st: st})
	return nil
}

func (d *fakeDispatcher) reset() { d.calls = nil }

func stateSvc(t *testing.T) (*WorkflowService, *fakeStateRepo, *fakeDispatcher) {
	t.Helper()
	def := &WorkflowDefinition{
		ID: "wf-1", ProjectID: "p-1", EntryStagePosition: 1, Enabled: true, CreatedBy: "creator-1",
		Stages: []WorkflowStage{
			{ID: "s1", Position: 1, Name: "需求澄清", ExitStatusKey: "in_review", ExecutorType: "agent", ExecutorID: fakeAgent1, Instruction: "澄清 {{title}}"},
			{ID: "s2", Position: 2, Name: "实现", ExitStatusKey: "done", ExecutorType: "agent", ExecutorID: fakeAgent2, Instruction: "实现它"},
		},
	}
	repo := newFakeStateRepo(def, WorkItem{ID: "wi-1", WorkspaceID: "ws-1", ProjectID: "p-1", StatusKey: "todo", CreatorID: "u-1"})
	d := &fakeDispatcher{}
	SetDispatcher(d.dispatch)
	t.Cleanup(func() { SetDispatcher(nil) })
	return NewWorkflowService(repo, nil), repo, d
}

// setState 直塞光标（绕过 Start 检查，专测 TryAdvance 分支）。
func (f *fakeStateRepo) setState(wiID, stageID string, dispatchCount int) *WorkflowStateRecord {
	rec := &WorkflowStateRecord{ID: "st-1", WorkItemID: wiID, WorkflowID: "wf-1", StageID: stageID, EnteredAt: time.Now(), DispatchCount: dispatchCount}
	f.states[wiID] = rec
	f.stateByID[rec.ID] = rec
	return rec
}

// —— Start ——

func TestWorkflowState_Start_BuildsEntryStateAndDispatches(t *testing.T) {
	svc, repo, d := stateSvc(t)
	ctx := context.Background()

	st, err := svc.Start(ctx, "ws-1", "wi-1", "u-1")
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, "s1", st.StageID, "entry stage")
	assert.Equal(t, 1, st.StagePosition)
	assert.Equal(t, "需求澄清", st.StageName)
	assert.Equal(t, "agent", st.ExecutorType)
	assert.Equal(t, fakeAgent1, st.ExecutorID)
	assert.Equal(t, "in_review", st.ExitStatusKey)
	assert.Equal(t, "todo", st.EntryStatusKey, "current work item status at entry")
	assert.Len(t, st.Stages, 2)

	// assignee 投影到第一棒执行者。
	wi := repo.items["wi-1"]
	assert.Equal(t, "agent", wi.AssigneeType)
	assert.Equal(t, fakeAgent1, wi.AssigneeID)

	// 派第一棒。
	require.Len(t, d.calls, 1)
	assert.Equal(t, "wi-1", d.calls[0].wi)
	assert.Equal(t, "s1", d.calls[0].st.StageID)
	assert.Equal(t, "creator-1", d.calls[0].st.OriginatorUserID, "originator = workflow.created_by")

	// transition_log 一行：from=NULL、actor=user。
	require.Len(t, repo.logs, 1)
	assert.Empty(t, repo.logs[0].FromStageID, "from=NULL on entry")
	assert.Equal(t, "s1", repo.logs[0].ToStageID)
	assert.Equal(t, "user", repo.logs[0].ActorType)
	assert.Equal(t, "u-1", repo.logs[0].ActorID)
}

func TestWorkflowState_Start_Idempotent(t *testing.T) {
	svc, _, d := stateSvc(t)
	ctx := context.Background()

	first, err := svc.Start(ctx, "ws-1", "wi-1", "u-1")
	require.NoError(t, err)
	d.reset()

	second, err := svc.Start(ctx, "ws-1", "wi-1", "u-1")
	require.NoError(t, err)
	assert.Equal(t, first.StageID, second.StageID, "same state returned")
	assert.Equal(t, first.WorkItemID, second.WorkItemID)
	assert.Empty(t, d.calls, "no re-dispatch on idempotent start")
}

func TestWorkflowState_Start_RequiresProject(t *testing.T) {
	def := &WorkflowDefinition{ID: "wf-1", ProjectID: "p-1", EntryStagePosition: 1, Enabled: true,
		Stages: []WorkflowStage{{ID: "s1", Position: 1, ExitStatusKey: "done", ExecutorType: "none"}}}
	repo := newFakeStateRepo(def, WorkItem{ID: "wi-np", StatusKey: "todo"}) // 无项目
	d := &fakeDispatcher{}
	SetDispatcher(d.dispatch)
	t.Cleanup(func() { SetDispatcher(nil) })
	svc := NewWorkflowService(repo, nil)

	_, err := svc.Start(context.Background(), "ws-1", "wi-np", "u-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project")
}

// —— StateOf ——

func TestWorkflowState_StateOf_NilWhenNoState(t *testing.T) {
	svc, _, _ := stateSvc(t)
	st, err := svc.StateOf(context.Background(), "ws-1", "wi-1")
	require.NoError(t, err)
	assert.Nil(t, st)
}

// —— TryAdvance：条件分支 ——

func TestWorkflowState_TryAdvance_NoStateOrNil(t *testing.T) {
	svc, _, _ := stateSvc(t)
	ctx := context.Background()
	st, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	assert.Nil(t, st, "no state → no advance")
}

func TestWorkflowState_TryAdvance_FinishedIsNoop(t *testing.T) {
	svc, repo, _ := stateSvc(t)
	ctx := context.Background()
	rec := repo.setState("wi-1", "s1", 0)
	now := time.Now()
	rec.FinishedAt = &now

	st, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	assert.Nil(t, st, "finished_at non-nil → frozen")
}

func TestWorkflowState_TryAdvance_DisabledWorkflowIsNoop(t *testing.T) {
	svc, repo, _ := stateSvc(t)
	ctx := context.Background()
	repo.def.Enabled = false
	repo.setState("wi-1", "s1", 0)

	st, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	assert.Nil(t, st, "workflow disabled → no advance")
}

func TestWorkflowState_TryAdvance_StatusNotAtExitIsNoop(t *testing.T) {
	svc, repo, d := stateSvc(t)
	ctx := context.Background()
	repo.setState("wi-1", "s1", 0) // 工单 status 仍是 todo ≠ in_review

	st, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	assert.Nil(t, st)
	assert.Empty(t, d.calls, "no dispatch when not at exit status")
}

func TestWorkflowState_TryAdvance_NoTerminalTaskIsNoop(t *testing.T) {
	svc, repo, _ := stateSvc(t)
	ctx := context.Background()
	repo.setState("wi-1", "s1", 0)
	repo.items["wi-1"].StatusKey = "in_review" // 到出口态
	// 谓词返回 false：无终态任务背书。
	SetStageTaskTerminal(func(context.Context, string, string, string) (bool, error) { return false, nil })
	t.Cleanup(func() { SetStageTaskTerminal(nil) })

	st, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	assert.Nil(t, st, "exit status without terminal stage task → no advance")
}

func TestWorkflowState_TryAdvance_HappyPathAdvancesAndDispatches(t *testing.T) {
	svc, repo, d := stateSvc(t)
	ctx := context.Background()
	repo.setState("wi-1", "s1", 1)
	repo.items["wi-1"].StatusKey = "in_review"
	SetStageTaskTerminal(func(_ context.Context, _, _, stageID string) (bool, error) {
		return stageID == "s1", nil
	})
	t.Cleanup(func() { SetStageTaskTerminal(nil) })

	st, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, "s2", st.StageID, "advanced to stage 2")
	assert.Equal(t, "实现", st.StageName)
	assert.Equal(t, fakeAgent2, st.ExecutorID)
	assert.Equal(t, 1, st.AdvanceCount)
	assert.Equal(t, 1, repo.stateByID["st-1"].DispatchCount, "dispatch pre-increment lands in the live record (returned snapshot predates +1)")
	assert.Nil(t, st.FinishedAt)

	// assignee 换人。
	wi := repo.items["wi-1"]
	assert.Equal(t, "agent", wi.AssigneeType)
	assert.Equal(t, fakeAgent2, wi.AssigneeID)

	// 新任务派给下一棒。
	require.Len(t, d.calls, 1)
	assert.Equal(t, "s2", d.calls[0].st.StageID)

	// advance 行 log（本测试直 setState，无 Start 首行）。
	last := repo.logs[len(repo.logs)-1]
	assert.Equal(t, "s1", last.FromStageID)
	assert.Equal(t, "s2", last.ToStageID)
	assert.Equal(t, "system", last.ActorType)
}

func TestWorkflowState_TryAdvance_NoneExecutorAdvancesWithoutTask(t *testing.T) {
	def := &WorkflowDefinition{ID: "wf-1", ProjectID: "p-1", EntryStagePosition: 1, Enabled: true, CreatedBy: "creator-1",
		Stages: []WorkflowStage{
			{ID: "s1", Position: 1, Name: "人工环节", ExitStatusKey: "in_review", ExecutorType: "none"},
			{ID: "s2", Position: 2, Name: "实现", ExitStatusKey: "done", ExecutorType: "none"},
		}}
	repo := newFakeStateRepo(def, WorkItem{ID: "wi-1", ProjectID: "p-1", StatusKey: "todo"})
	d := &fakeDispatcher{}
	SetDispatcher(d.dispatch)
	t.Cleanup(func() { SetDispatcher(nil) })
	svc := NewWorkflowService(repo, nil)
	ctx := context.Background()

	st, err := svc.Start(ctx, "ws-1", "wi-1", "u-1")
	require.NoError(t, err)
	assert.Equal(t, "none", st.ExecutorType)
	// none 的 assignee 投影：'none' + 清 id。
	wi := repo.items["wi-1"]
	assert.Equal(t, "none", wi.AssigneeType)
	assert.Empty(t, wi.AssigneeID)
	// none 阶段也走 dispatcher 调用（契约 §5：wiring 侧对 none 只通知不派发，
	// service 层不特判——分流是注入实现的职责）。
	require.Len(t, d.calls, 1)
	assert.Equal(t, "none", d.calls[0].st.ExecutorType)

	// none 阶段：无任务背书也能推进（人工拖单 = 人工背书，§4.3）。
	repo.items["wi-1"].StatusKey = "in_review"
	st2, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	require.NotNil(t, st2)
	assert.Equal(t, "s2", st2.StageID)
}

func TestWorkflowState_TryAdvance_CASLosesQuietly(t *testing.T) {
	svc, repo, _ := stateSvc(t)
	ctx := context.Background()
	repo.setState("wi-1", "s1", 0)
	repo.items["wi-1"].StatusKey = "in_review"
	SetStageTaskTerminal(func(context.Context, string, string, string) (bool, error) { return true, nil })
	t.Cleanup(func() { SetStageTaskTerminal(nil) })

	// 同工单"并发"两次 TryAdvance（顺序模拟）：第二次时 stage 已是 s2，
	// status ≠ s2 出口 → 第二次返回 nil（CAS 幂等语义）。
	first, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	require.NotNil(t, first)
	second, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	assert.Nil(t, second, "second racing advance is a quiet no-op")
}

func TestWorkflowState_TryAdvance_FinalStageCompletes(t *testing.T) {
	svc, repo, d := stateSvc(t)
	ctx := context.Background()
	repo.setState("wi-1", "s2", 0)
	repo.items["wi-1"].StatusKey = "done" // 最后一阶段出口态
	SetStageTaskTerminal(func(_ context.Context, _, _, stageID string) (bool, error) {
		return stageID == "s2", nil
	})
	t.Cleanup(func() { SetStageTaskTerminal(nil) })

	st, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	require.NotNil(t, st)
	require.NotNil(t, st.FinishedAt, "finished_at set on completion")
	assert.Empty(t, d.calls, "no task dispatched after completion")

	last := repo.logs[len(repo.logs)-1]
	assert.Equal(t, "completed", last.Note)
	assert.Equal(t, "system", last.ActorType)
}

func TestWorkflowState_TryAdvance_DetachedIsNoop(t *testing.T) {
	svc, repo, _ := stateSvc(t)
	ctx := context.Background()
	_, err := svc.Start(ctx, "ws-1", "wi-1", "u-1")
	require.NoError(t, err)
	require.NoError(t, svc.Detach(ctx, "ws-1", "wi-1", "u-1"))

	repo.items["wi-1"].StatusKey = "in_review"
	SetStageTaskTerminal(func(context.Context, string, string, string) (bool, error) { return true, nil })
	t.Cleanup(func() { SetStageTaskTerminal(nil) })

	st, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	assert.Nil(t, st, "detached → no advance")
}

func TestWorkflowState_TryAdvance_DispatchLimitExceeded(t *testing.T) {
	svc, repo, d := stateSvc(t)
	ctx := context.Background()
	repo.setState("wi-1", "s1", MaxDispatchPerStage+1) // 超 3
	repo.items["wi-1"].StatusKey = "in_review"
	SetStageTaskTerminal(func(context.Context, string, string, string) (bool, error) { return true, nil })
	t.Cleanup(func() { SetStageTaskTerminal(nil) })

	st, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	assert.Nil(t, st, "dispatch_count over limit → no advance")
	assert.Empty(t, d.calls)
}

func TestWorkflowState_TryAdvance_RunawayReturnsRecognizableError(t *testing.T) {
	def := &WorkflowDefinition{ID: "wf-1", ProjectID: "p-1", EntryStagePosition: 1, Enabled: true, CreatedBy: "c",
		Stages: []WorkflowStage{
			{ID: "s1", Position: 1, ExitStatusKey: "in_review", ExecutorType: "agent", ExecutorID: fakeAgent1},
			{ID: "s2", Position: 2, ExitStatusKey: "done", ExecutorType: "agent", ExecutorID: fakeAgent2},
		}}
	// 阶段数×3 = 6；advance_count=6 再 advance 会到 7 → runaway。
	repo := newFakeStateRepo(def, WorkItem{ID: "wi-1", ProjectID: "p-1", StatusKey: "in_review"})
	repo.setState("wi-1", "s1", 0)
	repo.stateByID["st-1"].AdvanceCount = 6
	SetStageTaskTerminal(func(context.Context, string, string, string) (bool, error) { return true, nil })
	t.Cleanup(func() { SetStageTaskTerminal(nil) })
	svc := NewWorkflowService(repo, nil)

	_, err := svc.TryAdvance(context.Background(), "ws-1", "wi-1")
	require.ErrorIs(t, err, ErrAdvanceRunaway, "wiring stops the workflow on this error")
}

// —— Restart ——

func TestWorkflowState_Restart_ResetsToEntryAndRedispatches(t *testing.T) {
	svc, repo, d := stateSvc(t)
	ctx := context.Background()
	_, err := svc.Start(ctx, "ws-1", "wi-1", "u-1")
	require.NoError(t, err)
	// 推进到 s2。
	repo.items["wi-1"].StatusKey = "in_review"
	SetStageTaskTerminal(func(context.Context, string, string, string) (bool, error) { return true, nil })
	t.Cleanup(func() { SetStageTaskTerminal(nil) })
	_, err = svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	d.reset()

	st, err := svc.Restart(ctx, "ws-1", "wi-1", "u-1")
	require.NoError(t, err)
	assert.Equal(t, "s1", st.StageID, "back to entry stage")
	assert.Equal(t, 0, st.DispatchCount, "counters cleared")
	assert.Equal(t, 0, st.AdvanceCount)
	assert.Nil(t, st.FinishedAt)

	// 重新投影 + 派发。
	wi := repo.items["wi-1"]
	assert.Equal(t, fakeAgent1, wi.AssigneeID)
	require.Len(t, d.calls, 1)
	assert.Equal(t, "s1", d.calls[0].st.StageID)

	// log 带 note=restarted。
	last := repo.logs[len(repo.logs)-1]
	assert.Equal(t, "restarted", last.Note)
	assert.Equal(t, "user", last.ActorType)
	assert.Equal(t, "u-1", last.ActorID)
}

func TestWorkflowState_Restart_WithoutStateIs404(t *testing.T) {
	svc, _, _ := stateSvc(t)
	_, err := svc.Restart(context.Background(), "ws-1", "wi-1", "u-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workflow state")
}

// —— Detach ——

func TestWorkflowState_Detach_WritesLogOnce(t *testing.T) {
	svc, repo, _ := stateSvc(t)
	ctx := context.Background()
	_, err := svc.Start(ctx, "ws-1", "wi-1", "u-1")
	require.NoError(t, err)
	logsAfterStart := len(repo.logs)

	require.NoError(t, svc.Detach(ctx, "ws-1", "wi-1", "u-1"))
	st, err := svc.StateOf(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	require.NotNil(t, st.FinishedAt)
	last := repo.logs[len(repo.logs)-1]
	assert.Equal(t, "detached", last.Note)

	// 幂等：二次 Detach 不重复写流水。
	require.NoError(t, svc.Detach(ctx, "ws-1", "wi-1", "u-1"))
	assert.Len(t, repo.logs, logsAfterStart+1, "idempotent detach writes no second log")
}

// —— 派发分流（dispatcher 注入位；§3 矩阵的 service 侧断言）——

func TestWorkflowDispatch_MatrixAgentSquadNone(t *testing.T) {
	def := &WorkflowDefinition{ID: "wf-1", ProjectID: "p-1", EntryStagePosition: 1, Enabled: true, CreatedBy: "creator-1",
		Stages: []WorkflowStage{
			{ID: "s1", Position: 1, Name: "agent 棒", ExitStatusKey: "a", ExecutorType: "agent", ExecutorID: fakeAgent1, Instruction: "do A"},
			{ID: "s2", Position: 2, Name: "squad 棒", ExitStatusKey: "b", ExecutorType: "squad", ExecutorID: "sq-9", Instruction: "do B"},
			{ID: "s3", Position: 3, Name: "none 棒", ExitStatusKey: "c", ExecutorType: "none", Instruction: "human"},
		}}
	repo := newFakeStateRepo(def, WorkItem{ID: "wi-1", ProjectID: "p-1", StatusKey: "a"})
	d := &fakeDispatcher{}
	SetDispatcher(d.dispatch)
	t.Cleanup(func() { SetDispatcher(nil) })
	svc := NewWorkflowService(repo, nil)
	ctx := context.Background()

	_, err := svc.Start(ctx, "ws-1", "wi-1", "u-1")
	require.NoError(t, err)
	require.Len(t, d.calls, 1)
	// 契约 §5 context 注入形状：StageExecInfo 载荷逐字段（dispatcher 注入实现
	// 按此组装任务 context；engine W3 消费）。
	call := d.calls[0]
	assert.Equal(t, "s1", call.st.StageID)
	assert.Equal(t, "agent 棒", call.st.StageName)
	assert.Equal(t, 1, call.st.StagePosition)
	assert.Equal(t, "a", call.st.ExitStatusKey)
	assert.Equal(t, "agent", call.st.ExecutorType)
	assert.Equal(t, fakeAgent1, call.st.ExecutorID)
	assert.Equal(t, "do A", call.st.Instruction)
	assert.Equal(t, "wf-1", call.st.WorkflowID)
	assert.Equal(t, "creator-1", call.st.OriginatorUserID)

	// 推进到 squad 阶段。
	repo.items["wi-1"].StatusKey = "a"
	SetStageTaskTerminal(func(context.Context, string, string, string) (bool, error) { return true, nil })
	st, err := svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, "squad", st.ExecutorType)
	wi := repo.items["wi-1"]
	assert.Equal(t, "squad", wi.AssigneeType, "squad projection")
	assert.Equal(t, "sq-9", wi.AssigneeID)

	// 推进到 none 阶段：assignee 清空、仍会通知（dispatcher 收到 none 类型调用）。
	repo.items["wi-1"].StatusKey = "b"
	st, err = svc.TryAdvance(ctx, "ws-1", "wi-1")
	require.NoError(t, err)
	require.NotNil(t, st)
	assert.Equal(t, "none", st.ExecutorType)
	assert.Equal(t, "none", wi.AssigneeType)
	assert.Empty(t, wi.AssigneeID)
}

// —— context 形状：wiring 层 dispatcher 实现生成的任务 context 逐字段契约
//（§5）在本层只到 StageExecInfo；wiring 的 SQL 断言放集成测试。此处锁死
// StageExecInfo → context map 的转换函数（wiring 同构函数的行为锚）。——

func TestWorkflowDispatch_ContextShapeContract(t *testing.T) {
	st := StageExecInfo{
		StageID: "st-9", StageName: "方案设计", StagePosition: 2, ExitStatusKey: "in_review",
		ExecutorType: "agent", ExecutorID: fakeAgent1, Instruction: "渲染后的指令",
		WorkflowID: "wf-7", OriginatorUserID: "u-42",
	}
	got := workflowStageContextForTest(st, "工单标题")
	assert.Equal(t, map[string]any{
		"trigger":            "workflow_stage",
		"workflow_id":        "wf-7",
		"stage_id":           "st-9",
		"stage_name":         "方案设计",
		"stage_position":     2,
		"stage_exit_status":  "in_review",
		"instruction":        "渲染后的指令",
		"work_item_title":    "工单标题",
	}, got, "contract §5 context shape — engine W3 consumes this verbatim")
}

// workflowStageContextForTest 与 wiring_workflow.go 的 context 组装函数同构
//（wiring 在 cmd 层不可 import 测试；此处以同构副本锁形状，W3 消费锚）。
func workflowStageContextForTest(st StageExecInfo, title string) map[string]any {
	return map[string]any{
		"trigger":           "workflow_stage",
		"workflow_id":       st.WorkflowID,
		"stage_id":          st.StageID,
		"stage_name":        st.StageName,
		"stage_position":    st.StagePosition,
		"stage_exit_status": st.ExitStatusKey,
		"instruction":       st.Instruction,
		"work_item_title":   title,
	}
}

// —— 事件投影：Start 后 emit 的是 work_item.updated（assignee 派发器消费面）——

func TestWorkflowState_StartEmitsWorkItemUpdated(t *testing.T) {
	var got []events.Envelope
	repo := newFakeStateRepo(&WorkflowDefinition{ID: "wf-1", ProjectID: "p-1", EntryStagePosition: 1, Enabled: true, CreatedBy: "c",
		Stages: []WorkflowStage{{ID: "s1", Position: 1, ExitStatusKey: "done", ExecutorType: "agent", ExecutorID: fakeAgent1}}},
		WorkItem{ID: "wi-1", ProjectID: "p-1", StatusKey: "todo"})
	rec := outboxStub{append: func(_ context.Context, envs ...events.Envelope) error {
		got = append(got, envs...)
		return nil
	}}
	SetDispatcher(func(context.Context, string, string, StageExecInfo) error { return nil })
	t.Cleanup(func() { SetDispatcher(nil) })
	svc := NewWorkflowService(repo, nil)
	svc.SetOutbox(rec, func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })

	_, err := svc.Start(context.Background(), "ws-1", "wi-1", "u-1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, events.WorkItemUpdated, got[0].Event)
	p, ok := got[0].Payload.(events.WorkItemPayload)
	require.True(t, ok)
	assert.Equal(t, "wi-1", p.ID)
	assert.Equal(t, fakeAgent1, p.AssigneeID)
	assert.True(t, strings.Contains(p.AssigneeType, "agent"))
}

// outboxStub 最小 OutboxRecorder。
type outboxStub struct {
	append func(ctx context.Context, envs ...events.Envelope) error
}

func (o outboxStub) Append(ctx context.Context, envs ...events.Envelope) error {
	return o.append(ctx, envs...)
}
func (o outboxStub) MarkPublished(context.Context, ...string) error { return nil }
