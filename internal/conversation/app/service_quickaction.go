package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"orkome/internal/contract/apierr"
)

const (
	MaxQuickActions    = 3
	MaxQuickLabelRunes = 80
)

const CodeQuickActionUnavailable = "E_SERVICE_UNAVAILABLE"

type QuickAction struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	Label       string    `json:"label"`
	Prompt      string    `json:"prompt"`
	IsPrivate   bool      `json:"is_private"`
	CreatedAt   time.Time `json:"created_at"`
}

type QuickActionRepo interface {
	InsertQuickAction(ctx context.Context, q QuickAction) (QuickAction, error)
	ListQuickActions(ctx context.Context, workspaceID, userID string) ([]QuickAction, error)
	UpdateQuickAction(ctx context.Context, workspaceID, id, userID, label, prompt string) (QuickAction, bool, error)
	DeleteQuickAction(ctx context.Context, workspaceID, id, userID string) error
}

type QuickActionService struct {
	repo QuickActionRepo

	enqueueWorkItem EnqueueWorkItemTaskFunc

	resolveTargetAgent ResolveTargetAgentFunc
}

func NewQuickActionService(repo QuickActionRepo) *QuickActionService {
	return &QuickActionService{repo: repo}
}

type EnqueueWorkItemTaskFunc func(ctx context.Context, workspaceID, workItemID, agentID, userID, prompt string) (taskID string, err error)

type ResolveTargetAgentFunc func(ctx context.Context, workspaceID, workItemID string) (agentID string, err error)

func (s *QuickActionService) SetEnqueueWorkItemTask(f EnqueueWorkItemTaskFunc) { s.enqueueWorkItem = f }

func (s *QuickActionService) SetResolveTargetAgent(f ResolveTargetAgentFunc) {
	s.resolveTargetAgent = f
}

func (s *QuickActionService) Run(ctx context.Context, workspaceID, workItemID, id, userID string) (string, error) {
	q, err := s.Get(ctx, workspaceID, id, userID)
	if err != nil {
		return "", err
	}
	if s.resolveTargetAgent == nil {
		return "", errUnavailable("quick action run pending wiring: ResolveTargetAgent port not wired")
	}
	agentID, err := s.resolveTargetAgent(ctx, workspaceID, workItemID)
	if err != nil {
		return "", err
	}
	if agentID == "" {
		return "", errValidation("work item has no agent assignee")
	}
	if s.enqueueWorkItem == nil {
		return "", errUnavailable("quick action run pending wiring: EnqueueWorkItemTask port not wired")
	}
	return s.enqueueWorkItem(ctx, workspaceID, workItemID, agentID, userID, q.Prompt)
}

func (s *QuickActionService) Render(ctx context.Context, workspaceID, id, userID string) (*QuickAction, error) {
	return s.Get(ctx, workspaceID, id, userID)
}

func (s *QuickActionService) Create(ctx context.Context, q QuickAction) (*QuickAction, error) {
	if err := validateQuick(q.Label, q.Prompt); err != nil {
		return nil, err
	}
	q.IsPrivate = true
	out, err := s.repo.InsertQuickAction(ctx, q)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *QuickActionService) Get(ctx context.Context, workspaceID, id, userID string) (*QuickAction, error) {
	list, err := s.repo.ListQuickActions(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, apierr.New(apierr.NotFound, "quick action not found")
}

func (s *QuickActionService) List(ctx context.Context, workspaceID, userID string) ([]QuickAction, error) {
	return s.repo.ListQuickActions(ctx, workspaceID, userID)
}

func (s *QuickActionService) Update(ctx context.Context, workspaceID, id, userID, label, prompt string) (*QuickAction, error) {
	if err := validateQuick(label, prompt); err != nil {
		return nil, err
	}
	out, ok, err := s.repo.UpdateQuickAction(ctx, workspaceID, id, userID, label, prompt)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apierr.New(apierr.NotFound, "quick action not found")
	}
	return &out, nil
}

func (s *QuickActionService) Delete(ctx context.Context, workspaceID, id, userID string) error {
	return s.repo.DeleteQuickAction(ctx, workspaceID, id, userID)
}

func validateQuick(label, prompt string) error {
	label = strings.TrimSpace(label)
	if label == "" || len([]rune(label)) > MaxQuickLabelRunes {
		return apierr.NewValidation("label required, ≤80 runes")
	}
	if strings.TrimSpace(prompt) == "" {
		return apierr.NewValidation("prompt required")
	}
	return nil
}

func ParseSuggestions(raw string) []QuickAction {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "["); i > 0 {
		raw = raw[i:]
	}
	if j := strings.LastIndex(raw, "]"); j >= 0 && j < len(raw)-1 {
		raw = raw[:j+1]
	}
	var out []struct {
		Label  string `json:"label"`
		Prompt string `json:"prompt"`
	}
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	var qs []QuickAction
	for _, s := range out {
		if len(qs) >= MaxQuickActions {
			break
		}
		if err := validateQuick(s.Label, s.Prompt); err != nil {
			continue
		}
		qs = append(qs, QuickAction{Label: s.Label, Prompt: s.Prompt})
	}
	return qs
}

type QuickActionGenerator struct {
	Engine interface {
		Generate(ctx context.Context, agent AgentLite, credentialSecret, system, input string) (string, UsageLite, error)
	}

	TaskLookup func(ctx context.Context, taskID string) (wsID, agentID, creatorID string, ok bool)

	AgentLookup func(ctx context.Context, wsID, agentID string) (AgentLite, bool)

	AuxUsage func(ctx context.Context, wsID string, u UsageLite) error

	Emit func(ctx context.Context, wsID, sessionID string, created []QuickAction)

	Log  func(msg string, args ...any)
	repo QuickActionRepo
}

type AgentLite struct {
	WorkspaceID   string
	ID            string
	Description   string
	RuntimeConfig map[string]any
}

type UsageLite struct {
	Provider         string
	Model            string
	PromptTokens     int64
	CompletionTokens int64
}

const SystemPrompt = "You are a quick-action suggester. Based on the assistant's latest reply, propose up to 3 follow-up actions the user might want." +
	"Output only a JSON array with elements like {\"label\":\"short label (≤80 chars)\",\"prompt\":\"executable instruction\"} and nothing else."

func (g *QuickActionGenerator) OnChatDone(ctx context.Context, sessionID, taskID, content string) {
	defer func() {
		if r := recover(); r != nil && g.Log != nil {
			g.Log("quick action generator panic", "panic", r)
		}
	}()
	if content == "" || taskID == "" {
		return
	}
	wsID, agentID, creator, ok := g.TaskLookup(ctx, taskID)
	if !ok || creator == "" {
		return
	}
	agent, ok := g.AgentLookup(ctx, wsID, agentID)
	if !ok {
		return
	}
	out, usage, err := g.Engine.Generate(ctx, agent, "", SystemPrompt, "Assistant reply:\n"+content+"\n\nPlease output JSON suggestions.")
	if err != nil {
		if g.Log != nil {
			g.Log("quick action generate failed", "err", err)
		}
		return
	}
	if g.AuxUsage != nil {
		_ = g.AuxUsage(ctx, wsID, usage)
	}
	var created []QuickAction
	for _, s := range ParseSuggestions(out) {
		row, err := g.repo.InsertQuickAction(ctx, QuickAction{
			WorkspaceID: wsID, UserID: creator, Label: s.Label, Prompt: s.Prompt, IsPrivate: true,
		})
		if err != nil {
			continue
		}
		created = append(created, row)
	}
	if len(created) > 0 && g.Emit != nil {
		g.Emit(ctx, wsID, sessionID, created)
	}
}

func NewQuickActionGenerator(repo QuickActionRepo) *QuickActionGenerator {
	return &QuickActionGenerator{repo: repo}
}
