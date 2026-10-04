package workers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	notifapp "orkome/internal/notification/app"
	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/outboxx"
	workapp "orkome/internal/work/app"
)

func TestM6_Outbox_CrashReplay_NoDuplicates(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m6a@example.com")
	ws := e.createWS(t, user, "m6-ob")
	wi := e.createWorkItem(t, ws, user, "replay")
	_ = wi

	notifRepo := notifpgrepo.New(e.db)
	notify := notifapp.NewNotifyService(notifRepo, subscriberReader{e})

	rec := outboxx.NewRecorder(e.db)
	env := events.Envelope{
		Event: "task.completed", EventID: "evt-crash-1",
		WorkspaceID: ws, Payload: map[string]any{"work_item_id": wi},
	}
	require.NoError(t, rec.Append(context.Background(), env))

	relay := outboxx.NewRelay(e.db, func(ctx context.Context, env events.Envelope) error {

		return notify.Handle(ctx, notifapp.EventIn{
			EventID: env.EventID, WorkspaceID: env.WorkspaceID, Type: "task_completed",
			Title: "task completed", DirectUserIDs: []string{user},
		})
	}, logNoop())

	n, err := relay.ReplayOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "should replay one row")

	cnt, err := notifRepo.UnreadCount(context.Background(), ws, user)
	require.NoError(t, err)
	assert.Equal(t, 1, cnt)

	_, err = e.db.Tx(context.Background()).Exec(context.Background(),
		`UPDATE event_outbox SET published_at=NULL WHERE id=$1`, env.EventID)
	require.NoError(t, err)
	_, err = relay.ReplayOnce(context.Background())
	require.NoError(t, err)
	cnt2, _ := notifRepo.UnreadCount(context.Background(), ws, user)
	assert.Equal(t, 1, cnt2, "replayed duplicate deliveries must not produce duplicate rows (internal archive 02 §6.4)")
}

func TestM6_I9_CascadeBeforeProjection(t *testing.T) {
	bus := eventbus.New(logNoop())
	var order []int32
	bus.On(eventbus.PhaseCascade, func(context.Context, events.Envelope) { order = append(order, 1) })
	bus.On(eventbus.PhaseProject, func(context.Context, events.Envelope) { order = append(order, 2) })
	bus.Emit(context.Background(), events.Envelope{Event: "x.y"})
	require.Len(t, order, 2)
	assert.Equal(t, int32(1), order[0], "PhaseCascade must precede PhaseProject (I9)")
	assert.Equal(t, int32(2), order[1])
}

func TestM6_NotificationRoutingAndPreferences(t *testing.T) {
	e := newEnv(t)
	alice := e.createUser(t, "m6alice@example.com")
	bob := e.createUser(t, "m6bob@example.com")
	carol := e.createUser(t, "m6carol@example.com")
	ws := e.createWS(t, alice, "m6-notification")
	wi := e.createWorkItem(t, ws, alice, "notification routing")

	_, _, err := e.comments.Create(context.Background(), workapp.NewComment{
		WorkspaceID: ws, WorkItemID: wi, AuthorType: "user", AuthorID: alice,
		Content: "look at [@Bob](mention://user/" + bob + ")",
	})
	require.NoError(t, err)

	notifRepo := notifpgrepo.New(e.db)
	notify := notifapp.NewNotifyService(notifRepo, subscriberReader{e})

	prefs := notifapp.NewPreferenceService(notifRepo)
	require.NoError(t, prefs.Upsert(context.Background(), ws, carol, notifapp.Preference{
		MutedTypes: []string{"task_completed"},
	}))

	require.NoError(t, notify.Handle(context.Background(), notifapp.EventIn{
		EventID: "evt-c1", WorkspaceID: ws, Type: "comment_created", WorkItemID: wi,
		Title: "new comment", Body: "content", MentionedUserIDs: notifapp.ParseMentionedUsers("x [@Bob](mention://user/" + bob + ")"),
	}))
	ca, _ := notifRepo.UnreadCount(context.Background(), ws, alice)
	cb, _ := notifRepo.UnreadCount(context.Background(), ws, bob)
	assert.Equal(t, 1, ca, "comment event: the subscriber (creator) should receive it (comment event only at this point)")
	assert.Equal(t, 1, cb, "mentioners should receive items")

	require.NoError(t, notify.Handle(context.Background(), notifapp.EventIn{
		EventID: "evt-t1", WorkspaceID: ws, Type: "task_completed", WorkItemID: wi,
		Title: "task completed", DirectUserIDs: []string{carol},
	}))
	cc, _ := notifRepo.UnreadCount(context.Background(), ws, carol)
	assert.Equal(t, 0, cc, "muted types should be filtered (internal archive 05 §11)")
	ca2, _ := notifRepo.UnreadCount(context.Background(), ws, alice)
	assert.Equal(t, 2, ca2, "subscribers receive both comment + task events")

	notification := notifapp.NewNotificationService(notifRepo)
	items, _, err := notification.List(context.Background(), ws, alice, false, 10, "")
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, it := range items {
		require.NoError(t, notification.MarkRead(context.Background(), ws, alice, it.ID, true))
	}
	ua, _ := notifRepo.UnreadCount(context.Background(), ws, alice)
	assert.Equal(t, 0, ua)
	require.NoError(t, notification.ArchiveAll(context.Background(), ws, alice))
	arch, _, _ := notification.List(context.Background(), ws, alice, true, 10, "")
	assert.Len(t, arch, 2)
}

type subscriberReader struct{ e *env }

func (s subscriberReader) ListSubscriberUserIDs(ctx context.Context, workItemID string) ([]string, error) {
	subs, err := s.e.wRepo.ListSubscribers(ctx, workItemID)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(subs))
	for i, sub := range subs {
		out[i] = sub.UserID
	}
	return out, nil
}
