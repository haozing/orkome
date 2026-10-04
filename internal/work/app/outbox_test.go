package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
)

type txOp struct {
	name string
	inTx bool
}

type outboxSpy struct {
	log  []string
	inTx bool
}

func (s *outboxSpy) rec(e string) { s.log = append(s.log, e) }

func outboxUowOf(spy *outboxSpy) Within {
	return func(ctx context.Context, fn func(context.Context) error) error {
		spy.rec("tx:begin")
		spy.inTx = true
		err := fn(ctx)
		spy.inTx = false
		if err != nil {
			spy.rec("tx:rollback")
			return err
		}
		spy.rec("tx:commit")
		return nil
	}
}

type fakeOutbox struct {
	spy     *outboxSpy
	envs    []events.Envelope
	appends []txOp
	marked  []string
}

func (f *fakeOutbox) Append(_ context.Context, envs ...events.Envelope) error {
	for _, e := range envs {
		f.spy.rec("outbox:append")
		f.appends = append(f.appends, txOp{"outbox:append", f.spy.inTx})
		f.envs = append(f.envs, e)
	}
	return nil
}

func (f *fakeOutbox) MarkPublished(_ context.Context, ids ...string) error {
	f.spy.rec("outbox:mark")
	f.marked = append(f.marked, ids...)
	return nil
}

type spyEmitter struct {
	spy    *outboxSpy
	events []events.Envelope
	emits  []txOp
}

func (e *spyEmitter) Emit(_ context.Context, ev events.Envelope) {
	e.spy.rec("emit:" + ev.Event)
	e.emits = append(e.emits, txOp{"emit", e.spy.inTx})
	e.events = append(e.events, ev)
}

type outboxWIRepo struct {
	Repo
	spy    *outboxSpy
	ops    []txOp
	failAt string

	manualCreated bool
	subRemoved    bool
	subtree       []string
}

func (f *outboxWIRepo) rec(name string) {
	f.spy.rec("write:" + name)
	f.ops = append(f.ops, txOp{name, f.spy.inTx})
}

func (f *outboxWIRepo) EnsureStatusSeed(context.Context, string) error { return nil }
func (f *outboxWIRepo) ListStatuses(context.Context, string) ([]StatusDef, error) {
	return []StatusDef{{Key: "todo", Category: "unstarted"}, {Key: "doing", Category: "started"}}, nil
}
func (f *outboxWIRepo) GetWorkItem(_ context.Context, _, id string) (WorkItem, bool, error) {
	return WorkItem{ID: id, WorkspaceID: "ws-1", StatusKey: "todo"}, true, nil
}
func (f *outboxWIRepo) AdvisoryLockTitle(context.Context, string, string) (func(), error) {
	return func() {}, nil
}
func (f *outboxWIRepo) CountActiveDuplicate(context.Context, string, string, *string, *string, []string, string) (int, error) {
	return 0, nil
}

func (f *outboxWIRepo) CreateWorkItem(_ context.Context, in NewWorkItem) (WorkItem, error) {
	if f.failAt == "create" {
		return WorkItem{}, errors.New("create boom")
	}
	f.rec("create")
	return WorkItem{ID: "wi-new", WorkspaceID: in.WorkspaceID, Number: 7, Title: in.Title, StatusKey: in.StatusKey, Revision: 1}, nil
}

func (f *outboxWIRepo) UpsertSubscribers(_ context.Context, _ string, _ []SubscriberWrite) error {
	f.rec("upsert_subscribers")
	return nil
}

func (f *outboxWIRepo) UpdateWorkItem(_ context.Context, _, _ string, _ WorkItemPatch, _ int64) (WorkItem, error) {
	if f.failAt == "update" {
		return WorkItem{}, errors.New("update boom")
	}
	f.rec("update")

	return WorkItem{ID: "wi-1", WorkspaceID: "ws-1", StatusKey: "todo", Revision: 2,
		AssigneeType: "user", AssigneeID: "00000000-0000-4000-8000-0000000000a1"}, nil
}

func (f *outboxWIRepo) UpsertSubscriber(_ context.Context, _, _, _ string) error {
	f.rec("upsert_subscriber")
	return nil
}

func (f *outboxWIRepo) DeleteWorkItem(_ context.Context, _, _ string) error {
	if f.failAt == "delete" {
		return errors.New("delete boom")
	}
	f.rec("delete")
	return nil
}

func newOutboxWI(failAt string) (*WorkItemService, *outboxSpy, *fakeOutbox, *spyEmitter, *outboxWIRepo) {
	spy := &outboxSpy{}
	ob := &fakeOutbox{spy: spy}
	em := &spyEmitter{spy: spy}
	repo := &outboxWIRepo{spy: spy, failAt: failAt}
	svc := NewWorkItemService(repo, em, nil)
	svc.SetOutbox(ob, outboxUowOf(spy))
	return svc, spy, ob, em, repo
}

func TestOutboxWorkItemCreate_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, repo := newOutboxWI("")
	ctx := context.Background()

	wi, err := svc.Create(ctx, NewWorkItem{WorkspaceID: "ws-1", Title: "t", StatusKey: "todo"}, "u-1")
	require.NoError(t, err)
	assert.Equal(t, "wi-new", wi.ID)

	assert.Equal(t, []string{
		"tx:begin",
		"write:create", "write:upsert_subscribers",
		"outbox:append",
		"tx:commit",
		"emit:work_item.created",
		"outbox:mark",
	}, spy.log, "global ordering: write → row lands → commit → Emit → mark")

	require.Len(t, repo.ops, 2)
	for _, op := range repo.ops {
		assert.True(t, op.inTx, "business write %s inside the transaction", op.name)
	}
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx, "outbox.Append inside the transaction")
	require.Len(t, em.emits, 1)
	assert.False(t, em.emits[0].inTx, "Emit after commit (I4)")

	require.Len(t, em.events, 1)
	require.NotEmpty(t, em.events[0].EventID)
	assert.Equal(t, events.WorkItemCreated, em.events[0].Event)
	require.Len(t, ob.envs, 1)
	assert.Equal(t, em.events[0].EventID, ob.envs[0].EventID, "Append rows share the EventID with the fast path")
	p, ok := em.events[0].Payload.(events.WorkItemPayload)
	require.True(t, ok, "payload completed inside the write closure (env pointer semantics)")
	assert.Equal(t, "wi-new", p.ID)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
}

func TestOutboxWorkItemUpdate_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxWI("")
	ctx := context.Background()
	assignee := "00000000-0000-4000-8000-0000000000a1"

	_, err := svc.Update(ctx, "ws-1", "wi-1", WorkItemPatch{Title: strp("new"), AssigneeID: &assignee}, 0, "u-1")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"tx:begin",
		"write:update", "write:upsert_subscriber",
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

func TestOutboxWorkItemDelete_SameTxPostCommitEmit(t *testing.T) {
	svc, spy, ob, em, _ := newOutboxWI("")
	ctx := context.Background()

	require.NoError(t, svc.Delete(ctx, "ws-1", "wi-1", "u-1"))
	assert.Equal(t, []string{
		"tx:begin",
		"write:delete",
		"outbox:append",
		"tx:commit",
		"emit:work_item.deleted",
		"outbox:mark",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.False(t, em.emits[0].inTx)
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
}

func TestOutboxWriteFailure_NoEmitNoOutboxRow(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		failAt string
		call   func(svc *WorkItemService) error
	}{
		{"create failed", "create", func(svc *WorkItemService) error {
			_, err := svc.Create(ctx, NewWorkItem{WorkspaceID: "ws-1", Title: "t", StatusKey: "todo"}, "u-1")
			return err
		}},
		{"update failed", "update", func(svc *WorkItemService) error {
			_, err := svc.Update(ctx, "ws-1", "wi-1", WorkItemPatch{Title: strp("x")}, 0, "u-1")
			return err
		}},
		{"delete failed", "delete", func(svc *WorkItemService) error {
			return svc.Delete(ctx, "ws-1", "wi-1", "u-1")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, spy, ob, em, _ := newOutboxWI(tc.failAt)
			require.Error(t, tc.call(svc))
			assert.Equal(t, []string{"tx:begin", "tx:rollback"}, spy.log, "transaction rolled back, no rows landed, no commit")
			assert.Empty(t, em.events, "no Emit")
			assert.Empty(t, ob.appends, "no outbox rows")
			assert.Empty(t, ob.marked, "no marks")
		})
	}
}

func TestOutboxNilPorts_FallbackLegacyPath(t *testing.T) {
	ctx := context.Background()

	t.Run("work item create", func(t *testing.T) {
		spy := &outboxSpy{}
		em := &spyEmitter{spy: spy}
		repo := &outboxWIRepo{spy: spy}
		svc := NewWorkItemService(repo, em, nil)
		ob := &fakeOutbox{spy: spy}

		_, err := svc.Create(ctx, NewWorkItem{WorkspaceID: "ws-1", Title: "t", StatusKey: "todo"}, "u-1")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"write:create", "write:upsert_subscribers", "emit:work_item.created",
		}, spy.log, "old path: direct send after write, zero transaction involvement")
		require.Len(t, em.events, 1)
		assert.False(t, em.emits[0].inTx)
		assert.Empty(t, ob.appends, "unwired outbox untouched")
	})

	t.Run("comment create", func(t *testing.T) {
		spy := &outboxSpy{}
		em := &spyEmitter{spy: spy}
		repo := &outboxCommentRepo{spy: spy}
		svc := NewCommentService(repo, em, nil)

		c, _, err := svc.Create(ctx, NewComment{
			WorkItemID: "wi-1", WorkspaceID: "ws-1",
			AuthorType: "user", AuthorID: "u-1", Content: "hi",
		})
		require.NoError(t, err)
		assert.Equal(t, "c-new", c.ID)
		assert.Equal(t, []string{
			"write:create_comment", "write:touch", "write:upsert_subscriber",
			"emit:comment.created",
		}, spy.log)
		require.Len(t, em.events, 1)
		assert.Equal(t, events.CommentCreated, em.events[0].Event)
	})
}

type outboxCommentRepo struct {
	Repo
	spy    *outboxSpy
	ops    []txOp
	failAt string

	live *Comment
}

func (f *outboxCommentRepo) rec(name string) {
	f.spy.rec("write:" + name)
	f.ops = append(f.ops, txOp{name, f.spy.inTx})
}

func (f *outboxCommentRepo) GetWorkItem(_ context.Context, _, id string) (WorkItem, bool, error) {
	return WorkItem{ID: id, WorkspaceID: "ws-1"}, true, nil
}

func (f *outboxCommentRepo) GetComment(_ context.Context, _ string) (Comment, bool, error) {
	if f.live != nil {
		return *f.live, true, nil
	}
	return Comment{}, false, nil
}

func (f *outboxCommentRepo) CreateComment(_ context.Context, in NewComment) (Comment, error) {
	if f.failAt == "create_comment" {
		return Comment{}, errors.New("create comment boom")
	}
	f.rec("create_comment")
	return Comment{ID: "c-new", WorkItemID: in.WorkItemID, AuthorType: in.AuthorType, AuthorID: in.AuthorID, Content: in.Content}, nil
}

func (f *outboxCommentRepo) TouchWorkItem(_ context.Context, _ string) error {
	f.rec("touch")
	return nil
}

func (f *outboxCommentRepo) UpsertSubscriber(_ context.Context, _, _, _ string) error {
	f.rec("upsert_subscriber")
	return nil
}

func TestOutboxCommentCreate_SameTxPostCommitEmit(t *testing.T) {
	spy := &outboxSpy{}
	ob := &fakeOutbox{spy: spy}
	em := &spyEmitter{spy: spy}
	repo := &outboxCommentRepo{spy: spy}
	svc := NewCommentService(repo, em, nil)
	svc.SetOutbox(ob, outboxUowOf(spy))
	ctx := context.Background()

	c, admission, err := svc.Create(ctx, NewComment{
		WorkItemID: "wi-1", WorkspaceID: "ws-1",
		AuthorType: "user", AuthorID: "u-1",
		Content: "hi [@u2](mention://user/00000000-0000-4000-8000-0000000000b2)",
	})
	require.NoError(t, err)
	require.NotNil(t, c)
	require.NotNil(t, admission, "engine not wired → blocked fallback (assertion face not blocked)")
	assert.Equal(t, []string{
		"tx:begin",
		"write:create_comment", "write:touch", "write:upsert_subscriber", "write:upsert_subscriber",
		"outbox:append",
		"tx:commit",
		"emit:comment.created",
		"outbox:mark",
	}, spy.log)
	require.Len(t, em.events, 1)
	assert.False(t, em.emits[0].inTx, "Emit after commit (I4)")
	require.Len(t, ob.appends, 1)
	assert.True(t, ob.appends[0].inTx, "outbox.Append inside the transaction")
	require.Len(t, repo.ops, 4)
	for _, op := range repo.ops {
		assert.True(t, op.inTx, "business write %s inside the transaction", op.name)
	}
	require.NotEmpty(t, em.events[0].EventID)
	assert.Equal(t, []string{em.events[0].EventID}, ob.marked)
	p, ok := em.events[0].Payload.(events.CommentPayload)
	require.True(t, ok)
	assert.Equal(t, "c-new", p.ID)
}

func TestOutboxCommentCreate_WriteFailure(t *testing.T) {
	spy := &outboxSpy{}
	ob := &fakeOutbox{spy: spy}
	em := &spyEmitter{spy: spy}
	repo := &outboxCommentRepo{spy: spy, failAt: "create_comment"}
	svc := NewCommentService(repo, em, nil)
	svc.SetOutbox(ob, outboxUowOf(spy))

	_, _, err := svc.Create(context.Background(), NewComment{
		WorkItemID: "wi-1", WorkspaceID: "ws-1",
		AuthorType: "user", AuthorID: "u-1", Content: "hi",
	})
	require.Error(t, err)
	assert.Equal(t, []string{"tx:begin", "tx:rollback"}, spy.log)
	assert.Empty(t, em.events)
	assert.Empty(t, ob.appends)
	assert.Empty(t, ob.marked)
}
