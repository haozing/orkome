package app

import (
	"context"
	"encoding/json"
	"time"

	"orkome/internal/automation/domain"
)

type Wakeup struct {
	ID              string         `json:"id"`
	WorkspaceID     string         `json:"workspace_id"`
	WorkItemID      string         `json:"work_item_id"`
	AgentID         string         `json:"agent_id,omitempty"`
	Kind            string         `json:"kind"`
	Instruction     string         `json:"instruction"`
	IntervalSeconds int            `json:"interval_seconds,omitempty"`
	CronExpression  string         `json:"cron_expression,omitempty"`
	Timezone        string         `json:"timezone"`
	NextFireAt      *time.Time     `json:"next_fire_at,omitempty"`
	Enabled         bool           `json:"enabled"`
	FireCount       int            `json:"fire_count"`
	MaxFires        int            `json:"max_fires,omitempty"`
	Condition       map[string]any `json:"condition,omitempty"`
	ConditionState  *string        `json:"-"`
	EventTypes      []string       `json:"event_types,omitempty"`
	FilterAgentID   string         `json:"filter_agent_id,omitempty"`
	FilterTaskID    string         `json:"filter_task_id,omitempty"`
	FilterActorType string         `json:"filter_actor_type,omitempty"`
	FilterActorID   string         `json:"filter_actor_id,omitempty"`
	SystemRule      string         `json:"system_rule,omitempty"`
	ExpiresAt       *time.Time     `json:"expires_at,omitempty"`
	PausedReason    string         `json:"paused_reason,omitempty"`
	LastTaskID      string         `json:"last_task_id,omitempty"`
	CreatedBy       string         `json:"-"`
	Revision        int64          `json:"-"`
}

type Receipt struct {
	ID          string
	WakeupID    string
	Revision    int64
	EventKey    string
	WorkspaceID string
	CapturedAt  time.Time
	Payload     []byte
}

const (
	ScanDue       = "due"
	ScanExpire    = "expire"
	ScanCondition = "condition"
	ScanReceipt   = "receipt"
)

type DueWakeup struct {
	Wakeup     Wakeup
	Source     string
	ReceiptID  string
	ReceiptRev int64
	EventKey   string
	CapturedAt *time.Time
}

type WakeupSummary struct {
	WorkItemID string     `json:"work_item_id"`
	Total      int        `json:"total"`
	Enabled    int        `json:"enabled"`
	Paused     int        `json:"paused"`
	NextFireAt *time.Time `json:"next_fire_at,omitempty"`
	LastTaskID string     `json:"last_task_id,omitempty"`
}

type SystemWakeupDefault struct {
	Enabled     bool   `json:"enabled"`
	Instruction string `json:"instruction,omitempty"`
}

type WakeupTaskRef struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

type Autopilot struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	Name          string         `json:"name"`
	ExecMode      string         `json:"exec_mode"`
	Template      map[string]any `json:"template"`
	TargetAgentID string         `json:"target_agent_id"`
	Enabled       bool           `json:"enabled"`
	DailyLimit    int            `json:"daily_limit"`
	CreatedBy     string         `json:"-"`

	PausedReason string `json:"paused_reason,omitempty"`
}

type Trigger struct {
	ID             string     `json:"id"`
	AutopilotID    string     `json:"autopilot_id"`
	Kind           string     `json:"kind"`
	CronExpression string     `json:"cron_expression,omitempty"`
	Timezone       string     `json:"timezone"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	WebhookToken   string     `json:"-"`
	SigningSecret  string     `json:"-"`
	Enabled        bool       `json:"enabled"`
	workspace      string
}

func (t *Trigger) SetWorkspace(ws string) { t.workspace = ws }

type DeleteOutcome struct {
	Found           bool
	Deleted         bool
	BlockedTriggers bool
	BlockedRuns     bool
}

type Run struct {
	ID                 string     `json:"id"`
	AutopilotID        string     `json:"autopilot_id"`
	TriggerID          string     `json:"trigger_id,omitempty"`
	Source             string     `json:"source"`
	Status             string     `json:"status"`
	WorkItemID         string     `json:"work_item_id,omitempty"`
	TaskID             string     `json:"task_id,omitempty"`
	PlannedAt          time.Time  `json:"planned_at"`
	TriggeredAt        *time.Time `json:"triggered_at,omitempty"`
	ReasonCode         string     `json:"reason_code,omitempty"`
	QuotaReservationID string     `json:"-"`
}

type Delivery struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	TriggerID   string    `json:"trigger_id"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`

	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}

type AutopilotFailureStat struct {
	AutopilotID string
	WorkspaceID string
	Name        string
	CreatedBy   string
	Runs        int
	Failed      int
}

type TaskEnqueuer interface {
	EnqueueForWakeup(ctx context.Context, workspaceID, workItemID, agentID, userID, evidence string) (taskID string, merged bool, err error)
}

type WakeupIDEnqueuer interface {
	EnqueueForWakeupWithID(ctx context.Context, workspaceID, workItemID, agentID, userID, wakeupID, evidence string) (taskID string, merged bool, err error)
}

type WakeupChainEnqueuer interface {
	EnqueueForWakeupWithChain(ctx context.Context, workspaceID, workItemID, agentID, userID, wakeupID string, chain []string, evidence string) (taskID string, merged bool, err error)
}

type FailureNotifier func(ctx context.Context, workspaceID, creatorUserID, autopilotName, stat string)

type WorkItemCreator interface {
	CreateForAutopilot(ctx context.Context, workspaceID, title, description, assigneeAgentID, creatorUserID, origin string) (workItemID string, err error)
}

type SubscriberRef struct {
	UserID string `json:"user_id"`
}

type RuleVersion struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id"`
	AutopilotID     string          `json:"autopilot_id"`
	PublishedByType string          `json:"published_by_type"`
	PublishedByID   string          `json:"published_by_id,omitempty"`
	ConfigSummary   json.RawMessage `json:"config_summary"`
	CreatedAt       time.Time       `json:"created_at"`
}

type Collaborator struct {
	AutopilotID string    `json:"autopilot_id"`
	UserID      string    `json:"user_id"`
	UserType    string    `json:"user_type"`
	GrantedBy   string    `json:"granted_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type SubscriberAwareCreator interface {
	CreateForAutopilotWithSubscribers(ctx context.Context, workspaceID, title, description, assigneeAgentID, creatorUserID, origin string, subscribers []SubscriberRef) (workItemID string, err error)
}

type FactsReader interface {
	WorkItemFacts(ctx context.Context, workspaceID, workItemID string) (status string, priority int, err error)

	ChildStageFacts(ctx context.Context, workspaceID, workItemID string) ([]domain.ChildStageFact, error)

	WakeupTargetAgent(ctx context.Context, workspaceID, workItemID string) (agentID string, err error)
}

type UoW func(ctx context.Context, fn func(ctx context.Context) error) error

type Repo interface {
	CreateWakeup(ctx context.Context, w Wakeup) (Wakeup, error)
	GetWakeup(ctx context.Context, workspaceID, id string) (Wakeup, bool, error)
	ListWakeupsByWorkItem(ctx context.Context, workItemID string) ([]Wakeup, error)
	SetWakeupEnabled(ctx context.Context, workspaceID, id string, enabled bool) error
	DeleteWakeup(ctx context.Context, workspaceID, id string) error

	UpdateWakeup(ctx context.Context, w Wakeup, expectedRev int64) (Wakeup, bool, error)
	SetWakeupInstruction(ctx context.Context, workspaceID, id, instruction string) (bool, error)
	ListWorkspaceWakeups(ctx context.Context, workspaceID string) ([]Wakeup, error)
	ListPausedWakeups(ctx context.Context, workspaceID string) ([]Wakeup, error)
	ListWakeupSummaries(ctx context.Context, workspaceID string) ([]WakeupSummary, error)

	ListDueWakeups(ctx context.Context, now time.Time, limit int) ([]DueWakeup, error)

	ListConditionWakeups(ctx context.Context) ([]Wakeup, error)

	ListEventWakeups(ctx context.Context, workspaceID, eventType string) ([]Wakeup, error)

	InsertReceipt(ctx context.Context, rc Receipt) error
	MarkReceiptProcessed(ctx context.Context, id string) error

	MarkWakeupExpired(ctx context.Context, id string, expectedRev int64) (bool, error)

	ListRecentWakeupFires(ctx context.Context, wakeupID string, since time.Time, limit int) ([]time.Time, error)

	PauseWakeupBurst(ctx context.Context, id string, expectedRev int64, resumeAt time.Time) (bool, error)

	ListBurstPausedWakeups(ctx context.Context, now time.Time, limit int) ([]Wakeup, error)

	ResumeWakeupBurst(ctx context.Context, id string, expectedRev int64) (bool, error)

	LockTaskOwner(ctx context.Context, workItemID string) error

	LockWakeup(ctx context.Context, id string) (Wakeup, bool, error)

	FireWakeup(ctx context.Context, id string, expectedRev int64, nextFire *time.Time, condState *string, taskID string) (bool, error)

	ListSystemWakeups(ctx context.Context, workspaceID string) ([]Wakeup, error)
	ListSystemWakeupsByWorkItem(ctx context.Context, workspaceID, workItemID string) ([]Wakeup, error)
	EnsureSystemWakeup(ctx context.Context, w Wakeup) (Wakeup, error)
	UpdateSystemWakeup(ctx context.Context, workspaceID, id string, instruction *string, enabled *bool) (Wakeup, bool, error)

	GetSystemWakeupDefault(ctx context.Context, workspaceID string) (SystemWakeupDefault, bool, error)
	SetSystemWakeupDefault(ctx context.Context, workspaceID string, d SystemWakeupDefault) error

	CreateAutopilot(ctx context.Context, a Autopilot) (Autopilot, error)
	GetAutopilot(ctx context.Context, workspaceID, id string) (Autopilot, bool, error)
	ListAutopilots(ctx context.Context, workspaceID string, limit int) ([]Autopilot, error)

	UpdateAutopilot(ctx context.Context, workspaceID, id string, name, execMode *string,
		template map[string]any, targetAgentID *string, enabled *bool) (Autopilot, bool, error)

	DeleteAutopilot(ctx context.Context, workspaceID, id string) (DeleteOutcome, error)
	CreateTrigger(ctx context.Context, t Trigger) (Trigger, error)
	GetTriggerByToken(ctx context.Context, token string) (Trigger, bool, error)
	ListDueScheduleTriggers(ctx context.Context, now time.Time) ([]Trigger, error)

	ClaimDueTrigger(ctx context.Context, id string, from, to *time.Time) (bool, error)
	UpdateTrigger(ctx context.Context, ws, autopilotID, triggerID string, cron, tz *string, nextRun *time.Time, enabled *bool) (bool, error)
	DeleteTrigger(ctx context.Context, ws, autopilotID, triggerID string) (bool, error)

	InsertRun(ctx context.Context, r Run) (Run, error)
	SetRunTask(ctx context.Context, id, taskID, workItemID string) error
	SetRunStatus(ctx context.Context, id, status, reason string) error
	ListRunsByAutopilot(ctx context.Context, ws, autopilotID string, limit int) ([]Run, error)
	GetRun(ctx context.Context, ws, runID string) (Run, bool, error)

	ReserveQuota(ctx context.Context, autopilotID string, windowStart, windowEnd time.Time, limit int) (reservationID string, err error)

	SetDispatchLockTimeout(ctx context.Context) error

	InsertDelivery(ctx context.Context, workspaceID, triggerID, dedupeKey string, payload []byte) (id string, dup bool, err error)

	ClaimDelivery(ctx context.Context) (deliveryID, triggerID, workspaceID string, payload []byte, attempts int, ok bool, err error)
	FinishDelivery(ctx context.Context, id, status, errMsg string) error

	FailDelivery(ctx context.Context, id string, deadLetter bool, nextAttemptAt *time.Time, errMsg string) error
	GetDelivery(ctx context.Context, workspaceID, id string) (Delivery, bool, error)
	ListDeliveries(ctx context.Context, workspaceID string, limit int) ([]Delivery, error)

	ReplayDelivery(ctx context.Context, workspaceID, id string) (bool, error)
	GetTriggerByID(ctx context.Context, id string) (Trigger, bool, error)
	SetRunStatusByTask(ctx context.Context, taskID, status string) error

	PauseAutopilotsByFailureRate(ctx context.Context, window time.Duration, minRuns int, failRate float64, pausedReason string) ([]AutopilotFailureStat, error)

	LatestWakeupChain(ctx context.Context, workspaceID, workItemID, agentID string) ([]string, error)

	PauseWakeupLoop(ctx context.Context, id string, expectedRev int64) (bool, error)

	SetTriggerSigningSecret(ctx context.Context, workspaceID, autopilotID, triggerID, secret string) (bool, error)

	SetTriggerWebhookToken(ctx context.Context, workspaceID, autopilotID, triggerID, token string) (bool, error)

	ListTemplateSubscribers(ctx context.Context, autopilotID string) ([]SubscriberRef, error)

	InsertRuleVersion(ctx context.Context, v RuleVersion) (RuleVersion, error)

	ListCollaborators(ctx context.Context, workspaceID, autopilotID string) ([]Collaborator, error)
	AddCollaborator(ctx context.Context, workspaceID, autopilotID, userID, grantedBy string) (bool, error)
	RemoveCollaborator(ctx context.Context, workspaceID, autopilotID, userID string) (bool, error)
}
