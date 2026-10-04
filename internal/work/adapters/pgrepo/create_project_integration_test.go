package pgrepo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"orkome/internal/platform/outboxx"
	workapp "orkome/internal/work/app"
)

func TestCreateProjectOutboxPayloadNotNull(t *testing.T) {
	ctx := context.Background()
	db, pool, ws, _, _ := batchEnv(t)

	catalog := workapp.NewCatalogService(&Repo{DB: db}, nil)
	rec := outboxx.NewRecorder(db)
	catalog.SetOutbox(rec, db.Within)

	p, err := catalog.CreateProject(ctx, ws, "regression project", "")
	require.NoError(t, err)
	require.NotEmpty(t, p.ID)
	require.Equal(t, "regression project", p.Name)

	var payload []byte
	err = pool.QueryRow(ctx,
		`SELECT payload FROM event_outbox WHERE event_type = 'project.created' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&payload)
	require.NoError(t, err)
	require.Contains(t, string(payload), p.ID)

	var name string
	err = pool.QueryRow(ctx, `SELECT name FROM project WHERE id = $1`, p.ID).Scan(&name)
	require.NoError(t, err)
	require.Equal(t, "regression project", name)
}
