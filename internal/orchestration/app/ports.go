package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"orkome/internal/contract/dispatchreason"
	"orkome/internal/contract/events"
	"orkome/internal/contract/modelmeta"
	"orkome/internal/orchestration/domain"
)

type Task struct {
	ID                  string         `json:"id"`
	WorkspaceID         string         `json:"workspace_id"`
	AgentID             string         `json:"agent_id"`
	WorkItemID          string         `json:"work_item_id,omitempty"`
	Status              string         `json:"status"`
	Priority            int            `json:"priority"`
	Attempt             int            `json:"attempt"`
	MaxAttempts         int            `json:"max_attempts"`
	Context             map[string]any `json:"context"`
	OriginatorUserID    string         `json:"originator_user_id,omitempty"`
	AccountableUserID   string         `json:"accountable_user_id,omitempty"`
	OriginatorSource    string         `json:"originator_source"`
	TriggerEvidenceKind string         `json:"trigger_evidence_kind,omitempty"`
	SessionRefIn        string         `json:"session_ref_in,omitempty"`
	IsLeaderTask        bool           `json:"is_leader_task,omitempty"`
	SquadID             string         `json:"squad_id,omitempty"`
	FailureReason       string         `json:"failure_reason,omitempty"`
	Error               string         `json:"error,omitempty"`
	Result              string         `json:"result,omitempty"`
	Summary             string         `json:"summary,omitempty"`
	RetryOfID           string         `json:"retry_of_task_id,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	CompletedAt         *time.Time     `json:"completed_at,omitempty"`
}

// RuntimeCredView 是 external agent 接入凭证的列表行（/access 管理面）。
// 明文永不回传——认证材料只有 mint 响应那一次。
type RuntimeCredView struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	PatPrefix string     `json:"pat_prefix"`
	Revoked   bool       `json:"revoked"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

type Agent struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	RuntimeConfig map[string]any `json:"runtime_config"`
	MaxConcurrent int            `json:"max_concurrent_tasks"`
	Visibility    string         `json:"visibility"`
	OwnerUserID   string         `json:"owner_user_id,omitempty"`
	Archived      bool           `json:"archived"`
	CreatedAt     time.Time      `json:"created_at"`

	// PackID 来源能力包（pack 一等公民 §1.2 修 D2）：空 = 手工创建。
	// 仅 Create 写入（provision 通道），Update 不改归属。
	PackID string `json:"pack_id,omitempty"`

	ConversationStarters []ConversationStarter `json:"conversation_starters"`

	InvocationTargets *[]InvocationTarget `json:"invocation_targets,omitempty"`

	RuntimeKind string `json:"runtime_kind,omitempty"`
}

type ConversationStarter struct {
	Label string `json:"label"`
}

type InvocationTarget struct {
	Type   string `json:"type"`
	UserID string `json:"user_id,omitempty"`
}

func (a Agent) Sanitized() Agent {
	a.RuntimeConfig = MaskSecrets(a.RuntimeConfig)
	return a
}

const MaskedValue = "•••"

func MaskSecrets(rc map[string]any) map[string]any {
	if rc == nil {
		return nil
	}
	return maskSecretMap(rc)
}

func maskSecretMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "key") || strings.Contains(lk, "secret") ||
			strings.Contains(lk, "token") || strings.Contains(lk, "password") {
			out[k] = MaskedValue
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = maskSecretMap(nested)
			continue
		}
		out[k] = v
	}
	return out
}

type WorkspaceMCP struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	Name        string         `json:"name"`
	Transport   string         `json:"transport"`
	URL         string         `json:"url"`
	Credentials map[string]any `json:"-"`
	HasCreds    bool           `json:"has_credentials"`
	CreatedAt   time.Time      `json:"created_at"`

	// PackID 来源能力包（pack 一等公民 §1.2）：空 = 手工注册。
	// 仅 Create 写入，Update 不改归属（MCP 行有独立生命周期，pack 删除时列置空）。
	PackID string `json:"pack_id,omitempty"`
}

type AgentMCP struct {
	ID string `json:"id"`

	WorkspaceID string         `json:"-"`
	AgentID     string         `json:"agent_id"`
	Name        string         `json:"name"`
	Transport   string         `json:"transport"`
	URL         string         `json:"url"`
	Enabled     bool           `json:"enabled"`
	Credentials map[string]any `json:"-"`
	HasCreds    bool           `json:"has_credentials"`
}

type TaskMessage struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Seq       int64     `json:"seq"`
	Kind      string    `json:"kind"`
	CallID    string    `json:"call_id,omitempty"`
	Content   string    `json:"content"`
	Truncated bool      `json:"truncated"`
	CreatedAt time.Time `json:"created_at"`
}

type AgentRunSnapshotItem struct {
	TaskID     string     `json:"task_id"`
	WorkItemID string     `json:"work_item_id,omitempty"`
	AgentID    string     `json:"agent_id"`
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"started_at"`
}

type TaskListRow struct {
	Task
	AgentName     string `json:"agent_name"`
	WorkItemTitle string `json:"work_item_title,omitempty"`
}

type TaskMessagesQuery struct {
	Limit     int
	BeforeSeq int64
	Desc      bool
	Kinds     []string
}

type TaskListFilter struct {
	Statuses []string
	AgentID  string
	Cursor   string
	Limit    int
}

type UncoveredComment struct {
	CommentID   string
	WorkspaceID string
	WorkItemID  string

	AuthorType string
	AuthorID   string
	Content    string

	ParentAuthorType string
	ParentAuthorID   string
	RootAuthorType   string
	RootAuthorID     string

	AssigneeType string
	AssigneeID   string
}

type SessionTurn struct {
	Role    string
	Content string
}

type TaskMessageSink interface {
	AppendTaskMessage(ctx context.Context, workspaceID, taskID, kind, callID, content string, truncated bool) (seq int64, err error)
}

type OutboxRecorder interface {
	Append(ctx context.Context, envs ...events.Envelope) error

	MarkPublished(ctx context.Context, ids ...string) error
}

type Within func(ctx context.Context, fn func(ctx context.Context) error) error

type UsageReport struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CostUSDTicks     int64  `json:"cost_usd_ticks"`
	AgentLabel       string `json:"agent_label"`
	ModelRetries     int    `json:"model_retries"`
	ModelFailovers   int    `json:"model_failovers"`
}

type UsageByProvider struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CostUSDTicks int64  `json:"cost_usd_ticks"`
}

type WorkItemUsage struct {
	WorkItemID        string            `json:"work_item_id"`
	Tasks             int64             `json:"tasks"`
	TotalInputTokens  int64             `json:"total_input_tokens"`
	TotalOutputTokens int64             `json:"total_output_tokens"`
	TotalCostUSDTicks int64             `json:"total_cost_usd_ticks"`
	ByProvider        []UsageByProvider `json:"by_provider"`
}

type SquadMember struct {
	AgentID       string         `json:"agent_id"`
	Role          string         `json:"role"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	RuntimeConfig map[string]any `json:"runtime_config"`
}

type SquadInfo struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Instructions string        `json:"instructions"`
	Members      []SquadMember `json:"members"`
}

type EngineOutput struct {
	Outcome    string
	Output     string
	SessionRef string
	Usage      UsageReport

	MemberUsage []MemberUsage

	Parts     []ContentPart
	Interrupt *InterruptInfo
}

type MemberUsage struct {
	AgentID string
	Label   string
	Usage   UsageReport
}

type ApprovalDecision struct {
	Approved bool   `json:"approved"`
	ByUserID string `json:"by_user_id"`
	Note     string `json:"note"`
}

type InterruptInfo struct {
	TargetID string
	Kind     string
	Payload  string
}

type Engine interface {
	Name() string
	Execute(ctx context.Context, task Task, agent Agent, credentialSecret string) (EngineOutput, error)
	Resume(ctx context.Context, task Task, agent Agent, credentialSecret string, targetID string, decision ApprovalDecision) (EngineOutput, error)

	Generate(ctx context.Context, agent Agent, credentialSecret, system, input string) (string, UsageReport, error)
}

type Repo interface {
	CreateAgent(ctx context.Context, a Agent) (Agent, error)
	GetAgent(ctx context.Context, workspaceID, id string) (Agent, bool, error)
	GetAgentByName(ctx context.Context, workspaceID, name string) (Agent, bool, error)
	ListAgents(ctx context.Context, workspaceID string) ([]Agent, error)
	ListAllAgents(ctx context.Context) ([]Agent, error)
	ArchiveAgent(ctx context.Context, workspaceID, id string, archived bool) error
	// SetAgentPack 挂载/卸载 agent 的能力包归属（能力包平台化，1042 的
	// pack_id 列；packID nil = 卸下）。ws 限定 + rows affected 收口在实现内。
	SetAgentPack(ctx context.Context, workspaceID, agentID string, packID *string) error
	UpdateAgent(ctx context.Context, a Agent) (Agent, bool, error)
	CancelAgentTasks(ctx context.Context, workspaceID, agentID string) (int, error)

	ListInvocationTargets(ctx context.Context, workspaceID string) (map[string][]InvocationTarget, error)

	GetInvocationTargets(ctx context.Context, workspaceID, agentID string) ([]InvocationTarget, error)

	ReplaceInvocationTargets(ctx context.Context, workspaceID, agentID string, targets []InvocationTarget) error

	GetCredential(ctx context.Context, workspaceID, id string) (provider, secret string, ok bool, err error)

	ListWorkspaceMCP(ctx context.Context, workspaceID string) ([]WorkspaceMCP, error)
	CreateWorkspaceMCP(ctx context.Context, m WorkspaceMCP) (WorkspaceMCP, error)
	UpdateWorkspaceMCP(ctx context.Context, m WorkspaceMCP) (WorkspaceMCP, bool, error)
	DeleteWorkspaceMCP(ctx context.Context, workspaceID, id string) error
	ListAgentMCP(ctx context.Context, workspaceID, agentID string) ([]AgentMCP, bool, error)

	// CreateAgentMCP：INSERT 语义，重名 23505 → 409（FP-07 P1-4：不再
	// ON CONFLICT 静默覆盖，与 ws 级 CreateWorkspaceMCP 对齐）。
	CreateAgentMCP(ctx context.Context, m AgentMCP) (AgentMCP, error)
	UpdateAgentMCP(ctx context.Context, m AgentMCP) (AgentMCP, bool, error)
	DeleteAgentMCP(ctx context.Context, workspaceID, agentID, id string) error

	InsertTask(ctx context.Context, t Task) (Task, error)
	InsertChatTask(ctx context.Context, t Task) (Task, error)
	GetTaskByChatSession(ctx context.Context, workspaceID, sessionID string) (Task, bool, error)
	GetTask(ctx context.Context, workspaceID, id string) (Task, bool, error)

	ClaimNext(ctx context.Context, agentID string, maxConcurrent int) (Task, bool, error)

	ClaimExternalTask(ctx context.Context, workspaceID, taskID string) (Task, bool, error)

	GetAgentIDByPATID(ctx context.Context, patID string) (agentID string, ok bool, err error)

	CountStaleExternalQueued(ctx context.Context, olderThanMinutes int) (int, error)

	StartTask(ctx context.Context, id, runtimeID, engineTag string) (Task, error)

	CompleteRun(ctx context.Context, id, result string, sessionRef string, engineTag string, summary string, usage UsageReport) error
	FailRun(ctx context.Context, id, reason, errMsg string, summary string) (attemptedRetry bool, err error)

	// LastTaskText 取任务最后一条 kind='text' 的 task_message 内容（无则空串）。
	// summary 生成已上移 service 层（A2）：repo 只提供材料读取与落存，
	// 不再自行生成 summary。
	LastTaskText(ctx context.Context, taskID string) (string, error)

	CancelRun(ctx context.Context, workspaceID, id string) (bool, error)
	ListTasksByWorkItem(ctx context.Context, workItemID string, newestFirst bool) ([]Task, error)
	ListActiveTasks(ctx context.Context, workItemID string) ([]Task, error)

	RecoverDispatched(ctx context.Context) (int, error)
	FailRunningAtStartup(ctx context.Context) (int, error)

	UpsertRuntime(ctx context.Context, instanceID, engine string, capabilities []string) error
	HeartbeatRuntime(ctx context.Context, instanceID string) error

	AppendTaskMessage(ctx context.Context, workspaceID, taskID, kind, callID, content string, truncated bool) (seq int64, err error)
	ListTaskMessages(ctx context.Context, workspaceID, taskID string, q TaskMessagesQuery) ([]TaskMessage, error)

	InsertAuxUsage(ctx context.Context, workspaceID string, u UsageReport) error

	FailStaleRunning(ctx context.Context, staleSeconds int) ([]string, error)

	SetRuntime(ctx context.Context, taskID, instanceID string) error

	CountPendingFence(ctx context.Context, workItemID, agentID string) (int, error)

	CountEvalToday(ctx context.Context, workspaceID string) (int, error)

	RollupUsageHourly(ctx context.Context, upto time.Time) (int, error)

	WorkItemUsage(ctx context.Context, workspaceID, workItemID string) (WorkItemUsage, bool, error)

	ListTasksByAgent(ctx context.Context, workspaceID, agentID string, limit int) ([]Task, error)
	GetActiveTask(ctx context.Context, workspaceID, workItemID string) (Task, bool, error)

	ListWorkspaceActiveTasks(ctx context.Context, workspaceID string) ([]AgentRunSnapshotItem, error)

	ListWorkspaceTasks(ctx context.Context, workspaceID string, filter TaskListFilter) ([]TaskListRow, string, error)
	InsertSupplementForPending(ctx context.Context, wsID, workItemID, agentID, content, authorUserID string) (bool, error)
	InsightWorkingAgents(ctx context.Context, workspaceID string) ([]map[string]any, error)
	InsightRunCounts(ctx context.Context, workspaceID string) ([]map[string]any, error)
	InsightActivity30d(ctx context.Context, workspaceID string) ([]map[string]any, error)
	CreateSquad(ctx context.Context, wsID, name, instructions, leaderID string) (map[string]any, error)
	ListSquads(ctx context.Context, wsID string) ([]map[string]any, error)

	GetSquadRow(ctx context.Context, wsID, squadID string) (map[string]any, bool, error)

	SquadNameTaken(ctx context.Context, wsID, name, excludeSquadID string) (bool, error)

	UpdateSquad(ctx context.Context, wsID, squadID, name, instructions, leaderAgentID string, joinLeader bool) (map[string]any, bool, error)

	DeleteSquad(ctx context.Context, wsID, squadID string) (SquadDeleteResult, error)
	ListSquadMembers(ctx context.Context, wsID, squadID string) ([]SquadMemberRow, error)

	AddSquadMember(ctx context.Context, wsID, squadID, agentID, role string) error

	RemoveSquadMember(ctx context.Context, wsID, squadID, agentID string) error

	UpdateSquadMemberRole(ctx context.Context, wsID, squadID, agentID, role string) (bool, error)

	ListSquadMemberTasks(ctx context.Context, wsID, squadID string) ([]SquadMemberTaskRow, error)

	SquadLeader(ctx context.Context, wsID, squadID string) (string, bool, error)

	GetSquad(ctx context.Context, workspaceID, squadID string) (SquadInfo, bool, error)

	LeaderAgentID(ctx context.Context, squadID string) string

	InsertMemberUsage(ctx context.Context, taskID, agentID, label string, u UsageReport) error

	ListSquadFacts(ctx context.Context, workspaceID string) ([]domain.SquadFacts, error)
	HistoryAgentIDs(ctx context.Context, workItemID string) ([]string, error)
	TrySteer(ctx context.Context, wsID, workItemID, agentID, content, authorUserID string) (bool, error)
	MergeIntoPending(ctx context.Context, wsID, workItemID, agentID, commentID string) (bool, error)
	HasActiveTask(ctx context.Context, wsID, workItemID, agentID string) (bool, error)

	InsertDeferredTask(ctx context.Context, t Task) (Task, error)
	// FindLeaderTaskByID 按 leader 任务 ID 直取（成员 context.leader_task_id 回指）。
	FindLeaderTaskByID(ctx context.Context, workspaceID, leaderTaskID string) (Task, bool, error)
	// CountOpenDispatchedMembers 工单内仍处未终态的 leader_dispatch 成员任务数
	//（queued/dispatched/running）。为 0 表示全部成员已收割完毕，可唤醒 leader。
	CountOpenDispatchedMembers(ctx context.Context, workspaceID, workItemID, leaderTaskID string) (int, error)

	PromoteDeferred(ctx context.Context) ([]Task, error)

	CancelTasksByTriggerComment(ctx context.Context, workspaceID, commentID string) ([]string, error)
	ClaimNextSupplement(ctx context.Context, taskID string) (id, content string, ok bool, err error)
	AckSupplementDelivered(ctx context.Context, id string) error
	SettlePendingSupplements(ctx context.Context, taskID string) error
	InsertRecoveryTask(ctx context.Context, src Task, note string) (Task, error)
	CountRecoveriesFrom(ctx context.Context, originTaskID string) (int, error)

	GetLastSessionRef(ctx context.Context, workItemID, agentID, engineTag string) (string, bool, error)

	SessionTranscript(ctx context.Context, workItemID, agentID string, limit int) ([]SessionTurn, error)

	ParkTask(ctx context.Context, id string, interrupt InterruptInfo) error
	ClearAwaiting(ctx context.Context, id string) error
	ListExpiredAwaiting(ctx context.Context, olderThanMinutes int) ([]string, error)
	CancelRunningTask(ctx context.Context, workspaceID, id string) (bool, error)
	DeleteTaskCheckpoints(ctx context.Context, taskID string) error

	DeleteStaleSessionCheckpoints(ctx context.Context, olderThanHours int) (int, error)

	ListUnsettledDelegatedFailures(ctx context.Context) ([]Task, error)

	ListUncoveredTriggerComments(ctx context.Context) ([]UncoveredComment, error)

	GetTaskAwaiting(ctx context.Context, workspaceID, id string) (Task, bool, error)

	ResetSupplementToPending(ctx context.Context, workspaceID, taskID, supplementID string) (bool, error)
}

type NotificationWriter interface {
	WriteTaskCompleted(ctx context.Context, workspaceID, userID, workItemID, agentName, result string) error
}

type CommentWriter interface {
	WriteAgentComment(ctx context.Context, workspaceID, workItemID, agentID, content string) error
	HasAgentComment(ctx context.Context, workItemID string) (bool, error)
}

// LeaderWakeHook 派发式小队的 leader 自动回环：成员任务终态后调用。
// 实现检查同工单同队是否全部成员已收割，是则给 leader 插入收割任务。
type LeaderWakeHook interface {
	WakeLeaderIfSquadDone(ctx context.Context, memberTask Task) error
}

// OutcomeWriter 工单产出写入端口（"需求→产出"一等公民）：任务结算 completed
// 时由 SettlementService 调用，后写覆盖。实现归 work 域（wiring 适配器注入），
// orchestration 不依赖 work 类型（域零）。
type OutcomeWriter interface {
	WriteOutcome(ctx context.Context, workspaceID, workItemID, taskID, summary string) error
}

type WorkItemInfo struct {
	ID    string
	Title string
	Desc  string
}

type WorkBench interface {
	PostAgentComment(ctx context.Context, workspaceID string, workItemID string, agentID string, content string) error
	GetWorkItemBrief(ctx context.Context, workspaceID string, workItemID string) (string, string, error)
}

type ChildDelegation struct {
	OriginType string

	ChainRootUserID string

	ChainRootDirectHuman bool
}

type ChildItemInput struct {
	WorkspaceID    string
	ParentID       string
	Title          string
	Description    string
	CreatorAgentID string

	Delegation ChildDelegation
}

type ChildItemCreator interface {
	CreateChildWorkItem(ctx context.Context, in ChildItemInput) (WorkItemInfo, error)
}

type AgentStatusUpdater interface {
	UpdateStatusByAgent(ctx context.Context, workspaceID, workItemID, agentID, statusKey string) error
}

type SquadEvaluation struct {
	WorkspaceID   string
	WorkItemID    string
	LeaderAgentID string
	SquadID       string
	TaskID        string
	Member        string
	Verdict       string
	Rationale     string
}

type SquadEvaluationRecorder interface {
	RecordSquadEvaluation(ctx context.Context, e SquadEvaluation) error
}

type LeaderDispatchInput struct {
	WorkspaceID       string
	WorkItemID        string
	SquadID           string
	LeaderTaskID      string
	LeaderAgentID     string
	MemberAgentID     string
	MemberName        string
	Instruction       string
	OriginatorUserID  string
	AccountableUserID string
}

type LeaderDispatcher interface {
	EnqueueLeaderDispatch(ctx context.Context, in LeaderDispatchInput) (string, string, error)
}

type WakeupCheckiner interface {
	CheckInWakeup(ctx context.Context, workspaceID, workItemID, wakeupID, taskID, note string) error
}

type ResolvedMCPServer struct {
	Name      string
	Transport string
	URL       string

	Credentials map[string]any
}

type MCPResolver interface {
	ResolveMCP(ctx context.Context, workspaceID, agentID string) ([]ResolvedMCPServer, error)
}

type TaskContextPatcher interface {
	PatchTaskContext(ctx context.Context, taskID string, patch map[string]any) error
}

type MCPSnapshotEntry struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	URL       string `json:"url"`

	Status    string `json:"status"`
	ToolCount int    `json:"tool_count,omitempty"`
	Note      string `json:"note,omitempty"`
}

// ToolSnapshotEntry 是 task.context.tools 快照里一个工具的清单条目（C1）。
// 统一覆盖内置工具与 MCP 桥接工具；JSON 形状冻结见
// docs/interfaces-tool-registry-2026-10-11.md §3（前端将来要读）。
type ToolSnapshotEntry struct {
	// Name 工具名（内置无前缀；MCP 桥接自动带 <pack>_ 前缀）。
	Name string `json:"name"`
	// Origin "builtin"（编译期内置工具）| "mcp"（pack 桥接工具）。
	Origin string `json:"origin"`
	// Server 归属 MCP 服务器名（仅 origin=mcp 有值；内置为空）。
	Server string `json:"server,omitempty"`
	// HasOutputSchema 是否声明了输出契约（OutputSchema/存档/协议自描述）。
	HasOutputSchema bool `json:"has_output_schema"`
	// ConcurrencySafe 是否显式声明可并行（fail-closed：默认 false）。
	ConcurrencySafe bool `json:"concurrency_safe"`
}

// ToolContextSnapshot 组装 task.context.tools 的写入载荷：mcp_servers 保持
// dispatcher 的 resolved 态（兼容旧键），tools 是统一注册表的全量清单。
// 两者同 patch 写入（见 workers.Dispatcher.snapshotMCP）。
func ToolContextSnapshot(mcp []MCPSnapshotEntry, tools []ToolSnapshotEntry) map[string]any {
	patch := map[string]any{}
	if rows := mcpSnapshotRows(mcp); rows != nil {
		patch["mcp_servers"] = rows
	}
	if len(tools) > 0 {
		rows := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			rows = append(rows, map[string]any{
				"name": t.Name, "origin": t.Origin, "server": t.Server,
				"has_output_schema": t.HasOutputSchema,
				"concurrency_safe":  t.ConcurrencySafe,
			})
		}
		patch["tools"] = rows
	}
	if len(patch) == 0 {
		return nil
	}
	return patch
}

// TaskContextTools 读取 task.context.tools（新键），兼容回落旧键
// task.context.mcp_servers 的服务器级聚合（老任务快照无 tools 键）。
// 返回 nil 表示两处都没有工具快照（兼容读旧路径的判定点，双测覆盖）。
// 值形态兼容两种：DB jsonb 回读的 []any 与进程内构造的 []map[string]any
// （引擎回写 patch 后的读路径是前者，单测直接喂 patch 是后者）。
func TaskContextTools(ctx map[string]any) []ToolSnapshotEntry {
	if raw, ok := ctx["tools"]; ok {
		return parseToolSnapshotRows(raw)
	}
	return mcpServersAsTools(ctx)
}

// parseToolSnapshotRows 解析 tools 键的工具清单行。
func parseToolSnapshotRows(raw any) []ToolSnapshotEntry {
	rows := anyRows(raw)
	if rows == nil {
		return nil
	}
	out := make([]ToolSnapshotEntry, 0, len(rows))
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		e := ToolSnapshotEntry{
			Name:            strOf(m, "name"),
			Origin:          strOf(m, "origin"),
			Server:          strOf(m, "server"),
			HasOutputSchema: boolOf(m, "has_output_schema"),
			ConcurrencySafe: boolOf(m, "concurrency_safe"),
		}
		if e.Name == "" {
			continue
		}
		out = append(out, e)
	}
	return out
}

// mcpServersAsTools 从旧键 mcp_servers（dispatcher resolved 态快照）合成
// 粗粒度工具清单：每服务器一条聚合条目（无逐工具明细——旧快照只有
// tool_count）。仅作兼容读，不产出新快照。
func mcpServersAsTools(ctx map[string]any) []ToolSnapshotEntry {
	rows := anyRows(ctx["mcp_servers"])
	if rows == nil {
		return nil
	}
	out := make([]ToolSnapshotEntry, 0, len(rows))
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		name := strOf(m, "name")
		if name == "" {
			continue
		}
		out = append(out, ToolSnapshotEntry{
			Name: name, Origin: "mcp", Server: name,
			HasOutputSchema: false, ConcurrencySafe: false,
		})
	}
	return out
}

// anyRows 归一化行切片的两种运行期形态（[]any / []map[string]any）。
func anyRows(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case []map[string]any:
		out := make([]any, len(t))
		for i, m := range t {
			out[i] = m
		}
		return out
	}
	return nil
}

func strOf(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func boolOf(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}

func mcpSnapshotRows(entries []MCPSnapshotEntry) []map[string]any {
	if len(entries) == 0 {
		return nil
	}
	rows := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, map[string]any{
			"name": e.Name, "transport": e.Transport, "url": e.URL,
			"status": e.Status, "tool_count": e.ToolCount, "note": e.Note,
		})
	}
	return rows
}

// MCPContextSnapshot 旧形状（仅 mcp_servers 键）：dispatcher resolved 态
// 快照的兼容写法，新代码用 ToolContextSnapshot（双键）。
// 保留：既有调用方（dispatcher 在 C1 前的路径）与测试依赖它。
func MCPContextSnapshot(entries []MCPSnapshotEntry) map[string]any {
	if len(entries) == 0 {
		return nil
	}
	return map[string]any{"mcp_servers": mcpSnapshotRows(entries)}
}

func (s *MCPServerService) ResolveForEngine(ctx context.Context, wsID, agentID string) ([]ResolvedMCPServer, error) {
	wsList, err := s.repo.ListWorkspaceMCP(ctx, wsID)
	if err != nil {
		return nil, err
	}
	agentList, _, err := s.repo.ListAgentMCP(ctx, wsID, agentID)
	if err != nil {
		return nil, err
	}
	ws := make([]domain.MCPServer, len(wsList))
	for i, m := range wsList {
		ws[i] = domain.MCPServer{Name: m.Name, Transport: m.Transport, URL: m.URL, Enabled: true}
	}
	ag := make([]domain.MCPServer, len(agentList))
	for i, m := range agentList {
		ag[i] = domain.MCPServer{Name: m.Name, Transport: m.Transport, URL: m.URL, Enabled: m.Enabled}
	}
	merged := domain.MergeMCP(ws, ag)

	agentCreds := make(map[string]map[string]any, len(agentList))
	for _, m := range agentList {
		agentCreds[m.Name] = m.Credentials
	}
	out := make([]ResolvedMCPServer, 0, len(merged))
	for _, m := range merged {
		creds := map[string]any{}
		if c, ok := agentCreds[m.Name]; ok {
			creds = c
		} else {
			for _, w := range wsList {
				if w.Name == m.Name {
					creds = w.Credentials
					break
				}
			}
		}
		out = append(out, ResolvedMCPServer{Name: m.Name, Transport: m.Transport, URL: m.URL, Credentials: creds})
	}
	return out, nil
}

func (s *EnqueueService) EnqueueLeaderDispatch(ctx context.Context, in LeaderDispatchInput) (string, string, error) {
	if in.MemberAgentID == in.LeaderAgentID {
		return "", dispatchreason.Blocked, nil
	}
	agent, ok, err := s.repo.GetAgent(ctx, in.WorkspaceID, in.MemberAgentID)
	if err != nil {
		return "", "", err
	}
	if !ok || agent.Archived {
		return "", dispatchreason.Blocked, nil
	}

	if agent.RuntimeKind != "external" {
		var rc modelmeta.RuntimeConfig
		if err := json.Unmarshal([]byte(mustJSON(agent.RuntimeConfig)), &rc); err != nil {
			return "", dispatchreason.ModelNotConfigured, nil
		}
		if !modelmeta.ValidConfig(rc) {
			return "", dispatchreason.ModelNotConfigured, nil
		}
	}
	if n, _ := s.repo.CountPendingFence(ctx, in.WorkItemID, in.MemberAgentID); n > 0 {
		return "", dispatchreason.AlreadyActive, nil
	}

	originator, accountable := domain.ResolveAttribution(in.OriginatorUserID, in.AccountableUserID, "", domain.SrcDelegation)
	ctxJSON := map[string]any{
		"trigger":        "leader_dispatch",
		"leader_task_id": in.LeaderTaskID,
		"squad_id":       in.SquadID,
		"instruction":    in.Instruction,
		"member_name":    in.MemberName,
	}
	if wi, ok, _ := s.readWorkItem(ctx, in.WorkspaceID, in.WorkItemID); ok {
		ctxJSON["work_item_title"] = wi.Title
	}

	sessionRefIn, _, _ := s.repo.GetLastSessionRef(ctx, in.WorkItemID, in.MemberAgentID, CurrentEngineTag())
	env := envelope(events.TaskQueued, in.WorkspaceID, originator, nil)
	var taskID string
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		inserted, err := s.repo.InsertTask(ctx, Task{
			WorkspaceID: in.WorkspaceID, AgentID: in.MemberAgentID, WorkItemID: in.WorkItemID,
			Status: domain.Queued, MaxAttempts: 2, Context: ctxJSON,
			OriginatorUserID: originator, AccountableUserID: accountable,
			OriginatorSource: domain.SrcDelegation, TriggerEvidenceKind: "leader_dispatch",
			SessionRefIn: sessionRefIn,
		})
		if err != nil {
			return err
		}
		taskID = inserted.ID
		env.Payload = TaskPayload{ID: inserted.ID, WorkspaceID: inserted.WorkspaceID,
			AgentID: inserted.AgentID, WorkItemID: inserted.WorkItemID, Status: inserted.Status}
		return nil
	}); err != nil {
		return "", "", err
	}
	return taskID, dispatchreason.Queued, nil
}

// SquadHarvestInput 派发式小队的 leader 收割任务入参（全部成员收割后的
// 自动回环；IsLeaderTask=true + SquadID，让引擎注入 squadBrief 与成员工具）。
type SquadHarvestInput struct {
	WorkspaceID       string
	WorkItemID        string
	SquadID           string
	LeaderTaskID      string
	LeaderAgentID     string
	Instruction       string
	OriginatorUserID  string
	AccountableUserID string
}

// EnqueueSquadHarvest 给 leader 插入收割任务。去重：同工单同队 leader 已有
// queued/dispatched/running 任务则 coalesce（返回 AlreadyActive，不入队）。
func (s *EnqueueService) EnqueueSquadHarvest(ctx context.Context, in SquadHarvestInput) (string, string, error) {
	active, err := s.repo.HasActiveTask(ctx, in.WorkspaceID, in.WorkItemID, in.LeaderAgentID)
	if err != nil {
		return "", "", err
	}
	if active {
		return "", dispatchreason.AlreadyActive, nil
	}
	ctxJSON := map[string]any{
		"trigger": "squad_harvest", "squad_id": in.SquadID, "leader_task_id": in.LeaderTaskID,
		"instruction": in.Instruction,
	}
	env := envelope(events.TaskQueued, in.WorkspaceID, in.OriginatorUserID, nil)
	var t Task
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		inserted, err := s.repo.InsertTask(ctx, Task{
			WorkspaceID: in.WorkspaceID, AgentID: in.LeaderAgentID, WorkItemID: in.WorkItemID,
			Status: domain.Queued, MaxAttempts: 1, Context: ctxJSON,
			OriginatorUserID: in.OriginatorUserID, AccountableUserID: in.AccountableUserID,
			OriginatorSource: domain.SrcDelegation, TriggerEvidenceKind: domain.EvidenceSquad,
			IsLeaderTask: true, SquadID: in.SquadID,
		})
		if err != nil {
			return err
		}
		t = inserted
		env.Payload = TaskPayload{ID: t.ID, WorkspaceID: t.WorkspaceID, AgentID: t.AgentID,
			WorkItemID: t.WorkItemID, Status: t.Status}
		return nil
	}); err != nil {
		return "", "", err
	}
	return t.ID, dispatchreason.Queued, nil
}

type ApprovalNotifier interface {
	WriteApprovalRequest(ctx context.Context, workspaceID, userID, taskID, payload string) error
}

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
}

type ChatWriter interface {
	AppendAssistantMessage(ctx context.Context, sessionID, taskID, content, kind string) error

	AppendAssistantParts(ctx context.Context, sessionID, taskID, content, kind string, parts []ContentPart) error

	UpdateResumePointer(ctx context.Context, sessionID, ref string) error

	ClearResumePointer(ctx context.Context, sessionID string) error
}

type WorkItemReader interface {
	GetWorkItem(ctx context.Context, workspaceID, id string) (WorkItemInfo, bool, error)
}

type SearchHit struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title"`
	Snippet    string `json:"snippet,omitempty"`
	Source     string `json:"source"`
}
