package pgrepo

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/orchestration/app"
)

func seedTaskWithMessages(t *testing.T, r *Repo, ws, ag string, n int) string {
	t.Helper()
	id := seedRunningTask(t, r, ws, ag)
	ctx := context.Background()
	for i := 1; i <= n; i++ {
		kind := "text"
		if i%2 == 0 {
			kind = "tool_result"
		}
		_, err := r.AppendTaskMessage(ctx, ws, id, kind, "", fmt.Sprintf("message %d", i), false)
		require.NoError(t, err)
	}
	return id
}

func TestListTaskMessagesDescPaging(t *testing.T) {
	db, _, ws, ag := summaryEnv(t)
	r := New(db)
	ctx := context.Background()
	id := seedTaskWithMessages(t, r, ws, ag, 12)

	page1, err := r.ListTaskMessages(ctx, ws, id, app.TaskMessagesQuery{Limit: 5, Desc: true})
	require.NoError(t, err)
	require.Len(t, page1, 5)
	assert.EqualValues(t, 12, page1[0].Seq)
	assert.EqualValues(t, 8, page1[4].Seq)

	page2, err := r.ListTaskMessages(ctx, ws, id, app.TaskMessagesQuery{Limit: 5, Desc: true, BeforeSeq: 8})
	require.NoError(t, err)
	require.Len(t, page2, 5)
	assert.EqualValues(t, 7, page2[0].Seq)
	assert.EqualValues(t, 3, page2[4].Seq)

	page3, err := r.ListTaskMessages(ctx, ws, id, app.TaskMessagesQuery{Limit: 5, Desc: true, BeforeSeq: 3})
	require.NoError(t, err)
	require.Len(t, page3, 2)
	assert.EqualValues(t, 2, page3[0].Seq)

	texts, err := r.ListTaskMessages(ctx, ws, id, app.TaskMessagesQuery{Limit: 3, Desc: true, Kinds: []string{"text"}})
	require.NoError(t, err)
	require.Len(t, texts, 3)
	for _, m := range texts {
		assert.Equal(t, "text", m.Kind)
	}
	assert.EqualValues(t, 11, texts[0].Seq)

	legacy, err := r.ListTaskMessages(ctx, ws, id, app.TaskMessagesQuery{})
	require.NoError(t, err)
	require.Len(t, legacy, 12)
	assert.EqualValues(t, 1, legacy[0].Seq)
}

func TestListTasksByWorkItemNewestFirst(t *testing.T) {
	db, pool, ws, ag := summaryEnv(t)
	r := New(db)
	ctx := context.Background()
	wi := seedWorkItemRow(t, pool, ws)
	for i := 0; i < 3; i++ {
		task, err := r.InsertTask(ctx, app.Task{
			WorkspaceID: ws, AgentID: ag, WorkItemID: wi, Priority: 1, MaxAttempts: 1,
			Context: map[string]any{"trigger": "test"},
		})
		require.NoError(t, err)
		_, err = r.q(ctx).Exec(ctx, `UPDATE agent_task_queue SET status='dispatched' WHERE id=$1`, task.ID)
		require.NoError(t, err)
		_, err = r.StartTask(ctx, task.ID, "m2-test", "eino")
		require.NoError(t, err)
		require.NoError(t, r.CompleteRun(ctx, task.ID, fmt.Sprintf("conclusion %d", i), "", "eino", "", app.UsageReport{}))
	}
	asc, err := r.ListTasksByWorkItem(ctx, wi, false)
	require.NoError(t, err)
	require.Len(t, asc, 3)
	assert.Contains(t, asc[0].Result, "conclusion 0", "ascending first = oldest")

	desc, err := r.ListTasksByWorkItem(ctx, wi, true)
	require.NoError(t, err)
	require.Len(t, desc, 3)
	assert.Contains(t, desc[0].Result, "conclusion 2", "descending first = newest")
	assert.Contains(t, desc[0].Summary, "conclusion 2", "list path carries the summary")
}

func TestWorkspaceTaskListCarriesSummary(t *testing.T) {
	db, _, ws, ag := summaryEnv(t)
	r := New(db)
	ctx := context.Background()
	id := seedRunningTask(t, r, ws, ag)
	_, err := r.AppendTaskMessage(ctx, ws, id, "text", "", "summary-level conclusion", false)
	require.NoError(t, err)
	require.NoError(t, r.CompleteRun(ctx, id, "full-text result…", "", "eino", "", app.UsageReport{}))

	rows, next, err := r.ListWorkspaceTasks(ctx, ws, app.TaskListFilter{})
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, rows, 1)
	assert.Equal(t, "summary-level conclusion", rows[0].Summary, "workspace task list carries the accounting summary")
}

func seedWorkItemRow(t *testing.T, pool *pgxpool.Pool, ws string) string {
	t.Helper()
	var wi string
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO work_item (workspace_id, number, title, status_key, creator_type, creator_id)
VALUES ($1,1,'M2 read-path','todo','user',(SELECT id FROM "user" LIMIT 1)) RETURNING id::text`, ws).Scan(&wi))
	return wi
}
