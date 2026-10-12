package app

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateSession_NilTitleIsNoOp(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, nil)

	sess, err := s.UpdateSession(context.Background(), "ws1", "sess1", "u1", nil)
	require.NoError(t, err)
	assert.Equal(t, "t", sess.Title, "title not provided: returned as-is")
	assert.Empty(t, repo.titleUpdated, "repo rename must not be called")

	_, err = s.UpdateSession(context.Background(), "ws1", "nope", "u1", nil)
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))
}

func TestUpdateSession_TitleValidation(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, nil)

	blank := "   "
	_, err := s.UpdateSession(context.Background(), "ws1", "sess1", "u1", &blank)
	require.Error(t, err)
	assert.Equal(t, "E_VALIDATION", errCode(t, err))
	assert.Empty(t, repo.titleUpdated, "invalid titles must not reach the repo")

	long := strings.Repeat("mark", MaxSessionTitleRunes+1)
	_, err = s.UpdateSession(context.Background(), "ws1", "sess1", "u1", &long)
	require.Error(t, err)
	assert.Equal(t, "E_VALIDATION", errCode(t, err))

	okTitle := strings.Repeat("x", MaxSessionTitleRunes)
	sess, err := s.UpdateSession(context.Background(), "ws1", "sess1", "u1", &okTitle)
	require.NoError(t, err)
	assert.Equal(t, okTitle, sess.Title)
}

func TestUpdateSession_RenamesAndTrims(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, nil)

	title := "new name  "
	sess, err := s.UpdateSession(context.Background(), "ws1", "sess1", "u1", &title)
	require.NoError(t, err)
	assert.Equal(t, "new name", sess.Title, "title should be persisted after trim")
	require.Len(t, repo.titleUpdated, 1)
	assert.Equal(t, "new name", repo.titleUpdated[0].title)
	assert.Equal(t, "sess1", repo.titleUpdated[0].id)

	_, err = s.UpdateSession(context.Background(), "ws1", "nope", "u1", &title)
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))
}

func TestSetPinned_TogglesAndNotFound(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, nil)

	sess, err := s.SetPinned(context.Background(), "ws1", "sess1", "u1", true)
	require.NoError(t, err)
	assert.True(t, sess.Pinned)
	sess, err = s.SetPinned(context.Background(), "ws1", "sess1", "u1", false)
	require.NoError(t, err)
	assert.False(t, sess.Pinned)
	require.Len(t, repo.pinSet, 2)
	assert.True(t, repo.pinSet[0].pinned)
	assert.False(t, repo.pinSet[1].pinned)

	_, err = s.SetPinned(context.Background(), "ws1", "nope", "u1", true)
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))
	assert.Len(t, repo.pinSet, 2, "missing session must not touch the repo again")
}

func TestDeleteSession_EmitsDeletedFrame(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, nil)

	var frames []struct{ event, ws, sid string }
	s.SetSessionEventSink(func(_ context.Context, event, ws, sid string) {
		frames = append(frames, struct{ event, ws, sid string }{event, ws, sid})
	})

	require.NoError(t, s.DeleteSession(context.Background(), "ws1", "sess1", "u1"))
	_, exists := repo.sessions["ws1/sess1"]
	assert.False(t, exists, "session row should be deleted")
	require.Len(t, frames, 1, "exactly one frame")
	assert.Equal(t, EventChatSessionDeleted, frames[0].event)
	assert.Equal(t, "ws1", frames[0].ws)
	assert.Equal(t, "sess1", frames[0].sid)
}

func TestDeleteSession_NotFoundAndNilSink(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, nil)

	err := s.DeleteSession(context.Background(), "ws1", "nope", "u1")
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))

	require.NoError(t, s.DeleteSession(context.Background(), "ws1", "sess1", "u1"), "nil port does not block deletion")
}

func TestSessionInUseError_Code(t *testing.T) {
	err := SessionInUseError("still bound")
	require.Equal(t, CodeSessionInUse, errCode(t, err))
}

func TestArchiveSessionsByUser_PassesThroughAndCounts(t *testing.T) {
	repo := newFakeChatRepo()
	mine := activeSession("ws1", "u1")
	mine.ID = "sess1"
	other := activeSession("ws1", "u2")
	other.ID = "sess2"
	mineB := activeSession("ws1", "u1")
	mineB.ID = "sess3"
	repo.sessions["ws1/sess1"] = mine
	repo.sessions["ws1/sess2"] = other
	repo.sessions["ws1/sess3"] = mineB
	s := NewChatService(repo, nil)

	n, err := s.ArchiveSessionsByUser(context.Background(), "ws1", "u1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "archive only this user's sessions in this workspace")
	require.Len(t, repo.archivedByUser, 1)
	assert.Equal(t, "ws1", repo.archivedByUser[0].ws)
	assert.Equal(t, "u1", repo.archivedByUser[0].uid)
	assert.Equal(t, "archived", repo.sessions["ws1/sess1"].Status)
	assert.Equal(t, "archived", repo.sessions["ws1/sess3"].Status)
	assert.Equal(t, "active", repo.sessions["ws1/sess2"].Status, "others' sessions untouched")

	n, err = s.ArchiveSessionsByUser(context.Background(), "ws1", "u1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}
