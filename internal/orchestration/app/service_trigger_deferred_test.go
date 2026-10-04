package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/dispatchreason"
	"orkome/internal/contract/events"
	"orkome/internal/orchestration/domain"
)

type fakeTriggerRepo struct {
	Repo
	agents      []Agent
	active      bool
	merge       bool
	inserted    []Task
	deferred    []Task
	insertErr   error
	promoted    []Task
	promoteErr  error
	promoteHits int
}

func (f *fakeTriggerRepo) ListAgents(_ context.Context, _ string) ([]Agent, error) {
	return f.agents, nil
}
func (f *fakeTriggerRepo) ListSquadFacts(_ context.Context, _ string) ([]domain.SquadFacts, error) {
	return nil, nil
}
func (f *fakeTriggerRepo) HistoryAgentIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (f *fakeTriggerRepo) ListInvocationTargets(_ context.Context, _ string) (map[string][]InvocationTarget, error) {
	return map[string][]InvocationTarget{}, nil
}
func (f *fakeTriggerRepo) TrySteer(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *fakeTriggerRepo) MergeIntoPending(_ context.Context, _, _, _, _ string) (bool, error) {
	return f.merge, nil
}
func (f *fakeTriggerRepo) HasActiveTask(_ context.Context, _, _, _ string) (bool, error) {
	return f.active, nil
}
func (f *fakeTriggerRepo) InsertTask(_ context.Context, t Task) (Task, error) {
	t.ID = "q-" + t.AgentID
	f.inserted = append(f.inserted, t)
	return t, nil
}
func (f *fakeTriggerRepo) InsertDeferredTask(_ context.Context, t Task) (Task, error) {
	if f.insertErr != nil {
		return Task{}, f.insertErr
	}
	t.ID = "d-" + t.AgentID
	f.deferred = append(f.deferred, t)
	return t, nil
}
func (f *fakeTriggerRepo) PromoteDeferred(_ context.Context) ([]Task, error) {
	f.promoteHits++
	if f.promoteErr != nil {
		return nil, f.promoteErr
	}
	return f.promoted, nil
}
func (f *fakeTriggerRepo) CompleteRun(_ context.Context, _, _, _, _ string, _ UsageReport) error {
	return nil
}
func (f *fakeTriggerRepo) FailRun(_ context.Context, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *fakeTriggerRepo) SettlePendingSupplements(_ context.Context, _ string) error {
	return nil
}

type recorderEmitter struct{ emitted []events.Envelope }

func (r *recorderEmitter) Emit(_ context.Context, e events.Envelope) {
	r.emitted = append(r.emitted, e)
}

func triggerFixture(wsID, wiID, agentID string) CommentInput {
	return CommentInput{
		WorkspaceID: wsID, WorkItemID: wiID,
		AuthorType: "user", AuthorID: "u1", Content: "handle this",
		AssigneeType: "agent", AssigneeID: agentID,
	}
}

func TestOnComment_ActiveRunning_RegistersDeferred(t *testing.T) {
	repo := &fakeTriggerRepo{agents: []Agent{{ID: "a1", WorkspaceID: "ws", Name: "worker"}}, active: true}
	svc := NewTriggerService(repo, &recorderEmitter{})

	result, reason, err := svc.OnComment(context.Background(), triggerFixture("ws", "wi", "a1"), "c-77")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Deferred, result, "miss=active should return deferred (internal archive 05 §4.4)")
	assert.Equal(t, dispatchreason.AlreadyActive, reason)
	require.Len(t, repo.deferred, 1, "one deferred task row should land")
	d := repo.deferred[0]
	assert.Equal(t, domain.Deferred, d.Status)
	assert.Equal(t, "ws", d.WorkspaceID)
	assert.Equal(t, "wi", d.WorkItemID)
	assert.Equal(t, "a1", d.AgentID)
	assert.Equal(t, "c-77", d.Context["trigger_comment_id"], "trigger context must be complete (B3② cancel-by-comment dependency)")
	assert.Equal(t, dispatchreason.AlreadyActive, d.Context["deferred_reason"])
	assert.Equal(t, domain.EvidenceMemberComment, d.TriggerEvidenceKind)
	assert.Empty(t, repo.inserted, "should not take the newly-created queued path")
}

func TestOnComment_NoActive_InsertsQueued(t *testing.T) {
	repo := &fakeTriggerRepo{agents: []Agent{{ID: "a1", WorkspaceID: "ws", Name: "worker"}}}
	svc := NewTriggerService(repo, &recorderEmitter{})

	result, _, err := svc.OnComment(context.Background(), triggerFixture("ws", "wi", "a1"), "c-1")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Queued, result)
	require.Len(t, repo.inserted, 1)
	assert.Equal(t, domain.Queued, repo.inserted[0].Status)
	assert.Empty(t, repo.deferred)
}

func TestOnComment_Pending_Coalesces(t *testing.T) {
	repo := &fakeTriggerRepo{agents: []Agent{{ID: "a1", WorkspaceID: "ws", Name: "worker"}}, merge: true}
	svc := NewTriggerService(repo, &recorderEmitter{})

	result, _, err := svc.OnComment(context.Background(), triggerFixture("ws", "wi", "a1"), "c-2")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Coalesced, result)
	assert.Empty(t, repo.deferred)
	assert.Empty(t, repo.inserted)
}

func TestOnComment_DeferredFenceHit_RerollsMerge(t *testing.T) {
	repo := &fakeTriggerRepo{
		agents: []Agent{{ID: "a1", WorkspaceID: "ws", Name: "worker"}}, active: true,
		insertErr: domain.ErrFenceHit,
	}
	svc := NewTriggerService(repo, &recorderEmitter{})

	result, reason, err := svc.OnComment(context.Background(), triggerFixture("ws", "wi", "a1"), "c-3")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Blocked, result, "re-roll merge still miss → converges to blocked")
	assert.Equal(t, dispatchreason.AlreadyActive, reason)

	repo.merge = true
	result, _, err = svc.OnComment(context.Background(), triggerFixture("ws", "wi", "a1"), "c-4")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Coalesced, result)
}

func TestSettlement_TerminalTriggersPromotion(t *testing.T) {
	repo := &fakeTriggerRepo{
		agents:   []Agent{{ID: "a1", WorkspaceID: "ws", Name: "worker"}},
		promoted: []Task{{ID: "d1", WorkspaceID: "ws", AgentID: "a1", WorkItemID: "wi", Status: domain.Queued}},
	}
	emit := &recorderEmitter{}
	settle := NewSettlementService(repo, emit, nil, nil, nil)

	task := Task{ID: "t1", WorkspaceID: "ws", AgentID: "a1", WorkItemID: "wi", Status: domain.Running}
	out := EngineOutput{Outcome: "completed", Output: "done", SessionRef: "session:x"}
	require.NoError(t, settle.Settle(context.Background(), task, repo.agents[0], out, nil))

	assert.Equal(t, 1, repo.promoteHits, "complete should trigger promotion after commit (internal archive 05 §2.7)")
	var queued []events.Envelope
	for _, e := range emit.emitted {
		if e.Event == events.TaskQueued {
			queued = append(queued, e)
		}
	}
	require.Len(t, queued, 1, "promoted rows should emit task.queued")
	p, ok := queued[0].Payload.(TaskPayload)
	require.True(t, ok)
	assert.Equal(t, "d1", p.ID)
	assert.Equal(t, domain.Queued, p.Status)
}

func TestSettlement_PromotionFailureBestEffort(t *testing.T) {
	repo := &fakeTriggerRepo{
		agents:     []Agent{{ID: "a1", WorkspaceID: "ws", Name: "worker"}},
		promoteErr: errors.New("fence conflict (concurrent insert)"),
	}
	settle := NewSettlementService(repo, &recorderEmitter{}, nil, nil, nil)
	task := Task{ID: "t1", WorkspaceID: "ws", AgentID: "a1", WorkItemID: "wi", Status: domain.Running}
	require.NoError(t, settle.Settle(context.Background(), task, repo.agents[0],
		EngineOutput{Outcome: "completed", Output: "done"}, nil),
		"promotion failure must not infect terminal settlement")
}

func TestSettlement_FailAlsoPromotes(t *testing.T) {
	repo := &fakeTriggerRepo{agents: []Agent{{ID: "a1", WorkspaceID: "ws", Name: "worker"}}}
	settle := NewSettlementService(repo, &recorderEmitter{}, nil, nil, nil)
	task := Task{ID: "t1", WorkspaceID: "ws", AgentID: "a1", WorkItemID: "wi", Status: domain.Running}
	err := settle.Settle(context.Background(), task, repo.agents[0], EngineOutput{}, &EngineError{Reason: "agent_provider.timeout", Detail: "x"})
	require.NoError(t, err)
	assert.Equal(t, 1, repo.promoteHits, "promotion also invoked after failure terminal state")
}
