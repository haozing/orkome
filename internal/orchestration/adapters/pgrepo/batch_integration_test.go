package pgrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	idpgrepo "orkome/internal/identity/adapters/pgrepo"
	"orkome/internal/orchestration/app"
	"orkome/internal/platform/pg"
	"orkome/internal/platform/pgmigrate"
)

func batchEnv(t *testing.T) (*pg.DB, *pgxpool.Pool, string, string, string) {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_obatch")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_obatch")
	admin.Close(context.Background())

	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	dsn := strings.Replace(base, "/"+cfg.Database, "/orkome_obatch", 1)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, "../../../../migrations")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `TRUNCATE work_item_batch_item, work_item_batch,
		activity_log, comment, work_item, work_item_status, project, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	idr := idpgrepo.New(pg.WrapPool(pool))
	u, err := idr.UpsertUserByEmail(ctx, "obatch@example.com")
	require.NoError(t, err)
	wsFull, _, err := idr.CreateWorkspace(ctx, "B", "obatch-ws", u.ID)
	require.NoError(t, err)
	ws := wsFull.ID
	var wi string
	err = pool.QueryRow(ctx, `INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1,1,'batch host','todo','user',$2) RETURNING id::text`, ws, u.ID).Scan(&wi)
	require.NoError(t, err)
	return pg.WrapPool(pool), pool, ws, wi, u.ID
}

type fakeOCR struct {
	failures atomic.Int32
	failOn   int32
}

func (f *fakeOCR) Kind() string { return "ocr" }
func (f *fakeOCR) Process(_ context.Context, _ map[string]any, payload map[string]any) (map[string]any, error) {
	n := f.failures.Add(1)
	if f.failOn > 0 && n == f.failOn {
		return nil, errors.New("simulated transient ocr failure")
	}
	ref, _ := payload["attachment_id"].(string)
	return map[string]any{"text": "ocr-of-" + ref, "engine": "fake"}, nil
}

type progRecorder struct{ events atomic.Int32 }

func (p *progRecorder) EmitBatchProgress(context.Context, string, int, int) error {
	p.events.Add(1)
	return nil
}

func TestM85_TenThousandItems_EndToEnd(t *testing.T) {
	db, pool, ws, wi, user := batchEnv(t)
	repo := &BatchRepo{DB: db}
	prog := &progRecorder{}
	svc := app.NewBatchService(repo, prog)

	const N = 10000
	payloads := make([]map[string]any, N)
	for i := range payloads {
		payloads[i] = map[string]any{"attachment_id": fmtS("att-%06d", i)}
	}
	b, err := svc.CreateAndMaterialize(context.Background(), app.Batch{
		WorkspaceID: ws, WorkItemID: wi, Kind: "ocr",
		Source: map[string]any{"filter": "all"}, Concurrency: 64, ChunkSize: 100,
		OriginatorUserID: user,
	}, payloads)
	require.NoError(t, err)
	require.Equal(t, N, b.Total)

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				n, err := svc.RunWorker(context.Background(), *b, &fakeOCR{}, fmtS("w%d", w), 8)
				if err != nil || n == 0 {
					return
				}
			}
		}(w)
	}
	wg.Wait()

	var status string
	var completedAt *time.Time
	err = pool.QueryRow(context.Background(),
		`SELECT status, completed_at FROM work_item_batch WHERE id=$1`, b.ID).Scan(&status, &completedAt)
	require.NoError(t, err)
	assert.Equal(t, "completed", status)
	require.NotNil(t, completedAt, "done timestamp should land")

	var succ, fail int
	_ = pool.QueryRow(context.Background(),
		`SELECT succeeded, failed FROM work_item_batch WHERE id=$1`, b.ID).Scan(&succ, &fail)
	assert.Equal(t, N, succ+fail)
	assert.Equal(t, 0, fail)

	assert.Less(t, int(prog.events.Load()), 200, "progress events should be milestone-throttled (~100 not 10000)")
	assert.Greater(t, int(prog.events.Load()), 0, "progress event should exist")
}

func TestM85_CompleteCAS_Idempotent(t *testing.T) {
	db, _, ws, wi, user := batchEnv(t)
	repo := &BatchRepo{DB: db}
	svc := app.NewBatchService(repo, nil)
	b, err := svc.CreateAndMaterialize(context.Background(), app.Batch{
		WorkspaceID: ws, WorkItemID: wi, Kind: "ocr", Source: map[string]any{},
		OriginatorUserID: user,
	}, []map[string]any{{"attachment_id": "a"}, {"attachment_id": "b"}})
	require.NoError(t, err)

	items, err := repo.ClaimItems(context.Background(), b.ID, "w", 2)
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, it := range items {
		done, err := repo.FinishItem(context.Background(), it.ID, "succeeded", map[string]any{"ok": true}, "")
		require.NoError(t, err)
		if done {
			ok, err := repo.CompleteIfAllDone(context.Background(), b.ID, "completed")
			require.NoError(t, err)
			assert.True(t, ok, "the first observer should win")
		}
	}

	ok2, err := repo.CompleteIfAllDone(context.Background(), b.ID, "completed")
	require.NoError(t, err)
	assert.False(t, ok2, "second done CAS must affect 0 rows (idempotent)")
}

func TestM85_LeaseReclaim(t *testing.T) {
	db, pool, ws, wi, user := batchEnv(t)
	repo := &BatchRepo{DB: db}
	svc := app.NewBatchService(repo, nil)
	b, err := svc.CreateAndMaterialize(context.Background(), app.Batch{
		WorkspaceID: ws, WorkItemID: wi, Kind: "ocr", Source: map[string]any{},
		OriginatorUserID: user,
	}, []map[string]any{{"attachment_id": "a"}})
	require.NoError(t, err)

	items, err := repo.ClaimItems(context.Background(), b.ID, "dead-worker", 1)
	require.NoError(t, err)
	require.Len(t, items, 1)

	_, err = pool.Exec(context.Background(),
		`UPDATE work_item_batch_item SET lease_expires_at = now() - interval '3 minutes' WHERE batch_id=$1`, b.ID)
	require.NoError(t, err)

	n, err := repo.ReclaimExpiredLeases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "expired items should be reclaimed as pending")

	items2, err := repo.ClaimItems(context.Background(), b.ID, "new-worker", 1)
	require.NoError(t, err)
	require.Len(t, items2, 1)
	assert.Equal(t, 2, items2[0].Attempts, "attempts kept")
}

func TestM85_PauseResumeCancel(t *testing.T) {
	db, _, ws, wi, user := batchEnv(t)
	repo := &BatchRepo{DB: db}
	svc := app.NewBatchService(repo, nil)
	b, err := svc.CreateAndMaterialize(context.Background(), app.Batch{
		WorkspaceID: ws, WorkItemID: wi, Kind: "ocr", Source: map[string]any{},
		OriginatorUserID: user,
	}, []map[string]any{{"attachment_id": "a"}})
	require.NoError(t, err)

	require.NoError(t, svc.Pause(context.Background(), ws, b.ID))
	require.NoError(t, svc.Resume(context.Background(), ws, b.ID))
	got, _, _ := repo.GetBatch(context.Background(), ws, b.ID)
	assert.Equal(t, "running", got.Status)
	require.NoError(t, svc.Pause(context.Background(), ws, b.ID))
	require.NoError(t, svc.Cancel(context.Background(), ws, b.ID))

	assert.Error(t, svc.Cancel(context.Background(), ws, b.ID))
}

func TestM85_FailedRequeue(t *testing.T) {
	db, pool, ws, wi, user := batchEnv(t)
	repo := &BatchRepo{DB: db}
	svc := app.NewBatchService(repo, nil)
	b, err := svc.CreateAndMaterialize(context.Background(), app.Batch{
		WorkspaceID: ws, WorkItemID: wi, Kind: "ocr", Source: map[string]any{},
		OriginatorUserID: user,
	}, []map[string]any{{"attachment_id": "a"}, {"attachment_id": "b"}})
	require.NoError(t, err)

	items, _ := repo.ClaimItems(context.Background(), b.ID, "w", 1)
	require.Len(t, items, 1)
	_, err = repo.FinishItem(context.Background(), items[0].ID, "failed", nil, "permanent error")
	require.NoError(t, err)

	items2, _ := repo.ClaimItems(context.Background(), b.ID, "w", 1)
	require.Len(t, items2, 1)
	done, err := repo.FinishItem(context.Background(), items2[0].ID, "succeeded", map[string]any{}, "")
	require.NoError(t, err)
	require.True(t, done)
	ok, err := repo.CompleteIfAllDone(context.Background(), b.ID, "completed")
	require.NoError(t, err)
	assert.True(t, ok)
	var status string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM work_item_batch WHERE id=$1`, b.ID).Scan(&status)
	assert.Equal(t, "completed", status, "dead-letter non-blocking batch done (internal archive 05 §2.14)")

	n, err := svc.RequeueFailed(context.Background(), ws, b.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	var st2 string
	var att int
	_ = pool.QueryRow(context.Background(),
		`SELECT status, attempts FROM work_item_batch_item WHERE id=$1`, items[0].ID).Scan(&st2, &att)
	assert.Equal(t, "pending", st2)
	assert.Equal(t, 0, att, "attempts reset and recounted")
}

func TestM85_PayloadReferenceGuard(t *testing.T) {
	db, _, ws, wi, user := batchEnv(t)
	repo := &BatchRepo{DB: db}
	svc := app.NewBatchService(repo, nil)
	_, err := svc.CreateAndMaterialize(context.Background(), app.Batch{
		WorkspaceID: ws, WorkItemID: wi, Kind: "ocr", Source: map[string]any{},
		OriginatorUserID: user,
	}, []map[string]any{{"content": strings.Repeat("x", 8192)}})
	assert.Error(t, err, "inline large objects should be rejected (I13)")
}

func fmtS(format string, a ...any) string { return fmt.Sprintf(format, a...) }
