package e2e

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/modelmeta"
	"orkome/internal/contract/wswire"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/logx"
	"orkome/internal/platform/outboxx"
	"orkome/internal/platform/pg"
	"orkome/internal/platform/pgmigrate"
	"orkome/internal/platform/webx"
	"orkome/internal/platform/wsx"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
)

const (
	perfTaskN     = 200
	perfAgents    = 4
	perfOutboxN   = 200
	perfCompleteN = 100
	perfTableN    = 10000
	perfTableIter = 50
)

type perfStack struct {
	t     *testing.T
	pool  *pgxpool.Pool
	db    *pg.DB
	orepo *orchpgrepo.Repo
	wRepo *workpgrepo.Repo
	wsID  string
	user  string
}

func perfEnv(t *testing.T) *perfStack {
	t.Helper()
	if os.Getenv("ORKOME_PERF_BASELINE") != "1" {
		t.Skip("ORKOME_PERF_BASELINE!=1 (baseline skipped by default; rerun command in docs/perf-baseline)")
	}
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	require.NoError(t, err)
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS orkome_perf")
	_, err = admin.Exec(ctx, "CREATE DATABASE orkome_perf")
	require.NoError(t, err)
	_ = admin.Close(ctx)
	t.Cleanup(func() {
		a, err := pgx.Connect(context.Background(), base)
		if err == nil {
			_, _ = a.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_perf WITH (FORCE)")
			_ = a.Close(context.Background())
		}
	})
	dsn := strings.Replace(base, "/orkome", "/orkome_perf", 1)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pgmigrate.New(pool, "../migrations").Up(ctx, 0)
	require.NoError(t, err)

	db := pg.WrapPool(pool)
	s := &perfStack{t: t, pool: pool, db: db,
		orepo: orchpgrepo.New(db), wRepo: &workpgrepo.Repo{DB: db}}
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO "user" (email, display_name) VALUES ('perf@example.com','perf') RETURNING id::text`).Scan(&s.user))
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO workspace (name, slug) VALUES ('perf','perf-ws') RETURNING id::text`).Scan(&s.wsID))
	_, err = pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, s.wsID, s.user)
	require.NoError(t, err)
	require.NoError(t, s.wRepo.EnsureStatusSeed(ctx, s.wsID))
	return s
}

func pct(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(float64(len(sorted))*p/100)) - 1
	if i < 0 {
		i = 0
	}
	return sorted[i]
}

func report(t *testing.T, name string, kv ...string) {
	t.Helper()
	t.Logf("BASELINE|%s|%s", name, strings.Join(kv, "|"))
}

func sortDur(ds []time.Duration) []time.Duration {
	out := append([]time.Duration(nil), ds...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), sql, args...).Scan(&n))
	return n
}

func (s *perfStack) seedAgents(n int) []string {
	modelmeta.Register("perf-fake", func(context.Context, modelmeta.RuntimeConfig, string) (model.BaseChatModel, error) {
		return nil, nil
	})
	ids := make([]string, n)
	for i := range ids {
		require.NoError(s.t, s.pool.QueryRow(context.Background(), `
INSERT INTO agent (workspace_id, name, description, runtime_config)
VALUES ($1,$2,'perf',$3) RETURNING id::text`, s.wsID, fmt.Sprintf("perf-agent-%d", i),
			fmt.Sprintf(`{"provider":"perf-fake","model":"fake-%d"}`, i)).Scan(&ids[i]))
	}
	return ids
}

func (s *perfStack) seedWorkItems(n int) []string {
	ctx := context.Background()
	rows, err := s.pool.Query(ctx, `
WITH base AS (
  SELECT generate_series(1, $2) AS i
)
INSERT INTO work_item (workspace_id, number, title, status_key, priority, creator_type, creator_id, properties)
SELECT $1,
       (SELECT coalesce(max(number),0)+base.i FROM work_item w2 WHERE w2.workspace_id=$1),
       'perf entry '||base.i,
       (ARRAY['backlog','todo','in_progress','in_review','blocked','done','cancelled'])[1 + ((base.i-1) % 7)],
       (base.i-1) % 5, 'user', $3,
       jsonb_build_object('p01', base.i::text, 'p02', (base.i % 100)::text, 'p20', 'x')
FROM base
RETURNING id::text`, s.wsID, n, s.user)
	require.NoError(s.t, err)
	defer rows.Close()
	ids := make([]string, 0, n)
	for rows.Next() {
		var id string
		require.NoError(s.t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.Len(s.t, ids, n)
	return ids
}

func TestPerf_EnqueueToClaim(t *testing.T) {
	s := perfEnv(t)
	ctx := context.Background()
	agents := s.seedAgents(perfAgents)
	wis := s.seedWorkItems(perfTaskN)

	t0 := make(map[string]time.Time, perfTaskN)
	var mu sync.Mutex
	for i := 0; i < perfTaskN; i++ {
		task, err := s.orepo.InsertTask(ctx, orchapp.Task{
			WorkspaceID: s.wsID, AgentID: agents[i%perfAgents], WorkItemID: wis[i],
			Status: "queued", MaxAttempts: 2, Context: map[string]any{"trigger": "assignment"},
			OriginatorUserID: s.user, OriginatorSource: "direct_human", TriggerEvidenceKind: "assignment",
		})
		require.NoError(t, err)
		mu.Lock()
		t0[task.ID] = time.Now()
		mu.Unlock()
	}

	var claimed int64
	latCh := make(chan time.Duration, perfTaskN)
	var wg sync.WaitGroup
	for _, ag := range agents {
		wg.Add(1)
		go func(agentID string) {
			defer wg.Done()
			for atomic.LoadInt64(&claimed) < perfTaskN {
				task, ok, err := s.orepo.ClaimNext(ctx, agentID, 10000)
				if err != nil || !ok {
					continue
				}
				mu.Lock()
				start := t0[task.ID]
				mu.Unlock()
				atomic.AddInt64(&claimed, 1)
				latCh <- time.Since(start)
			}
		}(ag)
	}
	wg.Wait()
	close(latCh)
	var lats []time.Duration
	for l := range latCh {
		lats = append(lats, l)
	}
	require.Len(t, lats, perfTaskN)
	sl := sortDur(lats)
	report(t, "enqueue_to_claim",
		fmt.Sprintf("n=%d", perfTaskN),
		fmt.Sprintf("concurrency=%d", perfAgents),
		fmt.Sprintf("p50_us=%d", sl[len(sl)/2].Microseconds()),
		fmt.Sprintf("p99_us=%d", pct(sl, 99).Microseconds()),
		fmt.Sprintf("max_us=%d", sl[len(sl)-1].Microseconds()),
		"note=claim-loop-no-poll-interval;production-adds-dispatcher-PollEvery-500ms")
}

func TestPerf_CompleteRun(t *testing.T) {
	s := perfEnv(t)
	ctx := context.Background()
	agents := s.seedAgents(1)
	wis := s.seedWorkItems(perfCompleteN)

	taskIDs := make([]string, 0, perfCompleteN)
	for _, wi := range wis {
		task, err := s.orepo.InsertTask(ctx, orchapp.Task{
			WorkspaceID: s.wsID, AgentID: agents[0], WorkItemID: wi, Status: "queued",
			MaxAttempts: 2, Context: map[string]any{"trigger": "assignment"},
			OriginatorUserID: s.user, OriginatorSource: "direct_human",
			TriggerEvidenceKind: "assignment",
		})
		require.NoError(t, err)
		_, ok, err := s.orepo.ClaimNext(ctx, agents[0], 10000)
		require.NoError(t, err)
		require.True(t, ok)
		_, err = s.orepo.StartTask(ctx, task.ID, "perf-runtime", "eino")
		require.NoError(t, err)
		taskIDs = append(taskIDs, task.ID)
	}

	usageBefore := count(t, s.pool, `SELECT count(*) FROM task_usage`)
	durs := make([]time.Duration, 0, len(taskIDs))
	for i, id := range taskIDs {
		u := orchapp.UsageReport{Provider: "perf-fake", Model: "fake-0",
			PromptTokens: 1000, CompletionTokens: 200, CostUSDTicks: 5}
		st := time.Now()
		require.NoError(t, s.orepo.CompleteRun(ctx, id,
			fmt.Sprintf("conclusion #%d", i), "sess-ref-perf", "eino", u))
		durs = append(durs, time.Since(st))
	}
	usageAfter := count(t, s.pool, `SELECT count(*) FROM task_usage`)
	require.Equal(t, len(taskIDs), usageAfter-usageBefore, "exactly one usage row per terminal transaction")
	sl := sortDur(durs)
	var sum time.Duration
	for _, d := range durs {
		sum += d
	}
	report(t, "complete_run_txn",
		fmt.Sprintf("n=%d", len(durs)),
		fmt.Sprintf("avg_us=%d", (sum/time.Duration(len(durs))).Microseconds()),
		fmt.Sprintf("p50_us=%d", sl[len(sl)/2].Microseconds()),
		fmt.Sprintf("p99_us=%d", pct(sl, 99).Microseconds()),
		"rows_per_txn=2(update agent_task_queue + insert task_usage; checkpoint delete 0 rows)")
}

func TestPerf_OutboxOverhead(t *testing.T) {
	s := perfEnv(t)
	ctx := context.Background()
	bus := eventbus.New(logx.New(logx.Options{Level: "error"}))
	agents := s.seedAgents(1)

	round := func(withOutbox bool, wis []string) time.Duration {
		enq := orchapp.NewEnqueueService(s.orepo, bus)
		if withOutbox {
			enq.SetOutbox(outboxx.NewRecorder(s.db), s.db.Within)
		}
		st := time.Now()
		for _, wi := range wis {
			_, reason, err := enq.EnqueueAssignment(ctx, s.wsID, wi, agents[0], s.user)
			require.NoError(t, err)
			require.Equal(t, "queued", reason)
		}
		return time.Since(st)
	}

	durPlain := round(false, s.seedWorkItems(perfOutboxN))
	nOutbox := count(t, s.pool, `SELECT count(*) FROM event_outbox`)
	durOutbox := round(true, s.seedWorkItems(perfOutboxN))
	nOutbox2 := count(t, s.pool, `SELECT count(*) FROM event_outbox`)

	ops := func(d time.Duration) float64 { return float64(perfOutboxN) / d.Seconds() }
	report(t, "outbox_overhead",
		fmt.Sprintf("n_per_round=%d", perfOutboxN),
		fmt.Sprintf("plain_ms=%d", durPlain.Milliseconds()),
		fmt.Sprintf("outbox_ms=%d", durOutbox.Milliseconds()),
		fmt.Sprintf("plain_ops=%.0f", ops(durPlain)),
		fmt.Sprintf("outbox_ops=%.0f", ops(durOutbox)),
		fmt.Sprintf("delta_pct=%.1f", (float64(durOutbox)/float64(durPlain)-1)*100),
		fmt.Sprintf("outbox_rows=%d", nOutbox2-nOutbox))
}

func TestPerf_TableView(t *testing.T) {
	s := perfEnv(t)
	ctx := context.Background()

	for i := 0; i < 20; i++ {
		typ := "text"
		if i%3 == 1 {
			typ = "select"
		} else if i%3 == 2 {
			typ = "number"
		}
		_, err := s.pool.Exec(ctx, `
INSERT INTO work_item_property_def (workspace_id, key, name, type, options)
VALUES ($1,$2,$3,$4, CASE WHEN $4='select' THEN '["a","b"]'::jsonb ELSE '[]'::jsonb END)`,
			s.wsID, fmt.Sprintf("p%02d", i), fmt.Sprintf("property%02d", i), typ)
		require.NoError(t, err)
	}

	_, err := s.pool.Exec(ctx, `
WITH base AS (SELECT generate_series(1, $2) AS i),
props AS (
  SELECT i, jsonb_object_agg('p'||lpad(k::text,2,'0'), ((k*7+i) % 97)::text) AS properties
  FROM base, generate_series(1,20) k GROUP BY i
)
INSERT INTO work_item (workspace_id, number, title, status_key, priority, creator_type, creator_id, properties)
SELECT $1,
       (SELECT coalesce(max(number),0)+base.i FROM work_item w2 WHERE w2.workspace_id=$1),
       'table perf '||base.i,
       (ARRAY['backlog','todo','in_progress','in_review','blocked','done','cancelled'])[1 + ((base.i-1) % 7)],
       (base.i-1) % 5, 'user', $3, props.properties
FROM base JOIN props USING (i)`, s.wsID, perfTableN, s.user)
	require.NoError(t, err)
	got := count(t, s.pool, `SELECT count(*) FROM work_item`)
	require.GreaterOrEqual(t, got, perfTableN)

	views := workapp.NewViewService(s.wRepo)
	filters := workapp.QueryFilters{Status: []string{"todo"}, Assignee: "none"}
	bench := func(name string, fn func() error) {
		var durs []time.Duration
		for i := 0; i < perfTableIter; i++ {
			st := time.Now()
			require.NoError(t, fn())
			durs = append(durs, time.Since(st))
		}
		sl := sortDur(durs)
		report(t, "table_view_"+name,
			fmt.Sprintf("n_items=%d", got),
			fmt.Sprintf("iter=%d", perfTableIter),
			fmt.Sprintf("p50_us=%d", sl[len(sl)/2].Microseconds()),
			fmt.Sprintf("p99_us=%d", pct(sl, 99).Microseconds()),
			fmt.Sprintf("max_us=%d", sl[len(sl)-1].Microseconds()))
	}
	bench("rows", func() error { _, err := views.Rows(ctx, s.wsID, filters, "", 50); return err })
	bench("groups_status", func() error { _, err := views.Groups(ctx, s.wsID, "status", filters); return err })
	bench("groups_assignee", func() error { _, err := views.Groups(ctx, s.wsID, "assignee", filters); return err })
	bench("facets", func() error { _, err := views.Facets(ctx, s.wsID, filters); return err })
}

func TestPerf_WSBroadcastFanout(t *testing.T) {
	if os.Getenv("ORKOME_PERF_BASELINE") != "1" {
		t.Skip("ORKOME_PERF_BASELINE!=1 (baseline skipped by default)")
	}
	conns := 1000
	if v := os.Getenv("PERF_WS_CONNS"); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &conns)
	}
	log := logx.New(logx.Options{Level: "error"})
	hub := wsx.NewHub(log)
	hub.PATResolve = func(context.Context, string) (*webx.Principal, error) {
		return &webx.Principal{UserID: "00000000-0000-0000-0000-000000000001", Source: webx.SourceSession}, nil
	}
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	const wsRef = "00000000-0000-0000-0000-0000000000aa"
	scope := wswire.ScopeKey(wswire.ScopeWorkspace, wsRef)

	connsCh := make(chan *websocket.Conn, conns)
	failCh := make(chan error, conns)
	var wgSub sync.WaitGroup
	sem := make(chan struct{}, 64)
	for i := 0; i < conns; i++ {
		wgSub.Add(1)
		go func() {
			defer wgSub.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				failCh <- err
				return
			}
			_ = c.WriteJSON(map[string]string{"type": "auth", "token": "ork_perf"})
			if !wsExpect(t, c, wswire.ConfirmPrefix+wswire.CtrlAuth) {
				failCh <- fmt.Errorf("auth not confirmed")
				_ = c.Close()
				return
			}
			_ = c.WriteJSON(map[string]string{"type": "subscribe", "scope": "workspace", "id": wsRef})
			if !wsExpect(t, c, wswire.ConfirmPrefix+wswire.CtrlSubscribe) {
				failCh <- fmt.Errorf("subscribe not confirmed")
				_ = c.Close()
				return
			}
			connsCh <- c
		}()
	}
	wgSub.Wait()
	close(connsCh)
	close(failCh)
	for err := range failCh {
		t.Fatalf("connect/subscribe failed: %v", err)
	}
	live := make([]*websocket.Conn, 0, conns)
	for c := range connsCh {
		live = append(live, c)
	}
	require.Len(t, live, conns, "all connections in place")
	t.Cleanup(func() {
		for _, c := range live {
			_ = c.Close()
		}
	})
	wsWaitFor(t, func() bool { return hub.RoomSize(scope) == conns }, "all connections join the room")

	const evtID = "perf-task-completed-1"
	var bcastNanos atomic.Int64
	lat := make(chan time.Duration, conns)
	var wgRead sync.WaitGroup
	for _, c := range live {
		wgRead.Add(1)
		go func(c *websocket.Conn) {
			defer wgRead.Done()
			for {
				var f struct {
					Type    string `json:"type"`
					EventID string `json:"event_id"`
				}
				if err := c.ReadJSON(&f); err != nil {
					return
				}
				if f.EventID == evtID {
					lat <- time.Duration(time.Now().UnixNano() - bcastNanos.Load())
					return
				}
			}
		}(c)
	}
	bcastNanos.Store(time.Now().UnixNano())
	hub.Broadcast(context.Background(), scope, wswire.Frame{Type: "task:completed", EventID: evtID})
	wgRead.Wait()
	close(lat)
	var lats []time.Duration
	for l := range lat {
		lats = append(lats, l)
	}
	require.Len(t, lats, conns)
	sl := sortDur(lats)
	report(t, "ws_broadcast_fanout",
		fmt.Sprintf("conns=%d", conns),
		fmt.Sprintf("p50_us=%d", sl[len(sl)/2].Microseconds()),
		fmt.Sprintf("p99_us=%d", pct(sl, 99).Microseconds()),
		fmt.Sprintf("max_us=%d", sl[len(sl)-1].Microseconds()))
}

func wsExpect(t *testing.T, c *websocket.Conn, frameType string) bool {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	var f struct {
		Type string `json:"type"`
	}
	if err := c.ReadJSON(&f); err != nil {
		return false
	}
	_ = c.SetReadDeadline(time.Time{})
	return f.Type == frameType
}

func wsWaitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("wait timeout: %s", what)
}
