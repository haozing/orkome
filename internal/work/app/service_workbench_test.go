package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/work/domain"
)

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var ae *apierr.Error
	require.Error(t, err)
	require.True(t, errors.As(err, &ae), "err=%v", err)
	return ae.Code
}

type wbFakeRepo struct {
	Repo
	items      map[string]WorkItem
	created    []NewWorkItem
	subs       map[string][]SubscriberWrite
	activities []struct {
		eventID, actorType, actorID, kind string
		payload                           map[string]any
	}
	movedTo int64
	nextPos int64
	err     error
}

func newWBFakeRepo(seed ...WorkItem) *wbFakeRepo {
	f := &wbFakeRepo{items: map[string]WorkItem{}, subs: map[string][]SubscriberWrite{}}
	for _, w := range seed {
		f.items[w.ID] = w
	}
	return f
}

func (f *wbFakeRepo) EnsureStatusSeed(context.Context, string) error { return nil }

func (f *wbFakeRepo) ListStatuses(context.Context, string) ([]StatusDef, error) {
	return []StatusDef{
		{Key: domain.StatusBacklog, Category: "unstarted", IsBuiltin: true},
		{Key: domain.StatusTodo, Category: "unstarted", IsBuiltin: true},
		{Key: domain.StatusInProgress, Category: "started", IsBuiltin: true},
		{Key: domain.StatusInReview, Category: "started", IsBuiltin: true},
		{Key: domain.StatusDone, Category: "done", IsBuiltin: true},
		{Key: domain.StatusCancelled, Category: "closed", IsBuiltin: true},
	}, nil
}

func (f *wbFakeRepo) GetWorkItem(_ context.Context, _, id string) (WorkItem, bool, error) {
	w, ok := f.items[id]
	return w, ok, nil
}

func (f *wbFakeRepo) AdvisoryLockTitle(context.Context, string, string) (func(), error) {
	return func() {}, nil
}

func (f *wbFakeRepo) CountActiveDuplicate(context.Context, string, string, *string, *string, []string, string) (int, error) {
	return 0, nil
}

func (f *wbFakeRepo) CreateWorkItem(_ context.Context, in NewWorkItem) (WorkItem, error) {
	f.created = append(f.created, in)
	w := WorkItem{ID: "child-new", WorkspaceID: in.WorkspaceID, Title: in.Title,
		StatusKey: in.StatusKey, CreatorType: in.CreatorType, CreatorID: in.CreatorID}
	f.items[w.ID] = w
	return w, nil
}

func (f *wbFakeRepo) UpsertSubscribers(_ context.Context, wi string, subs []SubscriberWrite) error {
	f.subs[wi] = append(f.subs[wi], subs...)
	return nil
}

func (f *wbFakeRepo) UpdateWorkItem(_ context.Context, _, id string, p WorkItemPatch, _ int64) (WorkItem, error) {
	w, ok := f.items[id]
	if !ok {
		return w, apierr.New(apierr.NotFound, "work item not found")
	}
	if p.StatusKey != nil {
		w.StatusKey = *p.StatusKey
	}
	f.items[id] = w
	return w, nil
}

func (f *wbFakeRepo) NextPositionInStatus(context.Context, string, string) (int64, error) {
	return f.nextPos, nil
}

func (f *wbFakeRepo) MovePosition(_ context.Context, _, _ string, pos int64) error {
	f.movedTo = pos
	return nil
}

func (f *wbFakeRepo) AppendActivity(_ context.Context, eventID, _, _, actorType, actorID, kind string, payload map[string]any) error {
	f.activities = append(f.activities, struct {
		eventID, actorType, actorID, kind string
		payload                           map[string]any
	}{eventID, actorType, actorID, kind, payload})
	return nil
}

func newWBService(f *wbFakeRepo) *WorkItemService {
	return NewWorkItemService(f, nil, nil)
}

const (
	wbRootUser = "00000000-0000-4000-8000-0000000000d1"
	wbAgent    = "00000000-0000-4000-8000-0000000000b1"
)

func TestCreateChildByAgent(t *testing.T) {
	parent := WorkItem{ID: "wi-parent", WorkspaceID: "ws-1", StatusKey: domain.StatusTodo}
	f := newWBFakeRepo(parent)
	s := newWBService(f)

	w, err := s.CreateChildByAgent(context.Background(), "ws-1", "wi-parent",
		"subtask", "description", wbAgent, domain.DelegationChain{
			OriginType: "agent_create", ChainRootUserID: wbRootUser, ChainRootDirectHuman: true})
	require.NoError(t, err)
	assert.Equal(t, "child-new", w.ID)
	require.Len(t, f.created, 1)
	in := f.created[0]
	assert.Equal(t, "agent", in.CreatorType, "creator = agent Principal (server-injected)")
	assert.Equal(t, wbAgent, in.CreatorID)
	assert.Equal(t, "wi-parent", in.ParentID)
	assert.Equal(t, domain.StatusTodo, in.StatusKey, "stage=todo: no triage landed (proposed is never terminal and would stall the children_done frontier)")
	assert.Equal(t, "agent_create", in.Delegation.OriginType)

	subs := f.subs["child-new"]
	require.Len(t, subs, 1)
	assert.Equal(t, SubscriberWrite{wbRootUser, domain.SubSourceDelegation, domain.NotifyLevelDelegated}, subs[0])

	_, err = s.CreateChildByAgent(context.Background(), "ws-1", "nope", "t", "", wbAgent, domain.DelegationChain{})
	assert.Equal(t, apierr.NotFound, codeOf(t, err))
}

func TestUpdateStatusByAgent(t *testing.T) {
	item := WorkItem{ID: "wi-1", WorkspaceID: "ws-1", StatusKey: domain.StatusTodo}
	f := newWBFakeRepo(item)
	s := newWBService(f)
	ctx := context.Background()

	f.nextPos = 41
	w, err := s.UpdateStatusByAgent(ctx, "ws-1", "wi-1", wbAgent, domain.StatusInProgress)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInProgress, w.StatusKey)
	assert.Equal(t, int64(42), f.movedTo, "column-swap relocation (internal archive 05 §3.2 position strategy)")

	f.movedTo = 0
	_, err = s.UpdateStatusByAgent(ctx, "ws-1", "wi-1", wbAgent, domain.StatusInProgress)
	require.NoError(t, err)
	assert.Equal(t, int64(0), f.movedTo, "same state idempotent pass-through")

	_, err = s.UpdateStatusByAgent(ctx, "ws-1", "wi-1", wbAgent, "wat")
	assert.Equal(t, apierr.Validation, codeOf(t, err))

	triaged := item
	ts := "triaging"
	triaged.TriageState = &ts
	f2 := newWBFakeRepo(triaged)
	_, err = newWBService(f2).UpdateStatusByAgent(ctx, "ws-1", "wi-1", wbAgent, domain.StatusDone)
	assert.Equal(t, apierr.Validation, codeOf(t, err))

	_, err = s.UpdateStatusByAgent(ctx, "ws-1", "gone", wbAgent, domain.StatusDone)
	assert.Equal(t, apierr.NotFound, codeOf(t, err))
}

func TestRecordSquadEvaluationByAgent(t *testing.T) {
	f := newWBFakeRepo()
	s := newWBService(f)
	ctx := context.Background()

	err := s.RecordSquadEvaluationByAgent(ctx, SquadEvaluationRecord{
		WorkspaceID: "ws-1", WorkItemID: "wi-1", LeaderAgentID: wbAgent,
		SquadID: "sq-1", TaskID: "task-9", Member: "scout",
		Verdict: "excellent", Rationale: "r"})
	assert.Equal(t, apierr.Validation, codeOf(t, err), "out-of-vocabulary verdict rejected")
	assert.Empty(t, f.activities)

	err = s.RecordSquadEvaluationByAgent(ctx, SquadEvaluationRecord{
		WorkspaceID: "ws-1", WorkItemID: "wi-1", Verdict: "action"})
	assert.Equal(t, apierr.Validation, codeOf(t, err), "missing provenance rejected (anti-forged source)")

	err = s.RecordSquadEvaluationByAgent(ctx, SquadEvaluationRecord{
		WorkspaceID: "ws-1", WorkItemID: "wi-1", LeaderAgentID: wbAgent,
		SquadID: "sq-1", TaskID: "task-9", Member: "scout",
		Verdict: "no_action", Rationale: "no action needed"})
	require.NoError(t, err)
	require.Len(t, f.activities, 1)
	a := f.activities[0]
	assert.Equal(t, "agent", a.actorType)
	assert.Equal(t, wbAgent, a.actorID, "actor = the task row's agent (source anti-forgery 1)")
	assert.Equal(t, "squad_leader_evaluated", a.kind)
	assert.Equal(t, "task-9", a.payload["task_id"], "provenance travels with the payload (anti-forgery 2)")
	assert.Equal(t, "sq-1", a.payload["squad_id"])
	assert.Equal(t, "no_action", a.payload["verdict"])

	err = s.RecordSquadEvaluationByAgent(ctx, SquadEvaluationRecord{
		WorkspaceID: "ws-1", WorkItemID: "wi-1", LeaderAgentID: wbAgent,
		SquadID: "sq-1", TaskID: "task-9", Member: "scout",
		Verdict: "action", Rationale: "retracted"})
	require.NoError(t, err)
	require.Len(t, f.activities, 2)
	assert.Equal(t, f.activities[0].eventID, f.activities[1].eventID,
		"idempotent key (task,member) deterministic — repo lands ON CONFLICT DO NOTHING")

	_ = s.RecordSquadEvaluationByAgent(ctx, SquadEvaluationRecord{
		WorkspaceID: "ws-1", WorkItemID: "wi-1", LeaderAgentID: wbAgent,
		SquadID: "sq-1", TaskID: "task-9", Member: "builder", Verdict: "failed"})
	assert.NotEqual(t, f.activities[0].eventID, f.activities[2].eventID)
}
