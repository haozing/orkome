package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	"orkome/internal/work/domain"
)

type WorkItemService struct {
	repo Repo
	emit Emitter
	now  Clock

	outbox OutboxRecorder
	uow    Within
}

func NewWorkItemService(repo Repo, emit Emitter, now Clock) *WorkItemService {
	if now == nil {
		now = time.Now
	}
	return &WorkItemService{repo: repo, emit: emit, now: now}
}

func (s *WorkItemService) SetOutbox(rec OutboxRecorder, uow Within) {
	s.outbox, s.uow = rec, uow
}

func (s *WorkItemService) emitCommitted(ctx context.Context, env *events.Envelope, write func(context.Context) error) error {
	return emitCommitted(ctx, s.emit, s.outbox, s.uow, env, write)
}

func (s *WorkItemService) emitCommittedEnvs(ctx context.Context, envs *[]events.Envelope, write func(context.Context) error) error {
	return emitCommittedEnvs(ctx, s.emit, s.outbox, s.uow, envs, write)
}

func (s *WorkItemService) Create(ctx context.Context, in NewWorkItem, actorUserID string) (*WorkItem, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len([]rune(in.Title)) > 500 {
		return nil, apierr.NewValidation("invalid title", apierr.FieldError{Field: "title", Problem: "1-500 chars"})
	}
	if err := s.repo.EnsureStatusSeed(ctx, in.WorkspaceID); err != nil {
		return nil, err
	}
	if in.StatusKey == "" {
		in.StatusKey = domain.StatusBacklog
	}
	defs, err := s.repo.ListStatuses(ctx, in.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if _, ok := domain.WritableCategory(toDomainDefs(defs), in.StatusKey); !ok {
		return nil, apierr.NewValidation("unknown status key")
	}
	if in.CreatorID == "" {
		in.CreatorID = actorUserID
		in.CreatorType = "user"
	}

	subs, subsErr := initialSubscribersOf(actorUserID, in)

	norm := domain.NormalizeTitle(in.Title)
	unlock, err := s.repo.AdvisoryLockTitle(ctx, in.WorkspaceID, norm)
	if err != nil {
		return nil, err
	}
	defer unlock()
	parentID := ptrOrNil(in.ParentID)
	projectID := ptrOrNil(in.ProjectID)
	if n, _ := s.repo.CountActiveDuplicate(ctx, in.WorkspaceID, norm, projectID, parentID,
		domain.ActiveStatusKeys(toDomainDefs(defs)), ""); n > 0 {

		return nil, apierr.New(apierr.ActiveDuplicate, "active duplicate exists")
	}
	if subsErr != nil {
		return nil, subsErr
	}

	env := ev(events.WorkItemCreated, in.WorkspaceID, actorUserID, nil)
	var wi WorkItem
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		created, err := s.repo.CreateWorkItem(ctx, in)
		if err != nil {
			return err
		}
		wi = created

		if err := s.repo.UpsertSubscribers(ctx, wi.ID, subs); err != nil {
			return err
		}
		env.Payload = wiEvent(wi)
		return nil
	}); err != nil {
		return nil, err
	}

	wi.StatusCategory = domain.WireCategory(toDomainDefs(defs), wi.StatusKey)
	return &wi, nil
}

func (s *WorkItemService) Get(ctx context.Context, workspaceID, id string) (*WorkItem, error) {
	wi, ok, err := s.repo.GetWorkItem(ctx, workspaceID, id)
	if err != nil || !ok {
		return nil, apierr.New(apierr.NotFound, "work item not found")
	}
	s.fillWireCategories(ctx, workspaceID, []*WorkItem{&wi})
	return &wi, nil
}

func (s *WorkItemService) List(ctx context.Context, q ListQuery) ([]WorkItem, string, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}

	if err := validateInvolves(q.Involves); err != nil {
		return nil, "", err
	}
	if len(q.Props) > 0 {
		defs, err := s.repo.ListPropertyDefs(ctx, q.WorkspaceID)
		if err != nil {
			return nil, "", err
		}
		resolved, err := resolvePropFilters(toDomainPropDefs(defs), q.Props)
		if err != nil {
			return nil, "", err
		}
		q.PropFilters = resolved
	}
	items, next, err := s.repo.ListWorkItems(ctx, q)
	if err != nil {
		return nil, "", err
	}

	s.fillWireCategories(ctx, q.WorkspaceID, itemsPtr(items))
	return items, next, nil
}

func itemsPtr(items []WorkItem) []*WorkItem {
	out := make([]*WorkItem, len(items))
	for i := range items {
		out[i] = &items[i]
	}
	return out
}

func (s *WorkItemService) fillWireCategories(ctx context.Context, workspaceID string, items []*WorkItem) {
	if len(items) == 0 || workspaceID == "" {
		return
	}
	if err := s.repo.EnsureStatusSeed(ctx, workspaceID); err != nil {
		return
	}
	defs, err := s.repo.ListStatuses(ctx, workspaceID)
	if err != nil {
		return
	}
	dd := toDomainDefs(defs)
	for _, wi := range items {
		wi.StatusCategory = domain.WireCategory(dd, wi.StatusKey)
	}
}

func (s *WorkItemService) Update(ctx context.Context, workspaceID, id string, patch WorkItemPatch, expectedRevision int64, actorUserID string) (*WorkItem, error) {

	current, ok, err := s.repo.GetWorkItem(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "work item not found")
	}
	if domain.TriageUpdateLocked(domain.InTriage(current.TriageState), triageLockOf(patch), domain.PatchClearsTriage(patch.TriageState)) {
		field := triageLockOf(patch).Field()
		return nil, apierr.NewValidation("work item in triage: locked field",
			apierr.FieldError{Field: field, Problem: "locked while triage_state is set; clear triage_state in the same patch to accept"})
	}
	if patch.Title != nil {
		t := strings.TrimSpace(*patch.Title)
		if t == "" {
			return nil, apierr.NewValidation("title cannot be empty")
		}
		patch.Title = &t
	}
	if patch.StatusKey != nil {
		defs, err := s.repo.ListStatuses(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		if _, ok := domain.WritableCategory(toDomainDefs(defs), *patch.StatusKey); !ok {
			return nil, apierr.NewValidation("unknown status key")
		}
	}
	if patch.Properties != nil {
		defs, err := s.repo.ListPropertyDefs(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		norm, err := domain.ValidateWorkItemProperties(toDomainPropDefs(defs), patch.Properties)
		if err != nil {
			return nil, apierr.NewValidation(err.Error())
		}
		patch.Properties = norm
	}

	if patch.DuplicateOf != nil && *patch.DuplicateOf != "" {
		if *patch.DuplicateOf == id {
			return nil, apierr.NewValidation("cannot mark duplicate of itself",
				apierr.FieldError{Field: "duplicate_of_work_item_id", Problem: "self reference"})
		}
		if _, ok, err := s.repo.GetWorkItem(ctx, workspaceID, *patch.DuplicateOf); err != nil {
			return nil, err
		} else if !ok {
			return nil, apierr.NewValidation("duplicate_of work item not found",
				apierr.FieldError{Field: "duplicate_of_work_item_id", Problem: "not found"})
		}
	}

	if patch.ParentID != nil {
		parent := strings.TrimSpace(*patch.ParentID)
		if parent == "" {
			patch.ParentID = ptrOrNil("")
		} else {
			if parent == id {
				return nil, apierr.NewValidation("cannot set parent to itself",
					apierr.FieldError{Field: "parent_work_item_id", Problem: "self reference"})
			}
			if _, ok, err := s.repo.GetWorkItem(ctx, workspaceID, parent); err != nil {
				return nil, err
			} else if !ok {
				return nil, apierr.NewValidation("parent work item not found",
					apierr.FieldError{Field: "parent_work_item_id", Problem: "not found"})
			}

			cursor, depth := parent, 0
			for cursor != "" && depth < 64 {
				if cursor == id {
					return nil, apierr.NewValidation("parent chain forms a cycle",
						apierr.FieldError{Field: "parent_work_item_id", Problem: "cycle"})
				}
				row, ok, err := s.repo.GetWorkItem(ctx, workspaceID, cursor)
				if err != nil {
					return nil, err
				} else if !ok {
					break
				}
				cursor = row.ParentID
				depth++
			}
			patch.ParentID = &parent
		}
	}

	if patch.StartDate != nil && *patch.StartDate != "" {
		if _, err := time.Parse("2006-01-02", *patch.StartDate); err != nil {
			return nil, apierr.NewValidation("invalid start_date",
				apierr.FieldError{Field: "start_date", Problem: "must be YYYY-MM-DD"})
		}
	}
	if patch.DueDate != nil && *patch.DueDate != "" {
		if _, err := time.Parse("2006-01-02", *patch.DueDate); err != nil {
			return nil, apierr.NewValidation("invalid due_date",
				apierr.FieldError{Field: "due_date", Problem: "must be YYYY-MM-DD"})
		}
	}

	env := ev(events.WorkItemUpdated, workspaceID, actorUserID, nil)
	var wi WorkItem
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		updated, err := s.repo.UpdateWorkItem(ctx, workspaceID, id, patch, expectedRevision)
		if err != nil {
			return err
		}
		wi = updated

		if wi.AssigneeType == "user" && wi.AssigneeID != "" {
			_ = s.repo.UpsertSubscriber(ctx, wi.ID, wi.AssigneeID, domain.SubSourceAssignee)
		}
		env.Payload = wiEvent(wi)
		return nil
	}); err != nil {
		return nil, err
	}
	return &wi, nil
}

var workItemTaskCanceller func(ctx context.Context, workspaceID, workItemID string) error

func SetWorkItemTaskCanceller(f func(ctx context.Context, workspaceID, workItemID string) error) {
	workItemTaskCanceller = f
}

func (s *WorkItemService) Delete(ctx context.Context, workspaceID, id, actorUserID string) error {
	if _, err := s.Get(ctx, workspaceID, id); err != nil {
		return err
	}
	if workItemTaskCanceller != nil {
		_ = workItemTaskCanceller(ctx, workspaceID, id)
	}

	env := ev(events.WorkItemDeleted, workspaceID, actorUserID,
		events.WorkItemPayload{ID: id, WorkspaceID: workspaceID})
	return s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		return s.repo.DeleteWorkItem(ctx, workspaceID, id)
	})
}

func (s *WorkItemService) Move(ctx context.Context, workspaceID, id string, before, after *string) error {
	wi, ok, err := s.repo.GetWorkItem(ctx, workspaceID, id)
	if err != nil || !ok {
		return apierr.New(apierr.NotFound, "work item not found")
	}
	if before == nil && after == nil {
		min, err := s.repo.NextPositionInStatus(ctx, workspaceID, wi.StatusKey)
		if err != nil {
			return err
		}
		return s.repo.MovePosition(ctx, workspaceID, id, domain.NextPositionTop(min))
	}

	posOf := func(anchor string) (int64, error) {
		a, ok, err := s.repo.GetWorkItem(ctx, workspaceID, anchor)
		if err != nil {
			return 0, err
		}
		if !ok || a.StatusKey != wi.StatusKey {
			return 0, apierr.New(apierr.Validation, "anchor must be a work item in the same status column")
		}
		return a.Position, nil
	}
	tail := func() error {
		pos, err := s.repo.NextPositionInStatus(ctx, workspaceID, wi.StatusKey)
		if err != nil {
			return err
		}
		return s.repo.MovePosition(ctx, workspaceID, id, domain.PositionBetween(pos, pos+2))
	}
	switch {
	case before != nil && after != nil:
		if *before == *after || *before == id || *after == id {
			return apierr.New(apierr.Validation, "anchors must differ and not reference the moved item")
		}
		pb, err := posOf(*before)
		if err != nil {
			return err
		}
		pa, err := posOf(*after)
		if err != nil {
			return err
		}
		lo, hi := pb, pa
		if lo > hi {
			lo, hi = hi, lo
		}
		if hi-lo > 1 {
			return s.repo.MovePosition(ctx, workspaceID, id, lo+(hi-lo)/2)
		}
		return tail()
	case before != nil:
		if *before == id {
			return apierr.New(apierr.Validation, "anchor must not reference the moved item")
		}
		pb, err := posOf(*before)
		if err != nil {
			return err
		}
		return s.repo.MovePosition(ctx, workspaceID, id, pb-1)
	default:
		if *after == id {
			return apierr.New(apierr.Validation, "anchor must not reference the moved item")
		}
		pa, err := posOf(*after)
		if err != nil {
			return err
		}
		return s.repo.MovePosition(ctx, workspaceID, id, pa+1)
	}
}

func (s *WorkItemService) AttachLabel(ctx context.Context, workspaceID, workItemID, labelID string) ([]Label, error) {
	if _, err := s.Get(ctx, workspaceID, workItemID); err != nil {
		return nil, err
	}
	if _, ok, err := s.repo.GetLabel(ctx, workspaceID, labelID); err != nil {
		return nil, err
	} else if !ok {
		return nil, apierr.New(apierr.NotFound, "label not found")
	}
	if err := s.repo.AttachWorkItemLabel(ctx, workspaceID, workItemID, labelID); err != nil {
		return nil, err
	}
	return s.repo.GetWorkItemLabels(ctx, workItemID)
}

func (s *WorkItemService) DetachLabel(ctx context.Context, workspaceID, workItemID, labelID string) ([]Label, error) {
	if _, err := s.Get(ctx, workspaceID, workItemID); err != nil {
		return nil, err
	}
	if err := s.repo.DetachWorkItemLabel(ctx, workspaceID, workItemID, labelID); err != nil {
		return nil, err
	}
	return s.repo.GetWorkItemLabels(ctx, workItemID)
}

func (s *WorkItemService) Subscribe(ctx context.Context, workspaceID, workItemID, userID string) (*Subscriber, error) {
	if _, err := s.Get(ctx, workspaceID, workItemID); err != nil {
		return nil, err
	}

	var sub Subscriber
	envs := make([]events.Envelope, 0, 1)
	if err := s.emitCommittedEnvs(ctx, &envs, func(ctx context.Context) error {
		row, created, err := s.repo.UpsertManualSubscriber(ctx, workspaceID, workItemID, userID)
		if err != nil {
			return err
		}
		sub = row
		if created {
			envs = append(envs, ev("subscriber.added", workspaceID, userID, map[string]any{
				"work_item_id": workItemID, "user_id": userID, "source": domain.SubSourceManual,
			}))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return &sub, nil
}

func toDomainDefs(defs []StatusDef) []domain.StatusDef {
	out := make([]domain.StatusDef, len(defs))
	for i, d := range defs {
		out[i] = domain.StatusDef{Key: d.Key, Name: d.Name, Category: d.Category, IsBuiltin: d.IsBuiltin, Position: d.Position}
	}
	return out
}

func toDomainPropDefs(defs []PropertyDef) []domain.PropertyDef {
	out := make([]domain.PropertyDef, len(defs))
	for i, d := range defs {
		out[i] = domain.PropertyDef{Key: d.Key, Name: d.Name, Type: d.Type, Options: d.Options}
	}
	return out
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func triageLockOf(patch WorkItemPatch) domain.TriagePatchLock {
	return domain.TriagePatchLock{
		AssigneeChanged: patch.AssigneeType != nil || patch.AssigneeID != nil,
		StatusChanged:   patch.StatusKey != nil,
		ParentChanged:   patch.ParentID != nil,
	}
}

func (s *WorkItemService) CountInTriage(ctx context.Context, workspaceID string, ids []string) (int, error) {
	uuids := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := domain.ParseUUID(id); ok {
			uuids = append(uuids, id)
		}
	}
	if len(uuids) == 0 {
		return 0, nil
	}
	return s.repo.CountWorkItemsInTriage(ctx, workspaceID, uuids)
}

func (s *WorkItemService) CheckBatchTriageLock(ctx context.Context, workspaceID string, ids []string, patch WorkItemPatch) error {
	lock := triageLockOf(patch)
	if !lock.Locked() {
		return nil
	}
	n, err := s.CountInTriage(ctx, workspaceID, ids)
	if err != nil {
		return err
	}
	if n > 0 {
		return apierr.NewValidation("batch update blocked: work item in triage",
			apierr.FieldError{Field: lock.Field(), Problem: "locked while triage_state is set"})
	}
	return nil
}

type WriteProbeFacts = domain.WriteTriggerFacts

func (s *WorkItemService) WriteTriggerProbe(ctx context.Context, workspaceID, id string, f WriteProbeFacts) domain.ProbeDecision {
	if wi, ok, err := s.repo.GetWorkItem(ctx, workspaceID, id); err == nil && ok {
		f.TriageState = wi.TriageState
		f.CreatorType = wi.CreatorType
		f.CreatorID = wi.CreatorID
		if f.AssigneeType == "" {
			f.AssigneeType = wi.AssigneeType
			f.AssigneeID = wi.AssigneeID
		}
	}
	return domain.WriteTriggerProbe(f)
}

func ev(name, wsID, actor string, payload any) events.Envelope {
	return events.Envelope{Event: name, WorkspaceID: wsID, ActorUserID: actor, OccurredAt: time.Now().UTC(), Payload: payload}
}

func wiEvent(wi WorkItem) events.WorkItemPayload {
	return events.WorkItemPayload{
		ID: wi.ID, WorkspaceID: wi.WorkspaceID, Number: wi.Number, Title: wi.Title,
		Status: wi.StatusKey, Revision: wi.Revision, AssigneeType: wi.AssigneeType,
		AssigneeID: wi.AssigneeID, CreatorType: wi.CreatorType, CreatorID: wi.CreatorID,
	}
}

func (s *WorkItemService) Labels(ctx context.Context, workspaceID, workItemID string) ([]Label, error) {
	if _, err := s.Get(ctx, workspaceID, workItemID); err != nil {
		return nil, err
	}
	return s.repo.GetWorkItemLabels(ctx, workItemID)
}

func (s *WorkItemService) Unsubscribe(ctx context.Context, workspaceID, workItemID, userID, targetUser string) (bool, error) {
	if _, err := s.Get(ctx, workspaceID, workItemID); err != nil {
		return false, err
	}
	if targetUser == "" {
		targetUser = userID
	}

	var removed bool
	envs := make([]events.Envelope, 0, 1)
	if err := s.emitCommittedEnvs(ctx, &envs, func(ctx context.Context) error {
		del, err := s.repo.RemoveSubscriber(ctx, workspaceID, workItemID, targetUser)
		if err != nil {
			return err
		}
		removed = del
		if removed {
			envs = append(envs, ev("subscriber.removed", workspaceID, userID, map[string]any{
				"work_item_id": workItemID, "user_id": targetUser,
			}))
		}
		return nil
	}); err != nil {
		return false, err
	}
	return removed, nil
}

func (s *WorkItemService) UnsubscribeSubtree(ctx context.Context, workspaceID, workItemID, userID, targetUser string) ([]string, error) {
	if _, err := s.Get(ctx, workspaceID, workItemID); err != nil {
		return nil, err
	}
	if targetUser == "" {
		targetUser = userID
	}
	var removed []string
	envs := make([]events.Envelope, 0, 4)
	if err := s.emitCommittedEnvs(ctx, &envs, func(ctx context.Context) error {
		del, err := s.repo.RemoveSubscriberSubtree(ctx, workspaceID, workItemID, targetUser)
		if err != nil {
			return err
		}
		removed = del
		for _, id := range removed {
			envs = append(envs, ev("subscriber.removed", workspaceID, userID, map[string]any{
				"work_item_id": id, "user_id": targetUser,
			}))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return removed, nil
}

func (s *WorkItemService) DeleteAllSubscriptionsByUser(ctx context.Context, workspaceID, userID string) (int64, error) {
	return s.repo.DeleteAllSubscriptionsByUser(ctx, workspaceID, userID)
}

func (s *WorkItemService) Usage(ctx context.Context, workspaceID, id string) ([]WorkItemUsage, error) {
	if _, err := s.Get(ctx, workspaceID, id); err != nil {
		return nil, err
	}
	return s.repo.ListWorkItemUsage(ctx, workspaceID, id)
}

func (s *WorkItemService) Duplicates(ctx context.Context, workspaceID, id string) ([]WorkItem, error) {
	if _, err := s.Get(ctx, workspaceID, id); err != nil {
		return nil, err
	}
	return s.repo.ListDuplicates(ctx, workspaceID, id)
}

const (
	metadataKeyMaxLen   = 128
	metadataValueMaxLen = 16 << 10
)

func metadataKeyOK(key string) bool {
	return key != "" && len(key) <= metadataKeyMaxLen
}

func (s *WorkItemService) GetMetadataKey(ctx context.Context, workspaceID, id, key string) (json.RawMessage, error) {
	if !metadataKeyOK(key) {
		return nil, apierr.NewValidation("invalid metadata key", apierr.FieldError{Field: "key", Problem: "1-128 chars"})
	}
	val, itemExists, keyExists, err := s.repo.GetMetadataKey(ctx, workspaceID, id, key)
	if err != nil {
		return nil, err
	}
	if !itemExists {
		return nil, apierr.New(apierr.NotFound, "work item not found")
	}
	if !keyExists {
		return nil, apierr.New(apierr.NotFound, "metadata key not found")
	}
	return val, nil
}

func (s *WorkItemService) SetMetadataKey(ctx context.Context, workspaceID, id, key string, value json.RawMessage, actorUserID string) (map[string]any, *WorkItem, error) {
	if !metadataKeyOK(key) {
		return nil, nil, apierr.NewValidation("invalid metadata key", apierr.FieldError{Field: "key", Problem: "1-128 chars"})
	}
	if len(value) == 0 {
		return nil, nil, apierr.NewValidation("metadata value required")
	}
	if len(value) > metadataValueMaxLen {
		return nil, nil, apierr.NewValidation("metadata value too large", apierr.FieldError{Field: "value", Problem: "<= 16KB"})
	}
	var meta map[string]any
	var wi WorkItem
	envs := make([]events.Envelope, 0, 1)
	if err := s.emitCommittedEnvs(ctx, &envs, func(ctx context.Context) error {
		m, ok, err := s.repo.UpdateMetadataKey(ctx, workspaceID, id, key, value)
		if err != nil {
			return err
		}
		if !ok {
			return apierr.New(apierr.NotFound, "work item not found")
		}
		meta = m
		w, _, _ := s.repo.GetWorkItem(ctx, workspaceID, id)
		wi = w
		if wi.ID != "" {
			envs = append(envs, ev(events.WorkItemUpdated, workspaceID, actorUserID, wiEvent(wi)))
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return meta, &wi, nil
}

func (s *WorkItemService) DeleteMetadataKey(ctx context.Context, workspaceID, id, key string, actorUserID string) error {
	if !metadataKeyOK(key) {
		return apierr.NewValidation("invalid metadata key", apierr.FieldError{Field: "key", Problem: "1-128 chars"})
	}
	envs := make([]events.Envelope, 0, 1)
	return s.emitCommittedEnvs(ctx, &envs, func(ctx context.Context) error {
		ok, err := s.repo.DeleteMetadataKey(ctx, workspaceID, id, key)
		if err != nil {
			return err
		}
		if !ok {
			return apierr.New(apierr.NotFound, "work item not found")
		}
		if wi, found, _ := s.repo.GetWorkItem(ctx, workspaceID, id); found {
			envs = append(envs, ev(events.WorkItemUpdated, workspaceID, actorUserID, wiEvent(wi)))
		}
		return nil
	})
}

func propertyKeyOK(key string) bool {
	return key != "" && len(key) <= metadataKeyMaxLen
}

func (s *WorkItemService) GetPropertyKey(ctx context.Context, workspaceID, id, key string) (json.RawMessage, error) {
	if !propertyKeyOK(key) {
		return nil, apierr.NewValidation("invalid property key", apierr.FieldError{Field: "key", Problem: "1-128 chars"})
	}
	val, itemExists, keyExists, err := s.repo.GetPropertyKey(ctx, workspaceID, id, key)
	if err != nil {
		return nil, err
	}
	if !itemExists {
		return nil, apierr.New(apierr.NotFound, "work item not found")
	}
	if !keyExists {
		return nil, apierr.New(apierr.NotFound, "property key not found")
	}
	return val, nil
}

func (s *WorkItemService) SetPropertyKey(ctx context.Context, workspaceID, id, key string, value json.RawMessage, actorUserID string) (any, map[string]any, *WorkItem, error) {
	if !propertyKeyOK(key) {
		return nil, nil, nil, apierr.NewValidation("invalid property key", apierr.FieldError{Field: "key", Problem: "1-128 chars"})
	}
	if len(value) == 0 {
		return nil, nil, nil, apierr.NewValidation("property value required")
	}
	if len(value) > metadataValueMaxLen {
		return nil, nil, nil, apierr.NewValidation("property value too large", apierr.FieldError{Field: "value", Problem: "<= 16KB"})
	}
	if _, err := s.Get(ctx, workspaceID, id); err != nil {
		return nil, nil, nil, err
	}
	var v any
	if err := json.Unmarshal(value, &v); err != nil {
		return nil, nil, nil, apierr.NewValidation("invalid JSON value")
	}
	defs, err := s.repo.ListPropertyDefs(ctx, workspaceID)
	if err != nil {
		return nil, nil, nil, err
	}
	norm, err := domain.ValidateWorkItemProperties(toDomainPropDefs(defs), map[string]any{key: v})
	if err != nil {
		return nil, nil, nil, apierr.NewValidation(err.Error(), apierr.FieldError{Field: key, Problem: err.Error()})
	}
	var props map[string]any
	var wi WorkItem
	envs := make([]events.Envelope, 0, 1)
	if err := s.emitCommittedEnvs(ctx, &envs, func(ctx context.Context) error {
		if norm[key] == nil {

			exists, err := s.repo.DeletePropertyKey(ctx, workspaceID, id, key)
			if err != nil {
				return err
			}
			if !exists {
				return apierr.New(apierr.NotFound, "work item not found")
			}
			props = map[string]any{}
		} else {
			normJSON, err := json.Marshal(norm[key])
			if err != nil {
				return err
			}
			p, ok, err := s.repo.SetPropertyKey(ctx, workspaceID, id, key, normJSON)
			if err != nil {
				return err
			}
			if !ok {
				return apierr.New(apierr.NotFound, "work item not found")
			}
			props = p
		}
		w, _, _ := s.repo.GetWorkItem(ctx, workspaceID, id)
		wi = w
		if wi.ID != "" {
			props = wi.Properties
			envs = append(envs, ev(events.WorkItemUpdated, workspaceID, actorUserID, wiEvent(wi)))
		}
		return nil
	}); err != nil {
		return nil, nil, nil, err
	}
	return norm[key], props, &wi, nil
}

func (s *WorkItemService) DeletePropertyKey(ctx context.Context, workspaceID, id, key string, actorUserID string) error {
	if !propertyKeyOK(key) {
		return apierr.NewValidation("invalid property key", apierr.FieldError{Field: "key", Problem: "1-128 chars"})
	}
	envs := make([]events.Envelope, 0, 1)
	return s.emitCommittedEnvs(ctx, &envs, func(ctx context.Context) error {
		exists, err := s.repo.DeletePropertyKey(ctx, workspaceID, id, key)
		if err != nil {
			return err
		}
		if !exists {
			return apierr.New(apierr.NotFound, "work item not found")
		}
		if wi, found, _ := s.repo.GetWorkItem(ctx, workspaceID, id); found {
			envs = append(envs, ev(events.WorkItemUpdated, workspaceID, actorUserID, wiEvent(wi)))
		}
		return nil
	})
}

const emojiMaxLen = 32

func (s *WorkItemService) AddReaction(ctx context.Context, workspaceID, id, userID, emoji string) ([]ReactionCount, error) {
	if emoji == "" || len(emoji) > emojiMaxLen {
		return nil, apierr.NewValidation("invalid emoji", apierr.FieldError{Field: "emoji", Problem: "1-32 chars"})
	}
	if _, err := s.Get(ctx, workspaceID, id); err != nil {
		return nil, err
	}

	env := ev("work_item_reaction.added", workspaceID, userID,
		map[string]any{"work_item_id": id, "emoji": emoji})
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		return s.repo.UpsertReaction(ctx, workspaceID, id, userID, emoji)
	}); err != nil {
		return nil, err
	}
	return s.repo.ListReactionCounts(ctx, workspaceID, id)
}

func (s *WorkItemService) RemoveReaction(ctx context.Context, workspaceID, id, userID, emoji string) ([]ReactionCount, error) {
	if emoji == "" || len(emoji) > emojiMaxLen {
		return nil, apierr.NewValidation("invalid emoji", apierr.FieldError{Field: "emoji", Problem: "1-32 chars"})
	}
	if _, err := s.Get(ctx, workspaceID, id); err != nil {
		return nil, err
	}
	env := ev("work_item_reaction.removed", workspaceID, userID,
		map[string]any{"work_item_id": id, "emoji": emoji})
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		_, err := s.repo.DeleteReaction(ctx, workspaceID, id, userID, emoji)
		return err
	}); err != nil {
		return nil, err
	}
	return s.repo.ListReactionCounts(ctx, workspaceID, id)
}

func (s *WorkItemService) ChildProgress(ctx context.Context, workspaceID, parentID string) ([]map[string]any, error) {
	if parentID == "" {
		return nil, apierr.NewValidation("parent query parameter required",
			apierr.FieldError{Field: "parent", Problem: "required"})
	}
	if _, err := s.Get(ctx, workspaceID, parentID); err != nil {
		return nil, err
	}
	return s.repo.ChildProgress(ctx, workspaceID, parentID)
}
