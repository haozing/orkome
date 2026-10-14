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

	// —— Project Workflow 定义面（W1；docs/wf-interfaces-frozen.md §2）——
	UpsertWorkflow(ctx context.Context, wsID, projectID string, in WorkflowUpsert) (*WorkflowDefinition, error)
	GetWorkflow(ctx context.Context, wsID, projectID string) (*WorkflowDefinition, error)
	DeleteWorkflow(ctx context.Context, wsID, projectID string) error
	SetWorkflowEnabled(ctx context.Context, wsID, projectID string, enabled bool) (bool, error)

	// —— Project Workflow 状态面（W2 追加段；契约 §2 的 W2 扩展，全部只摸
	// work 域四张 workflow 表。裁决记录见 docs/wf-w2-notes.md §关键裁决）——
	// GetWorkflowByID 按定义 ID 读（state 只存 workflow_id 软引用；无则 nil,nil）。
	GetWorkflowByID(ctx context.Context, wsID, workflowID string) (*WorkflowDefinition, error)
	// GetWorkflowState 读工单的推进光标（无则 nil,nil）。
	GetWorkflowState(ctx context.Context, wsID, workItemID string) (*WorkflowStateRecord, error)
	// InsertWorkflowState 建光标（UNIQUE work_item_id 兜底并发 Start：
	// ON CONFLICT DO NOTHING，未插入时回读现存行 inserted=false）。
	InsertWorkflowState(ctx context.Context, wsID string, in WorkflowStateInsert) (rec *WorkflowStateRecord, inserted bool, err error)
	// AdvanceWorkflowStateCAS 推进乐观锁：stage 前进 + advance_count+1 +
	// dispatch_count 清零 + entered_at=now，WHERE stage_id=fromStageID。
	// affected=0（并发输家）→ (nil, false, nil)。
	AdvanceWorkflowStateCAS(ctx context.Context, wsID, stateID, fromStageID, toStageID string) (*WorkflowStateRecord, bool, error)
	// RestartWorkflowState 光标重置回入口阶段：计数清零、finished_at 置空、entered_at=now。
	RestartWorkflowState(ctx context.Context, wsID, stateID, entryStageID string) (bool, error)
	// FinishWorkflowState 终态化（completed/detached 共用）：finished_at=now()
	// WHERE finished_at IS NULL（已终态返回 false，不重复写）。
	FinishWorkflowState(ctx context.Context, wsID, stateID string) (bool, error)
	// AdjWorkflowDispatchCount 派发计数增减（前置+1；blocked 未派出去回减）。
	AdjWorkflowDispatchCount(ctx context.Context, wsID, stateID string, delta int) error
	// AppendTransitionLog 推进流水一行（from/task_id 空串 = NULL）。
	AppendTransitionLog(ctx context.Context, wsID string, e TransitionLogEntry) error
	// AppendTransitionNote 在最新一条流水上追加 note（coalesced 标记用）。
	AppendTransitionNote(ctx context.Context, wsID, stateID, note string) error
}

// WorkflowStateRecord work_item_workflow_state 行（状态面读写的中立形状；
// service 组装 WorkflowState 契约形状，repo 不感知定义表）。
type WorkflowStateRecord struct {
	ID            string
	WorkItemID    string
	WorkflowID    string
	StageID       string
	EnteredAt     time.Time
	DispatchCount int
	AdvanceCount  int
	FinishedAt    *time.Time
}

// WorkflowStateInsert 建光标入参。
type WorkflowStateInsert struct {
	WorkItemID string
	WorkflowID string
	StageID    string
}

// TransitionLogEntry workflow_transition_log 一行。
type TransitionLogEntry struct {
	StateID     string
	FromStageID string // "" = NULL（进入入口阶段）
	ToStageID   string
	ActorType   string // agent|user|system
	ActorID     string
	TaskID      string // "" = NULL
	Note        string
}

// WorkflowUpsert 流程定义整体替换的 repo 写入形状（stages 已排序已校验）。
type WorkflowUpsert struct {
	Name               string
	TriggerMode        string
	EntryStagePosition int
	CreatedBy          string
	Stages             []domain.WorkflowStageDef
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
