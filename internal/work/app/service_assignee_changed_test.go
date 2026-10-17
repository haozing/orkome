package app

// P0-1 单测：WorkItemUpdated 事件负载的 AssigneeChanged 标记——只有 patch
// 带 AssigneeType/AssigneeID（指派真变化）才置 true；改标题/状态/metadata
// 与 Create 路径一律 false。指派监听器（main.go PhaseCascade）据此早退，
// 保证"改标题不再静默重派 agent"。

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
)

// acFakeRepo：最小 fake——UpdateWorkItem 真实套用 patch（事件负载取的是
// 更新后的工单投影），CreateWorkItem 回填 ID/Revision。其余 Update/Create
// 路径会碰到的 repo 方法一并 stub 掉。
type acFakeRepo struct {
	Repo
	item  WorkItem
	seeds int
}

func (f *acFakeRepo) EnsureStatusSeed(context.Context, string) error {
	f.seeds++
	return nil
}
func (f *acFakeRepo) ListStatuses(context.Context, string) ([]StatusDef, error) {
	return []StatusDef{{Key: "todo", Category: "unstarted"}, {Key: "in_progress", Category: "started"}}, nil
}
func (f *acFakeRepo) ListPropertyDefs(context.Context, string) ([]PropertyDef, error) {
	return nil, nil
}
func (f *acFakeRepo) GetWorkItem(_ context.Context, _, id string) (WorkItem, bool, error) {
	if f.item.ID == id {
		return f.item, true, nil
	}
	return WorkItem{}, false, nil
}
func (f *acFakeRepo) UpdateWorkItem(_ context.Context, _, _ string, p WorkItemPatch, _ int64) (WorkItem, error) {
	if p.Title != nil {
		f.item.Title = *p.Title
	}
	if p.StatusKey != nil {
		f.item.StatusKey = *p.StatusKey
	}
	if p.AssigneeType != nil {
		f.item.AssigneeType = *p.AssigneeType
	}
	if p.AssigneeID != nil {
		f.item.AssigneeID = *p.AssigneeID
	}
	f.item.Revision++
	return f.item, nil
}
func (f *acFakeRepo) CreateWorkItem(_ context.Context, in NewWorkItem) (WorkItem, error) {
	f.item = WorkItem{
		ID: "wi-new", WorkspaceID: in.WorkspaceID, Title: in.Title, StatusKey: in.StatusKey,
		AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
		CreatorType: in.CreatorType, CreatorID: in.CreatorID, Revision: 1,
	}
	return f.item, nil
}
func (f *acFakeRepo) CountActiveDuplicate(context.Context, string, string, *string, *string, []string, string) (int, error) {
	return 0, nil
}
func (f *acFakeRepo) AdvisoryLockTitle(context.Context, string, string) (func(), error) {
	return func() {}, nil
}
func (f *acFakeRepo) UpsertSubscribers(context.Context, string, []SubscriberWrite) error { return nil }
func (f *acFakeRepo) UpsertSubscriber(context.Context, string, string, string) error     { return nil }
func (f *acFakeRepo) CountWorkItemsInTriage(context.Context, string, []string) (int, error) {
	return 0, nil
}
func (f *acFakeRepo) UpdateMetadataKey(_ context.Context, _, _ string, _ string, v json.RawMessage) (map[string]any, bool, error) {
	return map[string]any{"k": string(v)}, true, nil
}
func (f *acFakeRepo) DeleteMetadataKey(context.Context, string, string, string) (bool, error) {
	return true, nil
}

func acService(item WorkItem) (*WorkItemService, *captureEmitter) {
	bus := &captureEmitter{}
	return NewWorkItemService(&acFakeRepo{item: item}, bus, nil), bus
}

// updatedPayload 取第一条 WorkItemUpdated 事件的负载（不存在则 fail）。
func updatedPayload(t *testing.T, bus *captureEmitter) events.WorkItemPayload {
	t.Helper()
	for _, e := range bus.events {
		if e.Event != events.WorkItemUpdated {
			continue
		}
		p, ok := e.Payload.(events.WorkItemPayload)
		require.True(t, ok, "WorkItemUpdated payload must be events.WorkItemPayload")
		return p
	}
	t.Fatal("no WorkItemUpdated event emitted")
	return events.WorkItemPayload{}
}

func TestUpdateAssigneeChangedFlag(t *testing.T) {
	agent := "00000000-0000-4000-8000-0000000000a1"
	ctx := context.Background()

	t.Run("patch assignee(type+id) → true", func(t *testing.T) {
		svc, bus := acService(WorkItem{ID: "wi-1", WorkspaceID: "ws-1", StatusKey: "todo"})
		_, err := svc.Update(ctx, "ws-1", "wi-1",
			WorkItemPatch{AssigneeType: strp("agent"), AssigneeID: &agent}, 0, "u-1")
		require.NoError(t, err)
		p := updatedPayload(t, bus)
		assert.True(t, p.AssigneeChanged, "assignee patch must set AssigneeChanged")
		assert.Equal(t, "agent", p.AssigneeType)
		assert.Equal(t, agent, p.AssigneeID)
	})

	t.Run("patch assignee id only → true", func(t *testing.T) {
		svc, bus := acService(WorkItem{ID: "wi-1", WorkspaceID: "ws-1", StatusKey: "todo", AssigneeType: "agent"})
		_, err := svc.Update(ctx, "ws-1", "wi-1", WorkItemPatch{AssigneeID: &agent}, 0, "u-1")
		require.NoError(t, err)
		assert.True(t, updatedPayload(t, bus).AssigneeChanged)
	})

	t.Run("patch title only → false", func(t *testing.T) {
		svc, bus := acService(WorkItem{ID: "wi-1", WorkspaceID: "ws-1", StatusKey: "todo",
			AssigneeType: "agent", AssigneeID: agent})
		_, err := svc.Update(ctx, "ws-1", "wi-1", WorkItemPatch{Title: strp("renamed")}, 0, "u-1")
		require.NoError(t, err)
		assert.False(t, updatedPayload(t, bus).AssigneeChanged,
			"title-only update must NOT re-flag assignee change (silent re-dispatch guard)")
	})

	t.Run("patch status only → false", func(t *testing.T) {
		svc, bus := acService(WorkItem{ID: "wi-1", WorkspaceID: "ws-1", StatusKey: "todo",
			AssigneeType: "agent", AssigneeID: agent})
		_, err := svc.Update(ctx, "ws-1", "wi-1", WorkItemPatch{StatusKey: strp("in_progress")}, 0, "u-1")
		require.NoError(t, err)
		assert.False(t, updatedPayload(t, bus).AssigneeChanged,
			"status-only update must NOT re-flag assignee change")
	})

	t.Run("create with agent assignee → not flagged", func(t *testing.T) {
		svc, bus := acService(WorkItem{})
		_, err := svc.Create(ctx, NewWorkItem{
			WorkspaceID: "ws-1", Title: "brand new", StatusKey: "todo",
			AssigneeType: "agent", AssigneeID: agent,
		}, "u-1")
		require.NoError(t, err)
		var created *events.WorkItemPayload
		for _, e := range bus.events {
			if e.Event != events.WorkItemCreated {
				continue
			}
			p, ok := e.Payload.(events.WorkItemPayload)
			require.True(t, ok)
			created = &p
		}
		require.NotNil(t, created, "WorkItemCreated event emitted")
		assert.False(t, created.AssigneeChanged, "create path never flags assignee change")
		assert.Equal(t, "agent", created.AssigneeType, "assignee still carried on the payload")
	})

	t.Run("metadata put → WorkItemUpdated not flagged", func(t *testing.T) {
		svc, bus := acService(WorkItem{ID: "wi-1", WorkspaceID: "ws-1", StatusKey: "todo"})
		_, _, err := svc.SetMetadataKey(ctx, "ws-1", "wi-1", "k", []byte(`"v"`), "u-1")
		require.NoError(t, err)
		assert.False(t, updatedPayload(t, bus).AssigneeChanged,
			"metadata update re-emits WorkItemUpdated but assignee did not change")
	})
}
