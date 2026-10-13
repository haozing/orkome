package app

import (
	"context"
	contractevents "orkome/internal/contract/events"
	"regexp"
	"strings"
	"time"

	"orkome/internal/notification/domain"
)

type Item struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Body  string `json:"body"`

	WorkItemID string     `json:"work_item_id,omitempty"`
	ReadAt     *time.Time `json:"read_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Preference struct {
	MutedTypes       []string `json:"muted_types"`
	MutedWorkItemIDs []string `json:"muted_work_item_ids"`

	MutedConversationIDs []string `json:"muted_conversation_ids"`
}

type Repo interface {
	InsertNotification(ctx context.Context, workspaceID, recipientUserID string, workItemID *string, typ, title, body, dedupKey string) error
	ListNotification(ctx context.Context, workspaceID, userID string, archived bool, limit int, before string) ([]Item, string, error)
	UnreadCount(ctx context.Context, workspaceID, userID string) (int, error)
	MarkRead(ctx context.Context, workspaceID, userID, id string, read bool) error
	MarkAllRead(ctx context.Context, workspaceID, userID string) error
	Archive(ctx context.Context, workspaceID, userID, id string, archived bool) error
	ArchiveAll(ctx context.Context, workspaceID, userID string) error
	ArchiveAllRead(ctx context.Context, workspaceID, userID string) (int64, error)
	GetPreference(ctx context.Context, workspaceID, userID string) (Preference, error)
	UpsertPreference(ctx context.Context, workspaceID, userID string, p Preference) error

	Summary(ctx context.Context, workspaceID, userID string) (NotificationSummary, error)

	Facets(ctx context.Context, workspaceID, userID string) ([]Facet, error)
}

type SubscriberReader interface {
	ListSubscriberUserIDs(ctx context.Context, workItemID string) ([]string, error)
}

var mentionRe = regexp.MustCompile(`\[@([^\]]+)\]\(mention://(?:user|agent|squad|work_item|all)/([0-9a-fA-F-]{0,36})\)`)

type NotifyService struct {
	repo Repo
	subs SubscriberReader
}

func NewNotifyService(repo Repo, subs SubscriberReader) *NotifyService {
	return &NotifyService{repo: repo, subs: subs}
}

type EventIn struct {
	EventID     string
	WorkspaceID string
	Type        string
	WorkItemID  string

	// ActorUserID 事件起源者（Envelope.ActorUserID 透传）。非空时投影侧跳过
	// 本人——起源者不再收到"自己动作"的通知（FP-10 P2-2 自通知族）。
	ActorUserID string

	SessionID string
	Title     string
	Body      string

	MentionedUserIDs []string

	DirectUserIDs []string
}

func (s *NotifyService) Handle(ctx context.Context, in EventIn) error {
	recipients := map[string]bool{}

	if in.WorkItemID != "" && s.subs != nil {
		ids, err := s.subs.ListSubscriberUserIDs(ctx, in.WorkItemID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			recipients[id] = true
		}
	}

	for _, id := range in.MentionedUserIDs {
		if id != "" {
			recipients[id] = true
		}
	}

	for _, id := range in.DirectUserIDs {
		if id != "" {
			recipients[id] = true
		}
	}
	// FP-10：起源者自通知过滤——actor 本人无论经由订阅/提及/直发进入收件
	// 集合，一律跳过（评估/审批等直写路径不在本投影内，不受影响）。
	if in.ActorUserID != "" {
		delete(recipients, in.ActorUserID)
	}
	if len(recipients) == 0 {
		return nil
	}
	for uid := range recipients {

		pref, err := s.repo.GetPreference(ctx, in.WorkspaceID, uid)
		if err == nil && s.muted(pref, in) {
			continue
		}
		wi := ptrOrNil(in.WorkItemID)
		if err := s.repo.InsertNotification(ctx, in.WorkspaceID, uid, wi,
			in.Type, in.Title, in.Body,
			in.EventID+":"+uid); err != nil {
			return err
		}
	}
	return nil
}

func (s *NotifyService) muted(p Preference, in EventIn) bool {
	return domain.Muted(muteGroupsOf(p), domain.MuteTarget{
		Type: in.Type, WorkItemID: in.WorkItemID, SessionID: in.SessionID,
	})
}

func muteGroupsOf(p Preference) domain.MuteGroups {
	return domain.MuteGroups{
		Types: p.MutedTypes, WorkItemIDs: p.MutedWorkItemIDs,
		SessionIDs: p.MutedConversationIDs,
	}
}

func MutedFor(ctx context.Context, repo Repo, workspaceID, userID string, target domain.MuteTarget) bool {
	pref, err := repo.GetPreference(ctx, workspaceID, userID)
	if err != nil {
		return false
	}
	return domain.Muted(muteGroupsOf(pref), target)
}

func ParseMentionedUsers(content string) []string {
	var out []string
	for _, m := range mentionRe.FindAllStringSubmatch(content, -1) {
		out = append(out, strings.ToLower(m[2]))
	}
	return out
}

type NotificationService struct {
	repo   Repo
	frames *frameEmitter
}

func NewNotificationService(repo Repo) *NotificationService { return &NotificationService{repo: repo} }

func (s *NotificationService) List(ctx context.Context, ws, user string, archived bool, limit int, before string) ([]Item, string, error) {
	return s.repo.ListNotification(ctx, ws, user, archived, limit, before)
}

func (s *NotificationService) UnreadCount(ctx context.Context, ws, user string) (int, error) {
	return s.repo.UnreadCount(ctx, ws, user)
}

func (s *NotificationService) Summary(ctx context.Context, ws, user string) (NotificationSummary, error) {
	return s.repo.Summary(ctx, ws, user)
}

func (s *NotificationService) Facets(ctx context.Context, ws, user string) ([]Facet, error) {
	return s.repo.Facets(ctx, ws, user)
}

func (s *NotificationService) MarkRead(ctx context.Context, ws, user, id string, read bool) error {
	if err := s.repo.MarkRead(ctx, ws, user, id, read); err != nil {
		return err
	}
	s.emit(ctx, user, map[bool]string{true: contractevents.NotificationRead, false: contractevents.NotificationUnread}[read],
		map[string]any{"id": id})
	return nil
}

func (s *NotificationService) MarkAllRead(ctx context.Context, ws, user string) error {
	if err := s.repo.MarkAllRead(ctx, ws, user); err != nil {
		return err
	}
	s.emit(ctx, user, contractevents.NotificationBatchRead, map[string]any{})
	return nil
}

func (s *NotificationService) Archive(ctx context.Context, ws, user, id string, archived bool) error {
	if err := s.repo.Archive(ctx, ws, user, id, archived); err != nil {
		return err
	}
	s.emit(ctx, user, map[bool]string{true: contractevents.NotificationArchived, false: contractevents.NotificationUnarchived}[archived],
		map[string]any{"id": id})
	return nil
}

func (s *NotificationService) ArchiveAll(ctx context.Context, ws, user string) error {
	if err := s.repo.ArchiveAll(ctx, ws, user); err != nil {
		return err
	}
	s.emit(ctx, user, contractevents.NotificationBatchArchived, map[string]any{})
	return nil
}

func (s *NotificationService) ArchiveAllRead(ctx context.Context, ws, user string) (int, error) {
	n, err := s.repo.ArchiveAllRead(ctx, ws, user)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.emit(ctx, user, contractevents.NotificationBatchArchived, map[string]any{})
	}
	return int(n), nil
}

type PreferenceService struct{ repo Repo }

func NewPreferenceService(repo Repo) *PreferenceService { return &PreferenceService{repo: repo} }

func (s *PreferenceService) Get(ctx context.Context, ws, user string) (Preference, error) {
	return s.repo.GetPreference(ctx, ws, user)
}

func (s *PreferenceService) Upsert(ctx context.Context, ws, user string, p Preference) error {
	if err := validatePreference(p); err != nil {
		return err
	}
	return s.repo.UpsertPreference(ctx, ws, user, p)
}

const SessionMutePrefix = "session:"

func validatePreference(p Preference) error {
	if len(p.MutedTypes) > 50 || len(p.MutedWorkItemIDs) > 200 || len(p.MutedConversationIDs) > 200 {
		return errValidation("preference too large")
	}
	for _, t := range p.MutedTypes {
		if strings.HasPrefix(t, SessionMutePrefix) {
			return errValidation("muted type reserves session: prefix")
		}
	}
	return nil
}

type PreferencePatch struct {
	MutedTypes           *[]string `json:"muted_types,omitempty"`
	MutedWorkItemIDs     *[]string `json:"muted_work_item_ids,omitempty"`
	MutedConversationIDs *[]string `json:"muted_conversation_ids,omitempty"`
}

func (s *PreferenceService) Patch(ctx context.Context, ws, user string, patch PreferencePatch) (Preference, error) {
	cur, err := s.repo.GetPreference(ctx, ws, user)
	if err != nil {
		return Preference{}, err
	}
	if patch.MutedTypes != nil {
		cur.MutedTypes = *patch.MutedTypes
	}
	if patch.MutedWorkItemIDs != nil {
		cur.MutedWorkItemIDs = *patch.MutedWorkItemIDs
	}
	if patch.MutedConversationIDs != nil {
		cur.MutedConversationIDs = *patch.MutedConversationIDs
	}
	if err := validatePreference(cur); err != nil {
		return Preference{}, err
	}
	if err := s.repo.UpsertPreference(ctx, ws, user, cur); err != nil {
		return Preference{}, err
	}
	return cur, nil
}

type NotificationSummary struct {
	Unread   int `json:"unread"`
	TodayNew int `json:"today_new"`
}

type Facet struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type domainErr struct{ code, msg string }

func (e *domainErr) Error() string { return e.msg }
func (e *domainErr) Code() string  { return e.code }
func errValidation(m string) error { return &domainErr{"E_VALIDATION", m} }

type UserFrames interface {
	Push(ctx context.Context, userID, frameType string, payload map[string]any)
}

type frameEmitter struct{ push UserFrames }

func (s *NotificationService) SetUserFrames(f UserFrames) { s.frames = &frameEmitter{push: f} }

func (s *NotificationService) emit(ctx context.Context, userID, frameType string, payload map[string]any) {
	if s.frames != nil && s.frames.push != nil {
		s.frames.push.Push(ctx, userID, frameType, payload)
	}
}
