package pgrepo

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/platform/pg"
	"orkome/internal/platform/pgmigrate"
)

func newSeqDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)

	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_cseq WITH (FORCE)")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_cseq")
	admin.Close(context.Background())
	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	pool, err := pgxpool.New(context.Background(),
		strings.Replace(base, "/"+cfg.Database, "/orkome_cseq", 1))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	m := pgmigrate.New(pool, "../../../../migrations")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)
	return pool
}

func TestNextSeqAtomicity(t *testing.T) {
	pool := newSeqDB(t)
	ctx := context.Background()
	repo := New(pg.WrapPool(pool))

	var wsID, userID, agentID, sessID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('seq@x.io','SEQ') RETURNING id::text`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ('cseq','c-seq') RETURNING id::text`).Scan(&wsID))
	_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, wsID, userID)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO agent (workspace_id, name, runtime_config)
VALUES ($1, 'seq-agent', '{"provider":"fake","model":"m"}'::jsonb) RETURNING id::text`, wsID).Scan(&agentID))
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status)
VALUES ($1,$2,$3,'seq','active') RETURNING id::text`, wsID, agentID, userID).Scan(&sessID))

	for i := int64(1); i <= 3; i++ {
		seq, err := repo.NextSeq(ctx, sessID)
		require.NoError(t, err)
		assert.Equal(t, i, seq)
	}

	const n = 16
	var mu sync.Mutex
	seen := map[int64]bool{}
	var wg sync.WaitGroup
	var dupes atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seq, err := repo.NextSeq(ctx, sessID)
			if err != nil {
				t.Errorf("NextSeq: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[seq] {
				dupes.Add(1)
			}
			seen[seq] = true
		}()
	}
	wg.Wait()
	assert.Zero(t, dupes.Load(), "concurrent numbering must not duplicate (migration 022 counter column row-lock serialization)")

	s1, err := repo.NextSeq(ctx, sessID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
INSERT INTO chat_message (session_id, workspace_id, role, kind, content, seq)
VALUES ($1,$2,'user','message','dup', $3)`, sessID, wsID, s1)
	require.NoError(t, err, "first insert valid")
	var pgErr interface{ SQLState() string }
	_, err = pool.Exec(ctx, `
INSERT INTO chat_message (session_id, workspace_id, role, kind, content, seq)
VALUES ($1,$2,'assistant','message','dup-again', $3)`, sessID, wsID, s1)
	require.Error(t, err)
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23505", pgErr.SQLState(), "  (session_id,seq) unique index backstop effective")
}
