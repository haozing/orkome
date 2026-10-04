package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendMessage_FirstMessageInsertsAgentIntroBeforeUserRow(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	enqTx := newFakeEnqTx(repo)
	s := NewChatService(repo, &fakeEnqueuer{})
	s.SetEnqueueChatTx(enqTx.fn())
	s.SetAgentStarters(func(_ context.Context, ws, agent string) ([]string, error) {
		assert.Equal(t, "ws1", ws, "starters fetched by workspace + agent")
		assert.Equal(t, "ag1", agent)
		return []string{"summary progress", " check risks ", ""}, nil
	})

	m, _, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "hello")
	require.NoError(t, err)
	require.Len(t, repo.inserted, 2, "intro row + user row")
	intro, user := repo.inserted[0], repo.inserted[1]
	assert.Equal(t, "assistant", intro.Role)
	assert.Equal(t, "agent_intro", intro.Kind)
	assert.Equal(t, "- summary progress\n- check risks", intro.Content, "labels render as '- label' list (blank labels skipped)")
	assert.Empty(t, intro.TaskID, "intro rows do not belong to a task")
	assert.Less(t, intro.Seq, user.Seq, "intro row precedes the user row (seq ascending)")
	assert.Equal(t, "user", user.Role)
	assert.Equal(t, "hello", user.Content)
	assert.Equal(t, m.Content, "hello", "what is returned is still the user message row")
	assert.False(t, repo.rolledBack)
	assert.Equal(t, 1, repo.withinOpened, "intro and the user row in the same transaction")
}

func TestSendMessage_NonFirstMessageSkipsIntro(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	repo.listMsg = func(string, int, int64) ([]Message, error) {
		return []Message{{SessionID: "sess1", Role: "assistant", Kind: "message", Seq: 1}}, nil
	}
	called := false
	s := NewChatService(repo, &fakeEnqueuer{})
	s.SetAgentStarters(func(context.Context, string, string) ([]string, error) {
		called = true
		return []string{"summary"}, nil
	})

	_, _, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "second sentence")
	require.NoError(t, err)
	assert.False(t, called, "non-first messages do not hit the starters port")
	require.Len(t, repo.inserted, 1, "only the user row lands")
	assert.Equal(t, "user", repo.inserted[0].Role)
}

func TestSendMessage_EmptyStartersSkipIntro(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, &fakeEnqueuer{})
	s.SetAgentStarters(func(context.Context, string, string) ([]string, error) {
		return []string{"", ""}, nil
	})

	_, _, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "hello")
	require.NoError(t, err)
	require.Len(t, repo.inserted, 1, "empty starters insert no intro row")
	assert.Equal(t, "user", repo.inserted[0].Role)
}

func TestSendMessage_NilStartersPortSkipsIntro(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, &fakeEnqueuer{})

	_, _, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "hello")
	require.NoError(t, err)
	require.Len(t, repo.inserted, 1)
	assert.Equal(t, "user", repo.inserted[0].Role)
}

func TestSendMessage_TwoStepPathInsertsIntroFirst(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	enq := &fakeEnqueuer{}
	s := NewChatService(repo, enq)
	s.SetAgentStarters(func(context.Context, string, string) ([]string, error) {
		return []string{"check risks"}, nil
	})

	m, taskID, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "hello")
	require.NoError(t, err)
	assert.Equal(t, "task-new", taskID)
	require.Len(t, repo.inserted, 2)
	assert.Equal(t, "agent_intro", repo.inserted[0].Kind)
	assert.Equal(t, "user", repo.inserted[1].Role)
	assert.Equal(t, m.Kind, "message")
	assert.Zero(t, repo.withinOpened, "two-step path opens no transaction")
}

func TestSendMessage_StarterPortErrorPropagates(t *testing.T) {
	repo := newFakeChatRepo()
	repo.sessions["ws1/sess1"] = activeSession("ws1", "u1")
	s := NewChatService(repo, &fakeEnqueuer{})
	s.SetAgentStarters(func(context.Context, string, string) ([]string, error) {
		return nil, errors.New("agent svc down")
	})

	_, _, err := s.SendMessage(context.Background(), "ws1", "sess1", "u1", "hello")
	require.Error(t, err)
	assert.EqualError(t, err, "agent svc down")
	assert.Empty(t, repo.inserted, "failure lands no rows")
	assert.Empty(t, repo.withinOpened)
}
