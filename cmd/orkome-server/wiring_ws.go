package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/haozing/ploykit/platform/relayx"
	"github.com/haozing/ploykit/platform/webx"
	"github.com/haozing/ploykit/platform/wswire"
	fwwsx "github.com/haozing/ploykit/platform/wsx"
	"orkome/internal/contract/events"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
)

func wiringWS(ctx context.Context, log *slog.Logger, bus *eventbus.Bus,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
	pat webx.PATLookup, cfg *Config, rdb redis.UniversalClient, reg *Workers,
) (*fwwsx.Hub, fwwsx.Broadcaster) {
	registerRealtimeVocab()
	hub := fwwsx.NewHub(log)
	hub.Capabilities = wswire.Capabilities()
	hub.PATPrefix = "ork_"
	hub.PATResolve = func(ctx context.Context, token string) (*webx.Principal, error) {
		return pat.ResolvePAT(ctx, token, time.Now())
	}

	hub.Authorize = func(ctx context.Context, p *webx.Principal, connWS, _, _ string) bool {
		if p == nil || connWS == "" {
			return false
		}
		ok, err := memberCheck(ctx, connWS, p.UserID)
		return err == nil && ok
	}
	hub.AllowedOrigins = cfg.WSOrigins
	_ = hub

	broadcaster := hubBroadcaster{hub: hub}

	// Framework relayx: single semantic — a relay exists when Redis does
	// (multi-replica fanout); without Redis the hub stays single-instance.
	var rel *relayx.Relay
	if rdb != nil {
		rel = relayx.NewRelay("", relayx.NewRedisTransport(rdb), log)
		hub.Relay = rel
		reg.AddFunc("ws-relay", func(ctx context.Context) error {
			return rel.Run(ctx, func(env relayx.Envelope) {
				broadcaster.Broadcast(context.Background(), env.Scope, env.Frame)
			})
		})
	}
	log.Info("relayx: realtime mode", "mode", map[bool]string{true: "redis", false: "memory"}[rdb != nil], "redis", rdb != nil)

	bus.On(eventbus.PhaseBroadcast, func(ctx context.Context, e events.Envelope) {
		if e.WorkspaceID == "" {
			return
		}
		frame := wswire.Frame{Type: wswire.WSName(e.Event), EventID: e.EventID}
		if e.Payload != nil {
			b, err := json.Marshal(e.Payload)
			if err != nil {
				return
			}
			frame.Payload = b
		}
		scope := wswire.ScopeKey(wswire.ScopeWorkspace, e.WorkspaceID)
		broadcaster.Broadcast(ctx, scope, frame)

		if sub := scopedTarget(e); sub != "" {
			broadcaster.Broadcast(ctx, sub, frame)
		}
	})
	return hub, broadcaster
}

type hubBroadcaster struct{ hub *fwwsx.Hub }

func (h hubBroadcaster) Broadcast(ctx context.Context, scope string, frame wswire.Frame) {
	h.hub.Broadcast(ctx, scope, frame)
}

func (h hubBroadcaster) SendToUser(ctx context.Context, userID string, frame wswire.Frame) {
	h.hub.SendToUser(ctx, userID, frame)
}

func mountWS(mux *http.ServeMux, hub *fwwsx.Hub, auth func(http.Handler) http.Handler) {
	mux.Handle("GET /ws", auth(http.HandlerFunc(hub.ServeWS)))
}

type notifUserFrames struct{ cast fwwsx.Broadcaster }

func (n notifUserFrames) Push(ctx context.Context, userID, frameType string, payload map[string]any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	n.cast.SendToUser(ctx, userID, wswire.Frame{Type: frameType, Payload: b})
}

func scopedTarget(e events.Envelope) string {
	p, _ := e.Payload.(orchapp.TaskPayload)
	switch {
	case strings.HasPrefix(e.Event, "task.") && p.ID != "":
		return wswire.ScopeKey("task", p.ID)
	case e.Event == events.ChatDone:
		if cp, ok := e.Payload.(orchapp.ChatDoneFrame); ok && cp.SessionID != "" {
			return wswire.ScopeKey("chat", cp.SessionID)
		}
	}
	return ""
}

// registerRealtimeVocab registers orkome's realtime scopes and capabilities on
// the framework wswire (the framework ships only workspace/user + batch/
// notification/workspace; orkome adds task/chat scopes and its product caps).
func registerRealtimeVocab() {
	registerVocabOnce.Do(func() {
		_ = wswire.RegisterScope("task")
		_ = wswire.RegisterScope("chat")
		for _, cap := range []string{"task", "work_item", "comment", "chat", "agent", "autopilot", "squad"} {
			_ = wswire.RegisterCapability(cap)
		}
	})
}

var registerVocabOnce sync.Once
