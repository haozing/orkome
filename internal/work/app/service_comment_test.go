package app

import (
	"context"
	"orkome/internal/contract/apierr"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
)

type fakeCommentRepo struct {
	Repo
	items    map[string]WorkItem
	comments map[string]Comment
	touched  []string
	subs     [][2]string
	calls    []string
}

func newFakeCommentRepo() *fakeCommentRepo {
	return &fakeCommentRepo{items: map[string]WorkItem{}, comments: map[string]Comment{}}
}

func (f *fakeCommentRepo) GetWorkItem(_ context.Context, _, id string) (WorkItem, bool, error) {
	w, ok := f.items[id]
	return w, ok, nil
}

func (f *fakeCommentRepo) GetComment(_ context.Context, id string) (Comment, bool, error) {
	c, ok := f.comments[id]
	return c, ok, nil
}

func (f *fakeCommentRepo) CreateComment(_ context.Context, in NewComment) (Comment, error) {
	c := Comment{ID: "c-" + in.Content[:min(6, len(in.Content))], WorkItemID: in.WorkItemID,
		AuthorType: in.AuthorType, AuthorID: in.AuthorID, Content: in.Content, ParentID: in.ParentID}
	f.comments[c.ID] = c
	f.calls = append(f.calls, "create")
	return c, nil
}

func (f *fakeCommentRepo) UpdateComment(_ context.Context, id, content string, expected int64) (Comment, error) {
	c := f.comments[id]
	if expected > 0 && c.Revision != expected {
		return c, apierr.New(apierr.Conflict, "revision conflict")
	}
	c.Content = content
	c.Revision++
	f.comments[id] = c
	return c, nil
}

func (f *fakeCommentRepo) TombstoneComment(_ context.Context, id string) error {
	c := f.comments[id]
	c.Deleted = true
	f.comments[id] = c
	return nil
}

func (f *fakeCommentRepo) SetResolved(_ context.Context, id string, resolved bool, _, _ string) error {
	c := f.comments[id]
	c.Resolved = resolved
	f.comments[id] = c
	return nil
}

func (f *fakeCommentRepo) TouchWorkItem(_ context.Context, wiID string) error {
	f.touched = append(f.touched, wiID)
	return nil
}

func (f *fakeCommentRepo) UpsertSubscriber(_ context.Context, wiID, userID, _ string) error {
	f.subs = append(f.subs, [2]string{wiID, userID})
	return nil
}

func (f *fakeCommentRepo) ListComments(_ context.Context, _ string) ([]Comment, error) {
	return nil, nil
}

type captureEmitter struct{ events []events.Envelope }

func (e *captureEmitter) Emit(_ context.Context, ev events.Envelope) { e.events = append(e.events, ev) }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestCommentUpdateDelete_AuthorGateAndCancel(t *testing.T) {
	repo := newFakeCommentRepo()
	repo.items["wi-1"] = WorkItem{ID: "wi-1", WorkspaceID: "ws-1"}
	repo.comments["c-1"] = Comment{ID: "c-1", WorkspaceID: "ws-1", WorkItemID: "wi-1", AuthorType: "user", AuthorID: "u-author"}
	var cancelled []string
	SetCommentTaskCanceller(func(_ context.Context, _, commentID string) (int, error) {
		cancelled = append(cancelled, commentID)
		return 1, nil
	})
	t.Cleanup(func() { SetCommentTaskCanceller(nil) })
	svc := NewCommentService(repo, nil, nil)
	ctx := context.Background()

	_, err := svc.Update(ctx, "ws-1", "c-1", "change", "u-other", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only author can edit")
	err = svc.Delete(ctx, "ws-1", "c-1", "u-other")
	require.Error(t, err, "deletion gate: non-author 403")
	assert.Contains(t, err.Error(), "only author can delete")

	out, err := svc.Update(ctx, "ws-1", "c-1", "author rewrite", "u-author", 0)
	require.NoError(t, err)
	assert.Equal(t, "author rewrite", out.Content)
	require.NoError(t, svc.Delete(ctx, "ws-1", "c-1", "u-author"))
	assert.True(t, repo.comments["c-1"].Deleted, "tombstone deletion keeps the row")

	assert.Equal(t, []string{"wi-1", "wi-1"}, repo.touched, "write path touches in the same transaction (I7)")
	assert.Equal(t, []string{"c-1", "c-1"}, cancelled, "both edit and delete cancel their triggered unstarted tasks (B3②)")

	_, err = svc.Update(ctx, "ws-1", "c-1", "x", "u-author", 0)
	require.Error(t, err)
}

func TestCommentResolve_OnlyThreadRoot(t *testing.T) {
	repo := newFakeCommentRepo()
	repo.items["wi-1"] = WorkItem{ID: "wi-1", WorkspaceID: "ws-1"}
	repo.comments["root"] = Comment{ID: "root", WorkspaceID: "ws-1", WorkItemID: "wi-1", AuthorType: "user"}
	repo.comments["child"] = Comment{ID: "child", WorkspaceID: "ws-1", WorkItemID: "wi-1", AuthorType: "user", ParentID: "root"}
	emit := &captureEmitter{}
	svc := NewCommentService(repo, emit, nil)
	ctx := context.Background()

	err := svc.SetResolve(ctx, "ws-1", "child", true, "user", "u-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only thread root")

	require.NoError(t, svc.SetResolve(ctx, "ws-1", "root", true, "user", "u-1"))
	assert.True(t, repo.comments["root"].Resolved)
	require.Len(t, emit.events, 1)
	assert.Equal(t, events.CommentResolved, emit.events[0].Event)
}

func TestCommentCreate_SubscriptionsAndAdmission(t *testing.T) {
	repo := newFakeCommentRepo()
	repo.items["wi-1"] = WorkItem{ID: "wi-1", WorkspaceID: "ws-1"}
	engine := &fakeTriggerEngine{result: "coalesced", reason: "already_active"}
	SetTriggerEngine(engine)
	t.Cleanup(func() { SetTriggerEngine(nil) })
	svc := NewCommentService(repo, nil, nil)
	ctx := context.Background()

	c, admission, err := svc.Create(ctx, NewComment{
		WorkspaceID: "ws-1", WorkItemID: "wi-1", AuthorType: "user", AuthorID: "u-author",

		Content: "look at this [@colleague](mention://user/123e4567-e89b-12d3-a456-426614174000)",
	})
	require.NoError(t, err)
	require.NotNil(t, c)
	require.NotNil(t, admission)
	assert.Equal(t, "coalesced", admission.Result, "engine five states passed through as-is")
	assert.Equal(t, "already_active", admission.ReasonCode)
	assert.Equal(t, 1, engine.calls, "decision tree wired via the TriggerEngine port")

	subbed := map[string]bool{}
	for _, s := range repo.subs {
		subbed[s[1]] = true
	}
	assert.True(t, subbed["u-author"], "source 3: commenter auto-subscribes")
	assert.True(t, subbed["123e4567-e89b-12d3-a456-426614174000"], "source 4: @-mentioned users subscribe")
	assert.Equal(t, []string{"wi-1"}, repo.touched, "I7: comment write touches the work item")
}

type fakeTriggerEngine struct {
	result, reason string
	calls          int
}

func (f *fakeTriggerEngine) OnComment(_ context.Context, _ TriggerInput, _ string) (string, string, error) {
	f.calls++
	return f.result, f.reason, nil
}

func (f *fakeTriggerEngine) Preview(_ context.Context, _ TriggerInput) ([]PreviewHit, []PreviewHit, error) {
	return nil, nil, nil
}

func TestUpdate_RevisionCAS(t *testing.T) {
	repo := &fakeCommentRepo{comments: map[string]Comment{"c-1": {
		ID: "c-1", WorkspaceID: "ws-1", AuthorType: "user", AuthorID: "u-author",
	}}}
	svc := NewCommentService(repo, nil, nil)
	ctx := context.Background()

	out, err := svc.Update(ctx, "ws-1", "c-1", "version 1", "u-author", 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), out.Revision)

	_, err = svc.Update(ctx, "ws-1", "c-1", "version 2", "u-author", 1)
	require.NoError(t, err)

	_, err = svc.Update(ctx, "ws-1", "c-1", "expired write", "u-author", 1)
	require.Error(t, err)
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, apierr.Conflict, ae.Code)
}

// FP-06 P1-2（IDOR）：评论写面（Update/Delete/SetResolve）必须做工作区
// fence——GetComment 只按 id 查，跨工作区写此前全部 2xx。回归锁定：
// 跨区一律 404（不泄露存在性），同区合法路径不回归。
func TestCommentWrite_CrossWorkspaceFence(t *testing.T) {
	repo := newFakeCommentRepo()
	repo.items["wi-1"] = WorkItem{ID: "wi-1", WorkspaceID: "ws-1"}
	repo.comments["c-1"] = Comment{
		ID: "c-1", WorkspaceID: "ws-1", WorkItemID: "wi-1",
		AuthorType: "user", AuthorID: "u-author", Content: "original",
	}
	var cancelled []string
	SetCommentTaskCanceller(func(_ context.Context, _, commentID string) (int, error) {
		cancelled = append(cancelled, commentID)
		return 1, nil
	})
	t.Cleanup(func() { SetCommentTaskCanceller(nil) })
	svc := NewCommentService(repo, nil, nil)
	ctx := context.Background()

	// 跨区写三连：一律 404，且不落任何写副作用
	_, err := svc.Update(ctx, "ws-other", "c-1", "cross ws edit", "u-author", 0)
	require.Error(t, err)
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.NotFound, ae.Code)
	assert.Equal(t, "original", repo.comments["c-1"].Content, "cross-ws edit must not persist")

	err = svc.Delete(ctx, "ws-other", "c-1", "u-author")
	require.Error(t, err)
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.NotFound, ae.Code)
	assert.False(t, repo.comments["c-1"].Deleted, "cross-ws delete must be refused")

	err = svc.SetResolve(ctx, "ws-other", "c-1", true, "user", "u-author")
	require.Error(t, err)
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.NotFound, ae.Code)
	assert.False(t, repo.comments["c-1"].Resolved, "cross-ws resolve must be refused")
	assert.Empty(t, cancelled, "refused cross-ws writes cancel nothing")

	// 同区合法路径不回归：作者改/删/解决依旧全通
	_, err = svc.Update(ctx, "ws-1", "c-1", "same ws edit ok", "u-author", 0)
	require.NoError(t, err)
	assert.Equal(t, "same ws edit ok", repo.comments["c-1"].Content)
	require.NoError(t, svc.SetResolve(ctx, "ws-1", "c-1", true, "user", "u-author"))
	assert.True(t, repo.comments["c-1"].Resolved)
	require.NoError(t, svc.Delete(ctx, "ws-1", "c-1", "u-author"))
	assert.True(t, repo.comments["c-1"].Deleted)
}
