package main

// FP-06 P1-1 / FP-20 F1-F3 的装配层最小断言：workspace scope 复活、
// task/chat 订阅 fence、relay 回调不再回环发布、batch 帧携带 WorkspaceID。
// 全部走真实 fwwsx.Hub（内存模式、无 Redis/DB），帧级断言用 gorilla
// websocket 客户端，对照 hub_test.go 的测试手法。

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/relayx"
	"github.com/haozing/ploykit/platform/webx"
	"github.com/haozing/ploykit/platform/wswire"
	fwwsx "github.com/haozing/ploykit/platform/wsx"
	"orkome/internal/contract/events"
	"orkome/internal/platform/eventbus"
)

const (
	testWSA   = "11111111-1111-1111-1111-111111111111" // 成员所在工作区
	testWSB   = "22222222-2222-2222-2222-222222222222" // 非成员工作区
	testTask  = "task-in-wsA"
	testTaskX = "task-in-wsB"
	testSess  = "session-in-wsA"
	testSessX = "session-in-wsB"
)

// wsMembership 是 fake 成员表：user-1 只在 wsA。
func wsMembership(_ context.Context, ws, userID string) (bool, error) {
	return ws == testWSA && userID == "user-1", nil
}

func wsTestFence() subScopeFence {
	return subScopeFence{
		task: func(_ context.Context, ws, id string) (bool, error) {
			return ws == testWSA && id == testTask, nil
		},
		chat: func(_ context.Context, ws, id, userID string) (bool, error) {
			// 裁决 D1：chat scope creator 私有——非 creator 订阅同区会话也拒绝
			return ws == testWSA && id == testSess && userID == "user-1", nil
		},
	}
}

func wsTestPrincipal() *webx.Principal {
	return &webx.Principal{UserID: "user-1", Source: webx.SourceSession}
}

// newTestHub 走真实 wiringWS（内存 relay 模式）。帧级用例经 dialWS 挂载，
// auth 中间件注入 principal（对应生产 Authenticate 的产物）。
func newTestHub(t *testing.T, principal *webx.Principal) (*fwwsx.Hub, fwwsx.Broadcaster) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return wiringWS(context.Background(), log, eventbus.New(log),
		wsMembership, wsTestFence(), nil, &Config{}, nil, NewWorkers(log))
}

func dialWS(t *testing.T, hub *fwwsx.Hub, principal *webx.Principal, connWS string) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal != nil {
			r = r.WithContext(webx.WithPrincipal(r.Context(), principal))
		}
		hub.ServeWS(w, r)
	}))
	t.Cleanup(srv.Close)
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws?workspace_id="+connWS, nil)
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

// subscribe 发一条订阅控制帧并返回 hub 的应答类型。
func subscribe(t *testing.T, c *websocket.Conn, scope, id string) string {
	t.Helper()
	require.NoError(t, c.WriteJSON(map[string]string{"type": "subscribe", "scope": scope, "id": id}))
	return readFrame(t, c).Type
}

// --- 修复 2：workspace scope 复活（WorkspaceMember 接线） ---

func TestWiringWS_WorkspaceMemberGate(t *testing.T) {
	hub, cast := newTestHub(t, wsTestPrincipal())
	require.NotNil(t, hub.WorkspaceMember, "wiringWS must set hub.WorkspaceMember (FP-06 P1-1)")

	p := wsTestPrincipal()
	require.True(t, hub.WorkspaceMember(context.Background(), p, testWSA))
	require.False(t, hub.WorkspaceMember(context.Background(), p, testWSB))
	require.False(t, hub.WorkspaceMember(context.Background(), nil, testWSA), "nil principal fail-closed")

	// 帧投递路径：成员握手 auto-join 后能收到工作区帧。
	member := dialWS(t, hub, wsTestPrincipal(), testWSA)
	require.Equal(t, wswire.ConnectedFrameType, readFrame(t, member).Type)
	cast.Broadcast(context.Background(), wswire.ScopeKey(wswire.ScopeWorkspace, testWSA),
		wswire.Frame{Type: "comment:created", Payload: []byte(`{}`)})
	require.Equal(t, "comment:created", readFrame(t, member).Type)

	// 非成员：握手成功（connected 照发）但收不到他区帧。
	outsider := dialWS(t, hub, &webx.Principal{UserID: "user-2", Source: webx.SourceSession}, testWSB)
	require.Equal(t, wswire.ConnectedFrameType, readFrame(t, outsider).Type)
	cast.Broadcast(context.Background(), wswire.ScopeKey(wswire.ScopeWorkspace, testWSB),
		wswire.Frame{Type: "comment:created", Payload: []byte(`{}`)})
	noFrame(t, outsider)
}

// --- 修复 3：Authorize 消费 scope/id（task/chat 订阅 fence，FP-20 F1） ---

func TestWiringWS_AuthorizeScopeFence(t *testing.T) {
	hub, cast := newTestHub(t, wsTestPrincipal())
	ctx := context.Background()
	p := wsTestPrincipal()
	outsider := &webx.Principal{UserID: "user-2", Source: webx.SourceSession}

	require.True(t, hub.Authorize(ctx, p, testWSA, "task", testTask), "workspace 内 task 可订阅")
	require.False(t, hub.Authorize(ctx, p, testWSA, "task", testTaskX), "他区 task 拒绝（IDOR 修复）")
	require.True(t, hub.Authorize(ctx, p, testWSA, "chat", testSess), "workspace 内会话可订阅")
	require.False(t, hub.Authorize(ctx, p, testWSA, "chat", testSessX), "他区会话拒绝")
	require.False(t, hub.Authorize(ctx, p, testWSA, "task", ""), "空 id fail-closed")
	require.False(t, hub.Authorize(ctx, p, testWSA, "notification", "x"), "未授权 scope fail-closed")
	require.False(t, hub.Authorize(ctx, outsider, testWSA, "task", testTask), "非成员连接拒绝")
	require.False(t, hub.Authorize(ctx, p, "", "task", testTask), "空连接工作区拒绝")
	require.False(t, hub.Authorize(ctx, nil, testWSA, "task", testTask), "nil principal 拒绝")

	// 帧级拒绝/确认路径 + task scope 帧投递。
	c := dialWS(t, hub, wsTestPrincipal(), testWSA)
	require.Equal(t, wswire.ConnectedFrameType, readFrame(t, c).Type)
	require.Equal(t, wswire.RejectedPrefix+wswire.CtrlSubscribe, subscribe(t, c, "task", testTaskX))
	require.Equal(t, wswire.ConfirmPrefix+wswire.CtrlSubscribe, subscribe(t, c, "task", testTask))
	cast.Broadcast(context.Background(), wswire.ScopeKey("task", testTask),
		wswire.Frame{Type: "task:completed", Payload: []byte(`{}`)})
	require.Equal(t, "task:completed", readFrame(t, c).Type)
}

// --- 修复 4：relay 回调只本地投递、不再回环发布（FP-20 F2） ---

type relayRecorder struct{ publishes int }

func (r *relayRecorder) PublishOut(context.Context, string, wswire.Frame) { r.publishes++ }

func TestWiringWS_RelayDeliverDoesNotRepublish(t *testing.T) {
	hub, cast := newTestHub(t, wsTestPrincipal())
	c := dialWS(t, hub, wsTestPrincipal(), testWSA)
	require.Equal(t, wswire.ConnectedFrameType, readFrame(t, c).Type)

	rec := &relayRecorder{}
	hub.Relay = rec // 内存模式下 wiringWS 不设 Relay；挂探针观测再发布

	remote := wswire.Frame{Type: "batch:created", Payload: []byte(`{}`)}
	relayDeliver(hub)(relayx.Envelope{Scope: wswire.ScopeKey(wswire.ScopeWorkspace, testWSA), Frame: remote})
	require.Equal(t, "batch:created", readFrame(t, c).Type, "远端帧应本地投递给订阅者")
	require.Zero(t, rec.publishes, "DeliverRemote 不得再发布（回环修复断言）")

	cast.Broadcast(context.Background(), wswire.ScopeKey(wswire.ScopeWorkspace, testWSA),
		wswire.Frame{Type: "batch:completed", Payload: []byte(`{}`)})
	require.Equal(t, 1, rec.publishes, "对照：Broadcast 会 PublishOut——探针有效，DeliverRemote 的零发布是语义而非巧合")
}

// --- 修复 5：batchEmitter 携带 WorkspaceID（FP-20 F3） ---

func TestBatchEmitter_CarriesWorkspaceID(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(log)
	var got []events.Envelope
	bus.On(eventbus.PhaseBroadcast, func(_ context.Context, e events.Envelope) { got = append(got, e) })
	emit := batchEmitter{bus: bus}

	emit.Emit(context.Background(), events.BatchCreated, map[string]any{
		"batch_id": "b1", "workspace_id": testWSA})
	require.Len(t, got, 1)
	require.Equal(t, testWSA, got[0].WorkspaceID,
		"Envelope 必须带 WorkspaceID，否则 wiring_ws 广播门禁整族丢弃 batch 帧")

	emit.Emit(context.Background(), events.BatchProgress, map[string]any{"batch_id": "b2"})
	require.Len(t, got, 2)
	require.Empty(t, got[1].WorkspaceID, "payload 缺 workspace_id 时空串（门禁丢弃路径保持不变）")
}
