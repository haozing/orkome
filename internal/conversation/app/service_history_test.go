package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type convFakeRepo struct {
	Repo
	sess     Session
	messages []Message
	nextSeq  int64
}

func (f *convFakeRepo) GetSession(_ context.Context, _, id string) (Session, bool, error) {
	if f.sess.ID == id && id != "" {
		return f.sess, true, nil
	}
	return Session{}, false, nil
}
func (f *convFakeRepo) ListMessages(_ context.Context, sessionID string, limit int, beforeSeq int64) ([]Message, error) {
	var desc []Message
	for i := len(f.messages) - 1; i >= 0; i-- {
		m := f.messages[i]
		if m.SessionID == sessionID && m.Seq < beforeSeq {
			desc = append(desc, m)
			if len(desc) == limit {
				break
			}
		}
	}
	return desc, nil
}
func (f *convFakeRepo) NextSeq(_ context.Context, _ string) (int64, error) {
	f.nextSeq++
	return f.nextSeq, nil
}
func (f *convFakeRepo) InsertMessage(_ context.Context, m Message) (Message, error) {
	m.ID = "m-" + m.Content
	f.messages = append(f.messages, m)
	return m, nil
}
func (f *convFakeRepo) CountAssistant(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func isNotFound(err error) bool {
	var d *domainErr
	return errors.As(err, &d) && d.code == "E_NOT_FOUND"
}

func isValidation(err error) bool {
	var d *domainErr
	return errors.As(err, &d) && d.code == "E_VALIDATION"
}

func TestHistoryTranscriptFallback(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repo := &convFakeRepo{sess: Session{ID: "s1", WorkspaceID: "ws", CreatorID: "u1", Status: "active"}}
	for i, role := range []string{"user", "assistant", "user", "assistant"} {
		repo.messages = append(repo.messages, Message{
			ID: "m", SessionID: "s1", Role: role, Content: role,
			Seq: int64(i + 1), CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	svc := NewChatService(repo, nil)

	page, err := svc.History(context.Background(), "ws", "s1", "u1", 2, "")
	require.NoError(t, err)
	assert.Equal(t, "transcript", page.ChannelType)
	assert.Equal(t, "s1", page.ThreadID)
	require.Len(t, page.Messages, 2)

	assert.Equal(t, "user", page.Messages[0].Role)
	assert.Equal(t, "User", page.Messages[0].Author)
	assert.Equal(t, "assistant", page.Messages[1].Role)
	assert.Equal(t, "Bot", page.Messages[1].Author)
	assert.Equal(t, "3", page.NextCursor, "full page: next_cursor = oldest seq in the page")

	page, err = svc.History(context.Background(), "ws", "s1", "u1", 2, "3")
	require.NoError(t, err)
	require.Len(t, page.Messages, 2)
	assert.Equal(t, int64(1), page.Messages[0].Seq)
	assert.Equal(t, "1", page.NextCursor)

	page, err = svc.History(context.Background(), "ws", "s1", "u1", 2, "1")
	require.NoError(t, err)
	assert.Empty(t, page.Messages)
	assert.Empty(t, page.NextCursor, "partial page has no next page")
	assert.NotEmpty(t, page.Note)
}

func TestHistoryOwnership(t *testing.T) {
	repo := &convFakeRepo{sess: Session{ID: "s1", WorkspaceID: "ws", CreatorID: "u1", Status: "active"}}
	svc := NewChatService(repo, nil)

	_, err := svc.History(context.Background(), "ws", "s1", "u2", 50, "")
	require.Error(t, err)
	assert.True(t, isNotFound(err), "anyone else gets 404, got %v", err)

	_, err = svc.History(context.Background(), "ws", "missing", "u1", 50, "")
	require.Error(t, err)
	assert.True(t, isNotFound(err))
}

func TestThreadTranscript(t *testing.T) {
	repo := &convFakeRepo{sess: Session{ID: "s1", WorkspaceID: "ws", CreatorID: "u1", Status: "active"}}
	repo.messages = []Message{{ID: "m", SessionID: "s1", Role: "user", Seq: 1, CreatedAt: time.Now()}}
	svc := NewChatService(repo, nil)

	page, err := svc.Thread(context.Background(), "ws", "s1", "u1", "", 50, "")
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)
	assert.Equal(t, "s1", page.ThreadID)

	page, err = svc.Thread(context.Background(), "ws", "s1", "u1", "s1", 50, "")
	require.NoError(t, err)
	require.Len(t, page.Messages, 1)

	_, err = svc.Thread(context.Background(), "ws", "s1", "u1", "other-thread", 50, "")
	require.Error(t, err)
	assert.True(t, isNotFound(err), "thread locators valid only within session context")
}

func TestHistoryBadCursor(t *testing.T) {
	repo := &convFakeRepo{sess: Session{ID: "s1", WorkspaceID: "ws", CreatorID: "u1"}}
	svc := NewChatService(repo, nil)
	_, err := svc.History(context.Background(), "ws", "s1", "u1", 50, "not-a-seq")
	require.Error(t, err)
	assert.True(t, isValidation(err))
}

func TestSendMessageValidation(t *testing.T) {
	repo := &convFakeRepo{sess: Session{ID: "s1", WorkspaceID: "ws", CreatorID: "u1", Status: "active"}}
	svc := NewChatService(repo, nil)

	_, _, err := svc.SendMessage(context.Background(), "ws", "s1", "u1", "   ")
	require.Error(t, err)
	assert.True(t, isValidation(err))

	_, _, err = svc.SendMessage(context.Background(), "ws", "missing", "u1", "hi")
	require.Error(t, err)
	assert.True(t, isNotFound(err))

	repo.sess.Status = "archived"
	_, _, err = svc.SendMessage(context.Background(), "ws", "s1", "u1", "hi")
	require.Error(t, err)
	assert.True(t, isValidation(err), "sending messages in an archived session = validation failure")
}
