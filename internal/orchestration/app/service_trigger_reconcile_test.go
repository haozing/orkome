package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/dispatchreason"
	"orkome/internal/orchestration/domain"
)

type fakeReconcileRepo struct {
	Repo
	agents    []Agent
	active    bool
	uncovered []UncoveredComment
	listErr   error
	inserted  []Task
	deferred  []Task
}

func (f *fakeReconcileRepo) ListAgents(_ context.Context, _ string) ([]Agent, error) {
	return f.agents, nil
}
func (f *fakeReconcileRepo) ListSquadFacts(_ context.Context, _ string) ([]domain.SquadFacts, error) {
	return nil, nil
}
func (f *fakeReconcileRepo) HistoryAgentIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeReconcileRepo) ListInvocationTargets(_ context.Context, _ string) (map[string][]InvocationTarget, error) {
	return map[string][]InvocationTarget{}, nil
}
func (f *fakeReconcileRepo) TrySteer(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *fakeReconcileRepo) MergeIntoPending(_ context.Context, _, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *fakeReconcileRepo) HasActiveTask(_ context.Context, _, _, _ string) (bool, error) {
	return f.active, nil
}
func (f *fakeReconcileRepo) InsertTask(_ context.Context, t Task) (Task, error) {
	t.ID = "q-" + t.AgentID
	f.inserted = append(f.inserted, t)
	return t, nil
}
func (f *fakeReconcileRepo) InsertDeferredTask(_ context.Context, t Task) (Task, error) {
	t.ID = "d-" + t.AgentID
	f.deferred = append(f.deferred, t)
	return t, nil
}
func (f *fakeReconcileRepo) ListUncoveredTriggerComments(_ context.Context) ([]UncoveredComment, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.uncovered, nil
}

func uncoveredFixture(commentID, agentID string) UncoveredComment {
	return UncoveredComment{
		CommentID: commentID, WorkspaceID: "ws", WorkItemID: "wi",
		AuthorType: "user", AuthorID: "u1", Content: "please handle",
		AssigneeType: "agent", AssigneeID: agentID,
	}
}

func TestReconcileUncovered_Uncovered_RetriggersWithMarker(t *testing.T) {
	repo := &fakeReconcileRepo{
		agents:    []Agent{{ID: "a1", WorkspaceID: "ws", Name: "worker"}},
		uncovered: []UncoveredComment{uncoveredFixture("c-77", "a1")},
	}
	emit := &recorderEmitter{}
	svc := NewTriggerService(repo, emit)

	n, err := svc.ReconcileUncovered(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "should process 1 unsettled comment")
	require.Len(t, repo.inserted, 1, "should re-take the protocol create-task path")
	task := repo.inserted[0]
	assert.Equal(t, domain.Queued, task.Status)
	assert.Equal(t, "a1", task.AgentID)
	assert.Equal(t, domain.TriggerDeferredFallback, task.Context["trigger"],
		"reconciliation re-fire TriggerKind=deferred_fallback (internal archive 05 §1 vocabulary)")
	assert.Equal(t, true, task.Context["reconcile_attempt"],
		"must carry the reconcile_attempt mark (anti-loop anchor: at most one re-fire per comment)")
	assert.Equal(t, "c-77", task.Context["trigger_comment_id"], "trigger context must be complete (B3② cancel-by-comment dependency)")
	assert.Equal(t, "u1", task.OriginatorUserID, "attribution = comment author (authorization track)")
	assert.Equal(t, "u1", task.AccountableUserID, "attribution = comment author (audit track, dual tracks merged)")
	assert.Equal(t, domain.SrcDirectHuman, task.OriginatorSource, "OriginatorSource stays direct_human")
	assert.Equal(t, domain.EvidenceMemberComment, task.TriggerEvidenceKind)
	assert.Empty(t, repo.deferred)

	require.Len(t, emit.emitted, 1)
	assert.Equal(t, "task.queued", emit.emitted[0].Event)
}

func TestReconcileUncovered_ActiveTask_RegistersDeferredWithMarker(t *testing.T) {
	repo := &fakeReconcileRepo{
		agents:    []Agent{{ID: "a1", WorkspaceID: "ws", Name: "worker"}},
		active:    true,
		uncovered: []UncoveredComment{uncoveredFixture("c-88", "a1")},
	}
	svc := NewTriggerService(repo, &recorderEmitter{})

	n, err := svc.ReconcileUncovered(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.Len(t, repo.deferred, 1, "miss=active should land a deferred registration")
	assert.Empty(t, repo.inserted)
	d := repo.deferred[0]
	assert.Equal(t, domain.Deferred, d.Status)
	assert.Equal(t, domain.TriggerDeferredFallback, d.Context["trigger"])
	assert.Equal(t, true, d.Context["reconcile_attempt"], "deferred registration form also carries the anti-loop marks")
	assert.Equal(t, dispatchreason.AlreadyActive, d.Context["deferred_reason"])
}

func TestReconcileUncovered_NoCandidates_Noop(t *testing.T) {
	repo := &fakeReconcileRepo{agents: []Agent{{ID: "a1", WorkspaceID: "ws"}}}
	svc := NewTriggerService(repo, &recorderEmitter{})

	n, err := svc.ReconcileUncovered(context.Background())
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Empty(t, repo.inserted)
	assert.Empty(t, repo.deferred)
}

func TestReconcileUncovered_ListError_Propagates(t *testing.T) {
	repo := &fakeReconcileRepo{listErr: errors.New("scan failed")}
	svc := NewTriggerService(repo, &recorderEmitter{})

	n, err := svc.ReconcileUncovered(context.Background())
	assert.Zero(t, n)
	require.Error(t, err)
}

func TestReconcileUncovered_ProtocolError_StopsAndReturnsCount(t *testing.T) {
	er := &errRepo{failAfter: 1}
	svc := NewTriggerService(er, &recorderEmitter{})

	n, err := svc.ReconcileUncovered(context.Background())
	assert.Equal(t, 1, n, "first processed, second fails and returns immediately")
	require.Error(t, err)
}

type errRepo struct {
	Repo
	failAfter int
	calls     int
}

func (e *errRepo) ListAgents(_ context.Context, _ string) ([]Agent, error) {
	return []Agent{{ID: "a1", WorkspaceID: "ws"}}, nil
}
func (e *errRepo) ListSquadFacts(_ context.Context, _ string) ([]domain.SquadFacts, error) {
	return nil, nil
}
func (e *errRepo) HistoryAgentIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (e *errRepo) ListInvocationTargets(_ context.Context, _ string) (map[string][]InvocationTarget, error) {
	return map[string][]InvocationTarget{}, nil
}
func (e *errRepo) TrySteer(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, nil
}
func (e *errRepo) MergeIntoPending(_ context.Context, _, _, _, _ string) (bool, error) {
	return false, nil
}
func (e *errRepo) HasActiveTask(_ context.Context, _, _, _ string) (bool, error) {
	return false, nil
}
func (e *errRepo) ListUncoveredTriggerComments(_ context.Context) ([]UncoveredComment, error) {
	return []UncoveredComment{uncoveredFixture("c-1", "a1"), uncoveredFixture("c-2", "a1")}, nil
}
func (e *errRepo) InsertTask(_ context.Context, t Task) (Task, error) {
	e.calls++
	if e.calls > e.failAfter {
		return Task{}, errors.New("insert blew up")
	}
	return t, nil
}

func TestReconcileUncovered_BoundedAtTen(t *testing.T) {
	uncovered := make([]UncoveredComment, 15)
	for i := range uncovered {
		uncovered[i] = uncoveredFixture("c-"+string(rune('a'+i)), "a1")
	}
	repo := &fakeReconcileRepo{
		agents:    []Agent{{ID: "a1", WorkspaceID: "ws"}},
		uncovered: uncovered,
	}
	svc := NewTriggerService(repo, &recorderEmitter{})

	n, err := svc.ReconcileUncovered(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 10, n, "at most 10 processed per round (disposition ledger 7.9 boundedness)")
	assert.Len(t, repo.inserted, 10)
}

func TestReconcileUncovered_NoTriggerTarget_BlockedStillCounted(t *testing.T) {
	u := uncoveredFixture("c-9", "a1")
	u.Content = "[@zhang3](mention://user/0a1b2c)"
	repo := &fakeReconcileRepo{
		agents:    []Agent{{ID: "a1", WorkspaceID: "ws"}},
		uncovered: []UncoveredComment{u},
	}
	svc := NewTriggerService(repo, &recorderEmitter{})

	n, err := svc.ReconcileUncovered(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "scanned and handled (protocol verdict blocked), counted as processed")
	assert.Empty(t, repo.inserted)
	assert.Empty(t, repo.deferred)
}
