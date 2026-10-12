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

// subScopeFence 校验 scoped 实体是否属于连接的授权工作区（FP-20 F1）。
// 工作区成员资格本身不足以解锁 task/chat 订阅——跨区订阅会持续收到
// Result/Content 全文，属订阅面 IDOR。
type subScopeFence struct {
	// task：taskID 是否属于 workspaceID。
	task func(ctx context.Context, workspaceID, taskID string) (bool, error)
	// chat：sessionID 是否为 userID 的会话。裁决 D1（docs/fix-wave2-chat-2026-10-12.md §7）：
	// 会话口径为 creator 私有，WS 订阅与 REST 读写同口径（ workspace 成员 ≠ 可订他人会话）。
	chat func(ctx context.Context, workspaceID, sessionID, userID string) (bool, error)
}

func wiringWS(ctx context.Context, log *slog.Logger, bus *eventbus.Bus,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
	fence subScopeFence,
	pat webx.PATLookup, cfg *Config, rdb redis.UniversalClient, reg *Workers,
) (*fwwsx.Hub, fwwsx.Broadcaster) {
	registerRealtimeVocab()
	hub := fwwsx.NewHub(log)
	hub.Capabilities = wswire.Capabilities()
	if pat != nil {
		hub.PATPrefix = "ork_"
		hub.PATResolve = func(ctx context.Context, token string) (*webx.Principal, error) {
			return pat.ResolvePAT(ctx, token, time.Now())
		}
	}

	// FP-06 P1-1：workspace scope 复活。此前从未设置 WorkspaceMember，
	// 握手 auto-join 与显式 workspace 订阅全部 rejected，comment:/work_item:/
	// label: 帧谁都收不到。MemberGate 是框架提供的 principal→成员校验适配器。
	hub.WorkspaceMember = fwwsx.MemberGate(memberCheck)
	hub.Authorize = func(ctx context.Context, p *webx.Principal, connWS, scope, id string) bool {
		if p == nil || connWS == "" {
			return false
		}
		ok, err := memberCheck(ctx, connWS, p.UserID)
		if err != nil || !ok {
			return false
		}
		// FP-20 F1：消费框架传入的 scope/id——workspace 成员资格不外溢到
		// task/chat scope。能走到 default 分支的 scope 都已注册（hub 侧对
		// 未注册 scope 静默丢弃），其余一律 fail-closed。
		switch scope {
		case "task":
			if id == "" || fence.task == nil {
				return false
			}
			ok, err := fence.task(ctx, connWS, id)
			return err == nil && ok
		case "chat":
			if id == "" || fence.chat == nil {
				return false
			}
			ok, err := fence.chat(ctx, connWS, id, p.UserID)
			return err == nil && ok
		default:
			return false
		}
	}
	hub.AllowedOrigins = cfg.WSOrigins
	log.Info("ws origins configured", "n", len(cfg.WSOrigins), "list", cfg.WSOrigins)

	broadcaster := hubBroadcaster{hub: hub}

	// Framework relayx: single semantic — a relay exists when Redis does
	// (multi-replica fanout); without Redis the hub stays single-instance.
	var rel *relayx.Relay
	if rdb != nil {
		rel = relayx.NewRelay("", relayx.NewRedisTransport(rdb), log)
		hub.Relay = rel
		reg.AddFunc("ws-relay", func(ctx context.Context) error {
			return rel.Run(ctx, relayDeliver(hub))
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

// relayDeliver 是 relayx 回调的框架契约实现（FP-20 F2）：远端帧只做本地
// 投递、不再发布。此前误用 Broadcast（= 本地投递 + PublishOut 再发布），
// ≥2 副本时 origin 抑制无法终止——2 副本无限空转、3+ 副本指数倍增。
func relayDeliver(hub *fwwsx.Hub) func(relayx.Envelope) {
	return func(env relayx.Envelope) {
		hub.DeliverRemote(env.Scope, env.Frame)
	}
}

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
