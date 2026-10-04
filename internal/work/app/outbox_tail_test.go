package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
)

func (f *outboxWIRepo) UpdateMetadataKey(_ context.Context, _, _, _ string, _ json.RawMessage) (map[string]any, bool, error) {
	if f.failAt == "update_metadata_key" {
		return nil, false, errors.New("update metadata boom")
	}
	f.rec("update_metadata_key")
	return map[string]any{"k": "v"}, true, nil
}

func (f *outboxWIRepo) DeleteMetadataKey(context.Context, string, string, string) (bool, error) {
	if f.failAt == "delete_metadata_key" {
		return false, errors.New("delete metadata boom")
	}
	f.rec("delete_metadata_key")
	return true, nil
}

func (f *outboxWIRepo) ListPropertyDefs(context.Context, string) ([]PropertyDef, error) {
	return []PropertyDef{{Key: "env", Name: "environment", Type: "text"}}, nil
}

func (f *outboxWIRepo) SetPropertyKey(_ context.Context, _, _, _ string, _ json.RawMessage) (map[string]any, bool, error) {
	if f.failAt == "set_property_key" {
		return nil, false, errors.New("set property boom")
	}
	f.rec("set_property_key")
	return map[string]any{"env": "prod"}, true, nil
}

func (f *outboxWIRepo) DeletePropertyKey(context.Context, string, string, string) (bool, error) {
	if f.failAt == "delete_property_key" {
		return false, errors.New("delete property boom")
	}
	f.rec("delete_property_key")
	return true, nil
}

func (f *outboxWIRepo) UpsertReaction(context.Context, string, string, string, string) error {
	if f.failAt == "upsert_reaction" {
		return errors.New("upsert reaction boom")
	}
	f.rec("upsert_reaction")
	return nil
}

func (f *outboxWIRepo) DeleteReaction(context.Context, string, string, string, string) (bool, error) {
	if f.failAt == "delete_reaction" {
		return false, errors.New("delete reaction boom")
	}
	f.rec("delete_reaction")
	return true, nil
}

func (f *outboxWIRepo) ListReactionCounts(context.Context, string, string) ([]ReactionCount, error) {
	f.rec("list_reaction_counts")
	return []ReactionCount{{Emoji: "👍", Count: 1}}, nil
}

func (f *outboxWIRepo) UpsertManualSubscriber(_ context.Context, _, workItemID, userID string) (Subscriber, bool, error) {
	if f.failAt == "upsert_manual_subscriber" {
		return Subscriber{}, false, errors.New("manual subscribe boom")
	}
	f.rec("upsert_manual_subscriber")
	return Subscriber{WorkItemID: workItemID, UserID: userID, Source: "manual"}, f.manualCreated, nil
}

func (f *outboxWIRepo) RemoveSubscriber(context.Context, string, string, string) (bool, error) {
	if f.failAt == "remove_subscriber" {
		return false, errors.New("unsubscribe boom")
	}
	f.rec("remove_subscriber")
	return f.subRemoved, nil
}

func (f *outboxWIRepo) RemoveSubscriberSubtree(context.Context, string, string, string) ([]string, error) {
	if f.failAt == "remove_subtree" {
		return nil, errors.New("subtree boom")
	}
	f.rec("remove_subtree")
	return f.subtree, nil
}

func (f *outboxWIRepo) NextPositionInStatus(_ context.Context, _, _ string) (int64, error) {
	f.rec("next_pos")
	return 40, nil
}

func (f *outboxWIRepo) MovePosition(context.Context, string, string, int64) error {
	f.rec("move")
	return nil
}

func newOutboxWIKnob(failAt string, manualCreated, subRemoved bool, subtree []string) (*WorkItemService, *outboxSpy, *fakeOutbox, *spyEmitter, *outboxWIRepo) {
	spy := &outboxSpy{}
	ob := &fakeOutbox{spy: spy}
	em := &spyEmitter{spy: spy}
	repo := &outboxWIRepo{spy: spy, failAt: failAt, manualCreated: manualCreated, subRemoved: subRemoved, subtree: subtree}
	svc := NewWorkItemService(repo, em, nil)
	svc.SetOutbox(ob, outboxUowOf(spy))
	return svc, spy, ob, em, repo
}

func TestOutboxSetMetadataKey_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxWIKnob("", false, false, nil)
	ctx := context.Background()

	meta, wi, err := svc.SetMetadataKey(ctx, "ws-1", "wi-1", "k", json.RawMessage(`"v"`), "u-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"k": "v"}, meta)
	assert.Equal(t, "wi-1", wi.ID)
	assert.Equal(t, []string{
		"tx:begin",
		"write:update_metadata_key",
		"outbox:append",
		"tx:commit",
		"emit:work_item.updated",
		"outbox:mark",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.Equal(t, events.WorkItemUpdated, em.events[0].Event)
	assert.False(t, em.emits[0].inTx, "Emit after commit (I4)")
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx, "outbox.Append inside the transaction")
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
}

func TestOutboxDeleteMetadataKey_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxWIKnob("", false, false, nil)
	ctx := context.Background()

	require.NoError(t, svc.DeleteMetadataKey(ctx, "ws-1", "wi-1", "k", "u-1"))
	assert.Equal(t, []string{
		"tx:begin",
		"write:delete_metadata_key",
		"outbox:append",
		"tx:commit",
		"emit:work_item.updated",
		"outbox:mark",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.Equal(t, events.WorkItemUpdated, em.events[0].Event)
	assert.False(t, em.emits[0].inTx)
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
}

func TestOutboxSetPropertyKey_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxWIKnob("", false, false, nil)
	ctx := context.Background()

	t.Run("set-value branch", func(t *testing.T) {
		norm, _, wi, err := svc.SetPropertyKey(ctx, "ws-1", "wi-1", "env", json.RawMessage(`"prod"`), "u-1")
		require.NoError(t, err)
		assert.Equal(t, "prod", norm)
		assert.Equal(t, "wi-1", wi.ID)
		assert.Equal(t, []string{
			"tx:begin",
			"write:set_property_key",
			"outbox:append",
			"tx:commit",
			"emit:work_item.updated",
			"outbox:mark",
		}, spy.log)
		require.Len(t, em.events, 1)
		assert.Equal(t, events.WorkItemUpdated, em.events[0].Event)
		assert.False(t, em.emits[0].inTx)
		require.Len(t, ob.appends, 1)
		assert.True(t, ob.appends[0].inTx)
		assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
	})

	spy2 := &outboxSpy{}
	ob2 := &fakeOutbox{spy: spy2}
	em2 := &spyEmitter{spy: spy2}
	repo2 := &outboxWIRepo{spy: spy2}
	svc2 := NewWorkItemService(repo2, em2, nil)
	svc2.SetOutbox(ob2, outboxUowOf(spy2))

	t.Run("null clears the key branch", func(t *testing.T) {
		_, _, _, err := svc2.SetPropertyKey(ctx, "ws-1", "wi-1", "env", json.RawMessage(`""`), "u-1")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"tx:begin",
			"write:delete_property_key",
			"outbox:append",
			"tx:commit",
			"emit:work_item.updated",
			"outbox:mark",
		}, spy2.log)
		require.Len(t, em2.events, 1)
		assert.False(t, em2.emits[0].inTx)
		require.Len(t, ob2.appends, 1)
		assert.True(t, ob2.appends[0].inTx)
	})
}

func TestOutboxDeletePropertyKey_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxWIKnob("", false, false, nil)
	ctx := context.Background()

	require.NoError(t, svc.DeletePropertyKey(ctx, "ws-1", "wi-1", "env", "u-1"))
	assert.Equal(t, []string{
		"tx:begin",
		"write:delete_property_key",
		"outbox:append",
		"tx:commit",
		"emit:work_item.updated",
		"outbox:mark",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.Equal(t, events.WorkItemUpdated, em.events[0].Event)
	assert.False(t, em.emits[0].inTx)
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
}

func TestOutboxAddReaction_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, repo := newOutboxWIKnob("", false, false, nil)
	ctx := context.Background()

	counts, err := svc.AddReaction(ctx, "ws-1", "wi-1", "u-1", "👍")
	require.NoError(t, err)
	assert.Len(t, counts, 1)
	assert.Equal(t, []string{
		"tx:begin",
		"write:upsert_reaction",
		"outbox:append",
		"tx:commit",
		"emit:work_item_reaction.added",
		"outbox:mark",
		"write:list_reaction_counts",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.Equal(t, "work_item_reaction.added", em.events[0].Event)
	assert.False(t, em.emits[0].inTx)
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx)
	assert.False(t, repo.ops[len(repo.ops)-1].inTx, "counting reads outside the transaction")
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
}

func TestOutboxRemoveReaction_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxWIKnob("", false, false, nil)
	ctx := context.Background()

	_, err := svc.RemoveReaction(ctx, "ws-1", "wi-1", "u-1", "👍")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"tx:begin",
		"write:delete_reaction",
		"outbox:append",
		"tx:commit",
		"emit:work_item_reaction.removed",
		"outbox:mark",
		"write:list_reaction_counts",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.Equal(t, "work_item_reaction.removed", em.events[0].Event)
	assert.False(t, em.emits[0].inTx)
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
}

func TestOutboxSubscribe_SameTxPostCommitEmit(t *testing.T) {
	ctx := context.Background()

	t.Run("new subscription", func(t *testing.T) {
		svc, spy, ob, em, _ := newOutboxWIKnob("", true, false, nil)
		sub, err := svc.Subscribe(ctx, "ws-1", "wi-1", "u-2")
		require.NoError(t, err)
		assert.Equal(t, "u-2", sub.UserID)
		assert.Equal(t, []string{
			"tx:begin",
			"write:upsert_manual_subscriber",
			"outbox:append",
			"tx:commit",
			"emit:subscriber.added",
			"outbox:mark",
		}, spy.log)
		require.Len(t, em.events, 1)
		assert.Equal(t, "subscriber.added", em.events[0].Event)
		assert.False(t, em.emits[0].inTx)
		require.Len(t, ob.appends, 1)
		assert.True(t, ob.appends[0].inTx)
		assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
		p, ok := em.events[0].Payload.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "wi-1", p["work_item_id"])
		assert.Equal(t, "u-2", p["user_id"])
	})

	t.Run("idempotent resubscribe zero events", func(t *testing.T) {
		svc, spy, ob, em, _ := newOutboxWIKnob("", false, false, nil)
		_, err := svc.Subscribe(ctx, "ws-1", "wi-1", "u-2")
		require.NoError(t, err)
		assert.Equal(t, []string{"tx:begin", "write:upsert_manual_subscriber", "tx:commit"}, spy.log,
			"idempotent resubscribe: no rows, no after-commit emissions")
		assert.Empty(t, em.events)
		assert.Empty(t, ob.appends)
		assert.Empty(t, ob.marked)
	})
}

func TestOutboxUnsubscribe_SameTxPostCommitEmit(t *testing.T) {
	ctx := context.Background()

	t.Run("rows deleted", func(t *testing.T) {
		svc, spy, ob, em, _ := newOutboxWIKnob("", false, true, nil)
		removed, err := svc.Unsubscribe(ctx, "ws-1", "wi-1", "u-1", "")
		require.NoError(t, err)
		assert.True(t, removed)
		assert.Equal(t, []string{
			"tx:begin",
			"write:remove_subscriber",
			"outbox:append",
			"tx:commit",
			"emit:subscriber.removed",
			"outbox:mark",
		}, spy.log)
		require.Len(t, em.events, 1)
		assert.Equal(t, "subscriber.removed", em.events[0].Event)
		assert.False(t, em.emits[0].inTx)
		require.Len(t, ob.appends, 1)
		assert.True(t, ob.appends[0].inTx)
		assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
	})

	t.Run("no-row idempotent zero events", func(t *testing.T) {
		svc, spy, ob, em, _ := newOutboxWIKnob("", false, false, nil)
		removed, err := svc.Unsubscribe(ctx, "ws-1", "wi-1", "u-1", "")
		require.NoError(t, err)
		assert.False(t, removed)
		assert.Equal(t, []string{"tx:begin", "write:remove_subscriber", "tx:commit"}, spy.log)
		assert.Empty(t, em.events)
		assert.Empty(t, ob.appends)
		assert.Empty(t, ob.marked)
	})
}

func TestOutboxUnsubscribeSubtree_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxWIKnob("", false, false, []string{"wi-1", "wi-2"})
	ctx := context.Background()

	removed, err := svc.UnsubscribeSubtree(ctx, "ws-1", "wi-1", "u-1", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"wi-1", "wi-2"}, removed)
	assert.Equal(t, []string{
		"tx:begin",
		"write:remove_subtree",
		"outbox:append", "outbox:append",
		"tx:commit",
		"emit:subscriber.removed", "outbox:mark",
		"emit:subscriber.removed", "outbox:mark",
	}, spy.log)
	require.Len(t, em.events, 2)
	require.Len(t, ob.envs, 2)
	for i := range em.events {
		assert.False(t, em.emits[i].inTx, "Emit[%d] after commit", i)
		assert.True(t, ob.appends[i].inTx, "Append[%d] inside the transaction", i)
		assert.Equal(t, em.events[i].EventID, ob.envs[i].EventID, "Append/Emit pair %d shares the EventID", i)
	}
	assert.NotEqual(t, em.events[0].EventID, em.events[1].EventID, "two event IDs independent")
	assert.Equal(t, []string{em.events[0].EventID, em.events[1].EventID}, ob.marked)

	p0, ok := em.events[0].Payload.(map[string]any)
	require.True(t, ok)
	p1, ok := em.events[1].Payload.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "wi-1", p0["work_item_id"])
	assert.Equal(t, "wi-2", p1["work_item_id"])
}

func TestOutboxUpdateStatusByAgent_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, repo := newOutboxWIKnob("", false, false, nil)
	ctx := context.Background()

	wi, err := svc.UpdateStatusByAgent(ctx, "ws-1", "wi-1", "agent-9", "doing")
	require.NoError(t, err)
	assert.Equal(t, "wi-1", wi.ID)
	assert.Equal(t, []string{
		"tx:begin",
		"write:update",
		"outbox:append",
		"tx:commit",
		"emit:work_item:updated_by_agent",
		"outbox:mark",
		"write:next_pos", "write:move",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.Equal(t, WorkItemUpdatedByAgent, em.events[0].Event)
	assert.False(t, em.emits[0].inTx)
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)

	require.Len(t, repo.ops, 3)
	assert.True(t, repo.ops[0].inTx, "status written inside the transaction")
	assert.False(t, repo.ops[1].inTx, "relocation reads outside the transaction")
	assert.False(t, repo.ops[2].inTx, "relocation writes outside the transaction")
}

func liveComment() *Comment {
	return &Comment{ID: "c-1", WorkspaceID: "ws-1", WorkItemID: "wi-1", AuthorType: "user", AuthorID: "u-1", Content: "hi"}
}

func (f *outboxCommentRepo) UpdateComment(_ context.Context, id, content string, _ int64) (Comment, error) {
	if f.failAt == "update_comment" {
		return Comment{}, errors.New("update comment boom")
	}
	f.rec("update_comment")
	return Comment{ID: id, WorkspaceID: "ws-1", WorkItemID: "wi-1", AuthorType: "user", AuthorID: "u-1", Content: content}, nil
}

func (f *outboxCommentRepo) TombstoneComment(_ context.Context, id string) error {
	if f.failAt == "tombstone_comment" {
		return errors.New("tombstone boom")
	}
	f.rec("tombstone_comment")
	return nil
}

func (f *outboxCommentRepo) SetResolved(context.Context, string, bool, string, string) error {
	if f.failAt == "set_resolved" {
		return errors.New("resolve boom")
	}
	f.rec("set_resolved")
	return nil
}

func (f *outboxCommentRepo) UpsertCommentReaction(context.Context, string, string, string, string) error {
	if f.failAt == "upsert_comment_reaction" {
		return errors.New("upsert comment reaction boom")
	}
	f.rec("upsert_comment_reaction")
	return nil
}

func (f *outboxCommentRepo) DeleteCommentReaction(context.Context, string, string, string, string) (bool, error) {
	if f.failAt == "delete_comment_reaction" {
		return false, errors.New("delete comment reaction boom")
	}
	f.rec("delete_comment_reaction")
	return true, nil
}

func (f *outboxCommentRepo) ListCommentReactionCounts(context.Context, string, string) ([]ReactionCount, error) {
	f.rec("list_comment_reaction_counts")
	return []ReactionCount{{Emoji: "🎉", Count: 1}}, nil
}

func newOutboxComment(failAt string, live *Comment) (*CommentService, *outboxSpy, *fakeOutbox, *spyEmitter, *outboxCommentRepo) {
	spy := &outboxSpy{}
	ob := &fakeOutbox{spy: spy}
	em := &spyEmitter{spy: spy}
	repo := &outboxCommentRepo{spy: spy, failAt: failAt, live: live}
	svc := NewCommentService(repo, em, nil)
	svc.SetOutbox(ob, outboxUowOf(spy))
	return svc, spy, ob, em, repo
}

func TestOutboxCommentUpdate_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxComment("", liveComment())
	ctx := context.Background()

	out, err := svc.Update(ctx, "ws-1", "c-1", "rewrite", "u-1", 0)
	require.NoError(t, err)
	assert.Equal(t, "rewrite", out.Content)
	assert.Equal(t, []string{
		"tx:begin",
		"write:update_comment", "write:touch",
		"outbox:append",
		"tx:commit",
		"emit:comment.updated",
		"outbox:mark",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.Equal(t, events.CommentUpdated, em.events[0].Event)
	assert.False(t, em.emits[0].inTx)
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
	p, ok := em.events[0].Payload.(events.CommentPayload)
	require.True(t, ok, "payload completed inside the write closure")
	assert.Equal(t, "c-1", p.ID)
	assert.Equal(t, "rewrite", p.Content)
}

func TestOutboxCommentDelete_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxComment("", liveComment())
	ctx := context.Background()

	require.NoError(t, svc.Delete(ctx, "ws-1", "c-1", "u-1"))
	assert.Equal(t, []string{
		"tx:begin",
		"write:tombstone_comment", "write:touch",
		"outbox:append",
		"tx:commit",
		"emit:comment.deleted",
		"outbox:mark",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.Equal(t, events.CommentDeleted, em.events[0].Event)
	assert.False(t, em.emits[0].inTx)
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
}

func TestOutboxCommentSetResolve_SameTxPostCommitEmit(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		resolved bool
		event    string
	}{
		{"resolve → comment.resolved", true, events.CommentResolved},
		{"undo → comment.updated", false, events.CommentUpdated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, spy, ob, em, _ := newOutboxComment("", liveComment())
			require.NoError(t, svc.SetResolve(ctx, "ws-1", "c-1", tc.resolved, "user", "u-2"))
			assert.Equal(t, []string{
				"tx:begin",
				"write:set_resolved",
				"outbox:append",
				"tx:commit",
				"emit:" + tc.event,
				"outbox:mark",
			}, spy.log)
			require.Len(t, em.events, 1)
			assert.Equal(t, tc.event, em.events[0].Event)
			assert.False(t, em.emits[0].inTx)
			require.Len(t, ob.appends, 1)
			assert.True(t, ob.appends[0].inTx)
			assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
		})
	}
}

func TestOutboxCommentReactions_SameTxPostCommitEmit(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		add     bool
		writeOp string
		event   string
	}{
		{"add → reaction.added", true, "upsert_comment_reaction", "reaction.added"},
		{"remove → reaction.removed", false, "delete_comment_reaction", "reaction.removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, spy, ob, em, _ := newOutboxComment("", liveComment())
			var err error
			if tc.add {
				_, err = svc.AddCommentReaction(ctx, "ws-1", "c-1", "u-2", "🎉")
			} else {
				_, err = svc.RemoveCommentReaction(ctx, "ws-1", "c-1", "u-2", "🎉")
			}
			require.NoError(t, err)
			assert.Equal(t, []string{
				"tx:begin",
				"write:" + tc.writeOp,
				"outbox:append",
				"tx:commit",
				"emit:" + tc.event,
				"outbox:mark",
				"write:list_comment_reaction_counts",
			}, spy.log)
			require.Len(t, em.events, 1)
			assert.Equal(t, tc.event, em.events[0].Event)
			assert.False(t, em.emits[0].inTx)
			require.Len(t, ob.appends, 1)
			assert.True(t, ob.appends[0].inTx)
			assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
		})
	}
}

type outboxCatalogRepo struct {
	Repo
	spy    *outboxSpy
	ops    []txOp
	failAt string
}

func (f *outboxCatalogRepo) rec(name string) {
	f.spy.rec("write:" + name)
	f.ops = append(f.ops, txOp{name, f.spy.inTx})
}

func (f *outboxCatalogRepo) GetProject(context.Context, string, string) (Project, bool, error) {
	return Project{ID: "p-1", Name: "P"}, true, nil
}

func (f *outboxCatalogRepo) CreateProject(_ context.Context, _, name, _ string) (Project, error) {
	if f.failAt == "create_project" {
		return Project{}, errors.New("create project boom")
	}
	f.rec("create_project")
	return Project{ID: "p-new", Name: name}, nil
}

func (f *outboxCatalogRepo) CreateProjectResource(_ context.Context, _, projectID, kind, _ string) (ProjectResource, bool, error) {
	if f.failAt == "create_project_resource" {
		return ProjectResource{}, false, errors.New("create resource boom")
	}
	f.rec("create_project_resource")
	return ProjectResource{ID: "r-new", ProjectID: projectID, Kind: kind}, true, nil
}

func (f *outboxCatalogRepo) UpdateProjectResource(_ context.Context, _, _, resourceID, kind, _ string) (ProjectResource, bool, error) {
	if f.failAt == "update_project_resource" {
		return ProjectResource{}, false, errors.New("update resource boom")
	}
	f.rec("update_project_resource")
	return ProjectResource{ID: resourceID, Kind: kind}, true, nil
}

func (f *outboxCatalogRepo) DeleteProjectResource(context.Context, string, string, string) error {
	if f.failAt == "delete_project_resource" {
		return errors.New("delete resource boom")
	}
	f.rec("delete_project_resource")
	return nil
}

func newOutboxCatalog(failAt string) (*CatalogService, *outboxSpy, *fakeOutbox, *spyEmitter, *outboxCatalogRepo) {
	spy := &outboxSpy{}
	ob := &fakeOutbox{spy: spy}
	em := &spyEmitter{spy: spy}
	repo := &outboxCatalogRepo{spy: spy, failAt: failAt}
	svc := NewCatalogService(repo, em)
	svc.SetOutbox(ob, outboxUowOf(spy))
	return svc, spy, ob, em, repo
}

func TestOutboxCatalogCreateProject_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxCatalog("")
	ctx := context.Background()

	p, err := svc.CreateProject(ctx, "ws-1", "new project", "d")
	require.NoError(t, err)
	assert.Equal(t, "p-new", p.ID)
	assert.Equal(t, []string{
		"tx:begin",
		"write:create_project",
		"outbox:append",
		"tx:commit",
		"emit:project.created",
		"outbox:mark",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.Equal(t, events.ProjectCreated, em.events[0].Event)
	assert.False(t, em.emits[0].inTx)
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
}

func TestOutboxCatalogProjectResources_SameTxPostCommitEmit(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		writeOp string
		event   string
		call    func(svc *CatalogService) error
	}{
		{"create → project_resource.created", "create_project_resource", "project_resource.created",
			func(svc *CatalogService) error {
				_, err := svc.CreateProjectResource(ctx, "ws-1", "p-1", "repo", "https://example.com/x")
				return err
			}},
		{"update → project_resource.updated", "update_project_resource", "project_resource.updated",
			func(svc *CatalogService) error {
				_, err := svc.UpdateProjectResource(ctx, "ws-1", "p-1", "r-1", "repo", "https://example.com/y")
				return err
			}},
		{"delete → project_resource.deleted", "delete_project_resource", "project_resource.deleted",
			func(svc *CatalogService) error {
				return svc.DeleteProjectResource(ctx, "ws-1", "p-1", "r-1")
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, spy, ob, em, _ := newOutboxCatalog("")
			require.NoError(t, tc.call(svc))
			assert.Equal(t, []string{
				"tx:begin",
				"write:" + tc.writeOp,
				"outbox:append",
				"tx:commit",
				"emit:" + tc.event,
				"outbox:mark",
			}, spy.log)
			require.Len(t, em.events, 1)
			assert.Equal(t, tc.event, em.events[0].Event)
			assert.False(t, em.emits[0].inTx)
			require.Len(t, ob.appends, 1)
			assert.True(t, ob.appends[0].inTx)
			assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
		})
	}
}

type outboxSourceRepo struct {
	Repo
	spy    *outboxSpy
	ops    []txOp
	failAt string
}

func (f *outboxSourceRepo) rec(name string) {
	f.spy.rec("write:" + name)
	f.ops = append(f.ops, txOp{name, f.spy.inTx})
}

func (f *outboxSourceRepo) GetComment(context.Context, string) (Comment, bool, error) {
	return *liveComment(), true, nil
}

func (f *outboxSourceRepo) GetWorkItem(_ context.Context, _, id string) (WorkItem, bool, error) {
	return WorkItem{ID: id, WorkspaceID: "ws-1", Title: "parent"}, true, nil
}

func (f *outboxSourceRepo) InsertSourceContext(context.Context, string, string, []byte, string) (string, bool, error) {
	if f.failAt == "insert_source_context" {
		return "", false, errors.New("insert source boom")
	}
	f.rec("insert_source_context")
	return "sc-1", true, nil
}

func (f *outboxSourceRepo) CreateWorkItem(_ context.Context, in NewWorkItem) (WorkItem, error) {
	if f.failAt == "create_child" {
		return WorkItem{}, errors.New("create child boom")
	}
	f.rec("create_child")
	return WorkItem{ID: "wi-child", WorkspaceID: in.WorkspaceID, Number: 8, Title: in.Title, StatusKey: in.StatusKey, Revision: 1}, nil
}

func newOutboxSource(failAt string) (*SourceContextService, *outboxSpy, *fakeOutbox, *spyEmitter, *outboxSourceRepo) {
	spy := &outboxSpy{}
	ob := &fakeOutbox{spy: spy}
	em := &spyEmitter{spy: spy}
	repo := &outboxSourceRepo{spy: spy, failAt: failAt}
	svc := NewSourceContextService(repo, em, nil)
	svc.Sign = func(string) string { return "sig" }
	svc.SetOutbox(ob, outboxUowOf(spy))
	return svc, spy, ob, em, repo
}

func TestOutboxSourceContextMaterialize_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxSource("")
	ctx := context.Background()
	token := fmt.Sprintf("%d.sig", time.Now().Add(time.Hour).Unix())

	wi, err := svc.CreateFromComment(ctx, "ws-1", "c-1", "u-1", token, "manual", "")
	require.NoError(t, err)
	assert.Equal(t, "wi-child", wi.ID)
	assert.Equal(t, []string{
		"tx:begin",
		"write:insert_source_context", "write:create_child",
		"outbox:append",
		"tx:commit",
		"emit:work_item.created",
		"outbox:mark",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.Equal(t, "work_item.created", em.events[0].Event)
	assert.False(t, em.emits[0].inTx)
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
	p, ok := em.events[0].Payload.(WorkItem)
	require.True(t, ok, "payload = sub work item row (existing-line shape)")
	assert.Equal(t, "wi-child", p.ID)
}

func TestOutboxWIWriteFailureTail_NoEmitNoOutboxRow(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		failAt string
		call   func(svc *WorkItemService) error
	}{
		{"set_metadata_key failed", "update_metadata_key", func(svc *WorkItemService) error {
			_, _, err := svc.SetMetadataKey(ctx, "ws-1", "wi-1", "k", json.RawMessage(`"v"`), "u-1")
			return err
		}},
		{"delete_metadata_key failed", "delete_metadata_key", func(svc *WorkItemService) error {
			return svc.DeleteMetadataKey(ctx, "ws-1", "wi-1", "k", "u-1")
		}},
		{"set_property_key failed", "set_property_key", func(svc *WorkItemService) error {
			_, _, _, err := svc.SetPropertyKey(ctx, "ws-1", "wi-1", "env", json.RawMessage(`"prod"`), "u-1")
			return err
		}},
		{"delete_property_key failed", "delete_property_key", func(svc *WorkItemService) error {
			return svc.DeletePropertyKey(ctx, "ws-1", "wi-1", "env", "u-1")
		}},
		{"add_reaction failed", "upsert_reaction", func(svc *WorkItemService) error {
			_, err := svc.AddReaction(ctx, "ws-1", "wi-1", "u-1", "👍")
			return err
		}},
		{"remove_reaction failed", "delete_reaction", func(svc *WorkItemService) error {
			_, err := svc.RemoveReaction(ctx, "ws-1", "wi-1", "u-1", "👍")
			return err
		}},
		{"subscribe failed", "upsert_manual_subscriber", func(svc *WorkItemService) error {
			_, err := svc.Subscribe(ctx, "ws-1", "wi-1", "u-2")
			return err
		}},
		{"un  subscribe failed", "remove_subscriber", func(svc *WorkItemService) error {
			_, err := svc.Unsubscribe(ctx, "ws-1", "wi-1", "u-1", "")
			return err
		}},
		{"unsubscribe_subtree failed", "remove_subtree", func(svc *WorkItemService) error {
			_, err := svc.UnsubscribeSubtree(ctx, "ws-1", "wi-1", "u-1", "")
			return err
		}},
		{"update_status_by_agent failed", "update", func(svc *WorkItemService) error {
			_, err := svc.UpdateStatusByAgent(ctx, "ws-1", "wi-1", "agent-9", "doing")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, spy, ob, em, _ := newOutboxWIKnob(tc.failAt, true, true, []string{"wi-1"})
			require.Error(t, tc.call(svc))
			assert.Equal(t, []string{"tx:begin", "tx:rollback"}, spy.log, "transaction rolled back, no rows landed, no commit")
			assert.Empty(t, em.events, "no Emit")
			assert.Empty(t, ob.appends, "no outbox rows")
			assert.Empty(t, ob.marked, "no marks")
		})
	}
}

func TestOutboxCommentWriteFailureTail_NoEmitNoOutboxRow(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		failAt string
		call   func(svc *CommentService) error
	}{
		{"update failed", "update_comment", func(svc *CommentService) error {
			_, err := svc.Update(ctx, "ws-1", "c-1", "x", "u-1", 0)
			return err
		}},
		{"delete failed", "tombstone_comment", func(svc *CommentService) error {
			return svc.Delete(ctx, "ws-1", "c-1", "u-1")
		}},
		{"set_resolve failed", "set_resolved", func(svc *CommentService) error {
			return svc.SetResolve(ctx, "ws-1", "c-1", true, "user", "u-2")
		}},
		{"add_comment_reaction failed", "upsert_comment_reaction", func(svc *CommentService) error {
			_, err := svc.AddCommentReaction(ctx, "ws-1", "c-1", "u-2", "🎉")
			return err
		}},
		{"remove_comment_reaction failed", "delete_comment_reaction", func(svc *CommentService) error {
			_, err := svc.RemoveCommentReaction(ctx, "ws-1", "c-1", "u-2", "🎉")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, spy, ob, em, _ := newOutboxComment(tc.failAt, liveComment())
			require.Error(t, tc.call(svc))
			assert.Equal(t, []string{"tx:begin", "tx:rollback"}, spy.log)
			assert.Empty(t, em.events)
			assert.Empty(t, ob.appends)
			assert.Empty(t, ob.marked)
		})
	}
}

func TestOutboxCatalogSourceWriteFailure_NoEmitNoOutboxRow(t *testing.T) {
	ctx := context.Background()
	token := fmt.Sprintf("%d.sig", time.Now().Add(time.Hour).Unix())
	for _, tc := range []struct {
		name    string
		wantLog []string
		call    func() (*outboxSpy, *fakeOutbox, *spyEmitter, error)
	}{
		{"create_project failed", []string{"tx:begin", "tx:rollback"}, func() (*outboxSpy, *fakeOutbox, *spyEmitter, error) {
			svc, spy, ob, em, _ := newOutboxCatalog("create_project")
			_, err := svc.CreateProject(ctx, "ws-1", "p", "")
			return spy, ob, em, err
		}},
		{"create_project_resource failed", []string{"tx:begin", "tx:rollback"}, func() (*outboxSpy, *fakeOutbox, *spyEmitter, error) {
			svc, spy, ob, em, _ := newOutboxCatalog("create_project_resource")
			_, err := svc.CreateProjectResource(ctx, "ws-1", "p-1", "repo", "https://example.com/x")
			return spy, ob, em, err
		}},
		{"update_project_resource failed", []string{"tx:begin", "tx:rollback"}, func() (*outboxSpy, *fakeOutbox, *spyEmitter, error) {
			svc, spy, ob, em, _ := newOutboxCatalog("update_project_resource")
			_, err := svc.UpdateProjectResource(ctx, "ws-1", "p-1", "r-1", "repo", "https://example.com/y")
			return spy, ob, em, err
		}},
		{"delete_project_resource failed", []string{"tx:begin", "tx:rollback"}, func() (*outboxSpy, *fakeOutbox, *spyEmitter, error) {
			svc, spy, ob, em, _ := newOutboxCatalog("delete_project_resource")
			err := svc.DeleteProjectResource(ctx, "ws-1", "p-1", "r-1")
			return spy, ob, em, err
		}},
		{"insert_source_context failed", []string{"tx:begin", "tx:rollback"}, func() (*outboxSpy, *fakeOutbox, *spyEmitter, error) {
			svc, spy, ob, em, _ := newOutboxSource("insert_source_context")
			_, err := svc.CreateFromComment(ctx, "ws-1", "c-1", "u-1", token, "manual", "")
			return spy, ob, em, err
		}},
		{"create_child failed (snapshot rolls back too)", []string{
			"tx:begin", "write:insert_source_context", "tx:rollback",
		}, func() (*outboxSpy, *fakeOutbox, *spyEmitter, error) {
			svc, spy, ob, em, _ := newOutboxSource("create_child")
			_, err := svc.CreateFromComment(ctx, "ws-1", "c-1", "u-1", token, "manual", "")
			return spy, ob, em, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy, ob, em, err := tc.call()
			require.Error(t, err)
			assert.Equal(t, tc.wantLog, spy.log, "transaction rolled back, no rows landed, no commit")
			assert.Empty(t, em.events, "no Emit")
			assert.Empty(t, ob.appends, "no outbox rows")
			assert.Empty(t, ob.marked, "no marks")
		})
	}
}

func TestOutboxNilPorts_ConditionalLegacyPath(t *testing.T) {
	ctx := context.Background()

	t.Run("new subscription direct-send after write", func(t *testing.T) {
		spy := &outboxSpy{}
		em := &spyEmitter{spy: spy}
		repo := &outboxWIRepo{spy: spy, manualCreated: true}
		svc := NewWorkItemService(repo, em, nil)
		ob := &fakeOutbox{spy: spy}

		_, err := svc.Subscribe(ctx, "ws-1", "wi-1", "u-2")
		require.NoError(t, err)
		assert.Equal(t, []string{"write:upsert_manual_subscriber", "emit:subscriber.added"}, spy.log,
			"old path: direct send after write, zero transaction involvement")
		require.Len(t, em.events, 1)
		assert.Empty(t, ob.appends, "unwired outbox untouched")
	})

	t.Run("idempotent resubscribe zero emissions", func(t *testing.T) {
		spy := &outboxSpy{}
		em := &spyEmitter{spy: spy}
		repo := &outboxWIRepo{spy: spy, manualCreated: false}
		svc := NewWorkItemService(repo, em, nil)

		_, err := svc.Subscribe(ctx, "ws-1", "wi-1", "u-2")
		require.NoError(t, err)
		assert.Equal(t, []string{"write:upsert_manual_subscriber"}, spy.log)
		assert.Empty(t, em.events)
	})
}
