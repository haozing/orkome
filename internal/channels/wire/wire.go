package wire

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Kind string

type ChatType string

const (
	ChatTypeP2P   ChatType = "p2p"
	ChatTypeGroup ChatType = "group"
)

type MsgType string

const (
	MsgTypeText    MsgType = "text"
	MsgTypeImage   MsgType = "image"
	MsgTypeFile    MsgType = "file"
	MsgTypeAudio   MsgType = "audio"
	MsgTypeVideo   MsgType = "video"
	MsgTypeUnknown MsgType = "unknown"
)

type Source struct {
	Kind       Kind
	RoutingKey string
	ChatID     string
	ChatType   ChatType
	SenderID   string
	ThreadID   string
}

type MediaRef struct {
	Type       MsgType
	StorageKey string
	StorageURL string
	Filename   string
	MimeType   string
	SizeBytes  int64
}

type InboundMessage struct {
	EventID   string
	MessageID string

	Source Source
	Type   MsgType
	Text   string

	CommandText string

	MediaRefs []MediaRef

	AddressedToBot bool

	Raw json.RawMessage
}

type OutboundMessage struct {
	ChatID   string
	Text     string
	ThreadID string
	ReplyTo  string
}

type SendResult struct {
	MessageID string
}

type InboundHandler func(ctx context.Context, msg InboundMessage) error

type Capability uint64

const (
	CapText Capability = 1 << iota
	CapRichCard
	CapThreadReply
	CapQuoteReply
	CapAttachment
	CapVoice
	CapTypingIndicator
	CapMessageEdit
	CapStreamingReply
)

var capabilityNames = []struct {
	bit  Capability
	name string
}{
	{CapText, "text"},
	{CapRichCard, "rich_card"},
	{CapThreadReply, "thread_reply"},
	{CapQuoteReply, "quote_reply"},
	{CapAttachment, "attachment"},
	{CapVoice, "voice"},
	{CapTypingIndicator, "typing_indicator"},
	{CapMessageEdit, "message_edit"},
	{CapStreamingReply, "streaming_reply"},
}

func (c Capability) Has(want Capability) bool { return c&want == want }

func (c Capability) String() string {
	if c == 0 {
		return "none"
	}
	var parts []string
	remaining := c
	for _, cn := range capabilityNames {
		if remaining&cn.bit == cn.bit {
			parts = append(parts, cn.name)
			remaining &^= cn.bit
		}
	}
	if remaining != 0 {
		parts = append(parts, fmt.Sprintf("0x%x", uint64(remaining)))
	}
	return strings.Join(parts, "|")
}

type Channel interface {
	Kind() Kind

	Connect(ctx context.Context) error

	Disconnect(ctx context.Context) error

	Send(ctx context.Context, out OutboundMessage) (SendResult, error)

	Capabilities() Capability
}

type Config struct {
	Kind    Kind
	Raw     json.RawMessage
	ID      string
	Handler InboundHandler
}

type Factory func(cfg Config) (Channel, error)

var ErrUnknownKind = fmt.Errorf("channel wire: no factory registered for kind")

type Registry struct {
	mu        sync.RWMutex
	factories map[Kind]Factory
}

func NewRegistry() *Registry { return &Registry{factories: map[Kind]Factory{}} }

func (r *Registry) Register(kind Kind, f Factory) {
	if kind == "" || f == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[kind] = f
}

func (r *Registry) Lookup(kind Kind) (Factory, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.factories[kind]
	return f, ok
}

func (r *Registry) Build(cfg Config) (Channel, error) {
	f, ok := r.Lookup(cfg.Kind)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, cfg.Kind)
	}
	return f(cfg)
}

func (r *Registry) Types() []Kind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Kind, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
