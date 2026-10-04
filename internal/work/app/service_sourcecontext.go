package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	"orkome/internal/work/domain"
)

const (
	SourceSnapshotMax = 1 << 20
	SourceAgentMax    = 64 << 10
	SourceTotalMax    = 72 << 10
	captureTokenTTL   = 15 * time.Minute
	OriginQuickCreate = "quick_create"
)

type SourcePreview struct {
	CaptureToken   string    `json:"capture_token"`
	Digest         string    `json:"digest"`
	ExpiresAt      time.Time `json:"expires_at"`
	WorkItemID     string    `json:"work_item_id"`
	WorkItemTitle  string    `json:"work_item_title"`
	CommentSnippet string    `json:"comment_snippet"`
	ThreadDepth    int       `json:"thread_depth"`
}

type SourceContextService struct {
	repo Repo
	emit Emitter
	now  Clock

	Sign func(msg string) string

	outbox OutboxRecorder
	uow    Within
}

func NewSourceContextService(repo Repo, emit Emitter, now Clock) *SourceContextService {
	if now == nil {
		now = time.Now
	}
	return &SourceContextService{repo: repo, emit: emit, now: now}
}

func (s *SourceContextService) SetOutbox(rec OutboxRecorder, uow Within) {
	s.outbox, s.uow = rec, uow
}

func (s *SourceContextService) emitCommitted(ctx context.Context, env *events.Envelope, write func(context.Context) error) error {
	return emitCommitted(ctx, s.emit, s.outbox, s.uow, env, write)
}

func (s *SourceContextService) threadSnapshot(ctx context.Context, c Comment) (map[string]any, []Comment, error) {
	var chain []Comment
	cur := c
	for i := 0; i < 64 && cur.ParentID != ""; i++ {
		p, ok, err := s.repo.GetComment(ctx, cur.ParentID)
		if err != nil || !ok {
			break
		}
		chain = append(chain, p)
		cur = p
	}

	snap := map[string]any{
		"comment_id":   c.ID,
		"work_item_id": c.WorkItemID,
		"content":      c.Content,
		"author_type":  c.AuthorType,
	}
	if len(chain) > 0 {
		anc := make([]map[string]any, 0, len(chain))
		for _, p := range chain {
			anc = append(anc, map[string]any{"id": p.ID, "content": p.Content, "author_type": p.AuthorType})
		}
		snap["ancestors"] = anc
	}
	return snap, chain, nil
}

func canonicalDigest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *SourceContextService) Preview(ctx context.Context, workspaceID, commentID, userID string) (*SourcePreview, error) {
	c, ok, err := s.repo.GetComment(ctx, commentID)
	if err != nil || !ok || c.Deleted {
		return nil, apierr.New(apierr.NotFound, "comment not found")
	}
	if _, ok, err := s.repo.GetWorkItem(ctx, workspaceID, c.WorkItemID); err != nil || !ok {
		return nil, apierr.New(apierr.NotFound, "work item not found")
	}
	snap, chain, _ := s.threadSnapshot(ctx, c)
	b, _ := json.Marshal(snap)
	if len(b) > SourceSnapshotMax {
		return nil, apierr.NewValidation("source context exceeds 1MiB limit")
	}
	digest := canonicalDigest(snap)
	wi, _, _ := s.repo.GetWorkItem(ctx, workspaceID, c.WorkItemID)
	exp := s.now().Add(captureTokenTTL)
	tok := s.captureToken(workspaceID, commentID, userID, digest, exp)
	return &SourcePreview{
		CaptureToken: tok, Digest: digest, ExpiresAt: exp,
		WorkItemID: c.WorkItemID, WorkItemTitle: wi.Title,
		CommentSnippet: snippet(c.Content, 120), ThreadDepth: len(chain) + 1,
	}, nil
}

func (s *SourceContextService) captureToken(ws, commentID, userID, digest string, exp time.Time) string {
	msg := strings.Join([]string{ws, commentID, userID, digest, fmt.Sprintf("%d", exp.Unix())}, "|")
	return fmt.Sprintf("%d.%s", exp.Unix(), s.Sign(msg))
}

func (s *SourceContextService) verifyToken(tok, ws, commentID, userID, digest string) error {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return apierr.NewValidation("invalid capture token")
	}
	var expUnix int64
	if _, err := fmt.Sscanf(parts[0], "%d", &expUnix); err != nil {
		return apierr.NewValidation("invalid capture token")
	}
	if s.now().Unix() > expUnix {
		return apierr.NewValidation("capture token expired")
	}
	exp := time.Unix(expUnix, 0)
	want := s.captureToken(ws, commentID, userID, digest, exp)
	if !hmac.Equal([]byte(want), []byte(tok)) {
		return apierr.New(apierr.Forbidden, "capture token mismatch")
	}
	return nil
}

func snippet(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (s *SourceContextService) CreateFromComment(ctx context.Context, workspaceID, commentID, userID, token, mode, title string) (*WorkItem, error) {
	c, ok, err := s.repo.GetComment(ctx, commentID)
	if err != nil || !ok || c.Deleted {
		return nil, apierr.New(apierr.NotFound, "comment not found")
	}
	wi, ok2, err := s.repo.GetWorkItem(ctx, workspaceID, c.WorkItemID)
	if err != nil || !ok2 {
		return nil, apierr.New(apierr.NotFound, "work item not found")
	}
	snap, _, _ := s.threadSnapshot(ctx, c)
	b, _ := json.Marshal(snap)
	limit := SourceSnapshotMax
	if mode == "agent" {
		limit = SourceAgentMax
	}
	if len(b) > limit {
		return nil, apierr.NewValidation("source context exceeds mode budget")
	}
	digest := canonicalDigest(snap)
	if err := s.verifyToken(token, workspaceID, commentID, userID, digest); err != nil {
		return nil, err
	}

	return s.materialize(ctx, workspaceID, userID, digest, b, c, wi, title, false)
}

func (s *SourceContextService) CreateFromDigest(ctx context.Context, workspaceID, digest, userID, title string) (*WorkItem, error) {
	_, snap, ok, err := s.repo.GetSourceContext(ctx, workspaceID, digest)
	if err != nil || !ok {
		return nil, apierr.New(apierr.NotFound, "source context not found")
	}
	var sc struct {
		WorkItemID string `json:"work_item_id"`
		CommentID  string `json:"comment_id"`
	}
	if json.Unmarshal(snap, &sc) != nil || sc.CommentID == "" {
		return nil, apierr.NewValidation("corrupted source context")
	}
	c, ok, err := s.repo.GetComment(ctx, sc.CommentID)
	if err != nil || !ok {
		return nil, apierr.New(apierr.NotFound, "source comment gone")
	}
	wi, ok2, err := s.repo.GetWorkItem(ctx, workspaceID, c.WorkItemID)
	if err != nil || !ok2 {
		return nil, apierr.New(apierr.NotFound, "work item not found")
	}

	return s.materialize(ctx, workspaceID, userID, digest, snap, c, wi, title, true)
}

func (s *SourceContextService) materialize(ctx context.Context, workspaceID, userID, digest string, snap []byte, c Comment, parent WorkItem, title string, retry bool) (*WorkItem, error) {
	var child WorkItem
	env := ev("work_item.created", workspaceID, userID, nil)
	if err := s.emitCommitted(ctx, &env, func(ctx context.Context) error {
		_, created, err := s.repo.InsertSourceContext(ctx, workspaceID, digest, snap, userID)
		if err != nil {
			return err
		}
		if !created && !retry {

			return apierr.New(apierr.Conflict, "source context already materialized")
		}
		t := title
		if t == "" {
			t = "Sub work items: " + snippet(c.Content, 50)
		}
		child, err = s.repo.CreateWorkItem(ctx, NewWorkItem{
			WorkspaceID: workspaceID, Title: t, StatusKey: domain.StatusTodo,
			CreatorType: "user", CreatorID: userID,
			ParentID: parent.ID, OriginType: OriginQuickCreate, OriginID: c.ID,
		})
		if err != nil {
			return err
		}
		env.Payload = child
		return nil
	}); err != nil {
		return nil, err
	}
	return &child, nil
}
