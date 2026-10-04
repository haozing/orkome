package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

type fakeSquadRepo struct {
	Repo
	tasks   map[string]Task
	members map[string][]SquadMemberTaskRow
	leaders map[string]string
	roles   map[string]string
}

func newFakeSquadRepo() *fakeSquadRepo {
	return &fakeSquadRepo{
		tasks: map[string]Task{}, members: map[string][]SquadMemberTaskRow{},
		leaders: map[string]string{}, roles: map[string]string{},
	}
}

func (f *fakeSquadRepo) GetTask(_ context.Context, ws, id string) (Task, bool, error) {
	t, ok := f.tasks[id]
	return t, ok && t.WorkspaceID == ws, nil
}

func (f *fakeSquadRepo) ListSquadMemberTasks(_ context.Context, ws, squadID string) ([]SquadMemberTaskRow, error) {
	return f.members[squadID], nil
}

func (f *fakeSquadRepo) SquadLeader(_ context.Context, ws, squadID string) (string, bool, error) {
	l, ok := f.leaders[squadID]
	return l, ok, nil
}

func (f *fakeSquadRepo) UpdateSquadMemberRole(_ context.Context, ws, squadID, agentID, role string) (bool, error) {
	k := squadID + "|" + agentID
	if _, ok := f.roles[k]; !ok {
		return false, nil
	}
	f.roles[k] = role
	return true, nil
}

func (f *fakeSquadRepo) AddSquadMember(_ context.Context, ws, squadID, agentID, role string) error {
	f.roles[squadID+"|"+agentID] = role
	return nil
}

type fakeRecorder struct {
	calls []SquadEvaluation
	err   error
}

func (f *fakeRecorder) RecordSquadEvaluation(_ context.Context, e SquadEvaluation) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, e)
	return nil
}

func TestSquadMembersStatusDerivation(t *testing.T) {
	repo := newFakeSquadRepo()
	repo.members["sq1"] = []SquadMemberTaskRow{
		{AgentID: "a1", Name: "leader", Role: "leader", TaskID: "t1", TaskStatus: "running"},
		{AgentID: "a2", Name: "worker", Role: "member", TaskID: "t2", TaskStatus: "queued"},
		{AgentID: "a2", Name: "worker", Role: "member", TaskID: "t3", TaskStatus: "completed"},
		{AgentID: "a3", Name: "idle-one", Role: "member", TaskID: "", TaskStatus: ""},
	}
	svc := NewSquadService(repo, nil)

	out, err := svc.MembersStatus(context.Background(), "ws", "sq1")
	require.NoError(t, err)
	require.Len(t, out, 3)
	assert.Equal(t, "running", out[0].Status)
	assert.Equal(t, "queued", out[1].Status)
	require.Len(t, out[1].ActiveTasks, 2, "expanded rows aggregate by member")
	assert.Equal(t, "idle", out[2].Status)
	assert.Empty(t, out[2].ActiveTasks)
}

func TestSquadRecordEvaluationGates(t *testing.T) {
	base := Task{ID: "tk1", WorkspaceID: "ws", AgentID: "ag1", WorkItemID: "wi1",
		IsLeaderTask: true, SquadID: "sq1"}

	t.Run("lands in the ledger after the triple gate", func(t *testing.T) {
		repo := newFakeSquadRepo()
		repo.tasks["tk1"] = base
		repo.leaders["sq1"] = "ag1"
		rec := &fakeRecorder{}
		svc := NewSquadService(repo, rec)

		require.NoError(t, svc.RecordEvaluation(context.Background(), "ws", "wi1",
			SquadEvalReport{TaskID: "tk1", Outcome: "no_action", Reason: "no action required"}))
		require.Len(t, rec.calls, 1)

		assert.Equal(t, "ag1", rec.calls[0].LeaderAgentID)
		assert.Equal(t, "sq1", rec.calls[0].SquadID)
		assert.Equal(t, "tk1", rec.calls[0].TaskID)
		assert.Equal(t, "no_action", rec.calls[0].Verdict)
	})

	t.Run("invalid outcome", func(t *testing.T) {
		svc := NewSquadService(newFakeSquadRepo(), &fakeRecorder{})
		err := svc.RecordEvaluation(context.Background(), "ws", "wi1",
			SquadEvalReport{TaskID: "tk1", Outcome: "maybe"})
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
	})

	t.Run("task does not exist", func(t *testing.T) {
		svc := NewSquadService(newFakeSquadRepo(), &fakeRecorder{})
		err := svc.RecordEvaluation(context.Background(), "ws", "wi1",
			SquadEvalReport{TaskID: "nope", Outcome: "action"})
		assert.Equal(t, apierr.NotFound, err.(*apierr.Error).Code)
	})

	t.Run("cross-work-item tasks rejected (ownership gate precedes other references)", func(t *testing.T) {
		repo := newFakeSquadRepo()
		repo.tasks["tk1"] = base
		svc := NewSquadService(repo, &fakeRecorder{})
		err := svc.RecordEvaluation(context.Background(), "ws", "wi-other",
			SquadEvalReport{TaskID: "tk1", Outcome: "action"})
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
	})

	t.Run("non-leader tasks rejected", func(t *testing.T) {
		repo := newFakeSquadRepo()
		nt := base
		nt.IsLeaderTask = false
		repo.tasks["tk1"] = nt
		svc := NewSquadService(repo, &fakeRecorder{})
		err := svc.RecordEvaluation(context.Background(), "ws", "wi1",
			SquadEvalReport{TaskID: "tk1", Outcome: "action"})
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
	})

	t.Run("leader already rotated, rejected (anti-forgery gate 3)", func(t *testing.T) {
		repo := newFakeSquadRepo()
		repo.tasks["tk1"] = base
		repo.leaders["sq1"] = "ag2"
		rec := &fakeRecorder{}
		svc := NewSquadService(repo, rec)
		err := svc.RecordEvaluation(context.Background(), "ws", "wi1",
			SquadEvalReport{TaskID: "tk1", Outcome: "action"})
		assert.Equal(t, apierr.Forbidden, err.(*apierr.Error).Code)
		assert.Empty(t, rec.calls)
	})

	t.Run("recorder not wired, 503 face", func(t *testing.T) {
		repo := newFakeSquadRepo()
		repo.tasks["tk1"] = base
		repo.leaders["sq1"] = "ag1"
		svc := NewSquadService(repo, nil)
		err := svc.RecordEvaluation(context.Background(), "ws", "wi1",
			SquadEvalReport{TaskID: "tk1", Outcome: "action"})
		assert.Equal(t, apierr.Internal, err.(*apierr.Error).Code)
	})
}

func TestSquadRoleValidation(t *testing.T) {
	repo := newFakeSquadRepo()
	repo.roles["sq1|ag1"] = "member"
	svc := NewSquadService(repo, nil)

	require.NoError(t, svc.AddMember(context.Background(), "ws", "sq1", "ag2", ""))

	err := svc.SetMemberRole(context.Background(), "ws", "sq1", "ag1", string(make([]rune, 81)))
	assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)

	err = svc.SetMemberRole(context.Background(), "ws", "sq1", "ghost", "worker")
	assert.Equal(t, apierr.NotFound, err.(*apierr.Error).Code)
}
