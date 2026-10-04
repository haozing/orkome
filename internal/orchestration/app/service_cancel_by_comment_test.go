package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
	"orkome/internal/orchestration/domain"
)

type fakeCancelRepo struct {
	Repo
	cancelledIDs []string
	calls        []string
}

func (f *fakeCancelRepo) CancelTasksByTriggerComment(_ context.Context, ws, commentID string) ([]string, error) {
	f.calls = append(f.calls, ws+"/"+commentID)
	out := append([]string(nil), f.cancelledIDs...)
	f.cancelledIDs = nil
	return out, nil
}

func cancelledEnvelopes(r *recorderEmitter) []events.Envelope {
	var out []events.Envelope
	for _, e := range r.emitted {
		if e.Event == events.TaskCancelled {
			out = append(out, e)
		}
	}
	return out
}

func TestCancelByTriggerComment_EmitsPerTask(t *testing.T) {
	repo := &fakeCancelRepo{cancelledIDs: []string{"t1", "t2"}}
	emit := &recorderEmitter{}
	settle := NewSettlementService(repo, emit, nil, nil, nil)

	n, err := settle.CancelByTriggerComment(context.Background(), "ws", "c-9", "u1")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	cancels := cancelledEnvelopes(emit)
	require.Len(t, cancels, 2)
	assert.Equal(t, "t1", cancels[0].Payload.(TaskPayload).ID)
	assert.Equal(t, domain.Cancelled, cancels[0].Payload.(TaskPayload).Status)
	assert.Equal(t, "u1", cancels[0].ActorUserID)

	n, err = settle.CancelByTriggerComment(context.Background(), "ws", "c-9", "u1")
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Len(t, cancelledEnvelopes(emit), 2)
}

func TestHandleCommentEvent_CancelsOnUpdateAndDelete(t *testing.T) {
	repo := &fakeCancelRepo{cancelledIDs: []string{"t1"}}
	emit := &recorderEmitter{}
	settle := NewSettlementService(repo, emit, nil, nil, nil)

	for _, ev := range []string{events.CommentUpdated, events.CommentDeleted} {
		require.NoError(t, settle.HandleCommentEvent(context.Background(), events.Envelope{
			Event: ev, WorkspaceID: "ws", ActorUserID: "u2",
			Payload: events.CommentPayload{ID: "c-" + ev, WorkspaceID: "ws"},
		}))
	}
	assert.Equal(t, []string{"ws/c-comment.updated", "ws/c-comment.deleted"}, repo.calls)
	require.Len(t, cancelledEnvelopes(emit), 1, "only first round hits (second round 0 rows)")
}

func TestHandleCommentEvent_IgnoresOthers(t *testing.T) {
	repo := &fakeCancelRepo{cancelledIDs: []string{"t1"}}
	settle := NewSettlementService(repo, &recorderEmitter{}, nil, nil, nil)

	require.NoError(t, settle.HandleCommentEvent(context.Background(), events.Envelope{
		Event: events.TaskCompleted, WorkspaceID: "ws",
		Payload: events.CommentPayload{ID: "c-1"},
	}))
	require.NoError(t, settle.HandleCommentEvent(context.Background(), events.Envelope{
		Event: events.CommentUpdated, WorkspaceID: "ws", Payload: nil,
	}))
	assert.Empty(t, repo.calls, "non-target events / bad payloads must be no-ops")
}
