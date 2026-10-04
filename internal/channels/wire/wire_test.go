package wire

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeChannel struct{ kind Kind }

func (f *fakeChannel) Kind() Kind                           { return f.kind }
func (f *fakeChannel) Connect(ctx context.Context) error    { return nil }
func (f *fakeChannel) Disconnect(ctx context.Context) error { return nil }
func (f *fakeChannel) Send(ctx context.Context, o OutboundMessage) (SendResult, error) {
	return SendResult{MessageID: "m1"}, nil
}
func (f *fakeChannel) Capabilities() Capability { return CapText }

func TestRegistryLastWriterWins(t *testing.T) {
	r := NewRegistry()
	r.Register("fake", func(Config) (Channel, error) { return &fakeChannel{kind: "fake"}, nil })
	r.Register("fake", func(Config) (Channel, error) { return &fakeChannel{kind: "fake-v2"}, nil })
	ch, err := r.Build(Config{Kind: "fake"})
	require.NoError(t, err)
	assert.Equal(t, Kind("fake-v2"), ch.Kind(), "later registration overrides earlier (deployment can override built-ins)")
}

func TestRegistryUnknownKind(t *testing.T) {
	_, err := NewRegistry().Build(Config{Kind: "nope"})
	assert.ErrorIs(t, err, ErrUnknownKind)
}

func TestRegistryIgnoresEmptyRegistration(t *testing.T) {
	r := NewRegistry()
	r.Register("", func(Config) (Channel, error) { return nil, nil })
	r.Register("fake", nil)
	assert.Empty(t, r.Types())
	_, ok := r.Lookup("fake")
	assert.False(t, ok)
}

func TestCapability(t *testing.T) {
	c := CapText | CapThreadReply
	assert.True(t, c.Has(CapText))
	assert.True(t, c.Has(CapText|CapThreadReply))
	assert.False(t, c.Has(CapRichCard))
	assert.True(t, Capability(0).Has(0), "empty requirement always satisfied")
	assert.Equal(t, "none", Capability(0).String())
	assert.Equal(t, "text|thread_reply", c.String())

	assert.Contains(t, Capability(1<<40).String(), "0x")
}

func TestCapabilityStreamingReplyBit(t *testing.T) {
	c := CapText | CapStreamingReply
	assert.True(t, c.Has(CapText|CapStreamingReply), "two-phase delivery requires text + streaming placeholder")
	assert.False(t, CapText.Has(CapStreamingReply), "plain-text channels do not get streaming by default")
	assert.False(t, (CapText | CapMessageEdit).Has(CapStreamingReply), "editable ≠ streaming placeholder")
	assert.Equal(t, "text|streaming_reply", c.String())
}

func TestSendResultRoundTrip(t *testing.T) {
	ch := &fakeChannel{kind: "fake"}
	res, err := ch.Send(context.Background(), OutboundMessage{ChatID: "c", Text: "hi"})
	require.NoError(t, err)
	assert.Equal(t, "m1", res.MessageID)
}

var errBuild = errors.New("build failed")

func TestRegistryBuildErrorPropagates(t *testing.T) {
	r := NewRegistry()
	r.Register("bad", func(Config) (Channel, error) { return nil, errBuild })
	_, err := r.Build(Config{Kind: "bad"})
	assert.ErrorIs(t, err, errBuild)
}
