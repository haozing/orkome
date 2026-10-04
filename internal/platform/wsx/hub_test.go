package wsx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/wswire"
	"orkome/internal/platform/webx"
)

const testWS = "11111111-1111-1111-1111-111111111111"

func newHubSrv(t *testing.T, h *Hub, principal *webx.Principal) (*Hub, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if principal != nil {
			r = r.WithContext(webx.WithPrincipal(r.Context(), principal))
		}
		h.ServeWS(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return h, "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(url+"?workspace_id="+testWS, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func readFrame(t *testing.T, c *websocket.Conn) wswire.Frame {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var f wswire.Frame
	require.NoError(t, c.ReadJSON(&f))
	return f
}

func noFrame(t *testing.T, c *websocket.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	var f wswire.Frame
	if err := c.ReadJSON(&f); err == nil {
		t.Fatalf("should receive no frame, got %+v", f)
	}
}

func frame(t *testing.T, eventID string) wswire.Frame {
	p, _ := json.Marshal(map[string]string{"hello": "world"})
	return wswire.Frame{Type: "task:completed", Payload: p, EventID: eventID}
}

func TestRoomDeliveryAndDedupe(t *testing.T) {
	h := NewHub(nil)
	p := &webx.Principal{UserID: "u1", Source: webx.SourceSession}
	_, url := newHubSrv(t, h, p)
	c1 := dial(t, url)
	c2 := dial(t, url)

	require.Eventually(t, func() bool { return h.RoomSize(wswire.ScopeKey(wswire.ScopeWorkspace, testWS)) == 2 },
		2*time.Second, 20*time.Millisecond)

	h.Broadcast(context.Background(), wswire.ScopeKey(wswire.ScopeWorkspace, testWS), frame(t, "e1"))
	assert.Equal(t, "task:completed", readFrame(t, c1).Type)
	assert.Equal(t, "task:completed", readFrame(t, c2).Type)

	h.Broadcast(context.Background(), wswire.ScopeKey(wswire.ScopeWorkspace, testWS), frame(t, "e1"))
	noFrame(t, c1)

	h.Broadcast(context.Background(), wswire.ScopeKey(wswire.ScopeWorkspace, "other"), frame(t, "e2"))
	noFrame(t, c1)
}

func TestSendToUser(t *testing.T) {
	h := NewHub(nil)
	p := &webx.Principal{UserID: "u1", Source: webx.SourceSession}
	_, url := newHubSrv(t, h, p)
	c := dial(t, url)
	require.Eventually(t, func() bool { return h.RoomSize(wswire.ScopeKey(wswire.ScopeUser, "u1")) == 1 },
		2*time.Second, 20*time.Millisecond)

	h.SendToUser(context.Background(), "u1", frame(t, "u1"))
	h.SendToUser(context.Background(), "u2", frame(t, "u2"))
	f := readFrame(t, c)
	assert.Equal(t, "u1", f.EventID)
	noFrame(t, c)
}

func TestFirstFrameAuth(t *testing.T) {
	h := NewHub(nil)
	h.PATResolve = func(_ context.Context, token string) (*webx.Principal, error) {
		if token != "ork_good" {
			return nil, assert.AnError
		}
		return &webx.Principal{UserID: "u9", Source: webx.SourcePAT}, nil
	}
	_, url := newHubSrv(t, h, nil)
	c := dial(t, url)

	require.NoError(t, c.WriteJSON(map[string]string{"type": "auth", "token": "ork_good"}))
	f := readFrame(t, c)
	assert.Equal(t, wswire.ConfirmPrefix+wswire.CtrlAuth, f.Type)
	require.Eventually(t, func() bool { return h.RoomSize(wswire.ScopeKey(wswire.ScopeUser, "u9")) == 1 },
		2*time.Second, 20*time.Millisecond)

	h.SendToUser(context.Background(), "u9", frame(t, "uf"))
	assert.Equal(t, "uf", readFrame(t, c).EventID)

	require.NoError(t, c.WriteJSON(map[string]string{"type": "subscribe", "scope": "workspace", "id": testWS}))
	f = readFrame(t, c)
	assert.Equal(t, wswire.ConfirmPrefix+wswire.CtrlSubscribe, f.Type)
}

func TestFirstFrameAuthReject(t *testing.T) {
	h := NewHub(nil)
	h.PATResolve = func(_ context.Context, _ string) (*webx.Principal, error) {
		return nil, assert.AnError
	}
	_, url := newHubSrv(t, h, nil)
	c := dial(t, url)
	require.NoError(t, c.WriteJSON(map[string]string{"type": "auth", "token": "ork_bad"}))
	f := readFrame(t, c)
	assert.Equal(t, wswire.RejectedPrefix+wswire.CtrlAuth, f.Type)
}

func TestCapabilitiesInHandshake(t *testing.T) {
	caps := []string{wswire.CapTask, wswire.CapChat, wswire.CapNotification}

	t.Run("PAT first-frame auth ack carries it", func(t *testing.T) {
		h := NewHub(nil)
		h.Capabilities = caps
		h.PATResolve = func(_ context.Context, token string) (*webx.Principal, error) {
			return &webx.Principal{UserID: "u9", Source: webx.SourcePAT}, nil
		}
		_, url := newHubSrv(t, h, nil)
		c := dial(t, url)
		require.NoError(t, c.WriteJSON(map[string]string{"type": "auth", "token": "ork_good"}))
		f := readFrame(t, c)
		assert.Equal(t, wswire.ConfirmPrefix+wswire.CtrlAuth, f.Type)
		var p struct {
			Capabilities []string `json:"capabilities"`
		}
		require.NoError(t, json.Unmarshal(f.Payload, &p))
		assert.Equal(t, caps, p.Capabilities)
	})

	t.Run("cookie-authenticated connections get an immediate connected frame", func(t *testing.T) {
		h := NewHub(nil)
		h.Capabilities = caps
		p := &webx.Principal{UserID: "u1", Source: webx.SourceSession}
		_, url := newHubSrv(t, h, p)
		c := dial(t, url)
		f := readFrame(t, c)
		assert.Equal(t, wswire.ConnectedFrameType, f.Type)
		var pl struct {
			Capabilities []string `json:"capabilities"`
		}
		require.NoError(t, json.Unmarshal(f.Payload, &pl))
		assert.Equal(t, caps, pl.Capabilities)
	})

	t.Run("undeclared capabilities mean the ack frame has no payload", func(t *testing.T) {
		h := NewHub(nil)
		h.PATResolve = func(_ context.Context, _ string) (*webx.Principal, error) {
			return &webx.Principal{UserID: "u9", Source: webx.SourcePAT}, nil
		}
		_, url := newHubSrv(t, h, nil)
		c := dial(t, url)
		require.NoError(t, c.WriteJSON(map[string]string{"type": "auth", "token": "ork_good"}))
		f := readFrame(t, c)
		assert.Equal(t, wswire.ConfirmPrefix+wswire.CtrlAuth, f.Type)
		assert.Nil(t, f.Payload)
	})
}

func TestUnknownFrameTypeIgnored(t *testing.T) {
	h := NewHub(nil)
	p := &webx.Principal{UserID: "u1", Source: webx.SourceSession}
	_, url := newHubSrv(t, h, p)
	c := dial(t, url)

	require.NoError(t, c.WriteJSON(map[string]any{"type": "future:control", "payload": map[string]string{"x": "y"}}))

	require.NoError(t, c.WriteJSON(map[string]any{"type": "", "token": "whatever"}))

	require.NoError(t, c.WriteMessage(websocket.TextMessage, []byte("not-json")))

	require.NoError(t, c.WriteJSON(map[string]string{"type": "subscribe", "scope": "task", "id": "t1"}))
	f := readFrame(t, c)
	assert.Equal(t, wswire.ConfirmPrefix+wswire.CtrlSubscribe, f.Type)
	noFrame(t, c)
}

func TestSubscribeAuthorization(t *testing.T) {
	h := NewHub(nil)
	h.Authorize = func(_ context.Context, p *webx.Principal, connWS, scope, id string) bool {
		return p != nil && connWS == testWS && scope == wswire.ScopeTask && id == "t1"
	}
	p := &webx.Principal{UserID: "u1", Source: webx.SourceSession}
	_, url := newHubSrv(t, h, p)
	c := dial(t, url)

	require.NoError(t, c.WriteJSON(map[string]string{"type": "subscribe", "scope": "task", "id": "t2"}))
	f := readFrame(t, c)
	assert.Equal(t, wswire.RejectedPrefix+wswire.CtrlSubscribe, f.Type)

	require.NoError(t, c.WriteJSON(map[string]string{"type": "subscribe", "scope": "task", "id": "t1"}))
	f = readFrame(t, c)
	assert.Equal(t, wswire.ConfirmPrefix+wswire.CtrlSubscribe, f.Type)

	h.Broadcast(context.Background(), wswire.ScopeKey(wswire.ScopeTask, "t1"), frame(t, "t1f"))
	assert.Equal(t, "t1f", readFrame(t, c).EventID)
}

func TestOriginCheck(t *testing.T) {
	h := NewHub(nil)
	_, url := newHubSrv(t, h, nil)
	hd := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	_, resp, err := hd.Dial(url, http.Header{"Origin": []string{"https://evil.example"}})
	require.Error(t, err, "malicious Origin should be rejected")
	if resp != nil {
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	}
}

func TestSlowConsumerEvicted(t *testing.T) {
	h := NewHub(nil)
	p := &webx.Principal{UserID: "u1", Source: webx.SourceSession}
	_, url := newHubSrv(t, h, p)
	c := dial(t, url)
	scope := wswire.ScopeKey(wswire.ScopeWorkspace, testWS)
	require.Eventually(t, func() bool { return h.RoomSize(scope) == 1 }, 2*time.Second, 20*time.Millisecond)

	for i := 0; i < sendBuffer+32; i++ {
		h.Broadcast(context.Background(), scope, frame(t, "evict-"+strings.Repeat("a", i+1)+time.Now().Format("150405.000000000")))
	}
	require.Eventually(t, func() bool { return h.RoomSize(scope) == 0 }, 3*time.Second, 20*time.Millisecond,
		"slow connections should be evicted from the room")
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var f wswire.Frame
		if err := c.ReadJSON(&f); err != nil {
			break
		}
	}
}

type fakeMetrics struct {
	incr   map[string]int
	gauges map[string]float64
}

func (f *fakeMetrics) Incr(name string) { f.incr[name]++ }
func (f *fakeMetrics) Gauge(name string, v float64) {
	if f.gauges == nil {
		f.gauges = map[string]float64{}
	}
	f.gauges[name] = v
}

func TestWSConnectionGauge(t *testing.T) {
	m := &fakeMetrics{incr: map[string]int{}}
	h := NewHub(nil)
	h.Metrics = m
	p := &webx.Principal{UserID: "u1", Source: webx.SourceSession}
	_, url := newHubSrv(t, h, p)
	c := dial(t, url)
	require.Eventually(t, func() bool { return m.gauges["ws_connections"] == 1 }, 2*time.Second, 20*time.Millisecond)
	_ = c.Close()
	require.Eventually(t, func() bool { return m.gauges["ws_connections"] == 0 }, 3*time.Second, 50*time.Millisecond,
		"connection gauge should return to zero after disconnect")
}

func TestWSEvictionCounted(t *testing.T) {
	m := &fakeMetrics{incr: map[string]int{}}
	h := NewHub(nil)
	h.Metrics = m
	p := &webx.Principal{UserID: "u1", Source: webx.SourceSession}
	_, url := newHubSrv(t, h, p)
	c := dial(t, url)
	scope := wswire.ScopeKey(wswire.ScopeWorkspace, testWS)
	require.Eventually(t, func() bool { return h.RoomSize(scope) == 1 }, 2*time.Second, 20*time.Millisecond)
	_ = c

	for i := 0; i < sendBuffer+32; i++ {
		h.Broadcast(context.Background(), scope, frame(t, "evict-"+strings.Repeat("b", i+1)+time.Now().Format("150405.000000000")))
	}
	require.Eventually(t, func() bool { return h.RoomSize(scope) == 0 }, 3*time.Second, 20*time.Millisecond)
	assert.Equal(t, 1, m.incr["ws_evictions"], "eviction count exactly once (concurrent full-buffer collisions deduped by CAS)")
}
