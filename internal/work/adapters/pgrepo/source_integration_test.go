package pgrepo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/logx"
	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/contract/apierr"
	"orkome/internal/platform/eventbus"
	"orkome/internal/work/app"
)

type srcEnv struct {
	db   *pg.DB
	pool *pgxpool.Pool
	ws   string
	user string
	wi   string
	svc  *app.SourceContextService

	now func() time.Time
}

func newSrcEnv(t *testing.T) *srcEnv {
	db, pool, ws, wi, user := batchEnv(t)

	_, err := pool.Exec(context.Background(),
		`UPDATE workspace SET work_item_counter = greatest(work_item_counter, 1) WHERE id=$1`, ws)
	require.NoError(t, err)
	bus := eventbus.New(logx.New(logx.Options{Level: "error"}))
	repo := &Repo{DB: db}
	e := &srcEnv{db: db, pool: pool, ws: ws, user: user, wi: wi, now: time.Now}
	e.svc = app.NewSourceContextService(repo, bus, func() time.Time { return e.now() })
	e.svc.Sign = func(msg string) string { return "sig:" + msg }
	return e
}

func (e *srcEnv) comment(t *testing.T, content string) app.Comment {
	t.Helper()
	repo := &Repo{DB: e.db}
	c, err := repo.CreateComment(context.Background(), app.NewComment{
		WorkItemID: e.wi, WorkspaceID: e.ws, AuthorType: "user", AuthorID: e.user, Content: content,
	})
	require.NoError(t, err)
	return c
}

func TestB6_SourcePreviewCreateFlow(t *testing.T) {
	e := newSrcEnv(t)
	ctx := context.Background()
	c := e.comment(t, "split into subtasks: research competitors' pricing")

	pv, err := e.svc.Preview(ctx, e.ws, c.ID, e.user)
	require.NoError(t, err)
	assert.NotEmpty(t, pv.CaptureToken)
	assert.Len(t, pv.Digest, 64)
	assert.Equal(t, e.wi, pv.WorkItemID)
	assert.Contains(t, pv.CommentSnippet, "research competitors")

	child, err := e.svc.CreateFromComment(ctx, e.ws, c.ID, e.user, pv.CaptureToken, "manual", "")
	require.NoError(t, err)
	assert.Equal(t, e.wi, child.ParentID)
	var originType, originID string
	require.NoError(t, e.db.Tx(ctx).QueryRow(ctx,
		`SELECT origin_type, coalesce(origin_id::text,'') FROM work_item WHERE id=$1`, child.ID).
		Scan(&originType, &originID))
	assert.Equal(t, "quick_create", originType)
	assert.Equal(t, c.ID, originID)
	assert.Contains(t, child.Title, "research competitors")

	var rows int
	_ = e.db.Tx(ctx).QueryRow(ctx,
		`SELECT count(*) FROM work_item_source_context WHERE workspace_id=$1`, e.ws).Scan(&rows)
	assert.Equal(t, 1, rows, "exactly one snapshot row")

	_, err = e.svc.CreateFromComment(ctx, e.ws, c.ID, e.user, pv.CaptureToken, "manual", "")
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Conflict, ae.Code)

	_, err = e.svc.CreateFromComment(ctx, e.ws, c.ID, e.user, "9999999999.forged", "manual", "")
	assert.Error(t, err)
}

func TestB6_TokenExpiry(t *testing.T) {
	e := newSrcEnv(t)
	ctx := context.Background()
	c := e.comment(t, "expired case")
	pv, err := e.svc.Preview(ctx, e.ws, c.ID, e.user)
	require.NoError(t, err)

	e.now = func() time.Time { return time.Now().Add(16 * time.Minute) }
	_, err = e.svc.CreateFromComment(ctx, e.ws, c.ID, e.user, pv.CaptureToken, "manual", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestB6_AgentModeTightBudget(t *testing.T) {
	e := newSrcEnv(t)
	ctx := context.Background()

	big := strings.Repeat("long", 33*1024)
	c := e.comment(t, big)
	pv, err := e.svc.Preview(ctx, e.ws, c.ID, e.user)
	require.NoError(t, err)

	_, err = e.svc.CreateFromComment(ctx, e.ws, c.ID, e.user, pv.CaptureToken, "agent", "")
	require.Error(t, err, "agent mode should be intercepted by the 64KiB budget")

	manual, err := e.svc.Preview(ctx, e.ws, c.ID, e.user)
	require.NoError(t, err)
	child, err := e.svc.CreateFromComment(ctx, e.ws, c.ID, e.user, manual.CaptureToken, "manual", "manual rebuild")
	require.NoError(t, err)
	assert.Equal(t, "manual rebuild", child.Title)
}

func TestB6_RetryFromDigest(t *testing.T) {
	e := newSrcEnv(t)
	ctx := context.Background()
	c := e.comment(t, "retry case")
	pv, err := e.svc.Preview(ctx, e.ws, c.ID, e.user)
	require.NoError(t, err)
	first, err := e.svc.CreateFromComment(ctx, e.ws, c.ID, e.user, pv.CaptureToken, "manual", "")
	require.NoError(t, err)

	second, err := e.svc.CreateFromDigest(ctx, e.ws, pv.Digest, e.user, "retry artifact")
	require.NoError(t, err)
	assert.Equal(t, "retry artifact", second.Title)
	assert.Equal(t, first.ParentID, second.ParentID, "retry artifact same parent")

	_, err = e.svc.CreateFromDigest(ctx, e.ws, strings.Repeat("0", 64), e.user, "")
	require.Error(t, err)
}

func TestI7_CommentTouchesWorkItem(t *testing.T) {
	e := newSrcEnv(t)
	ctx := context.Background()
	var rev0 int64
	var act0 time.Time
	require.NoError(t, e.db.Tx(ctx).QueryRow(ctx,
		`SELECT revision, last_activity_at FROM work_item WHERE id=$1`, e.wi).Scan(&rev0, &act0))

	comments := app.NewCommentService(&Repo{DB: e.db}, eventbus.New(logx.New(logx.Options{Level: "error"})), nil)
	_, _, err := comments.Create(ctx, app.NewComment{
		WorkItemID: e.wi, WorkspaceID: e.ws, AuthorType: "user", AuthorID: e.user, Content: "I7 comment",
	})
	require.NoError(t, err)

	var rev1 int64
	var act1 time.Time
	require.NoError(t, e.db.Tx(ctx).QueryRow(ctx,
		`SELECT revision, last_activity_at FROM work_item WHERE id=$1`, e.wi).Scan(&rev1, &act1))
	assert.Greater(t, rev1, rev0, "comment creation must touch the work item (revision moves forward)")
	assert.False(t, act1.Before(act0), "last_activity_at moved forward")
}
