package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

type fakePinRepo struct {
	getMember    func(wsID, userID string) (Member, bool)
	listPins     func(wsID, userID string) []Pin
	addPin       func(wsID, userID, itemType, itemID string) (Pin, bool)
	reorderPins  func(order []string) error
	reorderCalls int
}

func (f *fakePinRepo) GetMember(_ context.Context, wsID, userID string) (Member, bool, error) {
	if f.getMember != nil {
		m, ok := f.getMember(wsID, userID)
		return m, ok, nil
	}
	return Member{}, false, nil
}

func (f *fakePinRepo) ListPins(_ context.Context, wsID, userID string) ([]Pin, error) {
	if f.listPins != nil {
		return f.listPins(wsID, userID), nil
	}
	return nil, nil
}

func (f *fakePinRepo) AddPin(_ context.Context, wsID, userID, itemType, itemID string) (Pin, bool, error) {
	if f.addPin != nil {
		p, created := f.addPin(wsID, userID, itemType, itemID)
		return p, created, nil
	}
	return Pin{}, true, nil
}

func (f *fakePinRepo) ReorderPins(_ context.Context, _, _ string, order []string) error {
	f.reorderCalls++
	if f.reorderPins != nil {
		return f.reorderPins(order)
	}
	return nil
}

func (f *fakePinRepo) RemovePin(_ context.Context, _, _, _, _ string) error { return nil }

func pinFixture(pins ...Pin) (*fakePinRepo, *PinService) {
	repo := &fakePinRepo{
		getMember: func(ws, uid string) (Member, bool) {
			return Member{WorkspaceID: ws, UserID: uid, Role: "member"}, true
		},
		listPins: func(string, string) []Pin { return pins },
	}
	return repo, NewPinService(repo)
}

func TestPinService_NonMemberInvisible(t *testing.T) {
	repo, s := pinFixture()
	repo.getMember = func(string, string) (Member, bool) { return Member{}, false }

	_, err := s.List(context.Background(), "ws1", "u1")
	assert.Equal(t, apierr.NotFound, err.(*apierr.Error).Code)
	_, _, err = s.Add(context.Background(), "ws1", "u1", "work_item", "11111111-1111-1111-1111-111111111111")
	assert.Equal(t, apierr.NotFound, err.(*apierr.Error).Code)
}

func TestPinService_AddValidatesTypeAndUUID(t *testing.T) {
	repo, s := pinFixture()
	repo.addPin = func(string, string, string, string) (Pin, bool) {
		t.Fatal("invalid inputs must not reach the repository")
		return Pin{}, true
	}

	_, _, err := s.Add(context.Background(), "ws1", "u1", "agent", "11111111-1111-1111-1111-111111111111")
	require.Error(t, err)
	assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)

	_, _, err = s.Add(context.Background(), "ws1", "u1", "work_item", "not-a-uuid")
	require.Error(t, err)
	assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)

	err = s.Remove(context.Background(), "ws1", "u1", "agent", "11111111-1111-1111-1111-111111111111")
	assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
}

func TestPinService_ReorderRequiresExactFullSet(t *testing.T) {
	pins := []Pin{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}}
	repo, s := pinFixture(pins...)

	for _, order := range [][]string{
		{"p1", "p2"},
		{"p1", "p2", "p3", "p4"},
		{"p1", "p1", "p3"},
		{},
	} {
		err := s.Reorder(context.Background(), "ws1", "u1", order)
		require.Error(t, err, "order=%v should be rejected", order)
		assert.Equal(t, apierr.Validation, err.(*apierr.Error).Code)
	}
	assert.Zero(t, repo.reorderCalls, "invalid shape must not reach the repository")

	require.NoError(t, s.Reorder(context.Background(), "ws1", "u1", []string{"p3", "p1", "p2"}))
	assert.Equal(t, 1, repo.reorderCalls, "allowed only when it is a full reordering")
}
