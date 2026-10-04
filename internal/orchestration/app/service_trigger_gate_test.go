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

func TestInvocationAllowed_Public(t *testing.T) {
	assert.True(t, InvocationAllowed(Agent{Visibility: "public"}, nil, ""))
	assert.True(t, InvocationAllowed(Agent{Visibility: ""}, nil, "u-any"))
}

func TestInvocationAllowed_PublicToTargets(t *testing.T) {
	wsRow := []InvocationTarget{{Type: "workspace"}}
	members := []InvocationTarget{{Type: "member", UserID: "u-1"}}

	assert.True(t, InvocationAllowed(Agent{Visibility: "public_to"}, wsRow, ""), "workspace row = everyone (including empty actor)")
	assert.True(t, InvocationAllowed(Agent{Visibility: "public_to"}, members, "u-1"), "member hit")
	assert.False(t, InvocationAllowed(Agent{Visibility: "public_to"}, members, "u-2"), "member not hit")
	assert.False(t, InvocationAllowed(Agent{Visibility: "public_to"}, members, ""), "empty actor (agent/system author) naturally misses the member match")
	assert.False(t, InvocationAllowed(Agent{Visibility: "public_to"}, nil, "u-1"), "no allowlist row = deny (fail-closed)")
}

func TestInvocationAllowed_PrivateOwnerOrWhitelist(t *testing.T) {
	owner := Agent{Visibility: "private", OwnerUserID: "u-owner"}

	assert.True(t, InvocationAllowed(owner, nil, "u-owner"), "the owner themselves")
	assert.False(t, InvocationAllowed(owner, nil, ""), "empty actor = no human authorizing subject, owner match fails")
	assert.False(t, InvocationAllowed(owner, nil, "u-other"), "not the owner and not allowlisted")
	assert.True(t, InvocationAllowed(owner, []InvocationTarget{{Type: "member", UserID: "u-2"}}, "u-2"),
		"allowlist member hit (private also consumes the allowlist)")
	assert.False(t, InvocationAllowed(owner, []InvocationTarget{{Type: "member", UserID: "u-2"}}, "u-3"),
		"allowlist not hit")
	assert.False(t, InvocationAllowed(Agent{Visibility: "private", OwnerUserID: "u-owner"},
		[]InvocationTarget{{Type: "member", UserID: "u-2"}}, ""), "empty actor: neither owner nor member hits")
}

func TestInvocationAllowed_PrivateIgnoresWorkspaceRow(t *testing.T) {
	assert.False(t, InvocationAllowed(Agent{Visibility: "private", OwnerUserID: "u-o"},
		[]InvocationTarget{{Type: "workspace"}}, "u-x"))
}

func TestInvocationAllowed_UnknownVisibility(t *testing.T) {
	assert.False(t, InvocationAllowed(Agent{Visibility: "friends"}, nil, "u-1"))
}

type gateFakeRepo struct {
	Repo
	agents  []Agent
	targets map[string][]InvocationTarget
	err     error
}

func (f *gateFakeRepo) ListAgents(_ context.Context, _ string) ([]Agent, error) { return f.agents, nil }
func (f *gateFakeRepo) ListSquadFacts(_ context.Context, _ string) ([]domain.SquadFacts, error) {
	return nil, nil
}
func (f *gateFakeRepo) HistoryAgentIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *gateFakeRepo) ListInvocationTargets(_ context.Context, _ string) (map[string][]InvocationTarget, error) {
	return f.targets, f.err
}
func (f *gateFakeRepo) TrySteer(_ context.Context, _, _, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *gateFakeRepo) MergeIntoPending(_ context.Context, _, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *gateFakeRepo) HasActiveTask(_ context.Context, _, _, _ string) (bool, error) {
	return false, nil
}
func (f *gateFakeRepo) InsertTask(_ context.Context, tk Task) (Task, error) {
	tk.ID = "q-" + tk.AgentID
	return tk, nil
}

func mentionInput(wsID, agentID, authorID string) CommentInput {
	return CommentInput{
		WorkspaceID: wsID, WorkItemID: "wi", AuthorType: "user", AuthorID: authorID,
		Content: "[@agent](mention://agent/" + agentID + ") handle this",
	}
}

func TestOnComment_PublicToGateEndToEnd(t *testing.T) {
	repo := &gateFakeRepo{
		agents:  []Agent{{ID: "a1", WorkspaceID: "ws", Name: "guard", Visibility: "public_to"}},
		targets: map[string][]InvocationTarget{"a1": {{Type: "member", UserID: "u-in"}}},
	}
	svc := NewTriggerService(repo, &recorderEmitter{})

	out, reason, err := svc.OnComment(context.Background(), mentionInput("ws", "a1", "u-in"), "c-1")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Queued, out)
	assert.Equal(t, "3.explicit_mention", reason)

	out, reason, err = svc.OnComment(context.Background(), mentionInput("ws", "a1", "u-out"), "c-2")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Blocked, out)
	assert.Equal(t, dispatchreason.InvocationNotAllowed, reason, "I6: rejections uniformly invocation_not_allowed")
}

func TestOnComment_EmptyActorFailClosed(t *testing.T) {
	repo := &gateFakeRepo{
		agents: []Agent{
			{ID: "a1", WorkspaceID: "ws", Name: "guard", Visibility: "public_to"},
			{ID: "a2", WorkspaceID: "ws", Name: "open", Visibility: "public"},
		},
		targets: map[string][]InvocationTarget{"a1": {{Type: "member", UserID: "u-in"}}},
	}
	svc := NewTriggerService(repo, &recorderEmitter{})
	in := mentionInput("ws", "a1", "u-in")
	in.AuthorType = "agent"
	in.AuthorID = "other-agent"

	out, reason, err := svc.OnComment(context.Background(), in, "c-3")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Blocked, out)
	assert.Equal(t, dispatchreason.InvocationNotAllowed, reason)

	in2 := mentionInput("ws", "a2", "u-in")
	in2.AuthorType = "agent"
	in2.AuthorID = "other-agent"
	out, _, err = svc.OnComment(context.Background(), in2, "c-4")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Queued, out, "public gate independent of actor (unchanged)")
}

func TestLoadFacts_TargetsLoadFailureFailsClosed(t *testing.T) {
	boom := errors.New("targets store down")
	repo := &gateFakeRepo{
		agents:  []Agent{{ID: "a1", WorkspaceID: "ws", Name: "x", Visibility: "public"}},
		targets: map[string][]InvocationTarget{},
		err:     boom,
	}
	svc := NewTriggerService(repo, &recorderEmitter{})
	_, _, err := svc.OnComment(context.Background(), mentionInput("ws", "a1", "u-in"), "c-5")
	assert.ErrorIs(t, err, boom)

	_, err = svc.Preview(context.Background(), mentionInput("ws", "a1", "u-in"))
	assert.ErrorIs(t, err, boom)
}

func TestOnComment_PrivateGateEndToEnd(t *testing.T) {
	repo := &gateFakeRepo{
		agents:  []Agent{{ID: "a1", WorkspaceID: "ws", Name: "secret", Visibility: "private", OwnerUserID: "u-owner"}},
		targets: map[string][]InvocationTarget{"a1": {{Type: "member", UserID: "u-in"}}},
	}
	svc := NewTriggerService(repo, &recorderEmitter{})

	out, _, err := svc.OnComment(context.Background(), mentionInput("ws", "a1", "u-owner"), "c-6")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Queued, out, "owner allowed")

	out, _, err = svc.OnComment(context.Background(), mentionInput("ws", "a1", "u-in"), "c-7")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Queued, out, "allowlist member allowed")

	out, reason, err := svc.OnComment(context.Background(), mentionInput("ws", "a1", "u-out"), "c-8")
	require.NoError(t, err)
	assert.Equal(t, dispatchreason.Blocked, out)
	assert.Equal(t, dispatchreason.InvocationNotAllowed, reason)
}
