package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

type fakeWorkflowRepo struct {
	Repo
	statuses []StatusDef
	def      *WorkflowDefinition
	upserts  int
	deletes  int
	enabled  []bool
}

func (f *fakeWorkflowRepo) ListStatuses(context.Context, string) ([]StatusDef, error) {
	return f.statuses, nil
}

func (f *fakeWorkflowRepo) GetProject(context.Context, string, string) (Project, bool, error) {
	return Project{ID: "p-1", Name: "proj"}, true, nil
}

func (f *fakeWorkflowRepo) UpsertWorkflow(_ context.Context, _, _ string, in WorkflowUpsert) (*WorkflowDefinition, error) {
	f.upserts++
	stages := make([]WorkflowStage, 0, len(in.Stages))
	for _, d := range in.Stages {
		stages = append(stages, WorkflowStage{
			Position: d.Position, Name: d.Name, ExitStatusKey: d.ExitStatusKey,
			ExecutorType: d.ExecutorType, ExecutorID: d.ExecutorID, Instruction: d.Instruction,
		})
	}
	f.def = &WorkflowDefinition{
		ID: "wf-1", ProjectID: "p-1", Name: in.Name, TriggerMode: in.TriggerMode,
		EntryStagePosition: in.EntryStagePosition, Enabled: true, CreatedBy: in.CreatedBy,
		Stages: stages,
	}
	return f.def, nil
}

func (f *fakeWorkflowRepo) GetWorkflow(context.Context, string, string) (*WorkflowDefinition, error) {
	return f.def, nil
}

func (f *fakeWorkflowRepo) DeleteWorkflow(context.Context, string, string) error {
	f.deletes++
	f.def = nil
	return nil
}

func (f *fakeWorkflowRepo) SetWorkflowEnabled(_ context.Context, _, _ string, enabled bool) (bool, error) {
	f.enabled = append(f.enabled, enabled)
	if f.def != nil {
		f.def.Enabled = enabled
	}
	return true, nil
}

func wfStageIn(p int, name, exit, etype, eid string) WorkflowStageInput {
	return WorkflowStageInput{Position: p, Name: name, ExitStatusKey: exit, ExecutorType: etype, ExecutorID: eid}
}

func wfSvc(t *testing.T) (*WorkflowService, *fakeWorkflowRepo) {
	t.Helper()
	repo := &fakeWorkflowRepo{statuses: []StatusDef{
		{Key: "todo", Category: "unstarted"},
		{Key: "in_progress", Category: "started"},
		{Key: "in_review", Category: "started"},
		{Key: "done", Category: "done"},
		{Key: "cancelled", Category: "closed"},
		{Key: "archived", Category: "closed"},
	}}
	return NewWorkflowService(repo, nil), repo
}

func TestWorkflowUpsert_HappyPathAndNormalization(t *testing.T) {
	svc, repo := wfSvc(t)
	def, err := svc.Upsert(context.Background(), "ws-1", "p-1", WorkflowUpsertInput{
		Name: "主流程", TriggerMode: "auto", EntryStagePosition: 2,
		Stages: []WorkflowStageInput{
			wfStageIn(2, "实现", "done", "agent", "0b7d1f5e-6b2a-4c9e-8d3f-1a2b3c4d5e6f"),
			wfStageIn(1, "需求澄清", "in_review", "none", ""),
		},
	}, "u-1")
	require.NoError(t, err)
	require.NotNil(t, def)
	assert.True(t, def.Enabled, "fresh definition defaults enabled")
	assert.Equal(t, 2, len(def.Stages))
	assert.Equal(t, 1, def.Stages[0].Position, "stages stored/returned in ascending position order")
	assert.Equal(t, "需求澄清", def.Stages[0].Name)
	assert.Equal(t, 1, repo.upserts, "single upsert hit")
	assert.Equal(t, 0, len(repo.enabled))
}

func TestWorkflowUpsert_RejectsInvalidExitStatusKey(t *testing.T) {
	svc, _ := wfSvc(t)
	ctx := context.Background()

	// 不存在的 key → 400 形状（Validation）
	_, err := svc.Upsert(ctx, "ws-1", "p-1", WorkflowUpsertInput{
		Name: "w", EntryStagePosition: 1,
		Stages: []WorkflowStageInput{wfStageIn(1, "s", "nope", "none", "")},
	}, "u-1")
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Validation, ae.Code)

	// closed 类不能作出口（设计 §1.1）
	_, err = svc.Upsert(ctx, "ws-1", "p-1", WorkflowUpsertInput{
		Name: "w", EntryStagePosition: 1,
		Stages: []WorkflowStageInput{wfStageIn(1, "s", "cancelled", "none", "")},
	}, "u-1")
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Validation, ae.Code)

	// unstarted 也不行（只有 started|done 可作阶段出口）
	_, err = svc.Upsert(ctx, "ws-1", "p-1", WorkflowUpsertInput{
		Name: "w", EntryStagePosition: 1,
		Stages: []WorkflowStageInput{wfStageIn(1, "s", "todo", "none", "")},
	}, "u-1")
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Validation, ae.Code)
}

func TestWorkflowUpsert_RejectsBadEntryStage(t *testing.T) {
	svc, _ := wfSvc(t)
	_, err := svc.Upsert(context.Background(), "ws-1", "p-1", WorkflowUpsertInput{
		Name: "w", EntryStagePosition: 5,
		Stages: []WorkflowStageInput{wfStageIn(1, "s", "done", "none", "")},
	}, "u-1")
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Validation, ae.Code)
}

func TestWorkflowUpsert_RejectsBadTriggerModeAndEmptyStages(t *testing.T) {
	svc, _ := wfSvc(t)
	ctx := context.Background()
	_, err := svc.Upsert(ctx, "ws-1", "p-1", WorkflowUpsertInput{
		Name: "w", TriggerMode: "半自动", EntryStagePosition: 1,
		Stages: []WorkflowStageInput{wfStageIn(1, "s", "done", "none", "")},
	}, "u-1")
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Validation, ae.Code)

	_, err = svc.Upsert(ctx, "ws-1", "p-1", WorkflowUpsertInput{Name: "w"}, "u-1")
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Validation, ae.Code)
}

func TestWorkflowGet_NilWhenUndefined(t *testing.T) {
	svc, _ := wfSvc(t)
	def, err := svc.Get(context.Background(), "ws-1", "p-1")
	require.NoError(t, err)
	assert.Nil(t, def, "no definition → (nil, nil); HTTP layer renders {\"workflow\":null}")
}

func TestWorkflowSetEnabled_Toggles(t *testing.T) {
	svc, repo := wfSvc(t)
	ctx := context.Background()
	_, err := svc.Upsert(ctx, "ws-1", "p-1", WorkflowUpsertInput{
		Name: "w", EntryStagePosition: 1,
		Stages: []WorkflowStageInput{wfStageIn(1, "s", "done", "none", "")},
	}, "u-1")
	require.NoError(t, err)

	require.NoError(t, svc.SetEnabled(ctx, "ws-1", "p-1", false, "u-1"))
	assert.Equal(t, []bool{false}, repo.enabled)
	assert.False(t, repo.def.Enabled)

	require.NoError(t, svc.SetEnabled(ctx, "ws-1", "p-1", true, "u-1"))
	assert.Equal(t, []bool{false, true}, repo.enabled)
}

func TestWorkflowDelete_DropsDefinition(t *testing.T) {
	svc, repo := wfSvc(t)
	ctx := context.Background()
	_, err := svc.Upsert(ctx, "ws-1", "p-1", WorkflowUpsertInput{
		Name: "w", EntryStagePosition: 1,
		Stages: []WorkflowStageInput{wfStageIn(1, "s", "done", "none", "")},
	}, "u-1")
	require.NoError(t, err)

	require.NoError(t, svc.Delete(ctx, "ws-1", "p-1", "u-1"))
	assert.Equal(t, 1, repo.deletes)
	def, err := svc.Get(ctx, "ws-1", "p-1")
	require.NoError(t, err)
	assert.Nil(t, def)
}

// W2 波次已替换五方法实现体（真实现 + fake repo 断言见
// service_workflow_state_test.go），原 W1 占位断言随之退役。

// 域校验器经 service 入口的透传：stages 层的拒绝路径（position 断档/executor 形状）。
func TestWorkflowUpsert_PassesDomainValidationThrough(t *testing.T) {
	svc, _ := wfSvc(t)
	ctx := context.Background()
	_, err := svc.Upsert(ctx, "ws-1", "p-1", WorkflowUpsertInput{
		Name: "w", EntryStagePosition: 1,
		Stages: []WorkflowStageInput{
			wfStageIn(1, "s", "done", "none", ""),
			wfStageIn(3, "t", "done", "none", ""), // 断档：缺 2
		},
	}, "u-1")
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Validation, ae.Code)
	assert.Contains(t, ae.Details[0].Problem, "contiguous")
}
