package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMediaMaterializeMarkdown(t *testing.T) {
	img := MediaMaterializeMarkdown(MediaRef{
		Kind: "image", StorageKey: "media/a.png", StorageURL: "https://cdn.example/a.png"})
	assert.Equal(t, "![](https://cdn.example/a.png)\n<!-- channel-media: media/a.png -->", img)

	file := MediaMaterializeMarkdown(MediaRef{
		Kind: "file", StorageKey: "media/b.bin", StorageURL: "https://cdn.example/b.bin",
		Filename: "report[final].pdf"})
	assert.Equal(t, `[report\[final\].pdf](https://cdn.example/b.bin)`+"\n<!-- channel-media: media/b.bin -->", file)

	assert.Empty(t, MediaMaterializeMarkdown(MediaRef{Kind: "file", StorageKey: "x"}), "no URL = no reference face")
}

func TestMaterializeMedia(t *testing.T) {
	repo := newFakeChatRepo()
	svc := NewChatService(repo, &fakeEnqueuer{})
	ctx := context.Background()

	repo.inserted = []Message{{ID: "cm-1", SessionID: "s1", Role: "user", Kind: "message",
		Content: "look at this picture", Seq: 1}}

	require.NoError(t, svc.MaterializeMedia(ctx, "cm-1", []MediaRef{{
		Kind: "image", StorageKey: "media/a.png", StorageURL: "https://cdn.example/a.png"}}))
	got := repo.inserted[0].Content
	assert.Contains(t, got, "look at this picture\n\n![](https://cdn.example/a.png)", "original body kept, appended at the tail")
	assert.Contains(t, got, "<!-- channel-media: media/a.png -->", "provenance marks travel with the row")
	assert.Equal(t, "message", repo.inserted[0].Kind, "kind kept (internal archive 03 §3 vocabulary unchanged)")

	require.NoError(t, svc.MaterializeMedia(ctx, "cm-1", []MediaRef{{
		Kind: "image", StorageKey: "media/a.png", StorageURL: "https://cdn.example/a.png"}}))
	assert.Equal(t, got, repo.inserted[0].Content, "repeated materialization does not append again")
}

func TestMaterializeMedia_BatchAndMissingMessage(t *testing.T) {
	repo := newFakeChatRepo()
	svc := NewChatService(repo, &fakeEnqueuer{})
	ctx := context.Background()

	repo.inserted = []Message{{ID: "cm-2", SessionID: "s1", Role: "user", Kind: "message",
		Content: "two materials", Seq: 1}}
	require.NoError(t, svc.MaterializeMedia(ctx, "cm-2", []MediaRef{
		{Kind: "image", StorageKey: "m/1.png", StorageURL: "https://c/1.png"},
		{Kind: "file", StorageKey: "m/2.pdf", StorageURL: "https://c/2.pdf", Filename: "minutes.pdf"},
	}))
	got := repo.inserted[0].Content
	assert.Contains(t, got, "![](https://c/1.png)")
	assert.Contains(t, got, "[minutes.pdf](https://c/2.pdf)")
	assert.Contains(t, got, "channel-media: m/1.png")
	assert.Contains(t, got, "channel-media: m/2.pdf")

	require.NoError(t, svc.MaterializeMedia(ctx, "cm-none", []MediaRef{{
		Kind: "image", StorageKey: "m/x", StorageURL: "https://c/x"}}), "message does not exist = not an error (missing degrades)")
}
