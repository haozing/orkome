package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/channels/domain"
	"orkome/internal/channels/wire"
)

func TestTitleCallbackApply(t *testing.T) {
	repo := boundRepo()
	_, err := repo.InsertRoute(context.Background(), Route{
		InstallationID: "inst-1", WorkspaceID: "ws-1", ExternalChatID: "chat-1",
		ChatType: wire.ChatTypeP2P, ChatSessionID: "sess-1",
	})
	require.NoError(t, err)
	svc := NewTitleCallbackService(repo)
	ctx := context.Background()

	applied, err := svc.ApplyTitleCallback(ctx, "inst-1", "chat-1", "    LLM-polished session title\x00 ")
	require.NoError(t, err)
	assert.True(t, applied)

	repo.mu.Lock()
	rt := repo.routes["inst-1|chat-1"]
	updates := len(repo.titleUpdates)
	repo.mu.Unlock()
	assert.Equal(t, "LLM-polished session title", rt.Title, "NUL stripped + leading/trailing whitespace normalized")
	assert.Equal(t, "llm", rt.TitleSource)
	assert.Equal(t, 1, updates)

	applied, err = svc.ApplyTitleCallback(ctx, "inst-1", "chat-1", "   ")
	require.ErrorIs(t, err, ErrTitleCallbackRejected)
	assert.False(t, applied)

	applied, err = svc.ApplyTitleCallback(ctx, "inst-1", "chat-none", "x")
	require.NoError(t, err)
	assert.False(t, applied)
}

func TestTitleCallbackExplicitLocked(t *testing.T) {
	repo := boundRepo()
	_, err := repo.InsertRoute(context.Background(), Route{
		InstallationID: "inst-1", WorkspaceID: "ws-1", ExternalChatID: "chat-1",
		ChatType: wire.ChatTypeP2P, ChatSessionID: "sess-1", Title: "user-named", TitleSource: "explicit",
	})
	require.NoError(t, err)
	svc := NewTitleCallbackService(repo)

	applied, err := svc.ApplyTitleCallback(context.Background(), "inst-1", "chat-1", "late LLM title")
	require.NoError(t, err)
	assert.False(t, applied, "explicit lock: does not overwrite")
	repo.mu.Lock()
	rt := repo.routes["inst-1|chat-1"]
	repo.mu.Unlock()
	assert.Equal(t, "user-named", rt.Title)
}

func TestTitleCallbackNormalizeEdge(t *testing.T) {
	long := make([]rune, 300)
	for i := range long {
		long[i] = 'x'
	}
	got := domain.TitleCallbackNormalize(string(long))
	assert.LessOrEqual(t, len([]rune(got)), 200)
	assert.Equal(t, "…", string([]rune(got)[len([]rune(got))-1]))
	assert.Equal(t, "a b c", domain.TitleCallbackNormalize("a\t b\n c"))
}
