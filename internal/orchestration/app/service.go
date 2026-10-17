package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/haozing/ploykit/platform/egressx"
	"github.com/haozing/ploykit/platform/ids"
	"github.com/haozing/ploykit/platform/redactx"
	"orkome/internal/contract/apierr"
	"orkome/internal/contract/dispatchreason"
	"orkome/internal/contract/events"
	"orkome/internal/contract/modelmeta"
	"orkome/internal/orchestration/domain"
)

type AgentService struct {
	repo Repo
	emit Emitter

	Egress *egressx.Guard

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)
}

// validateVisibility：对齐迁移 1003 的 DB CHECK（'private','public','public_to'）。
// 服务层不设防时非法值会穿透到库层撞 CHECK 裸 500（生产实证 visibility="workspace"）。
func validateVisibility(v string) error {
	switch v {
	case "", "private", "public", "public_to":
		return nil
	}
	return apierr.NewValidation("visibility must be one of: private, public, public_to")
}

type Emitter interface {
	Emit(ctx context.Context, e events.Envelope)
}

func NewAgentService(repo Repo, emit Emitter) *AgentService {
	return &AgentService{repo: repo, emit: emit, Egress: egressx.DefaultGuard()}
}

func (s *AgentService) validateEgress(ctx context.Context, runtimeConfig map[string]any) error {
	if s.Egress == nil || runtimeConfig == nil {
		return nil
	}
	baseURL, _ := runtimeConfig["base_url"].(string)
	if baseURL == "" {
		return nil
	}
	return s.Egress.ValidateURL(ctx, baseURL)
}

func (s *AgentService) Create(ctx context.Context, a Agent) (*Agent, error) {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" || len(a.Name) > 100 {
		return nil, apierr.NewValidation("invalid agent name")
	}
	if err := s.validateEgress(ctx, a.RuntimeConfig); err != nil {
		return nil, apierr.NewValidation(err.Error())
	}
	if _, exists, _ := s.repo.GetAgentByName(ctx, a.WorkspaceID, a.Name); exists {
		return nil, apierr.NewValidation("agent name taken")
	}
	if a.MaxConcurrent <= 0 {
		a.MaxConcurrent = 6
	}
	if a.Visibility == "" {
		a.Visibility = "public"
	}
	if err := validateVisibility(a.Visibility); err != nil {
		return nil, err
	}

	switch a.RuntimeKind {
	case "":
		a.RuntimeKind = "engine"
	case "engine", "assistant_proxy":
	case "external":
		// 工人模型退役第一刀（v4 §6）：CreateAgent API 拒绝 external——
		// 该闸门从 W2 提前到本波（assistant_proxy 不走此入口，见下）。
		return nil, apierr.NewValidation("runtime_kind external is retired; external runtimes are frozen")
	default:
		return nil, apierr.NewValidation("runtime_kind must be engine or assistant_proxy")
	}
	if a.RuntimeKind == "assistant_proxy" {
		// 代理行是平台内部身份（惰性创建于 repo 层），不暴露给 Create API：
		// 它会做 visibility/invocation 校验，而 proxy 行 visibility='private'
		// 无 invocation target 属预期形状，绕过而非复用。
		return nil, apierr.NewValidation("runtime_kind assistant_proxy is managed internally")
	}
	if err := validateConversationStarters(a.ConversationStarters); err != nil {
		return nil, err
	}
	targets, err := s.validateInvocationTargets(ctx, &a)
	if err != nil {
		return nil, err
	}
	out, err := s.repo.CreateAgent(ctx, a)
	if err != nil {
		return nil, err
	}
	if targets != nil {
		if err := s.repo.ReplaceInvocationTargets(ctx, a.WorkspaceID, out.ID, targets); err != nil {
			return nil, err
		}
		out.InvocationTargets = &targets
	}
	return &out, nil
}

func (s *AgentService) Get(ctx context.Context, workspaceID, id string) (*Agent, error) {
	a, ok, err := s.repo.GetAgent(ctx, workspaceID, id)
	if err != nil || !ok {
		return nil, apierr.New(apierr.NotFound, "agent not found")
	}
	return &a, nil
}

func (s *AgentService) List(ctx context.Context, workspaceID string) ([]Agent, error) {
	return s.repo.ListAgents(ctx, workspaceID)
}

func (s *AgentService) Update(ctx context.Context, a Agent) (*Agent, error) {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" || len(a.Name) > 100 {
		return nil, apierr.NewValidation("invalid agent name")
	}

	existing, ok, err := s.repo.GetAgent(ctx, a.WorkspaceID, a.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "agent not found")
	}
	a.RuntimeConfig = mergeRuntimeConfig(existing.RuntimeConfig, a.RuntimeConfig)
	if err := s.validateEgress(ctx, a.RuntimeConfig); err != nil {
		return nil, apierr.NewValidation(err.Error())
	}
	if other, exists, _ := s.repo.GetAgentByName(ctx, a.WorkspaceID, a.Name); exists && other.ID != a.ID {
		return nil, apierr.NewValidation("agent name taken")
	}
	if a.MaxConcurrent <= 0 {
		a.MaxConcurrent = 6
	}
	if a.Visibility == "" {
		a.Visibility = "public"
	}
	if err := validateVisibility(a.Visibility); err != nil {
		return nil, err
	}
	if err := validateConversationStarters(a.ConversationStarters); err != nil {
		return nil, err
	}
	targets, err := s.validateInvocationTargets(ctx, &a)
	if err != nil {
		return nil, err
	}

	if a.ConversationStarters == nil {
		a.ConversationStarters = existing.ConversationStarters
	}
	out, ok, err := s.repo.UpdateAgent(ctx, a)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "agent not found")
	}
	if targets != nil {
		if err := s.repo.ReplaceInvocationTargets(ctx, a.WorkspaceID, a.ID, targets); err != nil {
			return nil, err
		}
		out.InvocationTargets = &targets
	}
	return &out, nil
}

func mergeRuntimeConfig(existing, incoming map[string]any) map[string]any {
	if incoming == nil {
		return existing
	}
	out := make(map[string]any, len(incoming))
	for k, v := range incoming {
		if ev, ok := existing[k]; ok {
			v = unmaskValue(ev, v)
		}
		out[k] = v
	}
	if _, ok := out["env"]; !ok {
		if ev, ok := existing["env"]; ok {
			out["env"] = ev
		}
	}
	return out
}

func unmaskValue(existing, incoming any) any {
	if s, ok := incoming.(string); ok && s == MaskedValue {
		return existing
	}
	in, okIn := anyMapOf(incoming)
	ex, okEx := anyMapOf(existing)
	if !okIn || !okEx {
		return incoming
	}
	merged := make(map[string]any, len(in))
	for k, v := range in {
		if ev, ok := ex[k]; ok {
			v = unmaskValue(ev, v)
		}
		merged[k] = v
	}
	return merged
}

func anyMapOf(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[string]string:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[k] = val
		}
		return out, true
	}
	return nil, false
}

const (
	MaxConversationStarters     = 3
	MaxConversationStarterRunes = 80
)

func validateConversationStarters(cs []ConversationStarter) error {
	if cs == nil {
		return nil
	}
	if len(cs) > MaxConversationStarters {
		return apierr.NewValidation("too many conversation starters",
			apierr.FieldError{Field: "conversation_starters", Problem: "at most 3 items"})
	}
	for i := range cs {
		label := strings.TrimSpace(cs[i].Label)
		if label == "" {
			return apierr.NewValidation("conversation starter label required",
				apierr.FieldError{Field: "conversation_starters", Problem: "label must be non-empty after trim"})
		}
		if utf8.RuneCountInString(label) > MaxConversationStarterRunes {
			return apierr.NewValidation("conversation starter label too long",
				apierr.FieldError{Field: "conversation_starters", Problem: "label must be at most 80 runes"})
		}
		cs[i].Label = label
	}
	return nil
}

func (s *AgentService) validateInvocationTargets(ctx context.Context, a *Agent) ([]InvocationTarget, error) {
	if a.InvocationTargets == nil {
		return nil, nil
	}
	targets := *a.InvocationTargets
	if len(targets) > 0 && a.Visibility != "public_to" {
		return nil, apierr.NewValidation("invocation targets require public_to visibility",
			apierr.FieldError{Field: "invocation_targets", Problem: "non-empty targets only allowed when visibility=public_to"})
	}
	seen := make(map[string]bool, len(targets))
	for i := range targets {
		t := targets[i]
		switch t.Type {
		case "workspace":
			if t.UserID != "" {
				return nil, apierr.NewValidation("workspace invocation target must not carry user_id",
					apierr.FieldError{Field: "invocation_targets", Problem: "type=workspace takes no user_id"})
			}
		case "member":
			if t.UserID == "" {
				return nil, apierr.NewValidation("member invocation target requires user_id",
					apierr.FieldError{Field: "invocation_targets", Problem: "type=member requires user_id"})
			}
		default:
			return nil, apierr.NewValidation("invalid invocation target type",
				apierr.FieldError{Field: "invocation_targets", Problem: "type must be workspace or member"})
		}
		key := t.Type + "\x00" + t.UserID
		if seen[key] {
			return nil, apierr.NewValidation("duplicate invocation target",
				apierr.FieldError{Field: "invocation_targets", Problem: "duplicate (type,user_id) pair"})
		}
		seen[key] = true
	}

	if s.MemberCheck != nil {
		for _, t := range targets {
			if t.Type != "member" {
				continue
			}
			ok, err := s.MemberCheck(ctx, a.WorkspaceID, t.UserID)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, apierr.NewValidation("invocation target user is not a workspace member",
					apierr.FieldError{Field: "invocation_targets", Problem: "user_id must be a member of this workspace"})
			}
		}
	} else {
		for _, t := range targets {
			if t.Type == "member" {
				return nil, apierr.NewValidation("member check not wired (fail-closed)",
					apierr.FieldError{Field: "invocation_targets", Problem: "member target validation unavailable"})
			}
		}
	}
	return targets, nil
}

func (s *AgentService) Archive(ctx context.Context, workspaceID, id string, archived bool) error {
	if err := s.repo.ArchiveAgent(ctx, workspaceID, id, archived); err != nil {
		return err
	}
	return nil
}

// SetAgentPack 挂载/卸载 agent 的能力包归属（能力包平台化，1042 的
// agent.pack_id 列；packID nil = 卸下）。先过 Get 的 ws 归属闸（跨区 404），
// repo 的 UPDATE 再带 workspace_id 限定二次收口。pack 本体的存在性校验在
// pack 域 HTTP 层（Packs.Owns），本域不反向依赖 pack（arch iron law）。
func (s *AgentService) SetAgentPack(ctx context.Context, workspaceID, agentID string, packID *string) error {
	if _, err := s.Get(ctx, workspaceID, agentID); err != nil {
		return err
	}
	return s.repo.SetAgentPack(ctx, workspaceID, agentID, packID)
}

func (s *AgentService) CancelTasks(ctx context.Context, workspaceID, id string) (int, error) {
	if _, err := s.Get(ctx, workspaceID, id); err != nil {
		return 0, err
	}
	return s.repo.CancelAgentTasks(ctx, workspaceID, id)
}

type MCPServerService struct {
	repo Repo

	Egress *egressx.Guard

	// ToolProbe 是单台服务器工具枚举的引擎侧实现（engine.ProbeServerTools，
	// 组装根接线）。第二参为 workspaceID（B1 存档按 workspace 拉取）。
	// nil = 未接线，ProbeTools 返回 501 语义错误。
	ToolProbe func(ctx context.Context, srv ResolvedMCPServer, workspaceID string) ([]ProbeTool, error)
}

func NewMCPServerService(repo Repo) *MCPServerService {
	return &MCPServerService{repo: repo, Egress: egressx.DefaultGuard()}
}

func (s *MCPServerService) List(ctx context.Context, wsID string) ([]WorkspaceMCP, error) {
	return s.repo.ListWorkspaceMCP(ctx, wsID)
}

func (s *MCPServerService) Create(ctx context.Context, m WorkspaceMCP) (*WorkspaceMCP, error) {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || m.URL == "" {
		return nil, apierr.NewValidation("name and url required")
	}
	if s.Egress != nil {
		if err := s.Egress.ValidateURL(ctx, m.URL); err != nil {
			return nil, apierr.NewValidation(err.Error())
		}
	}
	out, err := s.repo.CreateWorkspaceMCP(ctx, m)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *MCPServerService) Update(ctx context.Context, m WorkspaceMCP) (*WorkspaceMCP, error) {
	if m.Name == "" || m.URL == "" {
		return nil, apierr.NewValidation("name and url required")
	}
	if s.Egress != nil {
		if err := s.Egress.ValidateURL(ctx, m.URL); err != nil {
			return nil, apierr.NewValidation(err.Error())
		}
	}
	out, ok, err := s.repo.UpdateWorkspaceMCP(ctx, m)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "mcp server not found")
	}
	return &out, nil
}

func (s *MCPServerService) Delete(ctx context.Context, wsID, id string) error {
	return s.repo.DeleteWorkspaceMCP(ctx, wsID, id)
}

// ProbeTool 是 MCP 工具探针的 wire 形状（app 层定义、engine 侧映射填充，
// 避免 app → engine 反向依赖）。
type ProbeTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

// ProbeTools 对工作区级 MCP 服务器做一次实时建连 + ListTools，返回工具
// 清单（工具集成页"查看工具"）。凭证按存储原样使用（含密封解密后的
// headers），连接参数与运行期任务执行同参。
func (s *MCPServerService) ProbeTools(ctx context.Context, wsID, serverID string) ([]ProbeTool, error) {
	list, err := s.repo.ListWorkspaceMCP(ctx, wsID)
	if err != nil {
		return nil, err
	}
	var target *WorkspaceMCP
	for i := range list {
		if list[i].ID == serverID {
			target = &list[i]
			break
		}
	}
	if target == nil {
		return nil, apierr.New(apierr.NotFound, "mcp server not found")
	}
	if s.ToolProbe == nil {
		return nil, apierr.New(apierr.Internal, "tool probe not wired")
	}
	probe, err := s.ToolProbe(ctx, ResolvedMCPServer{
		Name: target.Name, Transport: target.Transport, URL: target.URL, Credentials: target.Credentials,
	}, wsID)
	if err != nil {
		return nil, err
	}
	return probe, nil
}

func (s *MCPServerService) AgentMCP(ctx context.Context, wsID, agentID string) ([]AgentMCP, error) {
	list, ok, err := s.repo.ListAgentMCP(ctx, wsID, agentID)
	if err != nil || !ok {
		if err == nil && !ok {
			return nil, apierr.New(apierr.NotFound, "agent not found")
		}
		return nil, err
	}
	return list, nil
}

func (s *MCPServerService) CreateAgentMCP(ctx context.Context, m AgentMCP) (*AgentMCP, error) {
	if err := s.mustOwnAgent(ctx, m.WorkspaceID, m.AgentID); err != nil {
		return nil, err
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || m.URL == "" {
		return nil, apierr.NewValidation("name and url required")
	}
	if s.Egress != nil {
		if err := s.Egress.ValidateURL(ctx, m.URL); err != nil {
			return nil, apierr.NewValidation(err.Error())
		}
	}
	// CreateAgentMCP：创建语义，重名由 repo 映射 23505 → 409（FP-07 P1-4：
	// agent 级不再静默 upsert 覆盖既有条目，对齐 ws 级）。
	out, err := s.repo.CreateAgentMCP(ctx, m)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *MCPServerService) UpdateAgentMCP(ctx context.Context, m AgentMCP) (*AgentMCP, error) {
	if err := s.mustOwnAgent(ctx, m.WorkspaceID, m.AgentID); err != nil {
		return nil, err
	}
	if m.Name == "" || m.URL == "" {
		return nil, apierr.NewValidation("name and url required")
	}
	if s.Egress != nil {
		if err := s.Egress.ValidateURL(ctx, m.URL); err != nil {
			return nil, apierr.NewValidation(err.Error())
		}
	}
	out, ok, err := s.repo.UpdateAgentMCP(ctx, m)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "agent mcp server not found")
	}
	return &out, nil
}

func (s *MCPServerService) DeleteAgentMCP(ctx context.Context, wsID, agentID, id string) error {
	if err := s.mustOwnAgent(ctx, wsID, agentID); err != nil {
		return err
	}
	return s.repo.DeleteAgentMCP(ctx, wsID, agentID, id)
}

func (s *MCPServerService) mustOwnAgent(ctx context.Context, wsID, agentID string) error {
	if _, ok, err := s.repo.GetAgent(ctx, wsID, agentID); err != nil {
		return err
	} else if !ok {
		return apierr.New(apierr.NotFound, "agent not found")
	}
	return nil
}

func (s *MCPServerService) Resolved(ctx context.Context, wsID, agentID string) ([]domain.MCPServer, error) {
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
	return domain.MergeMCP(ws, ag), nil
}

type EnqueueService struct {
	repo   Repo
	emit   Emitter
	outbox OutboxRecorder
	uow    Within
}

func NewEnqueueService(repo Repo, emit Emitter) *EnqueueService {
	return &EnqueueService{repo: repo, emit: emit}
}

func (s *EnqueueService) SetOutbox(rec OutboxRecorder, uow Within) {
	s.outbox, s.uow = rec, uow
}

func emitCommitted(ctx context.Context, emit Emitter, outbox OutboxRecorder, uow Within,
	env *events.Envelope, write func(context.Context) error) error {
	if outbox != nil && uow != nil {
		if err := uow(ctx, func(ctx context.Context) error {
			if err := write(ctx); err != nil {
				return err
			}
			return outbox.Append(ctx, *env)
		}); err != nil {
			return err
		}
		emit.Emit(ctx, *env)
		_ = outbox.MarkPublished(ctx, env.EventID)
		return nil
	}
	if err := write(ctx); err != nil {
		return err
	}
	emit.Emit(ctx, *env)
	return nil
}

func (s *EnqueueService) EnqueueQuickAction(ctx context.Context, workspaceID, workItemID, agentID, actorUserID, prompt string) (*Task, string, error) {
	a, ok, err := s.repo.GetAgent(ctx, workspaceID, agentID)
	if err != nil {
		return nil, "", err
	}
	if !ok || a.Archived {
		return nil, dispatchreason.Blocked, nil
	}
	if n, _ := s.repo.CountPendingFence(ctx, workItemID, agentID); n > 0 {
		return nil, dispatchreason.AlreadyActive, nil
	}
	ctxJSON := map[string]any{"trigger": "quick_action", "actor": actorUserID,
		"quick_action_prompt": prompt}
	if wi, ok, _ := s.readWorkItem(ctx, workspaceID, workItemID); ok {
		ctxJSON["work_item_title"] = wi.Title
	}
	env := envelope(events.TaskQueued, workspaceID, actorUserID, nil)
	var t Task
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		inserted, err := s.repo.InsertTask(ctx, Task{
			WorkspaceID: workspaceID, AgentID: agentID, WorkItemID: workItemID,
			Status: domain.Queued, MaxAttempts: 2, Context: ctxJSON,
			OriginatorUserID:  actorUserID,
			AccountableUserID: actorUserID,
			OriginatorSource:  domain.SrcDirectHuman, TriggerEvidenceKind: domain.EvidenceAssignment,
		})
		if err != nil {
			return err
		}
		t = inserted
		env.Payload = TaskPayload{ID: t.ID, WorkspaceID: t.WorkspaceID, AgentID: t.AgentID, Status: t.Status}
		return nil
	}); err != nil {
		return nil, "", err
	}
	return &t, dispatchreason.Queued, nil
}

func (s *EnqueueService) EnqueueAssignment(ctx context.Context, workspaceID, workItemID, agentID, actorUserID string) (*Task, string, error) {
	agent, ok, err := s.repo.GetAgent(ctx, workspaceID, agentID)
	if err != nil {
		return nil, "", err
	}
	if !ok || agent.Archived {
		return nil, dispatchreason.Blocked, nil
	}

	if agent.RuntimeKind != "external" {
		var rc modelmeta.RuntimeConfig
		if err := json.Unmarshal([]byte(mustJSON(agent.RuntimeConfig)), &rc); err != nil {
			return nil, dispatchreason.ModelNotConfigured, nil
		}
		if !modelmeta.ValidConfig(rc) {
			return nil, dispatchreason.ModelNotConfigured, nil
		}
	}
	if n, _ := s.repo.CountPendingFence(ctx, workItemID, agentID); n > 0 {
		return nil, dispatchreason.AlreadyActive, nil
	}
	originator, accountable := domain.ResolveAttribution(actorUserID, actorUserID, agent.OwnerUserID, domain.SrcDirectHuman)
	ctxJSON := map[string]any{"trigger": "assignment", "actor": actorUserID}
	if wi, ok, _ := s.readWorkItem(ctx, workspaceID, workItemID); ok {
		ctxJSON["work_item_title"] = wi.Title
	}

	sessionRefIn, _, _ := s.repo.GetLastSessionRef(ctx, workItemID, agentID, CurrentEngineTag())
	env := envelope(events.TaskQueued, workspaceID, actorUserID, nil)
	var t Task
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		inserted, err := s.repo.InsertTask(ctx, Task{
			WorkspaceID: workspaceID, AgentID: agentID, WorkItemID: workItemID,
			Status: domain.Queued, MaxAttempts: 2, Context: ctxJSON,
			OriginatorUserID: originator, AccountableUserID: accountable,
			OriginatorSource: domain.SrcDirectHuman, TriggerEvidenceKind: domain.EvidenceAssignment,
			SessionRefIn: sessionRefIn,
		})
		if err != nil {
			return err
		}
		t = inserted
		env.Payload = TaskPayload{ID: t.ID, WorkspaceID: t.WorkspaceID, AgentID: t.AgentID, WorkItemID: t.WorkItemID, Status: t.Status}
		return nil
	}); err != nil {
		return nil, "", err
	}
	return &t, dispatchreason.Queued, nil
}

var workItemReader WorkItemReader

func SetWorkItemReader(r WorkItemReader) { workItemReader = r }

var engineTag string

func SetEngineTag(tag string) { engineTag = tag }

func CurrentEngineTag() string { return engineTag }

func (s *EnqueueService) readWorkItem(ctx context.Context, ws, id string) (WorkItemInfo, bool, error) {
	if workItemReader == nil {
		return WorkItemInfo{}, false, nil
	}
	return workItemReader.GetWorkItem(ctx, ws, id)
}

type ClaimService struct{ repo Repo }

func NewClaimService(repo Repo) *ClaimService { return &ClaimService{repo: repo} }

func (s *ClaimService) ClaimNext(ctx context.Context, agent Agent) (Task, bool, error) {
	return s.repo.ClaimNext(ctx, agent.ID, agent.MaxConcurrent)
}

type SettlementService struct {
	repo Repo
	emit Emitter
	// notification 保留占位：任务完成通知已收敛为单一 outbox 投影路径
	//（FP-10 P1-2），Settle 内的 WriteTaskCompleted 直写第三条已下线；
	// 构造参数保留以兼容既有装配（cmd 侧无需改动）。
	notification NotificationWriter
	comments     CommentWriter
	// outcomes 工单产出写入端口（"需求→产出"一等公民）：completed 结算时
	// 后写覆盖工单 outcome。nil 时跳过（outcome 不落，行为同旧）。
	outcomes     OutcomeWriter
	// squadWake 派发式小队的 leader 自动回环：成员任务终态后检查同工单同队
	// 是否全部收割，是则给 leader 挂一个收割任务（record_evaluation/汇总）。
	squadWake    LeaderWakeHook
	chats        ChatWriter
	notifier     ApprovalNotifier
	engine       Engine
	secret       func(ctx context.Context, ws, credID string) (string, error)

	// summaries 生成结算 summary（A2 分层上移：LLM 七段 checkpoint，开关
	// 默认关时为纯截断回落）。nil 时按开关关处理。
	summaries *SummaryService

	// chatDonePush chat:done 收尾帧的 creator 定向投递端口（W2-1：帧不再进
	// workspace 广播，改 SendToUser(creator)；notification×6 帧同构，wiring
	// 侧以 notifUserFrames 适配 hub.SendToUser）。nil 时仅发 bus 事件。
	chatDonePush UserFramePusher

	instanceID string
	outbox     OutboxRecorder
	uow        Within
}

func NewSettlementService(repo Repo, emit Emitter, notification NotificationWriter, comments CommentWriter, chats ChatWriter) *SettlementService {
	return &SettlementService{repo: repo, emit: emit, notification: notification, comments: comments, chats: chats}
}

func (s *SettlementService) SetApprovalDeps(n ApprovalNotifier, e Engine, cred func(ctx context.Context, ws, credID string) (string, error)) {
	s.notifier, s.engine, s.secret = n, e, cred
}

// SetSummarySummarizer 接线 summary 生成器（A2）。不调用时 summaries 为
// nil，结算走 domain.TaskSummary 等价的纯截断路径（byte-identical 旧行为）。
func (s *SettlementService) SetSummarySummarizer(ss *SummaryService) { s.summaries = ss }

// SetChatDonePush 接线 chat:done 帧的 creator 定向投递（W2-1）。不调用时
// 只发 bus 事件（QuickActionGenerator 仍工作），creator 收不到 WS 帧。
func (s *SettlementService) SetChatDonePush(p UserFramePusher) { s.chatDonePush = p }

// SetOutcomeWriter 接线工单产出写入（"需求→产出"）。不调用时 completed
// 结算不落 outcome（评论兜底照旧——产出只活在工作项评论区）。
func (s *SettlementService) SetOutcomeWriter(w OutcomeWriter) { s.outcomes = w }

// SetSquadWake 接线派发式小队的 leader 自动回环（不调用则不唤醒——旧行为）。
func (s *SettlementService) SetSquadWake(h LeaderWakeHook) { s.squadWake = h }

// summaryForComplete 生成 completed 结算的 summary；summaries 未接线时
// 直接走 domain.TaskSummary（与上移前 repo 内行为 byte-identical）。
func (s *SettlementService) summaryForComplete(ctx context.Context, task Task, result string) string {
	if s.summaries == nil {
		return domain.TaskSummary(s.lastTaskText(ctx, task.ID), result)
	}
	return s.summaries.ForComplete(ctx, task, result)
}

// summaryForFail 生成 failed 结算的 summary；summaries 未接线时直接走
// domain.TaskSummary（fallback 口径 reason+": "+errMsg，byte-identical）。
func (s *SettlementService) summaryForFail(ctx context.Context, task Task, reason, errMsg string) string {
	if s.summaries == nil {
		return domain.TaskSummary(s.lastTaskText(ctx, task.ID), reason+": "+errMsg)
	}
	return s.summaries.ForFail(ctx, task, reason, errMsg)
}

func (s *SettlementService) lastTaskText(ctx context.Context, taskID string) string {
	txt, err := s.repo.LastTaskText(ctx, taskID)
	if err != nil {
		return ""
	}
	return txt
}

// SummaryForSweep 供 workers 的 liveness sweep（runtime_recovery / 判死路径）
// 生成失败结算 summary。sweep 侧只有 taskID（无 workspace 上下文），LLM 路
// 径会因材料不足自然回落纯截断——判死任务的轨迹通常也已残缺，这是可接受
// 的降级（见交接笔记）。
func (s *SettlementService) SummaryForSweep(ctx context.Context, taskID, reason, errMsg string) string {
	return s.summaryForFail(ctx, Task{ID: taskID}, reason, errMsg)
}

// WriteOutcomeFor 结算尾部公共层的 outcome 段（v4 §4.4 改造2，二轮 P0-3）：
// completed 任务的工单产出后写覆盖。从 Settle 抽出——引擎链之外的所有结算
// 口（external/assistant_proxy 直连任务）经此共用同一份落库与降级行为；
// 此前 external 从不落 outcome 是存量缺陷，本抽取顺带修复。
// outcome 落库失败必须留下痕迹（emit TaskFailed 携 outcome_write_failed），
// 但不使结算本身失败——产出缺失不该把"活干完了"改写成"失败"。
func (s *SettlementService) WriteOutcomeFor(ctx context.Context, task Task, result string) {
	if task.WorkItemID == "" || s.outcomes == nil || strings.TrimSpace(result) == "" {
		return
	}
	body := result
	if len([]rune(body)) > 4000 {
		body = string([]rune(body)[:4000])
	}
	if err := s.outcomes.WriteOutcome(ctx, task.WorkspaceID, task.WorkItemID, task.ID, body); err != nil {
		s.emit.Emit(ctx, envelope(events.TaskFailed, task.WorkspaceID, task.AgentID,
			map[string]any{"task_id": task.ID, "outcome_write_failed": err.Error()}))
	}
}

func (s *SettlementService) SetRuntimeInstance(instanceID string) { s.instanceID = instanceID }

func (s *SettlementService) SetOutbox(rec OutboxRecorder, uow Within) {
	s.outbox, s.uow = rec, uow
}

func (s *SettlementService) complete(ctx context.Context, task Task, out EngineOutput, env events.Envelope) error {
	summary := s.summaryForComplete(ctx, task, out.Output)
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		if err := s.repo.CompleteRun(ctx, task.ID, out.Output, out.SessionRef, CurrentEngineTag(), summary, out.Usage); err != nil {
			return err
		}
		if err := s.repo.SettlePendingSupplements(ctx, task.ID); err != nil {
			return err
		}

		if task.Context != nil {
			if sid, _ := task.Context["chat_session_id"].(string); sid != "" && s.chats != nil {
				if len(out.Parts) > 0 {
					if err := s.chats.AppendAssistantParts(ctx, sid, task.ID, out.Output, "message", out.Parts); err != nil {
						return err
					}
				} else if err := s.chats.AppendAssistantMessage(ctx, sid, task.ID, out.Output, "message"); err != nil {
					return err
				}
				if out.SessionRef != "" {
					if err := s.chats.UpdateResumePointer(ctx, sid, out.SessionRef); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	s.promoteAfterTerminal(ctx, task)
	s.wakeLeaderAfterTerminal(ctx, task)
	return nil
}

// wakeLeaderAfterTerminal 派发式小队自动回环：来自 leader_dispatch 的成员
// 任务终态后，若同工单同队全部成员已收割，则给 leader 插入收割任务
// （record_evaluation/汇总用）。子任务链的唤醒由 create_child_work_item 的
// 子工单完成事件另行驱动，不在此处。
func (s *SettlementService) wakeLeaderAfterTerminal(ctx context.Context, task Task) {
	if s.squadWake == nil || task.WorkItemID == "" {
		return
	}
	if task.Context == nil || task.Context["trigger"] != "leader_dispatch" {
		return
	}
	_ = s.squadWake.WakeLeaderIfSquadDone(ctx, task)
}

func (s *SettlementService) promoteAfterTerminal(ctx context.Context, task Task) {
	promoted, err := s.repo.PromoteDeferred(ctx)
	if err != nil {
		return
	}
	for _, p := range promoted {
		s.emit.Emit(ctx, envelope(events.TaskQueued, p.WorkspaceID, "",
			TaskPayload{ID: p.ID, WorkspaceID: p.WorkspaceID, AgentID: p.AgentID,
				WorkItemID: p.WorkItemID, Status: p.Status}))
	}
}

func (s *SettlementService) Settle(ctx context.Context, task Task, agent Agent, out EngineOutput, execErr error) error {
	out = redactedOutput(out)
	if execErr != nil {
		return s.fail(ctx, task, agent, execErr)
	}
	if out.Outcome == "awaiting_input" && out.Interrupt != nil {
		return s.park(ctx, task, agent, *out.Interrupt)
	}
	if err := s.complete(ctx, task, out, envelope(events.TaskCompleted, task.WorkspaceID, "", taskResult(task, out.Output))); err != nil {
		return err
	}

	for _, mu := range out.MemberUsage {
		_ = s.repo.InsertMemberUsage(ctx, task.ID, mu.AgentID, mu.Label, mu.Usage)
	}

	// 工单产出（"需求→产出"一等公民）：completed 时后写覆盖 outcome。
	// 结算尾部公共层（v4 §4.4 改造2）：external/proxy 直连结算共用 WriteOutcomeFor。
	s.WriteOutcomeFor(ctx, task, out.Output)

	if task.Context != nil {
		if sid, _ := task.Context["chat_session_id"].(string); sid != "" {
			s.emitChatDone(ctx, task, sid, out.Output, domain.Completed)
		}
	}
	// FP-10 P1-2 通知双发/三发治理：complete() 已在同一事务内把 TaskCompleted
	// 经 outbox 落一次（唯一投影源），此处不再重复 Emit（新 EventID 会绕过
	// 投影 dedup 造成订阅者双通知）；起源者直写第三条已下线——通知统一收敛
	// 为"投影单路径"，起源者自通知由投影侧按 Envelope.ActorUserID 过滤。
	return nil
}

func (s *SettlementService) park(ctx context.Context, task Task, agent Agent, it InterruptInfo) error {
	env := envelope(events.TaskAwaiting, task.WorkspaceID, "", taskResult(task, ""))
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		return s.repo.ParkTask(ctx, task.ID, it)
	}); err != nil {
		return err
	}
	if s.notifier != nil && task.OriginatorUserID != "" {
		_ = s.notifier.WriteApprovalRequest(ctx, task.WorkspaceID, task.OriginatorUserID, task.ID, it.Payload)
	}
	return nil
}

func (s *SettlementService) Approve(ctx context.Context, workspaceID, taskID, byUser string, approved bool, note string) error {
	task, ok, err := s.repo.GetTask(ctx, workspaceID, taskID)
	if err != nil || !ok {
		return apierr.New(apierr.NotFound, "task not found")
	}
	if task.Status != "running" {
		return apierr.New(apierr.Conflict, "task not awaiting")
	}
	agent, ok, err := s.repo.GetAgent(ctx, workspaceID, task.AgentID)
	if err != nil || !ok {
		return apierr.New(apierr.NotFound, "agent gone")
	}
	interruptMap, _ := task.Context["interrupt"].(map[string]any)
	targetID, _ := interruptMap["target_id"].(string)
	if targetID == "" {
		return apierr.New(apierr.Conflict, "no interrupt target")
	}
	if err := s.repo.ClearAwaiting(ctx, task.ID); err != nil {
		return err
	}

	if s.instanceID != "" {
		_ = s.repo.SetRuntime(ctx, task.ID, s.instanceID)
	}
	secret := s.resolveSecret(ctx, agent)
	out, execErr := s.engine.Resume(ctx, task, agent, secret, targetID,
		ApprovalDecision{Approved: approved, ByUserID: byUser, Note: note})
	out = redactedOutput(out)
	if execErr != nil {
		return s.fail(ctx, task, agent, execErr)
	}
	if out.Outcome == "awaiting_input" && out.Interrupt != nil {
		return s.park(ctx, task, agent, *out.Interrupt)
	}
	if err := s.complete(ctx, task, out, envelope(events.TaskCompleted, workspaceID, byUser, taskResult(task, out.Output))); err != nil {
		return err
	}

	if sid, _ := task.Context["chat_session_id"].(string); sid != "" {
		s.emitChatDone(ctx, task, sid, out.Output, domain.Completed)
	}
	// FP-10：complete() 已在事务内 emit 过 TaskCompleted，这里不再重复 Emit
	//（双 EventID 会让订阅者收到双通知）。actor=byUser 供投影侧过滤自通知。
	return nil
}

func (s *SettlementService) SweepExpiredAwaiting(ctx context.Context) (int, error) {
	ids, err := s.repo.ListExpiredAwaiting(ctx, 24*60)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := s.repo.FailRun(ctx, id, "interrupt_timeout", "approval wait exceeded 24h",
			s.summaryForFail(ctx, Task{ID: id}, "interrupt_timeout", "approval wait exceeded 24h")); err != nil {
			return 0, err
		}
		_ = s.repo.DeleteTaskCheckpoints(ctx, id)
	}
	return len(ids), nil
}

func (s *SettlementService) resolveSecret(ctx context.Context, agent Agent) string {
	if s.secret == nil {
		return ""
	}
	id, _ := agent.RuntimeConfig["credential_id"].(string)
	if id == "" {
		return ""
	}
	sec, err := s.secret(ctx, agent.WorkspaceID, id)
	if err != nil {
		return ""
	}
	return sec
}

func (s *SettlementService) fail(ctx context.Context, task Task, agent Agent, err error) error {
	reason := classify(err)
	detail := redactx.String(err.Error())
	summary := s.summaryForFail(ctx, task, reason, detail)
	env := envelope(events.TaskFailed, task.WorkspaceID, "", taskResult(task, ""))
	if cerr := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		if _, serr := s.repo.FailRun(ctx, task.ID, reason, detail, summary); serr != nil {
			return serr
		}
		if err := s.repo.SettlePendingSupplements(ctx, task.ID); err != nil {
			return err
		}

		if task.Context != nil {
			if sid, _ := task.Context["chat_session_id"].(string); sid != "" && s.chats != nil {
				return s.chats.AppendAssistantMessage(ctx, sid, task.ID, "", "message")
			}
		}
		return nil
	}); cerr != nil {
		return cerr
	}

	// W2-1：fail 路径同样发 chat:done 收尾帧（此前只发 task:failed，聊天页
	// 失败场景永远等不到收尾）。content 恒空、kind 走 no_response 规则——
	// 与 fail 落库的空 assistant 行（AppendAssistantMessage("")→no_response）
	// 保持 REST/帧一致；失败语义由 status=failed 携带。
	if task.Context != nil {
		if sid, _ := task.Context["chat_session_id"].(string); sid != "" {
			s.emitChatDone(ctx, task, sid, "", domain.Failed)
		}
	}

	s.promoteAfterTerminal(ctx, task)
	s.wakeLeaderAfterTerminal(ctx, task)
	return nil
}

func (s *SettlementService) CancelByTriggerComment(ctx context.Context, workspaceID, commentID, actorUserID string) (int, error) {
	ids, err := s.repo.CancelTasksByTriggerComment(ctx, workspaceID, commentID)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		s.emit.Emit(ctx, envelope(events.TaskCancelled, workspaceID, actorUserID,
			TaskPayload{ID: id, WorkspaceID: workspaceID, Status: domain.Cancelled}))
	}
	return len(ids), nil
}

func (s *SettlementService) HandleCommentEvent(ctx context.Context, e events.Envelope) error {
	switch e.Event {
	case events.CommentUpdated, events.CommentDeleted:
	default:
		return nil
	}
	p, ok := e.Payload.(events.CommentPayload)
	if !ok || p.ID == "" {
		return nil
	}
	_, err := s.CancelByTriggerComment(ctx, e.WorkspaceID, p.ID, e.ActorUserID)
	return err
}

func redactedOutput(out EngineOutput) EngineOutput {
	out.Output = redactx.String(out.Output)
	for i := range out.Parts {
		out.Parts[i].Text = redactx.String(out.Parts[i].Text)
	}
	return out
}

func classify(err error) string {
	var ee *EngineError
	if errors.As(err, &ee) {
		return ee.Reason
	}
	return "agent_error.tool_failed"
}

type EngineError struct {
	Reason string
	Detail string
}

func (e *EngineError) Error() string { return e.Reason + ": " + e.Detail }

func (s *EnqueueService) EnqueueChatInTx(ctx context.Context, workspaceID, sessionID, agentID, userID, content string) (string, error) {
	a, ok, err := s.repo.GetAgent(ctx, workspaceID, agentID)
	if err != nil {
		return "", err
	}
	// FP-14 P1-1：裸 error 会被 writeErr 兜底成 500 且无日志。分类：
	// 不存在 → 400（请求引用了不存在的 agent）；已归档 → 409 Conflict
	//（请求本身合法，是 agent 生命周期状态与之冲突——本仓 409 通用码为
	// apierr.Conflict/E_REVISION_CONFLICT，writeErr 双域均已映射 409）。
	if !ok {
		return "", apierr.NewValidation("agent not found in this workspace")
	}
	if a.Archived {
		return "", apierr.New(apierr.Conflict, "agent archived")
	}
	t, err := s.repo.InsertChatTask(ctx, Task{
		WorkspaceID: workspaceID, AgentID: agentID,
		Context:          map[string]any{"chat_session_id": sessionID, "message": content},
		OriginatorUserID: userID, TriggerEvidenceKind: domain.EvidenceChat,
	})
	if err != nil {
		return "", err
	}
	if s.outbox != nil {
		env := envelope(events.TaskQueued, workspaceID, userID,
			TaskPayload{ID: t.ID, WorkspaceID: t.WorkspaceID, AgentID: t.AgentID, Status: t.Status})
		if err := s.outbox.Append(ctx, env); err != nil {
			return "", err
		}
	}
	return t.ID, nil
}

func (s *EnqueueService) EnqueueChat(ctx context.Context, workspaceID, sessionID, agentID, userID, content string) (string, error) {
	a, ok, err := s.repo.GetAgent(ctx, workspaceID, agentID)
	if err != nil {
		return "", err
	}
	// 与 EnqueueChatInTx 同口径（FP-14 P1-1）：不存在→400、已归档→409。
	if !ok {
		return "", apierr.NewValidation("agent not found in this workspace")
	}
	if a.Archived {
		return "", apierr.New(apierr.Conflict, "agent archived")
	}
	env := envelope(events.TaskQueued, workspaceID, userID, nil)
	var taskID string
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		t, err := s.repo.InsertChatTask(ctx, Task{
			WorkspaceID: workspaceID, AgentID: agentID,
			Context:          map[string]any{"chat_session_id": sessionID, "message": content},
			OriginatorUserID: userID, TriggerEvidenceKind: domain.EvidenceChat,
		})
		if err != nil {
			return err
		}
		taskID = t.ID
		env.Payload = TaskPayload{ID: t.ID, WorkspaceID: t.WorkspaceID, AgentID: t.AgentID, Status: t.Status}
		return nil
	}); err != nil {
		return "", err
	}
	return taskID, nil
}

func (s *EnqueueService) EnqueueSquadTask(ctx context.Context, workspaceID, workItemID, squadID, actorUserID string) (*Task, error) {
	sq, ok, err := s.repo.GetSquad(ctx, workspaceID, squadID)
	if err != nil || !ok || len(sq.Members) == 0 {
		return nil, apierr.New(apierr.NotFound, "squad not found")
	}

	leaderID := s.leaderAgentID(ctx, workspaceID, squadID, sq)
	if leaderID == "" {
		return nil, apierr.NewValidation("squad has no leader")
	}
	ctxJSON := map[string]any{"trigger": "squad", "actor": actorUserID}
	env := envelope(events.TaskQueued, workspaceID, actorUserID, nil)
	var t Task
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		inserted, err := s.repo.InsertTask(ctx, Task{
			WorkspaceID: workspaceID, AgentID: leaderID, WorkItemID: workItemID,
			Status: domain.Queued, MaxAttempts: 1, Context: ctxJSON,
			OriginatorUserID: actorUserID, AccountableUserID: actorUserID,
			OriginatorSource: domain.SrcDirectHuman, TriggerEvidenceKind: domain.EvidenceSquad,
			IsLeaderTask: true, SquadID: squadID,
		})
		if err != nil {
			return err
		}
		t = inserted
		env.Payload = TaskPayload{ID: t.ID, WorkspaceID: t.WorkspaceID, AgentID: t.AgentID, WorkItemID: t.WorkItemID, Status: t.Status}
		return nil
	}); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *EnqueueService) leaderAgentID(ctx context.Context, wsID, squadID string, sq SquadInfo) string {
	if l := s.repo.LeaderAgentID(ctx, squadID); l != "" {
		return l
	}
	return sq.Members[0].AgentID
}

func (s *SettlementService) CancelChatTask(ctx context.Context, workspaceID, sessionID string) (string, string, bool, error) {
	t, ok, err := s.repo.GetTaskByChatSession(ctx, workspaceID, sessionID)
	if err != nil || !ok {
		return "", "", false, err
	}
	if t.Status != domain.Queued && t.Status != domain.Dispatched {
		return t.Status, t.ID, true, nil
	}
	if _, err := s.repo.CancelRun(ctx, workspaceID, t.ID); err != nil {
		return "", t.ID, false, err
	}
	return "cancelled", t.ID, true, nil
}

func (s *SettlementService) ChatTaskStatus(ctx context.Context, workspaceID, sessionID string) (status, taskID string, ok bool, err error) {
	t, ok, err := s.repo.GetTaskByChatSession(ctx, workspaceID, sessionID)
	return t.Status, t.ID, ok, err
}

func (s *SettlementService) Cancel(ctx context.Context, workspaceID, id string) (bool, error) {
	cancelled := false
	var env events.Envelope
	if s.outbox != nil && s.uow != nil {
		if err := s.uow(ctx, func(ctx context.Context) error {
			ok, err := s.repo.CancelRun(ctx, workspaceID, id)
			if err != nil {
				return err
			}
			cancelled = ok
			if !cancelled {
				return nil
			}
			env = envelope(events.TaskCancelled, workspaceID, "", TaskPayload{ID: id, WorkspaceID: workspaceID, Status: domain.Cancelled})
			return s.outbox.Append(ctx, env)
		}); err != nil {
			return false, err
		}
	} else {
		ok, err := s.repo.CancelRun(ctx, workspaceID, id)
		if err != nil {
			return false, err
		}
		cancelled = ok
		if cancelled {
			env = envelope(events.TaskCancelled, workspaceID, "", TaskPayload{ID: id, WorkspaceID: workspaceID, Status: domain.Cancelled})
		}
	}
	if cancelled {
		s.emit.Emit(ctx, env)
		if s.outbox != nil {
			_ = s.outbox.MarkPublished(ctx, env.EventID)
		}
	}
	return cancelled, nil
}

func envelope(name, wsID, actor string, payload any) events.Envelope {
	return events.Envelope{

		EventID: ids.NewV4().String(),
		Event:   name, WorkspaceID: wsID, ActorUserID: actor,
		OccurredAt: time.Now().UTC(), Payload: payload,
	}
}

func taskResult(t Task, result string) TaskPayload {
	return TaskPayload{ID: t.ID, WorkspaceID: t.WorkspaceID, AgentID: t.AgentID, WorkItemID: t.WorkItemID, Status: t.Status, Result: result, OriginatorSource: t.OriginatorSource}
}

type TaskPayload struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	AgentID     string `json:"agent_id"`
	WorkItemID  string `json:"work_item_id,omitempty"`
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"`
	// OriginatorSource 任务来源标记（v4 §4.2）：assistant_claim 时
	// TaskCompleted 的消费者跳过引擎闲置复位（resetIdleWorkItem）——代理
	// 任务不是引擎闲置，reset 的 status guard 会把工单打回 todo，与代理
	// 语义相悖。零值 omitempty：既有发射面 payload 形状不变。
	OriginatorSource string `json:"originator_source,omitempty"`
}

func mustJSON(m map[string]any) []byte {
	b, _ := json.Marshal(m)
	return b
}

func chatDonePayload(sessionID, taskID, content, status string) ChatDoneFrame {
	kind := "message"
	if strings.TrimSpace(content) == "" {
		kind, content = "no_response", ""
	}
	return ChatDoneFrame{SessionID: sessionID, TaskID: taskID, Role: "assistant", Kind: kind, Status: status, Content: content}
}

// chatDoneFramePayload ChatDoneFrame → Push 端口的 map 形参（键名与 JSON
// tag 一致，前端看到的帧形状与旧广播帧仅多 status 一键）。
func chatDoneFramePayload(f ChatDoneFrame) map[string]any {
	return map[string]any{"session_id": f.SessionID, "task_id": f.TaskID,
		"role": f.Role, "kind": f.Kind, "status": f.Status, "content": f.Content}
}

// emitChatDone W2-1：chat:done 收尾帧唯一出口，completed/approve/fail 三
// 结算路径共用（cancel 路径由 conversation 域 Finalize 发射——queued 直取消
// 不过 orchestration 事件，TaskCancelled 漏斗也终结在会话投影侧）。两路投递：
//  1. bus 事件：WorkspaceID 置空——带全文的帧禁止再进 workspace/chat scope
//     广播（creator 私有裁决，wiring_ws 的 PhaseBroadcast 对空 WorkspaceID
//     短路）；唯一消费方 QuickActionGenerator 按 payload 取数不受影响。
//  2. creator 定向 push：notification×6 帧的 SendToUser 模式，帧含完整回复
//     （creator 本人可见，与 REST 读口径一致，隐私矛盾消解）。
func (s *SettlementService) emitChatDone(ctx context.Context, task Task, sessionID, content, status string) {
	frame := chatDonePayload(sessionID, task.ID, content, status)
	s.emit.Emit(ctx, envelope(events.ChatDone, "", task.OriginatorUserID, frame))
	if s.chatDonePush != nil && task.OriginatorUserID != "" {
		s.chatDonePush.Push(ctx, task.OriginatorUserID, events.ChatDoneFrameType, chatDoneFramePayload(frame))
	}
}

// UserFramePusher 用户定向 WS 帧发送端口（notification 域 UserFrames 同构；
// cmd 侧 notifUserFrames 满足该接口，直接适配 hub.SendToUser）。
type UserFramePusher interface {
	Push(ctx context.Context, userID, frameType string, payload map[string]any)
}

// chatDoneFrame is the chat.done realtime payload (product vocabulary; the
// framework wswire ships the frame shape, not orkome's payload types).
// W2-1：+Status 终态语义（completed|failed|cancelled）——fail/cancel 路径
// 补帧后，前端靠 status 区分收尾原因；形状与 completed 帧完全一致。
type ChatDoneFrame struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"`
	Role      string `json:"role"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Content   string `json:"content"`
}
