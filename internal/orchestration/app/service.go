package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/dispatchreason"
	"orkome/internal/contract/events"
	"orkome/internal/contract/modelmeta"
	"orkome/internal/contract/wswire"
	"orkome/internal/orchestration/domain"
	"github.com/haozing/ploykit/platform/egressx"
	"github.com/haozing/ploykit/platform/ids"
	"github.com/haozing/ploykit/platform/redactx"
)

type AgentService struct {
	repo Repo
	emit Emitter

	Egress *egressx.Guard

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)
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

	switch a.RuntimeKind {
	case "":
		a.RuntimeKind = "engine"
	case "engine", "external":
	default:
		return nil, apierr.NewValidation("runtime_kind must be engine or external")
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

func (s *AgentService) CancelTasks(ctx context.Context, workspaceID, id string) (int, error) {
	if _, err := s.Get(ctx, workspaceID, id); err != nil {
		return 0, err
	}
	return s.repo.CancelAgentTasks(ctx, workspaceID, id)
}

type MCPServerService struct {
	repo Repo

	Egress *egressx.Guard
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
	out, err := s.repo.UpsertAgentMCP(ctx, m)
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
	repo         Repo
	emit         Emitter
	notification NotificationWriter
	comments     CommentWriter
	chats        ChatWriter
	notifier     ApprovalNotifier
	engine       Engine
	secret       func(ctx context.Context, ws, credID string) (string, error)

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

func (s *SettlementService) SetRuntimeInstance(instanceID string) { s.instanceID = instanceID }

func (s *SettlementService) SetOutbox(rec OutboxRecorder, uow Within) {
	s.outbox, s.uow = rec, uow
}

func (s *SettlementService) complete(ctx context.Context, task Task, out EngineOutput, env events.Envelope) error {
	if err := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		if err := s.repo.CompleteRun(ctx, task.ID, out.Output, out.SessionRef, CurrentEngineTag(), out.Usage); err != nil {
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
	return nil
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

	if task.WorkItemID != "" && s.comments != nil {
		has, _ := s.comments.HasAgentComment(ctx, task.WorkItemID)
		if !has && strings.TrimSpace(out.Output) != "" {
			body := out.Output
			if len([]rune(body)) > 8000 {
				body = string([]rune(body)[:8000])
			}
			_ = s.comments.WriteAgentComment(ctx, task.WorkspaceID, task.WorkItemID, task.AgentID, body)
		}
	}

	if task.Context != nil {
		if sid, _ := task.Context["chat_session_id"].(string); sid != "" {
			s.emit.Emit(ctx, envelope(events.ChatDone, task.WorkspaceID, "", chatDonePayload(sid, task.ID, out.Output)))
		}
	}
	s.emit.Emit(ctx, envelope(events.TaskCompleted, task.WorkspaceID, "", taskResult(task, out.Output)))

	if s.notification != nil && task.OriginatorUserID != "" {
		_ = s.notification.WriteTaskCompleted(ctx, task.WorkspaceID, task.OriginatorUserID, task.WorkItemID, agent.Name, out.Output)
	}
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
		s.emit.Emit(ctx, envelope(events.ChatDone, workspaceID, byUser, chatDonePayload(sid, task.ID, out.Output)))
	}
	if task.WorkItemID != "" && s.comments != nil {
		has, _ := s.comments.HasAgentComment(ctx, task.WorkItemID)
		if !has && strings.TrimSpace(out.Output) != "" {
			_ = s.comments.WriteAgentComment(ctx, task.WorkspaceID, task.WorkItemID, task.AgentID, out.Output)
		}
	}
	s.emit.Emit(ctx, envelope(events.TaskCompleted, workspaceID, byUser, taskResult(task, out.Output)))
	return nil
}

func (s *SettlementService) SweepExpiredAwaiting(ctx context.Context) (int, error) {
	ids, err := s.repo.ListExpiredAwaiting(ctx, 24*60)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := s.repo.FailRun(ctx, id, "interrupt_timeout", "approval wait exceeded 24h"); err != nil {
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
	env := envelope(events.TaskFailed, task.WorkspaceID, "", taskResult(task, ""))
	if cerr := emitCommitted(ctx, s.emit, s.outbox, s.uow, &env, func(ctx context.Context) error {
		if _, serr := s.repo.FailRun(ctx, task.ID, reason, detail); serr != nil {
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

	s.promoteAfterTerminal(ctx, task)
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
	if err != nil || !ok || a.Archived {
		return "", errors.New("agent unavailable")
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
	if err != nil || !ok || a.Archived {
		return "", errors.New("agent unavailable")
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
	return TaskPayload{ID: t.ID, WorkspaceID: t.WorkspaceID, AgentID: t.AgentID, WorkItemID: t.WorkItemID, Status: t.Status, Result: result}
}

type TaskPayload struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	AgentID     string `json:"agent_id"`
	WorkItemID  string `json:"work_item_id,omitempty"`
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"`
}

func mustJSON(m map[string]any) []byte {
	b, _ := json.Marshal(m)
	return b
}

func chatDonePayload(sessionID, taskID, content string) wswire.ChatDonePayload {
	kind := "message"
	if strings.TrimSpace(content) == "" {
		kind, content = "no_response", ""
	}
	return wswire.ChatDonePayload{SessionID: sessionID, TaskID: taskID, Role: "assistant", Kind: kind, Content: content}
}
