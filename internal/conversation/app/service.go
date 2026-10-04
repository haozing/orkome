package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	contractevents "orkome/internal/contract/events"
	"orkome/internal/conversation/domain"
)

type Session struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	AgentID     string    `json:"agent_id"`
	CreatorID   string    `json:"creator_id"`
	Title       string    `json:"title"`
	SessionRef  string    `json:"session_ref,omitempty"`
	Status      string    `json:"status"`
	Pinned      bool      `json:"pinned"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const (
	MaxSessionTitleRunes = 200

	EventChatSessionDeleted = contractevents.ChatSessionDeleted

	CodeSessionInUse = "E_SESSION_IN_USE"
)

type Message struct {
	ID        string           `json:"id"`
	SessionID string           `json:"session_id"`
	Role      string           `json:"role"`
	Kind      string           `json:"kind"`
	Content   string           `json:"content"`
	TaskID    string           `json:"task_id,omitempty"`
	Parts     []map[string]any `json:"parts,omitempty"`
	Seq       int64            `json:"seq"`
	CreatedAt time.Time        `json:"created_at"`
}

type Repo interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
	CreateSession(ctx context.Context, s Session) (Session, error)
	GetSession(ctx context.Context, workspaceID, id string) (Session, bool, error)
	ListSessions(ctx context.Context, workspaceID, userID string) ([]Session, error)
	ArchiveSession(ctx context.Context, workspaceID, id string, archived bool) error

	UpdateSessionTitle(ctx context.Context, workspaceID, id, title string) (Session, bool, error)

	SetSessionPin(ctx context.Context, workspaceID, id string, pinned bool) (Session, bool, error)

	DeleteSessionCascade(ctx context.Context, workspaceID, id string) (bool, error)
	NextSeq(ctx context.Context, sessionID string) (int64, error)
	InsertMessage(ctx context.Context, m Message) (Message, error)
	InsertMessageParts(ctx context.Context, m Message, parts []map[string]any) (Message, error)
	ListMessages(ctx context.Context, sessionID string, limit int, beforeSeq int64) ([]Message, error)
	CountAssistant(ctx context.Context, sessionID string) (int, error)
	DeleteUserMessagesSince(ctx context.Context, sessionID string, sinceSeq int64) error
	UpsertDraftRestore(ctx context.Context, workspaceID, sessionID, userID, content string) error
	FinalizeDraft(ctx context.Context, sessionID string, outcome string) error

	GetPendingDraft(ctx context.Context, sessionID, userID string) (content string, ok bool, err error)
	UpdateResumePointer(ctx context.Context, sessionID, ref string) error

	ClearResumePointer(ctx context.Context, sessionID string) error
	SetUnread(ctx context.Context, sessionID string, since time.Time) error
	MarkRead(ctx context.Context, sessionID string) error

	AppendMessageContent(ctx context.Context, messageID, appendage, dedupMarker string) (appended bool, err error)

	GetMessage(ctx context.Context, id string) (bool, error)

	ArchiveSessionsByUser(ctx context.Context, workspaceID, userID string) (int64, error)

	MarkCancelPending(ctx context.Context, sessionID, taskID, userID string) error

	GetCancelPending(ctx context.Context, sessionID, taskID string) (initiatorUserID string, ok bool, err error)

	ClearCancelPending(ctx context.Context, sessionID, taskID string) error

	HasStoppedMessage(ctx context.Context, sessionID, taskID string) (bool, error)
}

type ChatEnqueuer interface {
	EnqueueChat(ctx context.Context, workspaceID, sessionID, agentID, userID, content string) (taskID string, err error)
}

type EnqueueChatTxFunc func(ctx context.Context, workspaceID, sessionID, agentID, userID, content string) (taskID string, err error)

type AgentStartersFunc func(ctx context.Context, workspaceID, agentID string) (labels []string, err error)

type TaskCanceller interface {
	CancelChatTask(ctx context.Context, workspaceID, sessionID string) (status string, taskID string, ok bool, err error)
}

type ChatTaskOps interface {
	LatestChatTask(ctx context.Context, workspaceID, sessionID string) (status, taskID string, ok bool, err error)

	CancelQueued(ctx context.Context, workspaceID, taskID string) error

	SupplementRunning(ctx context.Context, workspaceID, taskID, content, userID string) (ok bool, err error)
}

type PendingChatTask struct {
	TaskID    string    `json:"task_id"`
	SessionID string    `json:"session_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type PendingChatReader interface {
	ListPendingChatTasks(ctx context.Context, workspaceID, userID string) ([]PendingChatTask, error)
	HasPendingChatTask(ctx context.Context, workspaceID, userID string) (bool, error)
}

type TaskPrioritizer interface {
	PrioritizeChatTask(ctx context.Context, workspaceID, sessionID, taskID string) (priority int, ok bool, err error)
}

type HistoryMessage struct {
	ID     string `json:"id"`
	Role   string `json:"role"`
	Author string `json:"author"`
	Text   string `json:"text"`
	TS     string `json:"ts"`
	Seq    int64  `json:"seq"`
}

type HistoryPage struct {
	ChannelType string           `json:"channel_type"`
	ThreadID    string           `json:"thread_id,omitempty"`
	Messages    []HistoryMessage `json:"messages"`
	NextCursor  string           `json:"next_cursor,omitempty"`
	Note        string           `json:"note,omitempty"`
}

type ChannelHistoryReader interface {
	ChannelOverview(ctx context.Context, chatSessionID string, limit int, before string) (*HistoryPage, error)
	Thread(ctx context.Context, chatSessionID, threadID string, limit int, before string) (*HistoryPage, error)
}

type ChatService struct {
	repo Repo
	enq  ChatEnqueuer

	enqTx   EnqueueChatTxFunc
	tasks   ChatTaskOps
	pending PendingChatReader
	prio    TaskPrioritizer
	chanHis ChannelHistoryReader

	agentStarters AgentStartersFunc

	emitSession func(ctx context.Context, event, workspaceID, sessionID string)
}

func NewChatService(repo Repo, enq ChatEnqueuer) *ChatService {
	return &ChatService{repo: repo, enq: enq}
}

func (s *ChatService) SetEnqueueChatTx(f EnqueueChatTxFunc) { s.enqTx = f }

func (s *ChatService) SetChatTaskOps(o ChatTaskOps) { s.tasks = o }

func (s *ChatService) SetPendingChatReader(p PendingChatReader) { s.pending = p }

func (s *ChatService) SetTaskPrioritizer(p TaskPrioritizer) { s.prio = p }

func (s *ChatService) SetChannelHistoryReader(h ChannelHistoryReader) { s.chanHis = h }

func (s *ChatService) SetAgentStarters(f AgentStartersFunc) { s.agentStarters = f }

func (s *ChatService) SetSessionEventSink(f func(ctx context.Context, event, workspaceID, sessionID string)) {
	s.emitSession = f
}

type MediaRef struct {
	Kind       string
	StorageKey string
	StorageURL string
	Filename   string
}

const mediaProvenancePrefix = "<!-- channel-media: "

func MediaMaterializeMarkdown(ref MediaRef) string {
	if ref.StorageURL == "" {
		return ""
	}
	label := ref.Filename
	if label == "" {
		label = ref.Kind
	}
	label = strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]").Replace(label)
	link := "[" + label + "](" + ref.StorageURL + ")"
	if ref.Kind == "image" {
		link = "![](" + ref.StorageURL + ")"
	}
	return link + "\n" + mediaProvenancePrefix + ref.StorageKey + " -->"
}

func (s *ChatService) MaterializeMedia(ctx context.Context, messageID string, refs []MediaRef) error {
	var b strings.Builder
	marker := ""
	for _, ref := range refs {
		block := MediaMaterializeMarkdown(ref)
		if block == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(block)
		if marker == "" {
			marker = ref.StorageKey
		}
	}
	if b.Len() == 0 {
		return nil
	}
	_, err := s.repo.AppendMessageContent(ctx, messageID, "\n\n"+b.String(), marker)
	return err
}

func (s *ChatService) CreateSession(ctx context.Context, workspaceID, agentID, userID, title string) (*Session, error) {
	if title == "" {
		title = "new session"
	}
	out, err := s.repo.CreateSession(ctx, Session{
		WorkspaceID: workspaceID, AgentID: agentID, CreatorID: userID,
		Title: title, Status: "active",
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *ChatService) ListSessions(ctx context.Context, workspaceID, userID string) ([]Session, error) {
	return s.repo.ListSessions(ctx, workspaceID, userID)
}

func (s *ChatService) GetSession(ctx context.Context, workspaceID, sessionID string) (*Session, error) {
	sess, ok, err := s.repo.GetSession(ctx, workspaceID, sessionID)
	if err != nil || !ok {
		return nil, errNotFound("session not found")
	}
	return &sess, nil
}

func (s *ChatService) ListMessages(ctx context.Context, sessionID string, limit int, beforeSeq int64) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	desc, err := s.repo.ListMessages(ctx, sessionID, limit, beforeSeq)
	if err != nil {
		return nil, err
	}
	out := make([]Message, len(desc))
	for i := range desc {
		out[len(desc)-1-i] = desc[i]
	}
	return out, nil
}

func (s *ChatService) History(ctx context.Context, workspaceID, sessionID, userID string, limit int, before string) (*HistoryPage, error) {
	if err := s.mustOwnSession(ctx, workspaceID, sessionID, userID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if s.chanHis != nil {
		return s.chanHis.ChannelOverview(ctx, sessionID, limit, before)
	}
	beforeSeq, err := parseHistoryCursor(before)
	if err != nil {
		return nil, errValidation("invalid before cursor")
	}
	msgs, err := s.repo.ListMessages(ctx, sessionID, limit, beforeSeq)
	if err != nil {
		return nil, err
	}
	return transcriptPage(sessionID, msgs, limit), nil
}

func (s *ChatService) Thread(ctx context.Context, workspaceID, sessionID, userID, threadID string, limit int, before string) (*HistoryPage, error) {
	if err := s.mustOwnSession(ctx, workspaceID, sessionID, userID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if s.chanHis != nil {
		return s.chanHis.Thread(ctx, sessionID, threadID, limit, before)
	}
	if threadID != "" && threadID != sessionID {
		return nil, errNotFound("thread not found")
	}
	beforeSeq, err := parseHistoryCursor(before)
	if err != nil {
		return nil, errValidation("invalid before cursor")
	}
	msgs, err := s.repo.ListMessages(ctx, sessionID, limit, beforeSeq)
	if err != nil {
		return nil, err
	}
	return transcriptPage(sessionID, msgs, limit), nil
}

func parseHistoryCursor(before string) (int64, error) {
	if before == "" {
		return 1 << 62, nil
	}
	var seq int64
	if _, err := fmt.Sscanf(before, "%d", &seq); err != nil || seq <= 0 {
		return 0, errValidation("invalid before cursor")
	}
	return seq, nil
}

func transcriptPage(sessionID string, msgs []Message, limit int) *HistoryPage {
	page := &HistoryPage{ChannelType: "transcript", ThreadID: sessionID, Messages: []HistoryMessage{}}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		author := "User"
		if m.Role == "assistant" {
			author = "Bot"
		}
		page.Messages = append(page.Messages, HistoryMessage{
			ID: m.ID, Role: m.Role, Author: author, Text: m.Content,
			TS: m.CreatedAt.UTC().Format(time.RFC3339Nano), Seq: m.Seq,
		})
	}
	if len(msgs) == limit && len(msgs) > 0 {
		page.NextCursor = fmt.Sprintf("%d", msgs[len(msgs)-1].Seq)
	}
	if len(page.Messages) == 0 {
		page.Note = "no readable history for this session"
	}
	return page
}

func (s *ChatService) Archive(ctx context.Context, workspaceID, sessionID string, archived bool) error {
	return s.repo.ArchiveSession(ctx, workspaceID, sessionID, archived)
}

func (s *ChatService) UpdateSession(ctx context.Context, workspaceID, sessionID string, title *string) (*Session, error) {
	if title == nil {
		return s.GetSession(ctx, workspaceID, sessionID)
	}
	t := strings.TrimSpace(*title)
	if t == "" || len([]rune(t)) > MaxSessionTitleRunes {
		return nil, errValidation("title required, ≤200 runes")
	}
	out, ok, err := s.repo.UpdateSessionTitle(ctx, workspaceID, sessionID, t)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotFound("session not found")
	}
	return &out, nil
}

func (s *ChatService) SetPinned(ctx context.Context, workspaceID, sessionID string, pinned bool) (*Session, error) {
	out, ok, err := s.repo.SetSessionPin(ctx, workspaceID, sessionID, pinned)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotFound("session not found")
	}
	return &out, nil
}

func (s *ChatService) DeleteSession(ctx context.Context, workspaceID, sessionID string) error {
	ok, err := s.repo.DeleteSessionCascade(ctx, workspaceID, sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound("session not found")
	}
	if s.emitSession != nil {
		s.emitSession(ctx, EventChatSessionDeleted, workspaceID, sessionID)
	}
	return nil
}

func (s *ChatService) MarkRead(ctx context.Context, sessionID string) error {
	return s.repo.MarkRead(ctx, sessionID)
}

func (s *ChatService) DiscardDraft(ctx context.Context, workspaceID, sessionID, userID string) error {
	if err := s.mustOwnSession(ctx, workspaceID, sessionID, userID); err != nil {
		return err
	}
	return s.repo.FinalizeDraft(ctx, sessionID, "discarded")
}

func (s *ChatService) SendMessage(ctx context.Context, workspaceID, sessionID, userID, content string) (*Message, string, error) {
	content = strings.TrimSpace(content)
	if content == "" || len([]rune(content)) > 50000 {
		return nil, "", errValidation("invalid content")
	}
	sess, ok, err := s.repo.GetSession(ctx, workspaceID, sessionID)
	if err != nil || !ok {
		return nil, "", errNotFound("session not found")
	}
	if sess.Status != "active" {
		return nil, "", errValidation("session not active")
	}

	steerTaskID := ""
	if s.tasks != nil {
		status, taskID, ok, err := s.tasks.LatestChatTask(ctx, workspaceID, sessionID)
		if err != nil {
			return nil, "", err
		}
		if ok {
			switch status {
			case "queued", "dispatched":

				if err := s.tasks.CancelQueued(ctx, workspaceID, taskID); err != nil {
					return nil, "", err
				}
			case "running":
				steerTaskID = taskID
			}
		}
	}

	introContent, err := s.prepareAgentIntro(ctx, sess, sessionID)
	if err != nil {
		return nil, "", err
	}

	if s.enqTx != nil {
		var (
			m      Message
			taskID string
		)
		if err := s.repo.Within(ctx, func(ctx context.Context) error {

			if err := s.insertAgentIntro(ctx, sessionID, introContent); err != nil {
				return err
			}
			seq, err := s.repo.NextSeq(ctx, sessionID)
			if err != nil {
				return err
			}
			if m, err = s.repo.InsertMessage(ctx, Message{
				SessionID: sessionID, Role: "user",
				Kind: "message", Content: content, Seq: seq,
			}); err != nil {
				return err
			}

			if steerTaskID != "" {
				steered, err := s.tasks.SupplementRunning(ctx, workspaceID, steerTaskID, content, userID)
				if err != nil {
					return err
				}
				if steered {
					taskID = steerTaskID
				}

			}
			if taskID == "" {
				if taskID, err = s.enqTx(ctx, workspaceID, sessionID, sess.AgentID, userID, content); err != nil {
					return err
				}
			}
			return s.repo.SetUnread(ctx, sessionID, time.Now())
		}); err != nil {
			return nil, "", err
		}
		return &m, taskID, nil
	}

	if err := s.insertAgentIntro(ctx, sessionID, introContent); err != nil {
		return nil, "", err
	}
	seq, err := s.repo.NextSeq(ctx, sessionID)
	if err != nil {
		return nil, "", err
	}
	m, err := s.repo.InsertMessage(ctx, Message{
		SessionID: sessionID, Role: "user",
		Kind: "message", Content: content, Seq: seq,
	})
	if err != nil {
		return nil, "", err
	}
	var taskID string
	if steerTaskID != "" {
		steered, err := s.tasks.SupplementRunning(ctx, workspaceID, steerTaskID, content, userID)
		if err != nil {
			return nil, "", err
		}
		if steered {
			taskID = steerTaskID
		}

	}
	if taskID == "" {
		taskID, err = s.enq.EnqueueChat(ctx, workspaceID, sessionID, sess.AgentID, userID, content)
		if err != nil {
			return nil, "", err
		}
	}
	_ = s.repo.SetUnread(ctx, sessionID, time.Now())
	return &m, taskID, nil
}

func (s *ChatService) prepareAgentIntro(ctx context.Context, sess Session, sessionID string) (string, error) {
	if s.agentStarters == nil {
		return "", nil
	}
	msgs, err := s.repo.ListMessages(ctx, sessionID, 1, 1<<62)
	if err != nil {
		return "", err
	}
	if len(msgs) > 0 {
		return "", nil
	}
	labels, err := s.agentStarters(ctx, sess.WorkspaceID, sess.AgentID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("- ")
		b.WriteString(label)
	}
	return b.String(), nil
}

func (s *ChatService) insertAgentIntro(ctx context.Context, sessionID, content string) error {
	if content == "" {
		return nil
	}
	seq, err := s.repo.NextSeq(ctx, sessionID)
	if err != nil {
		return err
	}
	_, err = s.repo.InsertMessage(ctx, Message{
		SessionID: sessionID, Role: "assistant",
		Kind: domain.KindAgentIntro, Content: content, Seq: seq,
	})
	return err
}

func (s *ChatService) Cancel(ctx context.Context, workspaceID, sessionID, userID string, tc TaskCanceller) (outcome string, err error) {
	status, taskID, ok, err := tc.CancelChatTask(ctx, workspaceID, sessionID)
	if err != nil || !ok {
		if err == nil && !ok {
			return "none", nil
		}
		return "", err
	}
	if status == "cancelled" {

		return s.Finalize(ctx, workspaceID, sessionID, userID, taskID)
	}

	if err := s.repo.MarkCancelPending(ctx, sessionID, taskID, userID); err != nil {
		return "", err
	}
	return "stop_requested", nil
}

func (s *ChatService) Finalize(ctx context.Context, workspaceID, sessionID, userID, taskID string) (string, error) {
	n, err := s.repo.CountAssistant(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if domain.FinalizeOutcome(n) == domain.OutcomeRestored {

		msgs, err := s.repo.ListMessages(ctx, sessionID, 1, 1<<62)
		if err != nil {
			return "", err
		}
		var lastUser *Message
		for i := range msgs {
			if msgs[i].Role == "user" {
				m := msgs[i]
				lastUser = &m
			}
		}
		if lastUser != nil {
			if err := s.repo.UpsertDraftRestore(ctx, workspaceID, sessionID, userID, lastUser.Content); err != nil {
				return "", err
			}
			if err := s.repo.DeleteUserMessagesSince(ctx, sessionID, lastUser.Seq); err != nil {
				return "", err
			}
		}

		return domain.OutcomeRestored, nil
	}

	stopped, err := s.repo.HasStoppedMessage(ctx, sessionID, taskID)
	if err != nil {
		return "", err
	}
	if !stopped {
		seq, err := s.repo.NextSeq(ctx, sessionID)
		if err != nil {
			return "", err
		}
		_, err = s.repo.InsertMessage(ctx, Message{
			SessionID: sessionID, Role: "assistant",
			Kind: "system_stopped", Content: "stopped", TaskID: taskID, Seq: seq,
		})
		if err != nil {
			return "", err
		}
	}
	_ = s.repo.FinalizeDraft(ctx, sessionID, "discarded")
	return domain.OutcomeStopped, nil
}

func (s *ChatService) HandleChatTaskTerminal(ctx context.Context, workspaceID, sessionID, initiatorUserID, taskID, terminalStatus string) (outcome string, err error) {
	if !domain.ShouldVerdictTerminal(terminalStatus) {
		return domain.OutcomeNone, nil
	}
	initiator, ok, err := s.repo.GetCancelPending(ctx, sessionID, taskID)
	if err != nil || !ok {
		return domain.OutcomeNone, err
	}
	if initiator == "" {
		initiator = initiatorUserID
	}
	outcome, err = s.Finalize(ctx, workspaceID, sessionID, initiator, taskID)
	if err != nil {
		return "", err
	}
	if err := s.repo.ClearCancelPending(ctx, sessionID, taskID); err != nil {
		return "", err
	}
	return outcome, nil
}

func (s *ChatService) RestoreDraft(ctx context.Context, workspaceID, sessionID, userID string) (string, bool, error) {
	if err := s.mustOwnSession(ctx, workspaceID, sessionID, userID); err != nil {
		return "", false, err
	}
	return s.repo.GetPendingDraft(ctx, sessionID, userID)
}

func (s *ChatService) mustOwnSession(ctx context.Context, workspaceID, sessionID, userID string) error {
	sess, ok, err := s.repo.GetSession(ctx, workspaceID, sessionID)
	if err != nil {
		return err
	}
	if !ok || sess.CreatorID != userID {
		return errNotFound("session not found")
	}
	return nil
}

func (s *ChatService) ListPendingTasks(ctx context.Context, workspaceID, userID string) ([]PendingChatTask, error) {
	if s.pending == nil {
		return []PendingChatTask{}, nil
	}
	return s.pending.ListPendingChatTasks(ctx, workspaceID, userID)
}

func (s *ChatService) HasPendingTask(ctx context.Context, workspaceID, userID string) (bool, error) {
	if s.pending == nil {
		return false, nil
	}
	return s.pending.HasPendingChatTask(ctx, workspaceID, userID)
}

func (s *ChatService) Prioritize(ctx context.Context, workspaceID, sessionID, taskID, userID string) (int, error) {
	if err := s.mustOwnSession(ctx, workspaceID, sessionID, userID); err != nil {
		return 0, err
	}
	if s.prio == nil {
		return 0, errNotFound("queued task not found")
	}
	p, ok, err := s.prio.PrioritizeChatTask(ctx, workspaceID, sessionID, taskID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, errNotFound("queued task not found")
	}
	return p, nil
}

type PointersService struct{ repo Repo }

func NewPointersService(repo Repo) *PointersService { return &PointersService{repo: repo} }

func (p *PointersService) UpdateResumePointer(ctx context.Context, sessionID, ref string) error {
	return p.repo.UpdateResumePointer(ctx, sessionID, ref)
}

func (p *PointersService) ClearResumePointer(ctx context.Context, sessionID string) error {
	return p.repo.ClearResumePointer(ctx, sessionID)
}

func (p *PointersService) AppendAssistantParts(ctx context.Context, sessionID string, taskID, content, kind string, parts []map[string]any) error {
	if kind == "" {
		kind = "message"
	}
	seq, err := p.repo.NextSeq(ctx, sessionID)
	if err != nil {
		return err
	}
	_, err = p.repo.InsertMessageParts(ctx, Message{
		SessionID: sessionID, Role: "assistant", Kind: kind, Content: content,
		TaskID: taskID, Seq: seq,
	}, parts)
	return err
}

func (p *PointersService) AppendAssistantMessage(ctx context.Context, sessionID string, taskID, content, kind string) error {
	if kind == "" {
		kind = "message"
	}
	if strings.TrimSpace(content) == "" && kind == "message" {
		kind = "no_response"
		content = ""
	}
	seq, err := p.repo.NextSeq(ctx, sessionID)
	if err != nil {
		return err
	}
	_, err = p.repo.InsertMessage(ctx, Message{
		SessionID: sessionID, Role: "assistant", Kind: kind, Content: content,
		TaskID: taskID, Seq: seq,
	})
	return err
}

type domainErr struct{ code, msg string }

func (e *domainErr) Error() string { return e.msg }
func (e *domainErr) Code() string  { return e.code }
func errNotFound(m string) error   { return &domainErr{"E_NOT_FOUND", m} }
func errValidation(m string) error { return &domainErr{"E_VALIDATION", m} }

func errUnavailable(m string) error { return &domainErr{CodeQuickActionUnavailable, m} }

func (s *ChatService) MessageExists(ctx context.Context, id string) (bool, error) {
	return s.repo.GetMessage(ctx, id)
}

func (s *ChatService) ArchiveSessionsByUser(ctx context.Context, workspaceID, userID string) (int64, error) {
	return s.repo.ArchiveSessionsByUser(ctx, workspaceID, userID)
}

func SessionInUseError(m string) error { return &domainErr{CodeSessionInUse, m} }
