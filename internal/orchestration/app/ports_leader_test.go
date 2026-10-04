package app

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/dispatchreason"
	"orkome/internal/contract/events"
	"orkome/internal/contract/modelmeta"
	"orkome/internal/orchestration/domain"
)

type leaderFakeRepo struct {
	Repo
	agents    map[string]Agent
	pending   int
	inserted  []Task
	wsMCP     []WorkspaceMCP
	agentMCPs map[string][]AgentMCP
	agentOK   map[string]bool
}

func newLeaderFakeRepo() *leaderFakeRepo {
	return &leaderFakeRepo{
		agents: map[string]Agent{}, agentMCPs: map[string][]AgentMCP{}, agentOK: map[string]bool{},
	}
}

func (f *leaderFakeRepo) GetAgent(_ context.Context, _, id string) (Agent, bool, error) {
	a, ok := f.agents[id]
	return a, ok, nil
}

func (f *leaderFakeRepo) CountPendingFence(context.Context, string, string) (int, error) {
	return f.pending, nil
}

func (f *leaderFakeRepo) InsertTask(_ context.Context, t Task) (Task, error) {
	t.ID = "member-task-1"
	f.inserted = append(f.inserted, t)
	return t, nil
}

func (f *leaderFakeRepo) GetLastSessionRef(context.Context, string, string, string) (string, bool, error) {
	return "", false, nil
}

func (f *leaderFakeRepo) ListWorkspaceMCP(context.Context, string) ([]WorkspaceMCP, error) {
	return f.wsMCP, nil
}

func (f *leaderFakeRepo) ListAgentMCP(_ context.Context, _ string, agentID string) ([]AgentMCP, bool, error) {
	if !f.agentOK[agentID] {
		return nil, false, nil
	}
	return f.agentMCPs[agentID], true, nil
}

func TestEnqueueLeaderDispatch_DelegationAttribution(t *testing.T) {
	modelmeta.Register("fake", func(context.Context, modelmeta.RuntimeConfig, string) (model.BaseChatModel, error) {
		return nil, nil
	})
	f := newLeaderFakeRepo()
	f.agents["m-1"] = Agent{ID: "m-1", WorkspaceID: "ws-1",
		RuntimeConfig: map[string]any{"provider": "fake", "model": "fake-1"}}
	emit := &recorderEmitter{}
	s := NewEnqueueService(f, emit)

	taskID, reason, err := s.EnqueueLeaderDispatch(context.Background(), LeaderDispatchInput{
		WorkspaceID: "ws-1", WorkItemID: "wi-1", SquadID: "sq-1",
		LeaderTaskID: "t-leader", LeaderAgentID: "leader-1",
		MemberAgentID: "m-1", MemberName: "scout", Instruction: "scan",
		OriginatorUserID: "human-1", AccountableUserID: "human-1",
	})
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Queued, reason)
	assert.Equal(t, "member-task-1", taskID)
	require.Len(t, f.inserted, 1)
	it := f.inserted[0]
	assert.Equal(t, "m-1", it.AgentID)
	assert.Equal(t, domain.Queued, it.Status)

	assert.Equal(t, "human-1", it.OriginatorUserID)
	assert.Equal(t, "human-1", it.AccountableUserID)
	assert.Equal(t, domain.SrcDelegation, it.OriginatorSource)
	assert.Equal(t, "leader_dispatch", it.TriggerEvidenceKind)
	assert.Equal(t, "t-leader", it.Context["leader_task_id"])
	assert.Equal(t, "scan", it.Context["instruction"])
	assert.Equal(t, "sq-1", it.Context["squad_id"])
	require.Len(t, emit.emitted, 1)
	assert.Equal(t, events.TaskQueued, emit.emitted[0].Event)
}

func TestEnqueueLeaderDispatch_NoOriginatorCopiesEmpty(t *testing.T) {
	f := newLeaderFakeRepo()
	f.agents["m-1"] = Agent{ID: "m-1", RuntimeConfig: map[string]any{"provider": "fake", "model": "x"}}
	s := NewEnqueueService(f, &recorderEmitter{})
	_, reason, err := s.EnqueueLeaderDispatch(context.Background(), LeaderDispatchInput{
		WorkspaceID: "ws-1", WorkItemID: "wi-1",
		LeaderAgentID: "leader-1", MemberAgentID: "m-1",
		AccountableUserID: "auditor-only",
	})
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Queued, reason)
	require.Len(t, f.inserted, 1)
	assert.Empty(t, f.inserted[0].OriginatorUserID, "authorized columns do not over-claim")
	assert.Equal(t, "auditor-only", f.inserted[0].AccountableUserID, "audit columns keep traceability")
}

func TestEnqueueLeaderDispatch_Gates(t *testing.T) {
	f := newLeaderFakeRepo()
	f.agents["m-1"] = Agent{ID: "m-1", RuntimeConfig: map[string]any{"provider": "fake", "model": "x"}}
	f.agents["m-arch"] = Agent{ID: "m-arch", Archived: true}
	s := NewEnqueueService(f, &recorderEmitter{})
	ctx := context.Background()
	in := func(member string) LeaderDispatchInput {
		return LeaderDispatchInput{WorkspaceID: "ws-1", WorkItemID: "wi-1",
			LeaderAgentID: "leader-1", MemberAgentID: member}
	}

	_, reason, err := s.EnqueueLeaderDispatch(ctx, in("leader-1"))
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Blocked, reason, "self-dispatch rejected (anti self-trigger loop)")
	assert.Empty(t, f.inserted)

	_, reason, err = s.EnqueueLeaderDispatch(ctx, in("m-arch"))
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Blocked, reason, "archive rejected")
	assert.Empty(t, f.inserted)

	_, reason, err = s.EnqueueLeaderDispatch(ctx, in("m-none"))
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Blocked, reason, "member  does not exist  rejected")

	_, reason, err = s.EnqueueLeaderDispatch(ctx, in("m-1"))
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Queued, reason)

	f2 := newLeaderFakeRepo()
	f2.agents["m-1"] = Agent{ID: "m-1", RuntimeConfig: map[string]any{"provider": "fake", "model": "x"}}
	f2.pending = 1
	_, reason, err = NewEnqueueService(f2, &recorderEmitter{}).EnqueueLeaderDispatch(ctx, in("m-1"))
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.AlreadyActive, reason, "I1 fence hit")
	assert.Empty(t, f2.inserted)
}

func TestResolveForEngine(t *testing.T) {
	f := newLeaderFakeRepo()
	f.wsMCP = []WorkspaceMCP{
		{Name: "shared", Transport: "http", URL: "https://ws.example/a", Credentials: map[string]any{"headers": map[string]any{"Authorization": "ws-token"}}},
		{Name: "ws-only", Transport: "http", URL: "https://ws.example/b", Credentials: map[string]any{"headers": map[string]any{"X-T": "b"}}},
	}
	f.agentOK["a-1"] = true
	f.agentMCPs["a-1"] = []AgentMCP{
		{Name: "shared", Transport: "http", URL: "https://agent.example/a", Enabled: true, Credentials: map[string]any{"headers": map[string]any{"Authorization": "agent-token"}}},
		{Name: "off", Transport: "http", URL: "https://agent.example/off", Enabled: false},
	}
	got, err := NewMCPServerService(f).ResolveForEngine(context.Background(), "ws-1", "a-1")
	require.NoError(t, err)

	byName := map[string]ResolvedMCPServer{}
	for _, s := range got {
		byName[s.Name] = s
	}
	require.Len(t, byName, 2, "same-name overwrite + enabled filter")

	assert.Equal(t, "https://agent.example/a", byName["shared"].URL)
	assert.Equal(t, map[string]any{"headers": map[string]any{"Authorization": "agent-token"}}, byName["shared"].Credentials)

	assert.Equal(t, "https://ws.example/b", byName["ws-only"].URL)
	assert.Equal(t, map[string]any{"headers": map[string]any{"X-T": "b"}}, byName["ws-only"].Credentials)

	_, ok := byName["off"]
	assert.False(t, ok)
}
