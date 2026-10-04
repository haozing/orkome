package relayx

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/wswire"
	"orkome/internal/platform/logx"
)

func redisUp(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "localhost:55379"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		t.Skip("Redis unreachable (orkome-redis not started)")
	}

	_ = c.Del(context.Background(),
		"testrelay:0", "testrelay:1", "testrelay:2", "testrelay:3",
		"testrelay:4", "testrelay:5", "testrelay:6", "testrelay:7").Err()
	t.Cleanup(func() { _ = c.Close() })
	return c
}

type captureHub struct {
	mu     sync.Mutex
	frames map[string][]wswire.Frame
}

func (h *captureHub) Broadcast(_ context.Context, scope string, f wswire.Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.frames == nil {
		h.frames = map[string][]wswire.Frame{}
	}
	h.frames[scope] = append(h.frames[scope], f)
}

func (h *captureHub) of(scope string) []wswire.Frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.frames[scope]
}

func TestRelayxDualReplica(t *testing.T) {
	rdb := redisUp(t)
	ctx := context.Background()
	log := logx.New(logx.Options{Level: "error"})

	hubB := &captureHub{}
	relayB := &Relay{RDB: rdb, Prefix: "testrelay", Shards: 4, Hub: hubB, Log: log}
	bctx, bcancel := context.WithCancel(ctx)
	go func() { _ = relayB.Run(bctx) }()
	defer bcancel()
	time.Sleep(150 * time.Millisecond)

	relayA := &Relay{RDB: rdb, Prefix: "testrelay", Shards: 4, Hub: &captureHub{}, Log: log}
	scopeA := wswire.ScopeKey(wswire.ScopeWorkspace, "ws-a")
	scopeB := wswire.ScopeKey(wswire.ScopeWorkspace, "ws-b")
	fA := wswire.Frame{Type: "task:completed", EventID: "evt-a-1"}
	fB := wswire.Frame{Type: "chat:message", EventID: "evt-b-1"}
	require.NoError(t, relayA.Publish(ctx, scopeA, fA))
	require.NoError(t, relayA.Publish(ctx, scopeB, fB))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(hubB.of(scopeA)) >= 1 && len(hubB.of(scopeB)) >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	got := hubB.of(scopeA)
	require.Len(t, got, 1, "replica A's writes should reach replica B (scope isolation)")
	assert.Equal(t, "evt-a-1", got[0].EventID, "event_id identical (06 acceptance wording)")
	assert.Equal(t, "task:completed", got[0].Type)
	gotB := hubB.of(scopeB)
	require.Len(t, gotB, 1)
	assert.Equal(t, "evt-b-1", gotB[0].EventID)
}

func TestRelayxStartupReplay(t *testing.T) {
	rdb := redisUp(t)
	ctx := context.Background()
	log := logx.New(logx.Options{Level: "error"})

	relayA := &Relay{RDB: rdb, Prefix: "testrelay", Shards: 4, Hub: &captureHub{}, Log: log}
	scope := wswire.ScopeKey(wswire.ScopeWorkspace, "ws-replay")
	require.NoError(t, relayA.Publish(ctx, scope, wswire.Frame{Type: "task:queued", EventID: "evt-replay-1"}))

	time.Sleep(100 * time.Millisecond)
	hubB := &captureHub{}
	relayB := &Relay{RDB: rdb, Prefix: "testrelay", Shards: 4, Hub: hubB, Log: log,
		ReplayWindow: 5 * time.Minute}
	bctx, bcancel := context.WithCancel(ctx)
	go func() { _ = relayB.Run(bctx) }()
	defer bcancel()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(hubB.of(scope)) >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.Len(t, hubB.of(scope), 1, "startup replay window: events published inside the window should be caught up (06)")
}

func TestRelayxShardDeterministic(t *testing.T) {
	r := &Relay{Shards: 8}
	s := "workspace:abc"
	assert.Equal(t, r.shardOf(s), r.shardOf(s))
	assert.Less(t, r.shardOf(s), 8)
	assert.GreaterOrEqual(t, r.shardOf(s), 0)
}

func TestRelayxParseMode(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		hasRedis bool
		want     Mode
		wantErr  bool
	}{
		{"empty + no Redis → memory", "", false, ModeMemory, false},
		{"empty + Redis → sharded", "", true, ModeSharded, false},
		{"memory explicit (valid even with Redis — single-node deployment)", "memory", true, ModeMemory, false},
		{"sharded+Redis", "sharded", true, ModeSharded, false},
		{"mirrored+Redis", "mirrored", true, ModeMirrored, false},
		{"sharded without Redis = fail-fast", "sharded", false, "", true},
		{"mirrored without Redis = fail-fast", "mirrored", false, "", true},
		{"case and whitespace normalized", "Mirrored ", true, ModeMirrored, false},
		{"invalid value", "fanout", true, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseMode(tc.raw, tc.hasRedis)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

type dedupHub struct {
	mu      sync.Mutex
	seen    map[string]struct{}
	deliver []wswire.Frame
}

func (h *dedupHub) Broadcast(_ context.Context, scope string, f wswire.Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seen == nil {
		h.seen = map[string]struct{}{}
	}
	if _, dup := h.seen[f.EventID]; dup {
		return
	}
	h.seen[f.EventID] = struct{}{}
	h.deliver = append(h.deliver, f)
}

func TestRelayxFanoutModes(t *testing.T) {
	rdb := redisUp(t)

	for _, m := range []Mode{ModeMemory, ModeSharded, ModeMirrored} {
		for i := 0; i < 4; i++ {
			_ = rdb.Del(context.Background(), "testfan:"+string(m)+":"+strconv.Itoa(i)).Err()
		}
	}
	ctx := context.Background()
	log := logx.New(logx.Options{Level: "error"})
	scope := wswire.ScopeKey(wswire.ScopeWorkspace, "ws-fanout")

	fan := func(mode Mode) (localCount int, streamLen int64) {
		rel := &Relay{RDB: rdb, Prefix: "testfan:" + string(mode), Shards: 4, Hub: &captureHub{}, Log: log, Mode: mode}
		frame := wswire.Frame{Type: "task:completed", EventID: "evt-" + string(mode)}
		err := rel.Fanout(ctx, scope, frame, func() { localCount++ })
		require.NoError(t, err)
		n, _ := rdb.XLen(ctx, rel.stream(rel.shardOf(scope))).Result()
		return localCount, n
	}

	l, s := fan(ModeMemory)
	assert.Equal(t, 1, l, "memory: local fan-out once")
	assert.Zero(t, s, "memory: no stream published")

	l, s = fan(ModeSharded)
	assert.Zero(t, l, "sharded: no local fan-out (done by the consume loop)")
	assert.EqualValues(t, 1, s, "sharded: events enter the stream once")

	l, s = fan(ModeMirrored)
	assert.Equal(t, 1, l, "mirrored: local immediate fan-out")
	assert.EqualValues(t, 1, s, "mirrored: enters the stream too")
}

func TestRelayxMirroredSameEventIDDedup(t *testing.T) {
	rdb := redisUp(t)
	ctx := context.Background()
	log := logx.New(logx.Options{Level: "error"})

	_ = rdb.Del(ctx, "testmir:0", "testmir:1", "testmir:2", "testmir:3").Err()

	const evtID = "evt-mirrored-1"
	frame := wswire.Frame{Type: "task:completed", EventID: evtID}
	scope := wswire.ScopeKey(wswire.ScopeWorkspace, "ws-mir")

	hub := &dedupHub{}
	consumer := &Relay{RDB: rdb, Prefix: "testmir", Shards: 4, Hub: hub, Log: log}
	cctx, cancel := context.WithCancel(ctx)
	go func() { _ = consumer.Run(cctx) }()
	defer cancel()

	warm := wswire.Frame{Type: "warmup", EventID: "evt-warm"}
	require.NoError(t, consumer.Publish(ctx, scope, warm))
	waitFor(t, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.deliver) >= 1 && hub.deliver[0].EventID == "evt-warm"
	}, "consume loop in place (warm-up frame arrived)")

	pub := &Relay{RDB: rdb, Prefix: "testmir", Shards: 4, Hub: hub, Log: log, Mode: ModeMirrored}
	require.NoError(t, pub.Fanout(ctx, scope, frame, func() { hub.Broadcast(ctx, scope, frame) }))

	deadline := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		n := len(hub.deliver)
		hub.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	require.Len(t, hub.deliver, 2, "warm-up + formal = two frames (each exactly once after dedup)")
	assert.Equal(t, "evt-warm", hub.deliver[0].EventID)
	assert.Equal(t, evtID, hub.deliver[1].EventID, "formal frames share the event_id (idempotent key)")
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("wait timeout: %s", what)
}
