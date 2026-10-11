package pgrepo

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/pg"
	autoppgrepo "orkome/internal/automation/adapters/pgrepo"
	autopapp "orkome/internal/automation/app"
	autodomain "orkome/internal/automation/domain"
	orchapp "orkome/internal/orchestration/app"
)

type captureEmitter struct {
	mu   sync.Mutex
	seen map[string]int
}

func (c *captureEmitter) EmitBatchProgress(ctx context.Context, batchID string, done, total int) error {
	c.mu.Lock()
	c.seen["progress"]++
	c.mu.Unlock()
	return nil
}

func TestI13_BatchMilestoneThrottle(t *testing.T) {
	db, _, ws, wi, user := batchEnv(t)
	repo := &BatchRepo{DB: db}
	ce := &captureEmitter{seen: map[string]int{}}
	svc := orchapp.NewBatchService(repo, ce)

	var payloads []map[string]any
	for i := 0; i < 100; i++ {
		payloads = append(payloads, map[string]any{"attachment_id": "att", "n": i})
	}
	b, err := svc.CreateAndMaterialize(context.Background(), orchapp.Batch{
		WorkspaceID: ws, WorkItemID: wi, Kind: "ocr", Concurrency: 8, ChunkSize: 25,
		OriginatorUserID: user,
	}, payloads)
	require.NoError(t, err)

	proc := &fakeOCR{}
	n, err := svc.RunWorker(context.Background(), *b, proc, "w1", 50)
	require.NoError(t, err)
	assert.Equal(t, 100, n)

	ce.mu.Lock()
	progressEvents := ce.seen["progress"]
	ce.mu.Unlock()
	assert.LessOrEqual(t, progressEvents, 6, "100 items chunk=25 → milestone events ≤ 4 + wrap-up (I13: never one per item)")
	assert.Greater(t, progressEvents, 0)

	pending, claimed, processing, done, err := svc.Counts(context.Background(), b.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, pending+claimed+processing)
	assert.Equal(t, 100, done)
	got, ok, err := svc.Get(context.Background(), ws, b.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "completed", got.Status)
}

func TestB17_BatchDoneWakeupFifthKind(t *testing.T) {
	db, pool, ws, wi, user := batchEnv(t)
	repo := &BatchRepo{DB: db}
	svc := orchapp.NewBatchService(repo, nil)

	_, err := pool.Exec(context.Background(), `
INSERT INTO agent (workspace_id, name, description, runtime_config)
VALUES ($1, 'b17-agent', '', '{"provider":"fake","model":"m"}'::jsonb)`, ws)
	require.NoError(t, err)
	var agentID string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT id::text FROM agent WHERE workspace_id=$1`, ws).Scan(&agentID))
	_, err = pool.Exec(context.Background(), `
INSERT INTO work_item_wakeup (workspace_id, work_item_id, agent_id, created_by, kind, instruction, enabled, condition)
VALUES ($1,$2,$3,$4,'condition','batch done then wrap up',true,
  '{"field":"batch","op":"eq","value":"done"}'::jsonb)`, ws, wi, agentID, user)
	require.NoError(t, err)

	notified := make(chan string, 2)
	var mu sync.Mutex
	fired := ""
	wu := newWakeupSvcForBatch(t, db, pool)
	svc.SetBatchPorts(
		notifierFunc(func(ctx context.Context, w, item, batch string) error {
			require.NoError(t, wu.NotifyBatchDone(ctx, w, item, batch))
			mu.Lock()
			fired = "yes"
			mu.Unlock()
			notified <- "done"
			return nil
		}),
		nil)

	var payloads []map[string]any
	for i := 0; i < 10; i++ {
		payloads = append(payloads, map[string]any{"n": i})
	}
	b, err := svc.CreateAndMaterialize(context.Background(), orchapp.Batch{
		WorkspaceID: ws, WorkItemID: wi, Kind: "ocr", OriginatorUserID: user,
	}, payloads)
	require.NoError(t, err)
	_, err = svc.RunWorker(context.Background(), *b, &fakeOCR{}, "w1", 10)
	require.NoError(t, err)

	<-notified
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "yes", fired)

	var tasks int
	require.NoError(t, pool.QueryRow(context.Background(), `
SELECT count(*) FROM agent_task_queue WHERE workspace_id=$1 AND agent_id=$2`, ws, agentID).Scan(&tasks))
	assert.Greater(t, tasks, 0, "batch_done should trigger the agent wrap-up task (internal archive 05 §2.14 fifth form)")
}

type notifierFunc func(ctx context.Context, ws, wi, batch string) error

func (f notifierFunc) NotifyBatchDone(ctx context.Context, ws, wi, batch string) error {
	return f(ctx, ws, wi, batch)
}

func newWakeupSvcForBatch(t *testing.T, db *pg.DB, pool *pgxpool.Pool) *autopapp.WakeupService {
	t.Helper()
	arepo := autoppgrepo.New(db)
	orepo := New(db)
	enq := batchTestEnq{repo: orepo}
	return autopapp.NewWakeupService(arepo, enq, batchFacts{pool: pool})
}

type batchTestEnq struct{ repo *Repo }

func (e batchTestEnq) EnqueueForWakeup(ctx context.Context, ws, wi, agentID, userID, evidence string) (string, bool, error) {
	tk, err := e.repo.InsertTask(ctx, orchapp.Task{
		WorkspaceID: ws, AgentID: agentID, WorkItemID: wi, Status: "queued", MaxAttempts: 2,
		Context:          map[string]any{"trigger": "wakeup", "wakeup_note": evidence},
		OriginatorUserID: userID, AccountableUserID: userID,
		OriginatorSource: "trigger_owner", TriggerEvidenceKind: "rule",
	})
	if err != nil {
		return "", false, err
	}
	return tk.ID, false, nil
}

type batchFacts struct{ pool *pgxpool.Pool }

func (f batchFacts) WorkItemFacts(ctx context.Context, ws, wi string) (string, int, error) {
	var st string
	var pr int
	err := f.pool.QueryRow(ctx,
		`SELECT status_key, priority FROM work_item WHERE id=$1`, wi).Scan(&st, &pr)
	return st, pr, err
}

func (f batchFacts) ChildStageFacts(context.Context, string, string) ([]autodomain.ChildStageFact, error) {
	return nil, nil
}

func (f batchFacts) WakeupTargetAgent(context.Context, string, string) (string, error) {
	return "", nil
}
