package app

import (
	"context"
	"encoding/json"
	"time"

	"orkome/internal/contract/events"
	"orkome/internal/work/domain"
)

type Repo interface {
	EnsureStatusSeed(ctx context.Context, workspaceID string) error
	ListStatuses(ctx context.Context, workspaceID string) ([]StatusDef, error)
	CreateStatus(ctx context.Context, workspaceID, key, name, category string, position int) (StatusDef, error)
	ReorderStatuses(ctx context.Context, workspaceID string, keys []string) error

	GetStatus(ctx context.Context, workspaceID, key string) (StatusDef, bool, error)

	UpdateStatus(ctx context.Context, workspaceID, key string, p StatusDefPatch) (StatusDef, bool, error)

	CreateWorkItem(ctx context.Context, in NewWorkItem) (WorkItem, error)
	GetWorkItem(ctx context.Context, workspaceID, id string) (WorkItem, bool, error)
	UpdateWorkItem(ctx context.Context, workspaceID, id string, patch WorkItemPatch, expectedRevision int64) (WorkItem, error)
	DeleteWorkItem(ctx context.Context, workspaceID, id string) error

	CountWorkItemsInTriage(ctx context.Context, workspaceID string, ids []string) (int, error)
	ListWorkItems(ctx context.Context, q ListQuery) ([]WorkItem, string, error)
	MovePosition(ctx context.Context, workspaceID, id string, position int64) error
	NextPositionInStatus(ctx context.Context, workspaceID, statusKey string) (int64, error)
	CountActiveDuplicate(ctx context.Context, workspaceID string, normTitle string, projectID *string, parentID *string, activeKeys []string, excludeID string) (int, error)
	AdvisoryLockTitle(ctx context.Context, workspaceID, normTitle string) (func(), error)

	UpsertLabel(ctx context.Context, workspaceID, name, color string) (Label, error)
	UpdateLabel(ctx context.Context, workspaceID, id, name, color string) (Label, bool, error)
	DeleteLabel(ctx context.Context, workspaceID, id string) error
	ListLabels(ctx context.Context, workspaceID string) ([]Label, error)
	SetWorkItemLabels(ctx context.Context, workItemID string, labelIDs []string) error
	GetWorkItemLabels(ctx context.Context, workItemID string) ([]Label, error)

	AttachWorkItemLabel(ctx context.Context, workspaceID, workItemID, labelID string) error
	DetachWorkItemLabel(ctx context.Context, workspaceID, workItemID, labelID string) error
	ListProjects(ctx context.Context, workspaceID string) ([]Project, error)
	CreateProject(ctx context.Context, workspaceID, name, desc string) (Project, error)

	GetProject(ctx context.Context, workspaceID, id string) (Project, bool, error)
	UpdateProject(ctx context.Context, workspaceID, id, name, desc string) (Project, bool, error)
	DeleteProject(ctx context.Context, workspaceID, id string) error

	CountWorkItemsInProject(ctx context.Context, workspaceID, projectID string) (int, error)

	SearchProjects(ctx context.Context, workspaceID, q string, limit int) ([]Project, error)

	ListProjectResources(ctx context.Context, workspaceID, projectID string) ([]ProjectResource, error)

	CreateProjectResource(ctx context.Context, workspaceID, projectID, kind, uri string) (ProjectResource, bool, error)
	UpdateProjectResource(ctx context.Context, workspaceID, projectID, resourceID, kind, uri string) (ProjectResource, bool, error)

	DeleteProjectResource(ctx context.Context, workspaceID, projectID, resourceID string) error
	ListPropertyDefs(ctx context.Context, workspaceID string) ([]PropertyDef, error)
	CreatePropertyDef(ctx context.Context, def PropertyDef, workspaceID string) (PropertyDef, error)

	GetPropertyDef(ctx context.Context, workspaceID, key string) (PropertyDef, bool, error)
	UpdatePropertyDef(ctx context.Context, workspaceID, key string, p PropertyDefPatch) (PropertyDef, bool, error)

	ArchivePropertyDef(ctx context.Context, workspaceID, key string) (archived, exists bool, err error)

	CreateComment(ctx context.Context, c NewComment) (Comment, error)
	GetComment(ctx context.Context, id string) (Comment, bool, error)
	UpdateComment(ctx context.Context, id, content string, expectedRevision int64) (Comment, error)
	TombstoneComment(ctx context.Context, id string) error
	SetResolved(ctx context.Context, id string, resolved bool, byType, byID string) error
	ListComments(ctx context.Context, workItemID string) ([]Comment, error)
	ListCommentPage(ctx context.Context, workItemID string, limit int, before string) ([]Comment, error)
	TouchWorkItem(ctx context.Context, workItemID string) error

	InsertSourceContext(ctx context.Context, workspaceID, digest string, snapshot []byte, createdBy string) (id string, created bool, err error)
	GetSourceContext(ctx context.Context, workspaceID, digest string) (id string, snapshot []byte, ok bool, err error)

	UpsertSubscriber(ctx context.Context, workItemID, userID, source string) error

	UpsertManualSubscriber(ctx context.Context, workspaceID, workItemID, userID string) (Subscriber, bool, error)

	UpsertSubscribers(ctx context.Context, workItemID string, subs []SubscriberWrite) error
	ListSubscribers(ctx context.Context, workItemID string) ([]Subscriber, error)

	RemoveSubscriber(ctx context.Context, workspaceID, workItemID, userID string) (bool, error)

	RemoveSubscriberSubtree(ctx context.Context, workspaceID, workItemID, userID string) ([]string, error)

	DeleteAllSubscriptionsByUser(ctx context.Context, workspaceID, userID string) (int64, error)
	AppendActivity(ctx context.Context, eventID, workspaceID, workItemID, actorType, actorID, kind string, payload map[string]any) error
	ListTimeline(ctx context.Context, workItemID string, limit int, before string) ([]TimelineEntry, error)

	ListWorkItemViews(ctx context.Context, workspaceID, userID string) ([]View, error)

	UpsertWorkItemView(ctx context.Context, workspaceID, userID string, v View) (View, bool, error)

	GetViewPreference(ctx context.Context, workspaceID, userID string) (viewID string, ok bool, err error)

	SetViewPreference(ctx context.Context, workspaceID, userID string, viewID *string) error

	QueryRows(ctx context.Context, sql string, args []any) ([]map[string]any, error)

	SearchRows(ctx context.Context, sql string, args []any) ([]map[string]any, error)

	GetLabel(ctx context.Context, workspaceID, id string) (Label, bool, error)

	AgentLabelTarget(ctx context.Context, workspaceID, agentID string) (bool, error)

	SkillLabelTarget(ctx context.Context, workspaceID, skillID string) (bool, error)

	AttachAgentLabel(ctx context.Context, workspaceID, agentID, labelID string) error
	DetachAgentLabel(ctx context.Context, workspaceID, agentID, labelID string) error
	ListAgentLabels(ctx context.Context, workspaceID, agentID string) ([]Label, error)
	AttachSkillLabel(ctx context.Context, workspaceID, skillID, labelID string) error
	DetachSkillLabel(ctx context.Context, workspaceID, skillID, labelID string) error
	ListSkillLabels(ctx context.Context, workspaceID, skillID string) ([]Label, error)

	SearchIndexSweepOrphans(ctx context.Context, olderThan time.Time, limit int) (int, error)

	PruneSearchIndexChanges(ctx context.Context, cutoff time.Time, limit int) (int, error)

	SearchIndexPruneMark(ctx context.Context) (string, bool, error)

	ClientUsageUpsert(ctx context.Context, in ClientUsageReport) error

	ListWorkItemUsage(ctx context.Context, workspaceID, id string) ([]WorkItemUsage, error)

	ListDuplicates(ctx context.Context, workspaceID, id string) ([]WorkItem, error)

	GetMetadataKey(ctx context.Context, workspaceID, id, key string) (val json.RawMessage, itemExists, keyExists bool, err error)

	UpdateMetadataKey(ctx context.Context, workspaceID, id, key string, value json.RawMessage) (map[string]any, bool, error)

	DeleteMetadataKey(ctx context.Context, workspaceID, id, key string) (bool, error)

	UpsertReaction(ctx context.Context, workspaceID, id, userID, emoji string) error

	DeleteReaction(ctx context.Context, workspaceID, id, userID, emoji string) (bool, error)

	ListReactionCounts(ctx context.Context, workspaceID, id string) ([]ReactionCount, error)

	ChildProgress(ctx context.Context, workspaceID, parentID string) ([]map[string]any, error)

	UpsertCommentReaction(ctx context.Context, workspaceID, commentID, userID, emoji string) error

	DeleteCommentReaction(ctx context.Context, workspaceID, commentID, userID, emoji string) (bool, error)

	ListCommentReactionCounts(ctx context.Context, workspaceID, commentID string) ([]ReactionCount, error)

	GetPropertyKey(ctx context.Context, workspaceID, id, key string) (val json.RawMessage, itemExists, keyExists bool, err error)

	SetPropertyKey(ctx context.Context, workspaceID, id, key string, value json.RawMessage) (map[string]any, bool, error)

	DeletePropertyKey(ctx context.Context, workspaceID, id, key string) (bool, error)
}

type StatusDefPatch struct {
	Name     *string
	Icon     *string
	Category *string
}

type PropertyDefPatch struct {
	Name        *string
	Description *string
	Options     []string
}

type NewWorkItem struct {
	WorkspaceID  string
	Title        string
	Description  string
	StatusKey    string
	Priority     int
	AssigneeType string
	AssigneeID   string
	CreatorType  string
	CreatorID    string
	ParentID     string
	ProjectID    string
	OriginType   string
	OriginID     string
	TriageState  string

	InitialSubscribers []SubscriberSpec

	Delegation domain.DelegationChain
}

type SubscriberSpec struct {
	UserID string
	Source string
}

type WorkItemPatch struct {
	Title        *string
	Description  *string
	StatusKey    *string
	Priority     *int
	AssigneeType *string
	AssigneeID   *string
	ParentID     *string
	ProjectID    *string
	DueDate      *string
	StartDate    *string
	Properties   map[string]any
	TriageState  *string
	DuplicateOf  *string
	// Outcome 非 nil 时覆盖工单产出（人工编辑路径）；nil = 不动。
	Outcome *Outcome
}

type ListQuery struct {
	WorkspaceID  string
	StatusKeys   []string
	AssigneeID   string
	CreatorID    string
	SubscriberID string
	LabelID      string
	ProjectID    string
	ParentID     *string
	Search       string
	Limit        int
	Cursor       string

	Involves string

	Props map[string]string

	PropFilters []domain.PropFilter
}

type NewComment struct {
	WorkItemID  string
	WorkspaceID string
	AuthorType  string
	AuthorID    string
	Content     string
	ParentID    string
}

type Emitter interface {
	Emit(ctx context.Context, e events.Envelope)
}

type OutboxRecorder interface {
	Append(ctx context.Context, envs ...events.Envelope) error

	MarkPublished(ctx context.Context, ids ...string) error
}

type Within func(ctx context.Context, fn func(ctx context.Context) error) error

type Clock func() time.Time
