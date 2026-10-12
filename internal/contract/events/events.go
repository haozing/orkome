package events

import "time"

const ChatSessionDeleted = "chat:session_deleted"

const WorkItemUpdatedByAgent = "work_item:updated_by_agent"

const (
	NotificationRead          = "notification:read"
	NotificationUnread        = "notification:unread"
	NotificationBatchRead     = "notification:batch_read"
	NotificationArchived      = "notification:archived"
	NotificationUnarchived    = "notification:unarchived"
	NotificationBatchArchived = "notification:batch_archived"
)

type Envelope struct {
	EventID     string
	Event       string
	WorkspaceID string
	ActorUserID string
	OccurredAt  time.Time
	Payload     any
}

const (
	WorkspaceCreated = "workspace.created"
	WorkspaceUpdated = "workspace.updated"
	WorkspaceDeleted = "workspace.deleted"

	MemberAdded   = "member.added"
	MemberUpdated = "member.updated"
	MemberRemoved = "member.removed"

	InvitationCreated  = "invitation.created"
	InvitationAccepted = "invitation.accepted"
	InvitationDeclined = "invitation.declined"
)

type WorkspacePayload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type MemberPayload struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	Role        string `json:"role"`
}

type InvitationPayload struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Email       string `json:"email"`
	Role        string `json:"role"`
}

const (
	WorkItemCreated = "work_item.created"
	WorkItemUpdated = "work_item.updated"
	WorkItemDeleted = "work_item.deleted"

	CommentCreated  = "comment.created"
	CommentUpdated  = "comment.updated"
	CommentDeleted  = "comment.deleted"
	CommentResolved = "comment.resolved"

	LabelCreated   = "label.created"
	ProjectCreated = "project.created"
)

type WorkItemPayload struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspace_id"`
	Number       int64  `json:"number"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	Revision     int64  `json:"revision"`
	AssigneeType string `json:"assignee_type,omitempty"`
	AssigneeID   string `json:"assignee_id,omitempty"`
	CreatorType  string `json:"creator_type"`
	CreatorID    string `json:"creator_id"`
}

type CommentPayload struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	WorkItemID  string `json:"work_item_id"`
	AuthorType  string `json:"author_type"`
	AuthorID    string `json:"author_id,omitempty"`
	Content     string `json:"content"`
}

const (
	TaskQueued    = "task.queued"
	TaskDispatch  = "task.dispatch"
	TaskRunning   = "task.running"
	TaskCompleted = "task.completed"
	TaskFailed    = "task.failed"
	TaskCancelled = "task.cancelled"
	TaskAwaiting  = "task.awaiting_input"
	TaskResumed   = "task.resumed"
	TaskMessage   = "task.message"

	BatchCreated   = "batch.created"
	BatchProgress  = "batch.progress"
	BatchCompleted = "batch.completed"
	BatchFailed    = "batch.failed"

	ChatDone = "chat.done"
)

// ChatDoneFrameType chat:done 用户定向 WS 帧类型（W2-1 creator 私有口径）。
// chat:done 帧不再走 workspace 广播，改经 SendToUser(creator) 定向投递
// （notification:×6 帧同构的 colon 形态；与 WSName(ChatDone) 映射结果一致）。
const ChatDoneFrameType = "chat:done"
