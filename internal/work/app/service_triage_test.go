package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/dispatchreason"
	"github.com/haozing/ploykit/platform/redactx"
	"orkome/internal/work/domain"
)

type triFakeRepo struct {
	Repo
	item    WorkItem
	updated int
	count   int
}

func (f *triFakeRepo) GetWorkItem(_ context.Context, _, _ string) (WorkItem, bool, error) {
	return f.item, true, nil
}
func (f *triFakeRepo) CountWorkItemsInTriage(_ context.Context, _ string, _ []string) (int, error) {
	return f.count, nil
}
func (f *triFakeRepo) UpdateWorkItem(_ context.Context, _, _ string, _ WorkItemPatch, _ int64) (WorkItem, error) {
	f.updated++
	f.item.Revision++
	return f.item, nil
}
func (f *triFakeRepo) ListStatuses(_ context.Context, _ string) ([]StatusDef, error) {
	return []StatusDef{{Key: "todo", Category: "unstarted"}}, nil
}
func (f *triFakeRepo) ListPropertyDefs(_ context.Context, _ string) ([]PropertyDef, error) {
	return nil, nil
}
func (f *triFakeRepo) EnsureStatusSeed(_ context.Context, _ string) error { return nil }
func (f *triFakeRepo) UpsertSubscriber(_ context.Context, _, _, _ string) error {
	return nil
}

func triService(item WorkItem) (*WorkItemService, *triFakeRepo) {
	repo := &triFakeRepo{item: item}
	return NewWorkItemService(repo, nil, nil), repo
}

func strp(s string) *string { return &s }

func TestUpdateTriageLockedFields(t *testing.T) {
	triage := strp("pending")
	newAssignee := "00000000-0000-4000-8000-0000000000a1"

	t.Run("triage + change assignee → 400 and not persisted", func(t *testing.T) {
		svc, repo := triService(WorkItem{ID: "wi-1", StatusKey: "todo", TriageState: triage})
		_, err := svc.Update(context.Background(), "ws", "wi-1",
			WorkItemPatch{AssigneeID: &newAssignee, AssigneeType: strp("agent")}, 0, "u1")
		require.Error(t, err)
		ae, ok := err.(*apierr.Error)
		require.True(t, ok)
		assert.Equal(t, apierr.Validation, ae.Code)
		assert.Equal(t, "assignee", ae.Details[0].Field)
		assert.Zero(t, repo.updated, "must not enter UPDATE when the guard hits")
	})

	t.Run("triage+same patch unlock(accept)→allowed", func(t *testing.T) {
		svc, repo := triService(WorkItem{ID: "wi-1", StatusKey: "todo", TriageState: triage})
		_, err := svc.Update(context.Background(), "ws", "wi-1",
			WorkItemPatch{AssigneeID: &newAssignee, AssigneeType: strp("agent"), TriageState: strp("")}, 0, "u1")
		require.NoError(t, err)
		assert.Equal(t, 1, repo.updated)
	})

	t.Run("triage+unlocked field(title)→allowed", func(t *testing.T) {
		svc, repo := triService(WorkItem{ID: "wi-1", StatusKey: "todo", TriageState: triage})
		_, err := svc.Update(context.Background(), "ws", "wi-1",
			WorkItemPatch{Title: strp("new title")}, 0, "u1")
		require.NoError(t, err)
		assert.Equal(t, 1, repo.updated)
	})

	t.Run("non-triage + change assignee → allowed", func(t *testing.T) {
		svc, repo := triService(WorkItem{ID: "wi-1", StatusKey: "todo"})
		_, err := svc.Update(context.Background(), "ws", "wi-1",
			WorkItemPatch{AssigneeID: &newAssignee, AssigneeType: strp("agent")}, 0, "u1")
		require.NoError(t, err)
		assert.Equal(t, 1, repo.updated)
	})

	t.Run("work item does not exist→404", func(t *testing.T) {
		svc, _ := triService(WorkItem{ID: "wi-1"})
		repo := svc.repo.(*triFakeRepo)
		repo.item = WorkItem{}
		missing := &triMissingRepo{}
		svc2 := NewWorkItemService(missing, nil, nil)
		_, err := svc2.Update(context.Background(), "ws", "wi-x", WorkItemPatch{Title: strp("t")}, 0, "u1")
		require.Error(t, err)
		ae, _ := err.(*apierr.Error)
		assert.Equal(t, apierr.NotFound, ae.Code)
	})
}

type triMissingRepo struct{ Repo }

func (f *triMissingRepo) GetWorkItem(_ context.Context, _, _ string) (WorkItem, bool, error) {
	return WorkItem{}, false, nil
}

func TestCheckBatchTriageLock(t *testing.T) {
	wiUUID := "00000000-0000-4000-8000-0000000000f1"
	svc, repo := triService(WorkItem{ID: wiUUID, TriageState: strp("pending")})
	patch := WorkItemPatch{AssigneeID: strp("00000000-0000-4000-8000-0000000000a1"), AssigneeType: strp("agent")}
	repo.count = 1

	err := svc.CheckBatchTriageLock(context.Background(), "ws", []string{wiUUID}, patch)
	require.Error(t, err)
	ae, _ := err.(*apierr.Error)
	assert.Equal(t, apierr.Validation, ae.Code)

	svc2, _ := triService(WorkItem{})
	require.NoError(t, svc2.CheckBatchTriageLock(context.Background(), "ws", []string{wiUUID}, WorkItemPatch{Title: strp("t")}))
}

type triEngine struct{ called int }

func (e *triEngine) OnComment(_ context.Context, _ TriggerInput, _ string) (string, string, error) {
	e.called++
	return dispatchreason.Queued, "", nil
}
func (e *triEngine) Preview(_ context.Context, _ TriggerInput) ([]PreviewHit, []PreviewHit, error) {
	e.called++
	return nil, nil, nil
}

type triCommentRepo struct {
	Repo
	item    WorkItem
	comment Comment
}

func (f *triCommentRepo) GetWorkItem(_ context.Context, _, _ string) (WorkItem, bool, error) {
	return f.item, true, nil
}
func (f *triCommentRepo) GetComment(_ context.Context, id string) (Comment, bool, error) {
	if f.comment.ID == id {
		return f.comment, true, nil
	}
	return Comment{ID: id, WorkItemID: f.item.ID}, true, nil
}
func (f *triCommentRepo) CreateComment(_ context.Context, in NewComment) (Comment, error) {
	return Comment{ID: "c-1", WorkItemID: in.WorkItemID, Content: redactx.Sanitize(in.Content)}, nil
}
func (f *triCommentRepo) TouchWorkItem(_ context.Context, _ string) error { return nil }
func (f *triCommentRepo) UpsertSubscriber(_ context.Context, _, _, _ string) error {
	return nil
}

func TestCommentCreateTriageGate(t *testing.T) {
	agentID := "00000000-0000-4000-8000-0000000000a1"
	mention := "[@nova](mention://agent/" + agentID + ")"

	run := func(triage *string, content string) (*AdmissionResult, *triEngine) {
		repo := &triCommentRepo{item: WorkItem{ID: "wi-1", WorkspaceID: "ws", TriageState: triage}}
		svc := NewCommentService(repo, nil, nil)
		eng := &triEngine{}
		SetTriggerEngine(eng)
		t.Cleanup(func() { SetTriggerEngine(nil) })
		_, admission, err := svc.Create(context.Background(), NewComment{
			WorkItemID: "wi-1", WorkspaceID: "ws",
			AuthorType: "user", AuthorID: "u1", Content: content,
		})
		require.NoError(t, err)
		return admission, eng
	}

	t.Run("triage + plain comment → blocked/work_item_in_triage, not into the engine", func(t *testing.T) {
		admission, eng := run(strp("pending"), "please handle")
		require.NotNil(t, admission)
		assert.Equal(t, dispatchreason.Blocked, admission.Result)
		assert.Equal(t, dispatchreason.WorkItemInTriage, admission.ReasonCode)
		assert.Zero(t, eng.called)
	})

	t.Run("triage + explicit @agent → allowed into the engine", func(t *testing.T) {
		admission, eng := run(strp("pending"), mention+"take a look at this")
		require.NotNil(t, admission)
		assert.Equal(t, dispatchreason.Queued, admission.Result)
		assert.Equal(t, 1, eng.called)
	})

	t.Run("non-triage plain comments → enter the engine", func(t *testing.T) {
		admission, eng := run(nil, "please handle")
		require.NotNil(t, admission)
		assert.Equal(t, dispatchreason.Queued, admission.Result)
		assert.Equal(t, 1, eng.called)
	})
}

func TestPreviewTriggerTriageGate(t *testing.T) {
	eng := &triEngine{}
	SetTriggerEngine(eng)
	t.Cleanup(func() { SetTriggerEngine(nil) })

	triggers, supps, err := PreviewTrigger(context.Background(), TriggerInput{
		AuthorType: "user", Content: "go",
		AssigneeType: "agent", AssigneeID: "00000000-0000-4000-8000-0000000000a1",
		TriageState: strp("pending"),
	})
	require.NoError(t, err)
	assert.Empty(t, triggers)
	require.Len(t, supps, 1)
	assert.Equal(t, dispatchreason.WorkItemInTriage, supps[0].Reason)
	assert.Zero(t, eng.called, "triage suppression does not enter the engine preview")

	_, _, err = PreviewTrigger(context.Background(), TriggerInput{
		AuthorType: "user", Content: "[@n](mention://agent/00000000-0000-4000-8000-0000000000a1)",
		TriageState: strp("pending"),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, eng.called)

	_, _, err = PreviewTrigger(context.Background(), TriggerInput{
		AuthorType: "user", Content: "go",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, eng.called)
}

func TestWorkItemServiceWriteTriggerProbe(t *testing.T) {
	svc, _ := triService(WorkItem{
		ID: "wi-1", TriageState: strp("pending"),
		AssigneeType: "agent", AssigneeID: "00000000-0000-4000-8000-0000000000a1",
	})
	d := svc.WriteTriggerProbe(context.Background(), "ws", "wi-1", WriteProbeFacts{
		ChangeKind: domain.ProbeChangeAssignee,
	})
	assert.False(t, d.Trigger)
	assert.Equal(t, dispatchreason.WorkItemInTriage, d.ReasonCode)

	svc2, _ := triService(WorkItem{ID: "wi-2", AssigneeType: "agent", AssigneeID: "00000000-0000-4000-8000-0000000000a1"})
	d = svc2.WriteTriggerProbe(context.Background(), "ws", "wi-2", WriteProbeFacts{
		ChangeKind: domain.ProbeChangeAssignee,
	})
	assert.True(t, d.Trigger)
	assert.Empty(t, d.ReasonCode)
}
