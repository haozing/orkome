package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/notification/domain"
)

type fakeRepo struct {
	prefs map[string]Preference

	notification map[string][]string

	prefReadErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{prefs: map[string]Preference{}, notification: map[string][]string{}}
}

var _ Repo = (*fakeRepo)(nil)

func (f *fakeRepo) InsertNotification(_ context.Context, ws, uid string, _ *string, _, _, _, dedup string) error {
	f.notification[ws+"/"+uid] = append(f.notification[ws+"/"+uid], dedup)
	return nil
}
func (f *fakeRepo) ListNotification(context.Context, string, string, bool, int, string) ([]Item, string, error) {
	panic("unused")
}
func (f *fakeRepo) UnreadCount(context.Context, string, string) (int, error) { panic("unused") }
func (f *fakeRepo) MarkRead(context.Context, string, string, string, bool) error {
	panic("unused")
}
func (f *fakeRepo) MarkAllRead(context.Context, string, string) error { panic("unused") }
func (f *fakeRepo) Archive(context.Context, string, string, string, bool) error {
	panic("unused")
}
func (f *fakeRepo) ArchiveAll(context.Context, string, string) error { panic("unused") }
func (f *fakeRepo) ArchiveAllRead(context.Context, string, string) (int64, error) {
	panic("unused")
}
func (f *fakeRepo) GetPreference(_ context.Context, ws, uid string) (Preference, error) {
	if f.prefReadErr != nil {
		return Preference{}, f.prefReadErr
	}
	return f.prefs[ws+"/"+uid], nil
}
func (f *fakeRepo) UpsertPreference(_ context.Context, ws, uid string, p Preference) error {
	f.prefs[ws+"/"+uid] = p
	return nil
}
func (f *fakeRepo) Summary(context.Context, string, string) (NotificationSummary, error) {
	panic("unused")
}
func (f *fakeRepo) Facets(context.Context, string, string) ([]Facet, error) { panic("unused") }

func TestNotify_Handle_MuteThreeGroups(t *testing.T) {
	ws, user := "ws-1", "u-1"
	cases := []struct {
		name string
		pref Preference
		in   EventIn
		want bool
	}{
		{"source group hit", Preference{MutedTypes: []string{"task_completed"}},
			EventIn{Type: "task_completed"}, false},
		{"task group hit", Preference{MutedWorkItemIDs: []string{"wi-1"}},
			EventIn{Type: "task_completed", WorkItemID: "wi-1"}, false},
		{"session group hit", Preference{MutedConversationIDs: []string{"sess-1"}},
			EventIn{Type: "comment_created", SessionID: "sess-1"}, false},
		{"session group does not match the other event", Preference{MutedConversationIDs: []string{"sess-1"}},
			EventIn{Type: "comment_created", SessionID: "sess-2"}, true},
		{"no preference", Preference{}, EventIn{Type: "task_completed"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeRepo()
			repo.prefs[ws+"/"+user] = tc.pref
			svc := NewNotifyService(repo, nil)
			tc.in.EventID, tc.in.WorkspaceID, tc.in.DirectUserIDs = "e-1", ws, []string{user}
			require.NoError(t, svc.Handle(context.Background(), tc.in))
			n := len(repo.notification[ws+"/"+user])
			if tc.want {
				assert.Equal(t, 1, n, "not muted should deliver")
			} else {
				assert.Zero(t, n, "mute hits must not deliver")
			}
		})
	}
}

func TestMutedFor_DirectWritePath(t *testing.T) {
	repo := newFakeRepo()
	repo.prefs["ws-1/u-1"] = Preference{MutedTypes: []string{"approval_request"}}
	ctx := context.Background()

	assert.True(t, MutedFor(ctx, repo, "ws-1", "u-1",
		domain.MuteTarget{Type: "approval_request"}), "source group hit should be muted")
	assert.False(t, MutedFor(ctx, repo, "ws-1", "u-1",
		domain.MuteTarget{Type: "eval_report"}), "no hit, not muted")
	assert.False(t, MutedFor(ctx, repo, "ws-1", "u-x",
		domain.MuteTarget{Type: "approval_request"}), "no preference row = not muted")

	repo.prefReadErr = assert.AnError
	assert.False(t, MutedFor(ctx, repo, "ws-1", "u-1",
		domain.MuteTarget{Type: "approval_request"}), "read failures deliver fail-open")
}

func TestNotify_Handle_ActorSelfNotificationFiltered(t *testing.T) {
	// FP-10：投影侧跳过 actor 本人——无论 actor 经由订阅、提及还是直发
	// 进入收件集合，都不再给自己发通知；其他收件人不受影响。
	repo := newFakeRepo()
	svc := NewNotifyService(repo, nil)
	in := EventIn{
		EventID: "e-1", WorkspaceID: "ws-1", Type: "task.completed",
		ActorUserID:      "u-actor",
		MentionedUserIDs: []string{"u-actor", "u-mentioned"},
		DirectUserIDs:    []string{"u-actor", "u-direct"},
	}
	require.NoError(t, svc.Handle(context.Background(), in))

	assert.Empty(t, repo.notification["ws-1/u-actor"], "actor 不自通知")
	assert.Len(t, repo.notification["ws-1/u-mentioned"], 1)
	assert.Len(t, repo.notification["ws-1/u-direct"], 1)

	// actor 为空（agent 结算路径）时不过滤，保持既有行为
	repo2 := newFakeRepo()
	require.NoError(t, NewNotifyService(repo2, nil).Handle(context.Background(), EventIn{
		EventID: "e-2", WorkspaceID: "ws-1", Type: "task.completed",
		DirectUserIDs: []string{"u-originator"},
	}))
	assert.Len(t, repo2.notification["ws-1/u-originator"], 1)
}

func TestPrefs_PatchThirdGroup(t *testing.T) {
	repo := newFakeRepo()
	svc := NewPreferenceService(repo)
	ctx := context.Background()

	cur, err := svc.Patch(ctx, "ws-1", "u-1", PreferencePatch{
		MutedConversationIDs: &[]string{"sess-1"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"sess-1"}, cur.MutedConversationIDs)

	cur, err = svc.Patch(ctx, "ws-1", "u-1", PreferencePatch{MutedTypes: &[]string{"task_completed"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"sess-1"}, cur.MutedConversationIDs, "PATCH-unprovided fields keep current values")
	assert.Equal(t, []string{"task_completed"}, cur.MutedTypes)

	bad := "session:evil"
	_, err = svc.Patch(ctx, "ws-1", "u-1", PreferencePatch{MutedTypes: &[]string{bad}})
	assert.Error(t, err, "muted_types disables the session: prefix")

	var big []string
	for i := 0; i < 201; i++ {
		big = append(big, "sess-x")
	}
	_, err = svc.Patch(ctx, "ws-1", "u-1", PreferencePatch{MutedConversationIDs: &big})
	assert.Error(t, err, "session group cap 200")
}
