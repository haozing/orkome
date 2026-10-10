package app

import (
	"context"
	"time"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/dispatchreason"
	"orkome/internal/contract/events"
	"github.com/haozing/ploykit/platform/redactx"
	"orkome/internal/work/domain"
)

type TriggerEngine interface {
	OnComment(ctx context.Context, in TriggerInput, commentID string) (result, reason string, err error)
	Preview(ctx context.Context, in TriggerInput) (triggers, suppressions []PreviewHit, err error)
}

type PreviewHit struct {
	AgentID string `json:"agent_id"`
	Reason  string `json:"reason"`
}

type TriggerInput struct {
	WorkspaceID      string
	WorkItemID       string
	AuthorType       string
	AuthorID         string
	Content          string
	ParentAuthorType string
	ParentAuthorID   string
	RootAuthorType   string
	RootAuthorID     string
	AssigneeType     string
	AssigneeID       string

	TriageState *string
}

var triggerEngine TriggerEngine

func SetTriggerEngine(te TriggerEngine) { triggerEngine = te }

var assigneeReader interface {
	WorkItemAssignee(ctx context.Context, workItemID string) (string, string)
}

func SetAssigneeReader(r interface {
	WorkItemAssignee(ctx context.Context, workItemID string) (string, string)
}) {
	assigneeReader = r
}

var commentTaskCanceller func(ctx context.Context, workspaceID, commentID string) (int, error)

func SetCommentTaskCanceller(f func(ctx context.Context, workspaceID, commentID string) (int, error)) {
	commentTaskCanceller = f
}

func cancelTasksOfComment(ctx context.Context, ws, commentID string) {
	if commentTaskCanceller != nil {
		_, _ = commentTaskCanceller(ctx, ws, commentID)
	}
}

func WorkItemAssigneeOf(ctx context.Context, workItemID string) (string, string) {
	if assigneeReader == nil {
		return "none", ""
	}
	return assigneeReader.WorkItemAssignee(ctx, workItemID)
}

func TriggerEngineWired() bool { return triggerEngine != nil }

func PreviewTrigger(ctx context.Context, in TriggerInput) ([]PreviewHit, []PreviewHit, error) {
	if triggerEngine == nil {
		return nil, nil, nil
	}
	if domain.TriageTriggerBlocked(domain.TriageGateFacts{
		TriageState:      in.TriageState,
		Mentions:         domain.ParseMentions(in.Content),
		AuthorType:       in.AuthorType,
		ParentAuthorType: in.ParentAuthorType,
	}) {
		var supps []PreviewHit
		if in.AssigneeType == "agent" && in.AssigneeID != "" {
			supps = append(supps, PreviewHit{AgentID: in.AssigneeID, Reason: dispatchreason.WorkItemInTriage})
		}
		return nil, supps, nil
	}
	return triggerEngine.Preview(ctx, in)
}

type CommentService struct {
	repo Repo
	emit Emitter
	now  Clock

	outbox OutboxRecorder
	uow    Within
}

func NewCommentService(repo Repo, emit Emitter, now Clock) *CommentService {
	if now == nil {
		now = time.Now
	}
	return &CommentService{repo: repo, emit: emit, now: now}
}

func (s *CommentService) SetOutbox(rec OutboxRecorder, uow Within) {
	s.outbox, s.uow = rec, uow
}

func (s *CommentService) emitCommitted(ctx context.Context, env *events.Envelope, write func(context.Context) error) error {
	return emitCommitted(ctx, s.emit, s.outbox, s.uow, env, write)
}

type AdmissionResult struct {
	Result     string `json:"result"`
	ReasonCode string `json:"reason_code,omitempty"`
}

func (s *CommentService) Create(ctx context.Context, in NewComment) (*Comment, *AdmissionResult, error) {
	in.Content = trimSpace(in.Content)
	in.Content = redactx.Sanitize(in.Content)
	if in.Content == "" || len([]rune(in.Content)) > 50000 {
		return nil, nil, apierr.NewValidation("invalid content")
	}
	wi, ok, err := s.repo.GetWorkItem(ctx, in.WorkspaceID, in.WorkItemID)
	if err != nil || !ok {
		return nil, nil, apierr.New(apierr.NotFound, "work item not found")
	}
	if in.ParentID != "" {
		parent, ok, err := s.repo.GetComment(ctx, in.ParentID)
		if err != nil || !ok || parent.Deleted {
			return nil, nil, apierr.NewValidation("parent comment not found")
		}
		if parent.WorkItemID != in.WorkItemID {
			return nil, nil, apierr.NewValidation("parent belongs to another work item")
		}

		root := rootOfComment(ctx, s.repo, parent)
		if rc, ok, err := s.repo.GetComment(ctx, root); err == nil && ok && rc.Resolved {
			if err := s.repo.SetResolved(ctx, root, false, in.AuthorType, in.AuthorID); err != nil {
				return nil, nil, err
			}
		}
	}

	env := ev(events.CommentCreated, in.WorkspaceID, in.AuthorID, nil)
	var c Comment
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		created, err := s.repo.CreateComment(ctx, in)
		if err != nil {
			return err
		}
		c = created
		if err := s.repo.TouchWorkItem(ctx, in.WorkItemID); err != nil {
			return err
		}

		if in.AuthorType == "user" && in.AuthorID != "" {
			_ = s.repo.UpsertSubscriber(ctx, in.WorkItemID, in.AuthorID, domain.SubSourceCommenter)
		}
		for _, m := range domain.ParseMentions(in.Content) {
			if m.Type == "user" && m.ID != "" {
				_ = s.repo.UpsertSubscriber(ctx, in.WorkItemID, m.ID, domain.SubSourceMentionee)
			}
		}
		env.Payload = events.CommentPayload{
			ID: c.ID, WorkspaceID: in.WorkspaceID, WorkItemID: in.WorkItemID,
			AuthorType: c.AuthorType, AuthorID: c.AuthorID, Content: c.Content,
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}

	var admission *AdmissionResult
	if triggerEngine != nil {
		root := rootOfComment(ctx, s.repo, c)
		rootType, rootID := "", ""
		if rc, ok, _ := s.repo.GetComment(ctx, root); ok {
			rootType, rootID = rc.AuthorType, rc.AuthorID
		}
		parentType, parentID := "", ""
		if in.ParentID != "" {
			if pc, ok, _ := s.repo.GetComment(ctx, in.ParentID); ok {
				parentType, parentID = pc.AuthorType, pc.AuthorID
			}
		}
		assigneeType, assigneeID := s.workItemAssignee(ctx, in.WorkItemID)

		if domain.TriageTriggerBlocked(domain.TriageGateFacts{
			TriageState: wi.TriageState, Mentions: domain.ParseMentions(in.Content),
			AuthorType: in.AuthorType, ParentAuthorType: parentType,
		}) {
			return &c, &AdmissionResult{
				Result:     dispatchreason.Blocked,
				ReasonCode: dispatchreason.WorkItemInTriage,
			}, nil
		}
		result, reason, err := triggerEngine.OnComment(ctx, TriggerInput{
			WorkspaceID: in.WorkspaceID, WorkItemID: in.WorkItemID,
			AuthorType: in.AuthorType, AuthorID: in.AuthorID, Content: in.Content,
			ParentAuthorType: parentType, ParentAuthorID: parentID,
			RootAuthorType: rootType, RootAuthorID: rootID,
			AssigneeType: assigneeType, AssigneeID: assigneeID,
		}, c.ID)
		if err != nil {
			return nil, nil, err
		}
		admission = &AdmissionResult{Result: result, ReasonCode: reason}
	} else {
		admission = &AdmissionResult{Result: dispatchreason.Blocked, ReasonCode: dispatchreason.TriggerEngineNotWired}
	}
	return &c, admission, nil
}

func (s *CommentService) workItemAssignee(ctx context.Context, workItemID string) (string, string) {
	if ws, ok := interface{}(s.repo).(WorkItemAssigneeReader); ok {
		return ws.GetAssignee(ctx, workItemID)
	}
	return "none", ""
}

type WorkItemAssigneeReader interface {
	GetAssignee(ctx context.Context, workItemID string) (assigneeType, assigneeID string)
}

func (s *CommentService) ListThread(ctx context.Context, workItemID string) ([]Comment, error) {
	rows, err := s.repo.ListComments(ctx, workItemID)
	if err != nil {
		return nil, err
	}
	return foldCommentRows(rows), nil
}

func (s *CommentService) ListThreadPage(ctx context.Context, workItemID string, limit int, before string) ([]Comment, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.repo.ListCommentPage(ctx, workItemID, limit, before)
	if err != nil {
		return nil, "", err
	}
	if len(rows) < limit {
		return foldCommentRows(rows), "", nil
	}

	var oldest time.Time
	for _, c := range rows {
		if c.ParentID == "" && (oldest.IsZero() || c.CreatedAt.Before(oldest)) {
			oldest = c.CreatedAt
		}
	}
	next := ""
	if !oldest.IsZero() {
		next = oldest.UTC().Format(time.RFC3339Nano)
	}
	return foldCommentRows(rows), next, nil
}

func foldCommentRows(rows []Comment) []Comment {
	trs := make([]domain.ThreadRow, len(rows))
	for i, r := range rows {
		trs[i] = domain.ThreadRow{
			ID: r.ID, ParentID: r.ParentID, AuthorID: r.AuthorID,
			AuthorType: r.AuthorType, Resolved: r.Resolved, Deleted: r.Deleted,
		}
	}
	ordered := domain.FoldThreads(trs)
	byID := map[string]Comment{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]Comment, 0, len(ordered))
	for _, tr := range ordered {
		if c, ok := byID[tr.ID]; ok {
			out = append(out, c)
		}
	}
	return out
}

func (s *CommentService) Update(ctx context.Context, workspaceID, id, content, actorID string, expectedRevision int64) (*Comment, error) {
	c, ok, err := s.repo.GetComment(ctx, id)
	if err != nil || !ok || c.Deleted {
		return nil, apierr.New(apierr.NotFound, "comment not found")
	}
	content = trimSpace(content)
	content = redactx.Sanitize(content)
	if content == "" {
		return nil, apierr.NewValidation("content cannot be empty")
	}

	if c.AuthorType == "user" && c.AuthorID != actorID {
		return nil, apierr.New(apierr.Forbidden, "only author can edit")
	}
	var out Comment
	env := ev(events.CommentUpdated, workspaceID, actorID, nil)
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		updated, err := s.repo.UpdateComment(ctx, id, content, expectedRevision)
		if err != nil {
			return err
		}
		out = updated
		if err := s.repo.TouchWorkItem(ctx, c.WorkItemID); err != nil {
			return err
		}
		env.Payload = events.CommentPayload{
			ID: out.ID, WorkspaceID: workspaceID, WorkItemID: out.WorkItemID,
			AuthorType: out.AuthorType, AuthorID: out.AuthorID, Content: out.Content,
		}
		return nil
	}); err != nil {
		return nil, err
	}
	cancelTasksOfComment(ctx, workspaceID, id)
	return &out, nil
}

func (s *CommentService) Delete(ctx context.Context, workspaceID, id, actorID string) error {
	c, ok, err := s.repo.GetComment(ctx, id)
	if err != nil || !ok {
		return apierr.New(apierr.NotFound, "comment not found")
	}
	if c.AuthorType == "user" && c.AuthorID != actorID {
		return apierr.New(apierr.Forbidden, "only author can delete")
	}
	env := ev(events.CommentDeleted, workspaceID, actorID, events.CommentPayload{
		ID: id, WorkspaceID: workspaceID, WorkItemID: c.WorkItemID,
	})
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		if err := s.repo.TombstoneComment(ctx, id); err != nil {
			return err
		}
		return s.repo.TouchWorkItem(ctx, c.WorkItemID)
	}); err != nil {
		return err
	}
	cancelTasksOfComment(ctx, workspaceID, id)
	return nil
}

func (s *CommentService) SetResolve(ctx context.Context, workspaceID, id string, resolved bool, actorType, actorID string) error {
	c, ok, err := s.repo.GetComment(ctx, id)
	if err != nil || !ok || c.Deleted {
		return apierr.New(apierr.NotFound, "comment not found")
	}
	if c.ParentID != "" {
		return apierr.NewValidation("only thread root can be resolved")
	}
	root := rootOfComment(ctx, s.repo, c)
	name := events.CommentResolved
	if !resolved {
		name = events.CommentUpdated
	}
	env := ev(name, workspaceID, actorID, events.CommentPayload{
		ID: root, WorkspaceID: workspaceID, WorkItemID: c.WorkItemID,
	})
	return s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		return s.repo.SetResolved(ctx, root, resolved, actorType, actorID)
	})
}

func rootOfComment(ctx context.Context, r Repo, c Comment) string {
	for c.ParentID != "" {
		p, ok, err := r.GetComment(ctx, c.ParentID)
		if err != nil || !ok {
			return c.ID
		}
		c = p
	}
	return c.ID
}

func (s *CommentService) commentVisible(ctx context.Context, workspaceID, commentID string) (*Comment, error) {
	c, ok, err := s.repo.GetComment(ctx, commentID)
	if err != nil {
		return nil, err
	}
	if !ok || c.Deleted || c.WorkspaceID != workspaceID {
		return nil, apierr.New(apierr.NotFound, "comment not found")
	}
	return &c, nil
}

func (s *CommentService) AddCommentReaction(ctx context.Context, workspaceID, commentID, userID, emoji string) ([]ReactionCount, error) {
	if emoji == "" || len(emoji) > emojiMaxLen {
		return nil, apierr.NewValidation("invalid emoji", apierr.FieldError{Field: "emoji", Problem: "1-32 chars"})
	}
	if _, err := s.commentVisible(ctx, workspaceID, commentID); err != nil {
		return nil, err
	}
	env := ev("reaction.added", workspaceID, userID,
		map[string]any{"comment_id": commentID, "emoji": emoji})
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		return s.repo.UpsertCommentReaction(ctx, workspaceID, commentID, userID, emoji)
	}); err != nil {
		return nil, err
	}
	return s.repo.ListCommentReactionCounts(ctx, workspaceID, commentID)
}

func (s *CommentService) RemoveCommentReaction(ctx context.Context, workspaceID, commentID, userID, emoji string) ([]ReactionCount, error) {
	if emoji == "" || len(emoji) > emojiMaxLen {
		return nil, apierr.NewValidation("invalid emoji", apierr.FieldError{Field: "emoji", Problem: "1-32 chars"})
	}
	if _, err := s.commentVisible(ctx, workspaceID, commentID); err != nil {
		return nil, err
	}
	env := ev("reaction.removed", workspaceID, userID,
		map[string]any{"comment_id": commentID, "emoji": emoji})
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		_, err := s.repo.DeleteCommentReaction(ctx, workspaceID, commentID, userID, emoji)
		return err
	}); err != nil {
		return nil, err
	}
	return s.repo.ListCommentReactionCounts(ctx, workspaceID, commentID)
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\n' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
