package pgrepo

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/orchestration/domain"
)

func TestUncoveredSQL_TerminalVocabulary(t *testing.T) {
	const frag = "t.status IN ('completed','failed','cancelled')"
	if !strings.Contains(uncoveredTriggerCommentsSQL, frag) {
		t.Fatalf("uncoveredTriggerCommentsSQL missing the terminal vocabulary %q (drift vs domain.Terminal)", frag)
	}
	for _, s := range domain.AllStatuses() {
		if strings.Contains(frag, "'"+s+"'") != domain.Terminal(s) {
			t.Errorf("status %s: SQL vocabulary out of sync with domain.Terminal", s)
		}
	}
}

func TestUncoveredSQL_Predicates(t *testing.T) {
	for _, frag := range []string{
		"t.completed_at < now() - interval '10 minutes'",
		"m.context->>'reconcile_attempt' IS NOT NULL",
		"ac.author_type = 'agent'",
		"ac.created_at > tc.created_at",
		"WITH RECURSIVE thread_root AS",
		"LIMIT 10",
	} {
		if !strings.Contains(uncoveredTriggerCommentsSQL, frag) {
			t.Errorf("uncoveredTriggerCommentsSQL missing the predicate anchor %q (candidate semantics drift)", frag)
		}
	}

	if !strings.Contains(uncoveredTriggerCommentsSQL, "tc.deleted_at IS NULL") {
		t.Error("uncoveredTriggerCommentsSQL missing the trigger-comment tombstone predicate tc.deleted_at IS NULL")
	}
}

func (e *deferredEnv) recComment(t *testing.T, wi, authorType, authorID, content, parentID, createdAgo string) string {
	t.Helper()
	var id string
	require.NoError(t, e.pool.QueryRow(e.ctx, `
INSERT INTO comment (work_item_id, workspace_id, author_type, author_id, content, parent_id, created_at)
VALUES ($1::uuid, $2, $3, nullif($4,'')::uuid, $5, nullif($6,'')::uuid, now() - $7::interval)
RETURNING id::text`, wi, e.ws, authorType, authorID, content, parentID, createdAgo).Scan(&id))
	return id
}

func (e *deferredEnv) recTask(t *testing.T, agentID, wi, status, ctxJSON, completedAgo string) string {
	t.Helper()
	var id string
	require.NoError(t, e.pool.QueryRow(e.ctx, `
INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status, max_attempts, context,
  originator_source, completed_at)
VALUES ($1, $2, $3::uuid, $4, 2, $5::jsonb, 'direct_human', now() - $6::interval)
RETURNING id::text`, e.ws, agentID, wi, status, ctxJSON, completedAgo).Scan(&id))
	return id
}

func (e *deferredEnv) recAssign(t *testing.T, wi, agentID string) {
	t.Helper()
	_, err := e.pool.Exec(e.ctx,
		`UPDATE work_item SET assignee_type='agent', assignee_id=$2::uuid WHERE id=$1::uuid`, wi, agentID)
	require.NoError(t, err)
}

func TestListUncoveredTriggerComments_Golden(t *testing.T) {
	e := newDeferredEnv(t)
	a := e.mkAgent(t, "r1")
	wi := e.mkWorkItem(t)
	e.recAssign(t, wi, a.ID)

	root := e.recComment(t, wi, "agent", a.ID, "root by agent", "", "25 minutes")
	tc := e.recComment(t, wi, "user", e.user, "please follow up", root, "20 minutes")
	e.recTask(t, a.ID, wi, "completed",
		`{"trigger":"comment","trigger_comment_id":"`+tc+`"}`, "11 minutes")

	rows, err := e.repo.ListUncoveredTriggerComments(e.ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1, "terminal + outside grace + no agent reply on thread → should hit")
	u := rows[0]
	assert.Equal(t, tc, u.CommentID)
	assert.Equal(t, e.ws, u.WorkspaceID)
	assert.Equal(t, wi, u.WorkItemID)
	assert.Equal(t, "user", u.AuthorType)
	assert.Equal(t, e.user, u.AuthorID)
	assert.Equal(t, "please follow up", u.Content)
	assert.Equal(t, "agent", u.ParentAuthorType, "parent comment author fact (7a routing)")
	assert.Equal(t, a.ID, u.ParentAuthorID)
	assert.Equal(t, "agent", u.RootAuthorType, "thread-root author fact (7b routing)")
	assert.Equal(t, a.ID, u.RootAuthorID)
	assert.Equal(t, "agent", u.AssigneeType, "work item assignment fact (7c fallback)")
	assert.Equal(t, a.ID, u.AssigneeID)
}

func TestListUncoveredTriggerComments_Predicates(t *testing.T) {
	e := newDeferredEnv(t)
	a1 := e.mkAgent(t, "r2")
	a2 := e.mkAgent(t, "r3")

	wi1 := e.mkWorkItem(t)
	tc1 := e.recComment(t, wi1, "user", e.user, "q1", "", "20 minutes")
	e.recTask(t, a1.ID, wi1, "completed", `{"trigger":"comment","trigger_comment_id":"`+tc1+`"}`, "11 minutes")
	e.recComment(t, wi1, "agent", a1.ID, "processing complete", tc1, "5 minutes")

	wi2 := e.mkWorkItem(t)
	tc2 := e.recComment(t, wi2, "user", e.user, "q2", "", "40 minutes")
	e.recTask(t, a1.ID, wi2, "completed", `{"trigger":"comment","trigger_comment_id":"`+tc2+`"}`, "30 minutes")
	e.recTask(t, a2.ID, wi2, "completed",
		`{"trigger":"deferred_fallback","trigger_comment_id":"`+tc2+`","reconcile_attempt":true}`, "15 minutes")

	wi3 := e.mkWorkItem(t)
	tc3 := e.recComment(t, wi3, "user", e.user, "q3", "", "10 minutes")
	e.recTask(t, a1.ID, wi3, "completed", `{"trigger":"comment","trigger_comment_id":"`+tc3+`"}`, "3 minutes")

	wi4 := e.mkWorkItem(t)
	tc4 := e.recComment(t, wi4, "user", e.user, "q4", "", "20 minutes")
	e.recTask(t, a1.ID, wi4, "running", `{"trigger":"comment","trigger_comment_id":"`+tc4+`"}`, "0 seconds")

	wi5 := e.mkWorkItem(t)
	tc5 := e.recComment(t, wi5, "user", e.user, "q5", "", "20 minutes")
	e.recTask(t, a1.ID, wi5, "completed", `{"trigger":"comment","trigger_comment_id":"`+tc5+`"}`, "11 minutes")
	otherRoot := e.recComment(t, wi5, "user", e.user, "another thread root", "", "19 minutes")
	e.recComment(t, wi5, "agent", a1.ID, "off-thread reply", otherRoot, "1 minutes")

	wi6 := e.mkWorkItem(t)
	tc6 := e.recComment(t, wi6, "user", e.user, "q6", "", "20 minutes")
	e.recTask(t, a1.ID, wi6, "completed", `{"trigger":"comment","trigger_comment_id":"`+tc6+`"}`, "11 minutes")
	_, err := e.pool.Exec(e.ctx, `UPDATE comment SET deleted_at=now() WHERE id=$1::uuid`, tc6)
	require.NoError(t, err)

	rows, err := e.repo.ListUncoveredTriggerComments(e.ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1, "only ⑤ answered off-thread should hit; ①②③④⑥ all excluded")
	assert.Equal(t, tc5, rows[0].CommentID)
}

func TestListUncoveredTriggerComments_BoundedAndDeduped(t *testing.T) {
	e := newDeferredEnv(t)
	a1 := e.mkAgent(t, "r4")
	a2 := e.mkAgent(t, "r5")
	wi := e.mkWorkItem(t)

	first := ""
	for i := 0; i < 12; i++ {
		tc := e.recComment(t, wi, "user", e.user, "q", "", "60 minutes")
		if i == 0 {
			first = tc
		}
		e.recTask(t, a1.ID, wi, "completed",
			`{"trigger":"comment","trigger_comment_id":"`+tc+`"}`, "11 minutes")
	}

	e.recTask(t, a2.ID, wi, "completed",
		`{"trigger":"comment","trigger_comment_id":"`+first+`"}`, "12 minutes")

	rows, err := e.repo.ListUncoveredTriggerComments(e.ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 10, "at most 10 per round (disposition ledger 7.9 boundedness)")
	seen := map[string]bool{}
	for _, u := range rows {
		assert.False(t, seen[u.CommentID], "the same comment must not appear twice (DISTINCT ON dedup)")
		seen[u.CommentID] = true
	}
}
