package app

import "time"

type StatusDef struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	IsBuiltin bool   `json:"is_builtin"`
	Position  int    `json:"position"`

	Icon string `json:"icon,omitempty"`
}

type PropertyDef struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Options     []string `json:"options,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Outcome 工单产出（一等公民，"需求→产出"模型的产出侧）。
// 由任务结算链路（completed）结构化写入，后写覆盖；也可人工经 PUT 编辑覆盖。
type Outcome struct {
	Summary     string    `json:"summary"`
	TaskID      string    `json:"task_id,omitempty"`
	CompletedAt time.Time `json:"completed_at"`
}

type WorkItem struct {
	ID                 string `json:"id"`
	WorkspaceID        string `json:"workspace_id"`
	Number             int64  `json:"number"`
	Title              string `json:"title"`
	Description        string `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
	StatusKey          string `json:"status"`

	StatusCategory string         `json:"status_category"`
	Priority       int            `json:"priority"`
	AssigneeType   string         `json:"assignee_type"`
	AssigneeID     string         `json:"assignee_id,omitempty"`
	CreatorType    string         `json:"creator_type"`
	CreatorID      string         `json:"creator_id"`
	ParentID       string         `json:"parent_work_item_id,omitempty"`
	ProjectID      string         `json:"project_id,omitempty"`
	Position       int64          `json:"position"`
	DueDate        string         `json:"due_date,omitempty"`
	StartDate      string         `json:"start_date,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Properties     map[string]any `json:"properties,omitempty"`
	Outcome        *Outcome       `json:"outcome,omitempty"`
	Revision       int64          `json:"revision"`
	TriageState    *string        `json:"triage_state,omitempty"`
	DuplicateOf    *string        `json:"duplicate_of_work_item_id,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	LastActivityAt time.Time      `json:"last_activity_at"`
}

type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	Count int    `json:"count,omitempty"`
}

type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type ProjectResource struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Kind      string    `json:"kind"`
	URI       string    `json:"uri"`
	CreatedAt time.Time `json:"created_at"`
}

type Comment struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	WorkItemID  string    `json:"work_item_id"`
	AuthorType  string    `json:"author_type"`
	AuthorID    string    `json:"author_id,omitempty"`
	Content     string    `json:"content"`
	Type        string    `json:"type"`
	ParentID    string    `json:"parent_id,omitempty"`
	Resolved    bool      `json:"resolved"`
	Deleted     bool      `json:"deleted"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Subscriber struct {
	WorkItemID string    `json:"work_item_id"`
	UserID     string    `json:"user_id"`
	Source     string    `json:"source"`
	Level      string    `json:"level"`
	CreatedAt  time.Time `json:"created_at"`
}

type SubscriberWrite struct {
	UserID string
	Source string
	Level  string
}

type TimelineEntry struct {
	Kind      string         `json:"kind"`
	ID        string         `json:"id"`
	ActorType string         `json:"actor_type,omitempty"`
	ActorID   string         `json:"actor_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type View struct {
	ID          string           `json:"id"`
	WorkspaceID string           `json:"workspace_id,omitempty"`
	UserID      string           `json:"user_id,omitempty"`
	Name        string           `json:"name"`
	Filters     map[string]any   `json:"filters"`
	GroupBy     string           `json:"group_by,omitempty"`
	Sort        []map[string]any `json:"sort"`
	UpdatedAt   time.Time        `json:"updated_at,omitempty"`
}

type WorkItemUsage struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	TaskCount        int64  `json:"task_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CostUSDTicks     int64  `json:"cost_usd_ticks"`
}

type ReactionCount struct {
	Emoji string `json:"emoji"`
	Count int64  `json:"count"`
}

type ClientUsageReport struct {
	WorkspaceID   string
	UserID        string
	ClientType    string
	InstallID     string
	ClientVersion string
	OS            string
}
