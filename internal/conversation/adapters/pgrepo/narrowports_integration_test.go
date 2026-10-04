package pgrepo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/platform/pg"
)

func fixtureNarrow(t *testing.T) (repo *Repo, wsID, u1, other, sess1, sess2, msgID string) {
	t.Helper()
	pool := newSeqDB(t)
	ctx := context.Background()
	repo = New(pg.WrapPool(pool))

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('np1@x.io','NP1') RETURNING id::text`).Scan(&u1))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('np2@x.io','NP2') RETURNING id::text`).Scan(&other))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ('cseq','c-seq') RETURNING id::text`).Scan(&wsID))
	for _, u := range []string{u1, other} {
		_, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, wsID, u)
		require.NoError(t, err)
	}
	var agentID string
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO agent (workspace_id, name, runtime_config)
VALUES ($1, 'np-agent', '{"provider":"fake","model":"m"}'::jsonb) RETURNING id::text`, wsID).Scan(&agentID))
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status)
VALUES ($1,$2,$3,'np-1','active') RETURNING id::text`, wsID, agentID, u1).Scan(&sess1))
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status)
VALUES ($1,$2,$3,'np-2','active') RETURNING id::text`, wsID, agentID, other).Scan(&sess2))
	_, err := pool.Exec(ctx, `
INSERT INTO chat_message (session_id, workspace_id, role, kind, content, seq)
VALUES ($1,$2,'user','message','np',1)`, sess1, wsID)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO chat_message (session_id, workspace_id, role, kind, content, seq)
VALUES ($1,$2,'assistant','message','np-a',2) RETURNING id::text`, sess1, wsID).Scan(&msgID))
	return repo, wsID, u1, other, sess1, sess2, msgID
}

func TestGetMessageExistence(t *testing.T) {
	repo, wsID, _, _, _, _, msgID := fixtureNarrow(t)
	ctx := context.Background()

	ok, err := repo.GetMessage(ctx, msgID)
	require.NoError(t, err)
	assert.True(t, ok, "persisted messages exist")

	ok, err = repo.GetMessage(ctx, "00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	assert.False(t, ok, "nonexistent uuid")

	ok, err = repo.GetMessage(ctx, "")
	require.NoError(t, err)
	assert.False(t, ok, "empty string short-circuits to false")

	assert.NotEqual(t, "", wsID)
}

func TestArchiveSessionsByUser(t *testing.T) {
	repo, wsID, u1, _, sess1, sess2, _ := fixtureNarrow(t)
	ctx := context.Background()

	n, err := repo.ArchiveSessionsByUser(ctx, wsID, u1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "u1 has exactly one session in this workspace")

	var status, otherStatus string
	require.NoError(t, repo.DB.Tx(ctx).QueryRow(ctx,
		`SELECT status FROM chat_session WHERE id=$1::uuid`, sess1).Scan(&status))
	require.NoError(t, repo.DB.Tx(ctx).QueryRow(ctx,
		`SELECT status FROM chat_session WHERE id=$1::uuid`, sess2).Scan(&otherStatus))
	assert.Equal(t, "archived", status)
	assert.Equal(t, "active", otherStatus, "others' sessions untouched")

	n, err = repo.ArchiveSessionsByUser(ctx, wsID, u1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "full sweep: archived rows hit again (idempotent rewrite)")
}
