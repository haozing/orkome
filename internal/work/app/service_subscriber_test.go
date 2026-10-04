package app

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/work/domain"
)

const (
	subCreator = "00000000-0000-4000-8000-0000000000c1"
	subTplA    = "00000000-0000-4000-8000-0000000000a1"
	subTplB    = "00000000-0000-4000-8000-0000000000a2"
	subRoot    = "00000000-0000-4000-8000-0000000000d1"
)

func TestInitialSubscribers(t *testing.T) {
	subs, err := initialSubscribersOf(subCreator, NewWorkItem{
		InitialSubscribers: AutopilotTemplateSubscribers([]string{subTplA, strings.ToUpper(subTplB), subCreator}),
		Delegation:         domain.DelegationChain{OriginType: "agent_create", ChainRootUserID: subRoot, ChainRootDirectHuman: true},
	})
	require.NoError(t, err)
	require.Len(t, subs, 4)

	assert.Equal(t, SubscriberWrite{subCreator, domain.SubSourceCreator, domain.NotifyLevelAll}, subs[0])
	assert.Equal(t, SubscriberWrite{subTplA, domain.SubSourceAutopilotTemplate, domain.NotifyLevelAll}, subs[1])
	assert.Equal(t, SubscriberWrite{subTplB, domain.SubSourceAutopilotTemplate, domain.NotifyLevelAll}, subs[2])

	assert.Equal(t, SubscriberWrite{subRoot, domain.SubSourceDelegation, domain.NotifyLevelDelegated}, subs[3])

	subs, err = initialSubscribersOf(subCreator, NewWorkItem{
		Delegation: domain.DelegationChain{OriginType: "agent_create", ChainRootUserID: subCreator, ChainRootDirectHuman: true},
	})
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, domain.SubSourceCreator, subs[0].Source)
	assert.Equal(t, domain.NotifyLevelAll, subs[0].Level)

	subs, err = initialSubscribersOf("", NewWorkItem{
		Delegation: domain.DelegationChain{OriginType: "agent_create", ChainRootUserID: subRoot, ChainRootDirectHuman: true},
	})
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, domain.SubSourceDelegation, subs[0].Source)

	subs, err = initialSubscribersOf(subCreator, NewWorkItem{})
	require.NoError(t, err)
	require.Len(t, subs, 1)
}

func TestInitialSubscribersInvalid(t *testing.T) {
	for _, in := range []NewWorkItem{
		{InitialSubscribers: []SubscriberSpec{{UserID: subTplA, Source: "creator"}}},
		{InitialSubscribers: []SubscriberSpec{{UserID: "nope", Source: domain.SubSourceAutopilotTemplate}}},
	} {
		_, err := initialSubscribersOf(subCreator, in)
		require.Error(t, err)
		ae, ok := err.(*apierr.Error)
		require.True(t, ok)
		assert.Equal(t, apierr.Validation, ae.Code)
	}
}

type fakeUserSubRepo struct {
	Repo
	itemWS map[string]string
	subs   map[[2]string]bool
	calls  [][2]string
}

func (f *fakeUserSubRepo) DeleteAllSubscriptionsByUser(_ context.Context, ws, uid string) (int64, error) {
	f.calls = append(f.calls, [2]string{ws, uid})
	var n int64
	for k := range f.subs {
		if f.itemWS[k[0]] == ws && k[1] == uid {
			delete(f.subs, k)
			n++
		}
	}
	return n, nil
}

func TestDeleteAllSubscriptionsByUser_OnlyOwnWorkspaceRows(t *testing.T) {
	repo := &fakeUserSubRepo{
		itemWS: map[string]string{"wi-1": "ws-1", "wi-2": "ws-1", "wi-3": "ws-2"},
		subs: map[[2]string]bool{
			{"wi-1", "u-1"}: true,
			{"wi-2", "u-1"}: true,
			{"wi-3", "u-1"}: true,
			{"wi-1", "u-2"}: true,
		},
	}
	svc := NewWorkItemService(repo, nil, nil)
	ctx := context.Background()

	n, err := svc.DeleteAllSubscriptionsByUser(ctx, "ws-1", "u-1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "returns the number of deleted rows")
	assert.Equal(t, map[[2]string]bool{
		{"wi-3", "u-1"}: true,
		{"wi-1", "u-2"}: true,
	}, repo.subs, "delete only own + this-workspace subscription rows")
	require.Len(t, repo.calls, 1)
	assert.Equal(t, [2]string{"ws-1", "u-1"}, repo.calls[0], "app passes params through unchanged")

	n, err = svc.DeleteAllSubscriptionsByUser(ctx, "ws-1", "u-1")
	require.NoError(t, err)
	assert.Zero(t, n, "repeated clearing = idempotent empty delete")
}
