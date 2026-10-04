package wsx

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"orkome/internal/contract/wswire"
	"orkome/internal/platform/webx"
)

const (
	sendBuffer = 16
	dedupDepth = 128
	readLimit  = 64 << 10
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 25 * time.Second
	authGrace  = 30 * time.Second
	maxOrigins = 32
)

type Broadcaster interface {
	Broadcast(ctx context.Context, scope string, frame wswire.Frame)
	SendToUser(ctx context.Context, userID string, frame wswire.Frame)
}

type Metrics interface {
	Incr(name string)
	Gauge(name string, v float64)
}

type Authorize func(ctx context.Context, p *webx.Principal, connWorkspace, scopePrefix, id string) bool

type PATResolve func(ctx context.Context, token string) (*webx.Principal, error)

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*client]struct{}

	conns atomic.Int64

	Log *slog.Logger

	Metrics Metrics

	Authorize Authorize

	PATResolve PATResolve

	OnSubscribe   func(scope string)
	OnUnsubscribe func(scope string)

	AllowedOrigins []string

	Capabilities []string

	upgrader websocket.Upgrader
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		rooms: make(map[string]map[*client]struct{}),
		Log:   log,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
}

func (h *Hub) Broadcast(_ context.Context, scope string, frame wswire.Frame) {
	data, err := json.Marshal(frame)
	if err != nil {
		return
	}
	h.mu.RLock()
	room := h.rooms[scope]
	if room == nil {
		h.mu.RUnlock()
		return
	}
	targets := make([]*client, 0, len(room))
	for c := range room {
		targets = append(targets, c)
	}
	h.mu.RUnlock()
	for _, c := range targets {
		c.deliver(frame.EventID, data)
	}
}

func (h *Hub) SendToUser(ctx context.Context, userID string, frame wswire.Frame) {
	h.Broadcast(ctx, wswire.ScopeKey(wswire.ScopeUser, userID), frame)
}

func (h *Hub) Rooms() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms)
}

func (h *Hub) RoomSize(scope string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms[scope])
}

func (h *Hub) join(c *client, scope string) {
	h.mu.Lock()
	if h.rooms[scope] == nil {
		h.rooms[scope] = make(map[*client]struct{})
		if h.OnSubscribe != nil {
			h.OnSubscribe(scope)
		}
	}
	c.scopes[scope] = struct{}{}
	h.rooms[scope][c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) leave(c *client, scope string) {
	h.mu.Lock()
	if room := h.rooms[scope]; room != nil {
		delete(room, c)
		if len(room) == 0 {
			delete(h.rooms, scope)
			if h.OnUnsubscribe != nil {
				h.OnUnsubscribe(scope)
			}
		}
	}
	delete(c.scopes, scope)
	h.mu.Unlock()
}

func (h *Hub) drop(c *client) {

	if !c.dropped.CompareAndSwap(false, true) {
		return
	}
	h.mu.Lock()
	for scope := range c.scopes {
		if room := h.rooms[scope]; room != nil {
			delete(room, c)
			if len(room) == 0 {
				delete(h.rooms, scope)
				if h.OnUnsubscribe != nil {
					h.OnUnsubscribe(scope)
				}
			}
		}
	}
	h.mu.Unlock()
	h.conns.Add(-1)
	h.observeConns()
}

func (h *Hub) observeConns() {
	if h.Metrics != nil {
		h.Metrics.Gauge("ws_connections", float64(h.conns.Load()))
	}
}

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	if !h.originOK(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	h.conns.Add(1)
	h.observeConns()
	p := webx.PrincipalFrom(r.Context())
	connWS := wsHeader(r)
	c := &client{
		hub: h, conn: conn, send: make(chan []byte, sendBuffer),
		scopes: make(map[string]struct{}), dedup: make(map[string]struct{}, dedupDepth),
		principal: p, authed: p != nil, connWS: connWS,
	}
	if p != nil {
		h.join(c, wswire.ScopeKey(wswire.ScopeWorkspace, connWS))
		h.join(c, wswire.ScopeKey(wswire.ScopeUser, p.UserID))

		if len(h.Capabilities) > 0 {
			c.sendJSON(wswire.ConnectedFrameType, h.capabilitiesPayload())
		}
	}
	go c.writePump()
	go c.readPump()
}

func (h *Hub) capabilitiesPayload() any {
	if len(h.Capabilities) == 0 {
		return nil
	}
	return map[string]any{"capabilities": h.Capabilities}
}

func wsHeader(r *http.Request) string {

	return r.URL.Query().Get("workspace_id")
}

func (h *Hub) originOK(r *http.Request) bool {
	orig := r.Header.Get("Origin")
	if orig == "" {
		return true
	}
	if len(h.AllowedOrigins) == 0 {

		u := strings.TrimPrefix(orig, "http://")
		u = strings.TrimPrefix(u, "https://")
		return u == r.Host
	}
	for _, a := range h.AllowedOrigins {
		if a == "*" || a == orig {
			return true
		}
	}
	return false
}

func (h *Hub) handleControl(c *client, raw []byte) {
	var in struct {
		Type  string `json:"type"`
		Token string `json:"token"`
		Scope string `json:"scope"`
		ID    string `json:"id"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return
	}
	switch in.Type {
	case wswire.CtrlAuth:
		if c.authed {
			return
		}
		if h.PATResolve == nil || !strings.HasPrefix(in.Token, "ork_") {
			c.sendJSON(wswire.RejectedPrefix+wswire.CtrlAuth, nil)
			return
		}
		p, err := h.PATResolve(context.Background(), in.Token)
		if err != nil || p == nil {
			c.sendJSON(wswire.RejectedPrefix+wswire.CtrlAuth, nil)
			return
		}
		c.principal, c.authed = p, true
		h.join(c, wswire.ScopeKey(wswire.ScopeUser, p.UserID))
		if ws := c.pendingWS; ws != "" {
			h.join(c, wswire.ScopeKey(wswire.ScopeWorkspace, ws))
		}

		c.sendJSON(wswire.ConfirmPrefix+wswire.CtrlAuth, h.capabilitiesPayload())
	case wswire.CtrlSubscribe:
		if !c.authed {
			c.sendJSON(wswire.RejectedPrefix+wswire.CtrlSubscribe, nil)
			return
		}
		switch in.Scope {
		case wswire.ScopeWorkspace:
			if ws := in.ID; ws != "" {
				c.pendingWS = ws
				h.join(c, wswire.ScopeKey(wswire.ScopeWorkspace, ws))
				c.sendJSON(wswire.ConfirmPrefix+wswire.CtrlSubscribe, map[string]string{"scope": in.Scope})
			}
		case wswire.ScopeTask, wswire.ScopeChat:
			if h.Authorize != nil && !h.Authorize(context.Background(), c.principal, c.connWS, in.Scope, in.ID) {
				c.sendJSON(wswire.RejectedPrefix+wswire.CtrlSubscribe, map[string]string{"scope": in.Scope})
				return
			}
			h.join(c, wswire.ScopeKey(in.Scope, in.ID))
			c.sendJSON(wswire.ConfirmPrefix+wswire.CtrlSubscribe, map[string]string{"scope": in.Scope})
		default:

		}
	case wswire.CtrlUnsub:
		if in.Scope != "" && in.ID != "" {
			h.leave(c, wswire.ScopeKey(in.Scope, in.ID))
		}
	}
}
