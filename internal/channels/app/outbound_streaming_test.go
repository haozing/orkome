package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/channels/domain"
	"orkome/internal/channels/wire"
)

var errSendFailed = errors.New("platform send failed")

type streamingChannel struct {
	recordingChannel
	nextID int
}

func (c *streamingChannel) Send(_ context.Context, out wire.OutboundMessage) (wire.SendResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	c.sent = append(c.sent, out)
	return wire.SendResult{MessageID: "plat-stream-" + out.Text[:1] + string(rune('0'+c.nextID))}, nil
}

func (c *streamingChannel) Capabilities() wire.Capability {
	return wire.CapText | wire.CapStreamingReply
}

func newStreamingOutbound(repo *fakeRepo, ch wire.Channel) *OutboundService {
	senders := NewSenderRegistry()
	senders.Register("inst-1", ch)
	return NewOutboundService(repo, senders, fakeGuard{}, &fakeTaskOutcomes{
		outputs: map[string]string{"task-s": "final draft content"},
	})
}

func streamingStamp(repo *fakeRepo, taskID string) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	repo.taskStamps[taskID] = TaskDelivery{
		TaskID: taskID, BindingID: "route-1", InstallationID: "inst-1", Kind: "fake",
		ExternalChatID: "chat-1", ChatType: wire.ChatTypeP2P, RouteRevision: 1, ContextRevision: 1,
	}
}

func TestStreamingTwoPhaseHappyPath(t *testing.T) {
	repo := boundRepo()
	_, err := repo.InsertRoute(context.Background(), Route{
		InstallationID: "inst-1", WorkspaceID: "ws-1", ExternalChatID: "chat-1",
		ChatType: wire.ChatTypeP2P, ChatSessionID: "sess-1",
	})
	require.NoError(t, err)
	ch := &streamingChannel{}
	svc := newStreamingOutbound(repo, ch)
	streamingStamp(repo, "task-s")

	res, err := svc.BeginStreamingReply(context.Background(), "task-s")
	require.NoError(t, err)
	assert.True(t, res.Sent)
	assert.Equal(t, "streaming_opened", res.Reason)

	repo.mu.Lock()
	assert.Equal(t, domain.ReplyPhaseStreaming, repo.replyPhase["task-s"], "reply row opens the placeholder phase")
	assert.NotEmpty(t, repo.placeholder["task-s"], "anchored by the placeholder message id (editable message)")
	card, hasCard := repo.cardRows["task-s"]
	phases1 := len(repo.settled)
	repo.mu.Unlock()
	require.True(t, hasCard, "rich-card placeholder row persisted")
	assert.Equal(t, domain.CardStatusStreaming, card.Status)
	assert.Equal(t, "chat-1", card.ExternalChatID)
	assert.NotEmpty(t, card.ExternalCardMessageID)

	res2, err := svc.DeliverTaskReply(context.Background(), "task-s")
	require.NoError(t, err)
	assert.True(t, res2.Sent)

	ch.mu.Lock()
	require.Len(t, ch.sent, 2, "placeholder + final draft, two deliveries")
	placeholderText, finalText := ch.sent[0].Text, ch.sent[1].Text
	ch.mu.Unlock()
	assert.Equal(t, domain.StreamingPlaceholderText, placeholderText)
	assert.Equal(t, "final draft content", finalText)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	assert.Equal(t, domain.ReplyPhaseSettled, repo.replyPhase["task-s"], "terminal state converges to settled")
	assert.Equal(t, domain.CardStatusFinal, repo.cardRows["task-s"].Status, "rich-card placeholder row advances final")
	assert.Equal(t, phases1, 0, "placeholder phase does not settle")
	require.Len(t, repo.settled, 1)
}

func TestBeginStreamingNoCapability(t *testing.T) {
	repo := boundRepo()
	ch := &recordingChannel{}
	svc := newStreamingOutbound(repo, ch)
	streamingStamp(repo, "task-s")

	res, err := svc.BeginStreamingReply(context.Background(), "task-s")
	require.NoError(t, err)
	assert.False(t, res.Sent)
	assert.Equal(t, "cap_streaming_absent", res.Reason)

	repo.mu.Lock()
	assert.Empty(t, repo.replyPhase)
	assert.Empty(t, repo.cardRows)
	repo.mu.Unlock()

	res2, err := svc.DeliverTaskReply(context.Background(), "task-s")
	require.NoError(t, err)
	assert.True(t, res2.Sent, "single-phase channel: terminal one-step settle unaffected")
	repo.mu.Lock()
	defer repo.mu.Unlock()
	assert.Empty(t, repo.terminalIDs, "no placeholder rows: terminal not advanced")
	assert.Len(t, repo.settled, 1)
}

func TestBeginStreamingNoReplayAfterTerminal(t *testing.T) {
	repo := boundRepo()
	ch := &streamingChannel{}
	svc := newStreamingOutbound(repo, ch)
	streamingStamp(repo, "task-s")

	_, err := svc.BeginStreamingReply(context.Background(), "task-s")
	require.NoError(t, err)
	_, err = svc.DeliverTaskReply(context.Background(), "task-s")
	require.NoError(t, err)

	ch.mu.Lock()
	sentBefore := len(ch.sent)
	ch.mu.Unlock()

	res, err := svc.BeginStreamingReply(context.Background(), "task-s")
	require.NoError(t, err)
	assert.False(t, res.Sent)
	assert.Equal(t, "phase_not_openable", res.Reason)

	ch.mu.Lock()
	defer ch.mu.Unlock()
	assert.Len(t, ch.sent, sentBefore, "no more placeholder deliveries after settled")
}

func TestBeginStreamingNotChannelIngested(t *testing.T) {
	repo := boundRepo()
	svc := newStreamingOutbound(repo, &streamingChannel{})

	res, err := svc.BeginStreamingReply(context.Background(), "task-web")
	require.NoError(t, err)
	assert.False(t, res.Sent)
	assert.Equal(t, "not_channel_ingested", res.Reason)

	res2, err := svc.BeginStreamingReply(context.Background(), "task-off")
	_ = res2
	require.NoError(t, err)
}

func TestBeginStreamingRetryAfterPlaceholderSendFailure(t *testing.T) {
	repo := boundRepo()
	ch := &failFirstSendChannel{caps: wire.CapText | wire.CapStreamingReply, fail: true}
	svc := newStreamingOutbound(repo, ch)
	streamingStamp(repo, "task-s")

	_, err := svc.BeginStreamingReply(context.Background(), "task-s")
	require.Error(t, err, "first delivery failure propagates (retryable)")

	repo.mu.Lock()
	assert.Equal(t, domain.ReplyPhaseStreaming, repo.replyPhase["task-s"], "row stays streaming: retry can take over")
	repo.mu.Unlock()

	ch.mu.Lock()
	ch.fail = false
	ch.mu.Unlock()
	res, err := svc.BeginStreamingReply(context.Background(), "task-s")
	require.NoError(t, err)
	assert.True(t, res.Sent, "retry reopens the placeholder successfully")
}

type failFirstSendChannel struct {
	mu     sync.Mutex
	sent   []wire.OutboundMessage
	fail   bool
	caps   wire.Capability
	nextID int
}

func (c *failFirstSendChannel) Kind() wire.Kind                  { return "fake" }
func (c *failFirstSendChannel) Connect(context.Context) error    { return nil }
func (c *failFirstSendChannel) Disconnect(context.Context) error { return nil }
func (c *failFirstSendChannel) Capabilities() wire.Capability    { return c.caps }
func (c *failFirstSendChannel) Send(_ context.Context, out wire.OutboundMessage) (wire.SendResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return wire.SendResult{}, errSendFailed
	}
	c.nextID++
	c.sent = append(c.sent, out)
	return wire.SendResult{MessageID: "plat-r"}, nil
}
