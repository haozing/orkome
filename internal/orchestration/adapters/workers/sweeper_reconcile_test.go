package workers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/orchestration/domain"
)

type nullEmitter struct{}

func (nullEmitter) Emit(context.Context, events.Envelope) {}

type fakeJanitorRepo struct {
	orchapp.Repo
	agents    []orchapp.Agent
	uncovered []orchapp.UncoveredComment
	listErr   error
	inserted  []orchapp.Task
}

func (f *fakeJanitorRepo) ListAgents(_ context.Context, _ string) ([]orchapp.Agent, error) {
	return f.agents, nil
}
func (f *fakeJanitorRepo) ListSquadFacts(_ context.Context, _ string) ([]domain.SquadFacts, error) {
	return nil, nil
}
func (f *fakeJanitorRepo) HistoryAgentIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeJanitorRepo) ListInvocationTargets(_ context.Context, _ string) (map[string][]orchapp.InvocationTarget, error) {
	return map[string][]orchapp.InvocationTarget{}, nil
}
func (f *fakeJanitorRepo) TrySteer(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *fakeJanitorRepo) MergeIntoPending(_ context.Context, _, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *fakeJanitorRepo) HasActiveTask(_ context.Context, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *fakeJanitorRepo) InsertTask(_ context.Context, t orchapp.Task) (orchapp.Task, error) {
	t.ID = "q-" + t.AgentID
	f.inserted = append(f.inserted, t)
	return t, nil
}
func (f *fakeJanitorRepo) DeleteStaleSessionCheckpoints(_ context.Context, _ int) (int, error) {
	return 0, nil
}
func (f *fakeJanitorRepo) ListUnsettledDelegatedFailures(_ context.Context) ([]orchapp.Task, error) {
	return nil, nil
}
func (f *fakeJanitorRepo) ListUncoveredTriggerComments(_ context.Context) ([]orchapp.UncoveredComment, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.uncovered, nil
}

func TestRunJanitorOnce_ReconcilesUncoveredComments(t *testing.T) {
	repo := &fakeJanitorRepo{
		agents: []orchapp.Agent{{ID: "a1", WorkspaceID: "ws", Name: "worker"}},
		uncovered: []orchapp.UncoveredComment{{
			CommentID: "c-1", WorkspaceID: "ws", WorkItemID: "wi",
			AuthorType: "user", AuthorID: "u1", Content: "please handle",
			AssigneeType: "agent", AssigneeID: "a1",
		}},
	}
	trigger := orchapp.NewTriggerService(repo, nullEmitter{})
	s := &LivenessSweeper{Repo: repo, Log: logNoop(), Trigger: trigger}

	s.RunJanitorOnce(context.Background())

	require.Len(t, repo.inserted, 1, "low-frequency tier should trigger reconciliation re-fire")
	task := repo.inserted[0]
	assert.Equal(t, domain.TriggerDeferredFallback, task.Context["trigger"])
	assert.Equal(t, true, task.Context["reconcile_attempt"])
	assert.Equal(t, "c-1", task.Context["trigger_comment_id"])
}

func TestRunJanitorOnce_NoUncovered_Noop(t *testing.T) {
	repo := &fakeJanitorRepo{agents: []orchapp.Agent{{ID: "a1", WorkspaceID: "ws"}}}
	trigger := orchapp.NewTriggerService(repo, nullEmitter{})
	s := &LivenessSweeper{Repo: repo, Log: logNoop(), Trigger: trigger}

	s.RunJanitorOnce(context.Background())
	assert.Empty(t, repo.inserted)
}

func TestRunJanitorOnce_ReconcileFailureLoggedNotFatal(t *testing.T) {
	repo := &fakeJanitorRepo{listErr: context.DeadlineExceeded}
	trigger := orchapp.NewTriggerService(repo, nullEmitter{})
	s := &LivenessSweeper{Repo: repo, Log: logNoop(), Trigger: trigger}

	assert.NotPanics(t, func() { s.RunJanitorOnce(context.Background()) })
	assert.Empty(t, repo.inserted)
}
