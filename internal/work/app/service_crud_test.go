package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/work/domain"
)

type fakeCrudRepo struct {
	Repo
	items    map[string]WorkItem
	comments map[string]Comment
	defs     []PropertyDef
	subs     map[[2]string]Subscriber
	cReacts  map[[3]string]bool
}

func newFakeCrudRepo() *fakeCrudRepo {
	return &fakeCrudRepo{
		items: map[string]WorkItem{
			"wi-1": {ID: "wi-1", WorkspaceID: "ws-1", Properties: map[string]any{}, Revision: 1},
		},
		comments: map[string]Comment{
			"c-1": {ID: "c-1", WorkspaceID: "ws-1", WorkItemID: "wi-1"},
		},
		defs: []PropertyDef{
			{Key: "tags", Name: "Tags", Type: domain.PropMultiSelect, Options: []string{"alpha", "beta", "gamma"}},
			{Key: "owner", Name: "Owner", Type: domain.PropActor},
		},
		subs:    map[[2]string]Subscriber{},
		cReacts: map[[3]string]bool{},
	}
}

func (f *fakeCrudRepo) EnsureStatusSeed(context.Context, string) error { return nil }
func (f *fakeCrudRepo) ListStatuses(context.Context, string) ([]StatusDef, error) {
	return nil, nil
}
func (f *fakeCrudRepo) ListPropertyDefs(context.Context, string) ([]PropertyDef, error) {
	return f.defs, nil
}
func (f *fakeCrudRepo) GetWorkItem(_ context.Context, _, id string) (WorkItem, bool, error) {
	w, ok := f.items[id]
	return w, ok, nil
}
func (f *fakeCrudRepo) GetComment(_ context.Context, id string) (Comment, bool, error) {
	c, ok := f.comments[id]
	return c, ok, nil
}

func (f *fakeCrudRepo) GetPropertyKey(_ context.Context, _, wiID, key string) (v json.RawMessage, item, keyExists bool, err error) {
	wi, ok := f.items[wiID]
	if !ok {
		return nil, false, false, nil
	}
	val, ok := wi.Properties[key]
	if !ok {
		return nil, true, false, nil
	}
	b, _ := json.Marshal(val)
	return b, true, true, nil
}

func (f *fakeCrudRepo) SetPropertyKey(_ context.Context, _, wiID, key string, value json.RawMessage) (map[string]any, bool, error) {
	wi := f.items[wiID]
	var v any
	_ = json.Unmarshal(value, &v)
	wi.Properties[key] = v
	wi.Revision++
	f.items[wiID] = wi
	return wi.Properties, true, nil
}

func (f *fakeCrudRepo) DeletePropertyKey(_ context.Context, _, wiID, key string) (bool, error) {
	wi := f.items[wiID]
	delete(wi.Properties, key)
	wi.Revision++
	f.items[wiID] = wi
	return true, nil
}

func (f *fakeCrudRepo) UpsertManualSubscriber(_ context.Context, _, wiID, userID string) (Subscriber, bool, error) {
	k := [2]string{wiID, userID}
	if s, ok := f.subs[k]; ok {
		return s, false, nil
	}
	s := Subscriber{WorkItemID: wiID, UserID: userID, Source: domain.SubSourceManual, Level: domain.NotifyLevelAll}
	f.subs[k] = s
	return s, true, nil
}

func (f *fakeCrudRepo) UpsertCommentReaction(_ context.Context, _, commentID, userID, emoji string) error {
	f.cReacts[[3]string{commentID, userID, emoji}] = true
	return nil
}

func (f *fakeCrudRepo) DeleteCommentReaction(_ context.Context, _, commentID, userID, emoji string) (bool, error) {
	k := [3]string{commentID, userID, emoji}
	had := f.cReacts[k]
	delete(f.cReacts, k)
	return had, nil
}

func (f *fakeCrudRepo) ListCommentReactionCounts(_ context.Context, _, commentID string) ([]ReactionCount, error) {
	n := int64(0)
	for k := range f.cReacts {
		if k[0] == commentID {
			n++
		}
	}
	if n == 0 {
		return nil, nil
	}
	return []ReactionCount{{Emoji: "👍", Count: n}}, nil
}

func TestSubscribe_ManualSourceIdempotentEvent(t *testing.T) {
	repo := newFakeCrudRepo()
	bus := &captureEmitter{}
	svc := NewWorkItemService(repo, bus, nil)
	ctx := context.Background()

	sub, err := svc.Subscribe(ctx, "ws-1", "wi-1", "u-9")
	require.NoError(t, err)
	assert.Equal(t, domain.SubSourceManual, sub.Source)
	assert.Equal(t, domain.NotifyLevelAll, sub.Level)

	_, err = svc.Subscribe(ctx, "ws-1", "wi-1", "u-9")
	require.NoError(t, err, "resubscribe idempotent success")
	added := 0
	for _, e := range bus.events {
		if e.Event == "subscriber.added" {
			added++
		}
	}
	assert.Equal(t, 1, added, "idempotent resubscribe does not re-emit events")

	_, err = svc.Subscribe(ctx, "ws-1", "wi-x", "u-9")
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.NotFound, ae.Code)
}

func TestSetPropertyKey_CanonicalAndClear(t *testing.T) {
	repo := newFakeCrudRepo()
	svc := NewWorkItemService(repo, nil, nil)
	ctx := context.Background()

	val, props, _, err := svc.SetPropertyKey(ctx, "ws-1", "wi-1", "tags", []byte(`["gamma","alpha"]`), "u-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "gamma"}, val, "stable sort by definition order (internal archive 05 §3.5)")
	assert.Equal(t, []any{"alpha", "gamma"}, props["tags"], "jsonb read-back face (element order preserved suffices)")

	val, _, _, err = svc.SetPropertyKey(ctx, "ws-1", "wi-1", "owner",
		[]byte(`"member:6F9619FF-8B86-D011-B42D-00C04FC964FF"`), "u-1")
	require.NoError(t, err)
	assert.Equal(t, "member:6f9619ff-8b86-d011-b42d-00c04fc964ff", val, "actor uuid normalized to lowercase")

	_, _, _, err = svc.SetPropertyKey(ctx, "ws-1", "wi-1", "tags", []byte(`[]`), "u-1")
	require.NoError(t, err, "empty array = unset semantics")
	_, itemExists, keyExists, err := repo.GetPropertyKey(ctx, "ws-1", "wi-1", "tags")
	require.NoError(t, err)
	assert.True(t, itemExists)
	assert.False(t, keyExists, "after clearing, the key does not exist")

	bad := []struct {
		key  string
		body string
	}{
		{"tags", `["nope"]`},
		{"owner", `"user:abc"`},
		{"owner", `"member:xx"`},
		{"nope", `"x"`},
		{"tags", `"alpha"`},
	}
	for _, tc := range bad {
		_, _, _, err := svc.SetPropertyKey(ctx, "ws-1", "wi-1", tc.key, []byte(tc.body), "u-1")
		var ae *apierr.Error
		require.ErrorAs(t, err, &ae, "PUT %s=%s should be validation-rejected", tc.key, tc.body)
		assert.Equal(t, apierr.Validation, ae.Code)
	}
}

func TestCommentReaction_EventsAndVisibility(t *testing.T) {
	repo := newFakeCrudRepo()
	bus := &captureEmitter{}
	svc := NewCommentService(repo, bus, nil)
	ctx := context.Background()

	_, err := svc.AddCommentReaction(ctx, "ws-1", "c-1", "u-1", "👍")
	require.NoError(t, err)
	_, err = svc.AddCommentReaction(ctx, "ws-1", "c-1", "u-1", "👍")
	require.NoError(t, err, "idempotent upsert")

	_, err = svc.RemoveCommentReaction(ctx, "ws-1", "c-1", "u-1", "👍")
	require.NoError(t, err)

	var names []string
	for _, e := range bus.events {
		names = append(names, e.Event)
	}
	assert.Equal(t, []string{"reaction.added", "reaction.added", "reaction.removed"}, names,
		"internal archive 04 §3 comment-domain reaction events (idempotent re-attach still emits each time faithfully)")

	_, err = svc.AddCommentReaction(ctx, "ws-2", "c-1", "u-1", "👍")
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.NotFound, ae.Code, "cross-workspace comment → 404 (I6)")
}
