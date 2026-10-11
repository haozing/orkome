package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/events"
	"orkome/internal/contract/wswire"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/relayx"
	"orkome/internal/platform/wsx"
)

func wiringWS(ctx context.Context, log *slog.Logger, bus *eventbus.Bus,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
	pat webx.PATLookup, cfg *Config, rdb redis.UniversalClient, reg *Workers,
) (*wsx.Hub, wsx.Broadcaster) {
	hub := wsx.NewHub(log)
	hub.Capabilities = wswire.Capabilities()
	hub.PATResolve = pat.ResolvePAT

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

	mode, modeErr := relayx.ParseMode(cfg.RealtimeRelayMode, rdb != nil)
	if modeErr != nil {
		log.Error("invalid REALTIME_RELAY_MODE, falling back to default", "err", modeErr)
		mode = relayx.ModeSharded
	}
	var rel *relayx.Relay
	if rdb != nil && mode != relayx.ModeMemory {
		rel = &relayx.Relay{RDB: rdb, Shards: 8, Hub: broadcaster, Log: log, Mode: mode}
		reg.AddFunc("ws-relay", rel.Run)
	}
	log.Info("relayx: realtime mode", "mode", string(mode), "redis", rdb != nil)

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
		if rel != nil {
			_ = rel.Fanout(ctx, scope, frame, func() { broadcaster.Broadcast(ctx, scope, frame) })
		} else {
			broadcaster.Broadcast(ctx, scope, frame)
		}

		if sub := scopedTarget(e); sub != "" {
			broadcaster.Broadcast(ctx, sub, frame)
		}
	})
	return hub, broadcaster
}

type hubBroadcaster struct{ hub *wsx.Hub }

func (h hubBroadcaster) Broadcast(ctx context.Context, scope string, frame wswire.Frame) {
	h.hub.Broadcast(ctx, scope, frame)
}

func (h hubBroadcaster) SendToUser(ctx context.Context, userID string, frame wswire.Frame) {
	h.hub.SendToUser(ctx, userID, frame)
}

func mountWS(mux *http.ServeMux, hub *wsx.Hub, auth func(http.Handler) http.Handler) {
	mux.Handle("GET /ws", auth(http.HandlerFunc(hub.ServeWS)))
}

type notifUserFrames struct{ cast wsx.Broadcaster }

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
		return wswire.ScopeKey(wswire.ScopeTask, p.ID)
	case e.Event == events.ChatDone:
		if cp, ok := e.Payload.(wswire.ChatDonePayload); ok && cp.SessionID != "" {
			return wswire.ScopeKey(wswire.ScopeChat, cp.SessionID)
		}
	}
	return ""
}
