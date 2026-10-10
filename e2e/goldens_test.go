package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	autoppgrepo "orkome/internal/automation/adapters/pgrepo"
	autopapp "orkome/internal/automation/app"
	orchapp "orkome/internal/orchestration/app"
	"github.com/haozing/ploykit/platform/pg"
)

func ctx0() context.Context { return context.Background() }

func TestE2E_G1_BrowserGolden(t *testing.T) {
	s := newStack(t)
	s.login("alice", "g1@example.com")
	s.wsID = s.createWS(t, "alice", "g1-ws")
	wi := s.createWorkItem(t, "alice", "goldens one")

	rec := s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/comments",
		map[string]any{"content": "start work"}, nil)
	require.Equal(t, 201, rec.Code, rec.Body.String())

	rec = s.do("alice", http.MethodGet, "/api/work-items/"+wi+"/subscribers", nil, nil)
	require.Equal(t, 200, rec.Code)
	var subs struct {
		Items []struct {
			Source string `json:"source"`
		} `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &subs)
	require.Len(t, subs.Items, 1)
	assert.Equal(t, "creator", subs.Items[0].Source)

	rec = s.do("alice", http.MethodGet, "/api/work-items/"+wi+"/timeline", nil, nil)
	require.Equal(t, 200, rec.Code)

	rec = s.do("alice", http.MethodPost, "/api/work-items/table/rows",
		map[string]any{"filters": map[string]any{"status": []string{"todo"}}}, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
}

func TestE2E_G2_AgentExecutionGolden(t *testing.T) {
	s := newStack(t)
	s.login("alice", "g2@example.com")
	s.wsID = s.createWS(t, "alice", "g2-ws")
	wi := s.createWorkItem(t, "alice", "goldens two")
	agent := s.createAgent(t, s.wsID, "g2-worker")

	_, err := s.pool.Exec(ctx0(),
		`UPDATE work_item SET assignee_type='agent', assignee_id=$2 WHERE id=$1`, wi, agent)
	require.NoError(t, err)
	task, err := orchEnqueue(s, s.wsID, wi, agent)
	require.NoError(t, err)
	require.NotEmpty(t, task)

	waitFor(t, func() bool {
		tt, ok, _ := s.orepo.GetTask(ctx0(), s.wsID, task)
		return ok && tt.Status == "completed"
	}, "task completed")

	var n int
	_ = s.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM comment WHERE work_item_id=$1 AND author_type='agent'`, wi).Scan(&n)
	assert.Equal(t, 1, n, "fallback comment should land")

	var un int
	_ = s.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM task_usage WHERE task_id=$1`, task).Scan(&un)
	assert.Equal(t, 1, un, "usage row should land")
}

func TestE2E_G3_CommentTriggerCoalesce(t *testing.T) {
	s := newStack(t)
	s.login("alice", "g3@example.com")
	s.wsID = s.createWS(t, "alice", "g3-ws")
	wi := s.createWorkItem(t, "alice", "goldens three")
	agent := s.createAgent(t, s.wsID, "g3-worker")
	_, err := s.pool.Exec(ctx0(),
		`UPDATE work_item SET assignee_type='agent', assignee_id=$2 WHERE id=$1`, wi, agent)
	require.NoError(t, err)

	rec := s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/comments",
		map[string]any{"content": "please handle"}, nil)
	require.Equal(t, 201, rec.Code)
	var out struct {
		Admission struct {
			Result string `json:"result"`
		} `json:"admission"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	assert.Equal(t, "queued", out.Admission.Result, "7c fallback should enqueue")

	_, err = s.pool.Exec(ctx0(),
		`UPDATE agent_task_queue SET status='dispatched' WHERE work_item_id=$1 AND status='queued'`, wi)
	require.NoError(t, err)

	rec2 := s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/comments",
		map[string]any{"content": "supplement info"}, nil)
	require.Equal(t, 201, rec2.Code)
	var out2 struct {
		Admission struct {
			Result string `json:"result"`
		} `json:"admission"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &out2)
	assert.Equal(t, "coalesced", out2.Admission.Result, "should merge into pending")
}

func TestE2E_G4_ChatGolden(t *testing.T) {
	s := newStack(t)
	s.login("alice", "g4@example.com")
	s.wsID = s.createWS(t, "alice", "g4-ws")
	agent := s.createAgent(t, s.wsID, "g4-chatter")

	var sess string
	err := s.pool.QueryRow(ctx0(), `
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
SELECT $1, $2, (SELECT creator_id FROM work_item LIMIT 1), 'goldens four'
WHERE EXISTS (SELECT 1 FROM work_item LIMIT 1) RETURNING id::text`, s.wsID, agent).Scan(&sess)
	if err != nil {

		var uid string
		_ = s.pool.QueryRow(ctx0(), `SELECT id FROM "user" LIMIT 1`).Scan(&uid)
		err = s.pool.QueryRow(ctx0(), `
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title) VALUES ($1,$2,$3,'goldens four') RETURNING id::text`,
			s.wsID, agent, uid).Scan(&sess)
	}
	require.NoError(t, err)

	_, err = s.orepo.InsertChatTask(ctx0(), orchTask(s.wsID, sess, agent, ""))
	require.NoError(t, err)

	waitFor(t, func() bool {
		var n int
		_ = s.pool.QueryRow(ctx0(),
			`SELECT count(*) FROM chat_message WHERE session_id=$1 AND role='assistant' AND content <> ''`, sess).Scan(&n)
		return n > 0
	}, "assistant rows should land (chat done accounting face)")
}

func TestE2E_G5_ApprovalGolden(t *testing.T) {
	s := newStack(t)
	s.login("alice", "g5@example.com")
	s.wsID = s.createWS(t, "alice", "g5-ws")
	wi := s.createWorkItem(t, "alice", "goldens five")
	agent := s.createAgent(t, s.wsID, "g5-approver")
	var uid string
	_ = s.pool.QueryRow(ctx0(), `SELECT id FROM "user" LIMIT 1`).Scan(&uid)
	task, err := orchEnqueueUID(s, s.wsID, wi, agent, uid)
	require.NoError(t, err)
	_ = task

	rec := s.do("alice", http.MethodPost, "/api/tasks/"+task+"/approve", nil, nil)
	t.Logf("approve body: %s", rec.Body.String())
	require.Equal(t, 409, rec.Code, "approving non-parked tasks should be 409 (E_AWAITING_STATE semantics)")
}

func TestE2E_G6_AutomationGolden(t *testing.T) {
	s := newStack(t)
	s.login("alice", "g6@example.com")
	s.wsID = s.createWS(t, "alice", "g6-ws")
	agent := s.createAgent(t, s.wsID, "g6-leader")
	var uid string
	_ = s.pool.QueryRow(ctx0(), `SELECT id FROM "user" LIMIT 1`).Scan(&uid)

	arepo := autoRepo(s)
	_ = arepo
	ap, _, err := s.auto.Create(ctx0(), autopapp.Autopilot{
		WorkspaceID: s.wsID, Name: "goldens six", ExecMode: "create_work_item",
		Template: map[string]any{"title": "goldens six {{date}}"}, TargetAgentID: agent, CreatedBy: uid,
	}, "*/5 * * * *", "UTC")
	require.NoError(t, err)
	_ = arepo

	_, err = s.pool.Exec(ctx0(),
		`UPDATE autopilot_trigger SET next_run_at=now()-interval '1 minute' WHERE autopilot_id=$1`, ap.ID)
	require.NoError(t, err)
	n, err := s.auto.CronTickOnce(ctx0())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "should trigger once")

	waitFor(t, func() bool {
		var cnt int
		_ = s.pool.QueryRow(ctx0(),
			`SELECT count(*) FROM autopilot_run WHERE autopilot_id=$1 AND status IN ('running','completed')`, ap.ID).Scan(&cnt)
		return cnt > 0
	}, "the run should be created and backfilled")
}

func TestE2E_G7_PreviewMatchesActual(t *testing.T) {
	s := newStack(t)
	s.login("alice", "g7@example.com")
	s.wsID = s.createWS(t, "alice", "g7-ws")
	wi := s.createWorkItem(t, "alice", "goldens seven")

	rec := s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/comments/trigger-preview",
		map[string]any{"content": "who will do it"}, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var pv struct {
		Result     string `json:"result"`
		ReasonCode string `json:"reason_code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &pv)
	assert.Equal(t, "blocked", pv.Result)
	assert.Equal(t, "no_trigger_target", pv.ReasonCode)

	agent := s.createAgent(t, s.wsID, "g7-worker")
	_, err := s.pool.Exec(ctx0(),
		`UPDATE work_item SET assignee_type='agent', assignee_id=$2 WHERE id=$1`, wi, agent)
	require.NoError(t, err)
	rec2 := s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/comments/trigger-preview",
		map[string]any{"content": "who will do it"}, nil)
	require.Equal(t, 200, rec2.Code)
	var pv2 struct {
		Result   string `json:"result"`
		Triggers []struct {
			AgentID string `json:"agent_id"`
			Reason  string `json:"reason"`
		} `json:"triggers"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &pv2)
	assert.Equal(t, "would_trigger", pv2.Result)
	require.Len(t, pv2.Triggers, 1)
	assert.Equal(t, agent, pv2.Triggers[0].AgentID)
	assert.Equal(t, "7c.work_item_assignee", pv2.Triggers[0].Reason)

	rec3 := s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/comments",
		map[string]any{"content": "start work"}, nil)
	require.Equal(t, 201, rec3.Code)
	var out struct {
		Admission struct {
			Result string `json:"result"`
		} `json:"admission"`
	}
	_ = json.Unmarshal(rec3.Body.Bytes(), &out)
	assert.Equal(t, "queued", out.Admission.Result, "preview says would_trigger→actually queued")
}

func TestE2E_G8_AssignmentGolden(t *testing.T) {
	s := newStack(t)
	s.login("alice", "g8@example.com")
	s.wsID = s.createWS(t, "alice", "g8-ws")

	rec := s.do("alice", http.MethodPost, "/api/work-items",
		map[string]any{"title": "goldens eight", "status": "in_progress"}, nil)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var wiOut struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &wiOut))
	wi := wiOut.ID
	agent := s.createAgent(t, s.wsID, "g8-worker")
	var uid string
	require.NoError(t, s.pool.QueryRow(ctx0(), `SELECT id FROM "user" LIMIT 1`).Scan(&uid))

	rec = s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/comments/trigger-preview",
		map[string]any{"change_kind": "assignee", "assignee_type": "user", "assignee_id": uid}, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var neg struct {
		Result     string `json:"result"`
		ReasonCode string `json:"reason_code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &neg))
	assert.Equal(t, "blocked", neg.Result, "user assignment has no trigger face")
	assert.Equal(t, "no_trigger_target", neg.ReasonCode)

	rec = s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/comments/trigger-preview",
		map[string]any{"change_kind": "assignee", "assignee_type": "agent", "assignee_id": agent}, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var pv struct {
		Result string `json:"result"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pv))
	assert.Equal(t, "would_trigger", pv.Result, "write-trigger preview: agent assignment should trigger")

	rec = s.do("alice", http.MethodPut, "/api/work-items/"+wi,
		map[string]any{"assignee_type": "agent", "assignee_id": agent}, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var at string
	require.NoError(t, s.pool.QueryRow(ctx0(),
		`SELECT assignee_type FROM work_item WHERE id=$1`, wi).Scan(&at))
	assert.Equal(t, "agent", at, "assignment should be persisted")

	var task, tstat string
	waitFor(t, func() bool {
		err := s.pool.QueryRow(ctx0(), `
SELECT id::text, status FROM agent_task_queue
WHERE work_item_id=$1 AND trigger_evidence_kind='assignment' ORDER BY created_at LIMIT 1`, wi).Scan(&task, &tstat)
		return err == nil
	}, "assigned tasks should enqueue")
	assert.Contains(t, []string{"queued", "dispatched", "running", "completed"}, tstat, "the task has entered the dispatch face")
	var trig string
	require.NoError(t, s.pool.QueryRow(ctx0(),
		`SELECT context->>'trigger' FROM agent_task_queue WHERE id=$1`, task).Scan(&trig))
	assert.Equal(t, "assignment", trig, "trigger evidence = assignment")

	rec = s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/comments/trigger-preview",
		map[string]any{"change_kind": "assignee"}, nil)
	require.Equal(t, 200, rec.Code)
	var pv2 struct {
		Result string `json:"result"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pv2))
	assert.Equal(t, "would_trigger", pv2.Result, "preview reads the current row: agent assignment is on the row")

	waitFor(t, func() bool {
		tt, ok, _ := s.orepo.GetTask(ctx0(), s.wsID, task)
		return ok && tt.Status == "completed"
	}, "task completed")

	var cAuthor, cContent string
	require.NoError(t, s.pool.QueryRow(ctx0(), `
SELECT author_id, content FROM comment
WHERE work_item_id=$1 AND author_type='agent' ORDER BY created_at LIMIT 1`, wi).Scan(&cAuthor, &cContent))
	assert.Equal(t, agent, cAuthor, "fallback comment author = the assigned agent")
	var result string
	require.NoError(t, s.pool.QueryRow(ctx0(),
		`SELECT result FROM agent_task_queue WHERE id=$1`, task).Scan(&result))
	assert.Equal(t, result, cContent, "fallback comment content = task output (same masking source)")

	waitFor(t, func() bool {
		var st string
		_ = s.pool.QueryRow(ctx0(), `SELECT status_key FROM work_item WHERE id=$1`, wi).Scan(&st)
		return st == "todo"
	}, "work item should reset to todo")

	var un int
	require.NoError(t, s.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM task_usage WHERE task_id=$1`, task).Scan(&un))
	assert.Equal(t, 1, un, "usage row should land")

	var inb int
	waitFor(t, func() bool {
		_ = s.pool.QueryRow(ctx0(), `
SELECT count(*) FROM notification WHERE type='task_completed' AND recipient_user_id=$1 AND work_item_id=$2`,
			uid, wi).Scan(&inb)
		return inb == 1
	}, "notification done notices should land (originator = the assigning operator)")

	var tn int
	require.NoError(t, s.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM agent_task_queue WHERE work_item_id=$1`, wi).Scan(&tn))
	assert.Equal(t, 1, tn, "assignment enqueues exactly once (reset does not re-trigger)")
}

func orchEnqueue(s *stack, ws, wi, agent string) (string, error) {
	var uid string
	_ = s.pool.QueryRow(ctx0(), `SELECT id FROM "user" LIMIT 1`).Scan(&uid)
	t, err := s.orepo.InsertTask(ctx0(), orchapp.Task{
		WorkspaceID: ws, AgentID: agent, WorkItemID: wi, Status: "queued", MaxAttempts: 2,
		Context:          map[string]any{"trigger": "assignment", "work_item_title": "goldens"},
		OriginatorUserID: uid, OriginatorSource: "direct_human", TriggerEvidenceKind: "assignment",
	})
	return t.ID, err
}

func orchEnqueueUID(s *stack, ws, wi, agent, uid string) (string, error) {
	t, err := s.orepo.InsertTask(ctx0(), orchapp.Task{
		WorkspaceID: ws, AgentID: agent, WorkItemID: wi, Status: "queued", MaxAttempts: 2,
		Context:          map[string]any{"trigger": "assignment", "work_item_title": "goldens"},
		OriginatorUserID: uid, OriginatorSource: "direct_human", TriggerEvidenceKind: "assignment",
	})
	return t.ID, err
}

func orchTask(ws, sess, agent, uid string) orchapp.Task {
	return orchapp.Task{
		WorkspaceID: ws, AgentID: agent,
		Context:          map[string]any{"chat_session_id": sess, "message": "goldens four message"},
		OriginatorUserID: uid, TriggerEvidenceKind: "chat",
	}
}

func orchTaskAttr(ws, wi, agent, uid string) orchapp.Task {
	return orchapp.Task{
		WorkspaceID: ws, AgentID: agent, WorkItemID: wi, Status: "queued", MaxAttempts: 2,
		Context:          map[string]any{"trigger": "assignment", "work_item_title": "goldens"},
		OriginatorUserID: uid, OriginatorSource: "direct_human", TriggerEvidenceKind: "assignment",
	}
}

func autoRepo(s *stack) *autoppgrepo.Repo { return autoppgrepo.New(pgWrap(s)) }

func pgWrap(s *stack) *pg.DB {
	return pg.WrapPool(s.pool)
}

func TestE2E_G9_BatchFixesGolden(t *testing.T) {
	s := newStack(t)
	s.login("alice", "g9@example.com")
	s.wsID = s.createWS(t, "alice", "g9-ws")
	ag := s.createAgent(t, s.wsID, "g9-agent")
	wi := s.createWorkItem(t, "alice", "goldens nine")

	rec := s.do("alice", http.MethodPut, "/api/work-items/"+wi,
		map[string]any{"assignee_type": "agent", "assignee_id": ag}, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())

	rec = s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/comments",
		map[string]any{"content": "[@zhang3](mention://user/00000000000000000000000000000001) please take a look"}, nil)
	require.Equal(t, 201, rec.Code)
	var adm struct {
		Admission struct {
			Result string `json:"result"`
			Reason string `json:"reason_code"`
		} `json:"admission"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &adm))
	assert.Equal(t, "blocked", adm.Admission.Result, "@user comment triggers zero: %s", rec.Body.String())

	var privID string
	require.NoError(t, s.pool.QueryRow(context.Background(), `
INSERT INTO agent (workspace_id, name, description, runtime_config, visibility)
VALUES ($1,'g9-private','private',$2,'private') RETURNING id::text`, s.wsID,
		map[string]any{"provider": "fake", "model": "fake-1"}).Scan(&privID))
	wi2 := s.createWorkItem(t, "alice", "goldens nine-b")
	rec = s.do("alice", http.MethodPut, "/api/work-items/"+wi2,
		map[string]any{"assignee_type": "agent", "assignee_id": privID}, nil)
	require.Equal(t, 200, rec.Code)
	rec = s.do("alice", http.MethodPost, "/api/work-items/"+wi2+"/comments",
		map[string]any{"content": "please handle"}, nil)
	require.Equal(t, 201, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &adm))
	assert.Equal(t, "blocked", adm.Admission.Result)
	assert.Equal(t, "invocation_not_allowed", adm.Admission.Reason, "private agent rejection code (I6 same code): %s", rec.Body.String())

	rec = s.do("alice", http.MethodPost, "/api/quick-actions",
		map[string]any{"label": "summary", "prompt": "please summarize current progress"}, nil)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var qa struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &qa))
	require.NotEmpty(t, qa.ID)
	rec = s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/quick-actions/"+qa.ID+"/run", nil, nil)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var run struct {
		TaskID string `json:"task_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &run))
	require.NotEmpty(t, run.TaskID, "the run must land a task: %s", rec.Body.String())

	var aliceID string
	require.NoError(t, s.pool.QueryRow(context.Background(),
		`SELECT id::text FROM "user" WHERE email='g9@example.com'`).Scan(&aliceID))
	require.NotEmpty(t, aliceID)
	_ = aliceID

	rec = s.do("alice", http.MethodPut, "/api/work-items/"+wi,
		map[string]any{"assignee_type": "user", "assignee_id": aliceID}, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	rec = s.do("alice", http.MethodPost, "/api/work-items/"+wi+"/system-wakeups", nil, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	rec = s.do("alice", http.MethodPost, "/api/work-items",
		map[string]any{"title": "goldens nine-sub", "status": "todo", "parent_work_item_id": wi}, nil)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var childWi struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &childWi))
	rec = s.do("alice", http.MethodPut, "/api/work-items/"+childWi.ID,
		map[string]any{"status": "done"}, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	waitFor(t, func() bool {
		rec := s.do("alice", http.MethodGet, "/api/notifications", nil, nil)
		if rec.Code != 200 {
			return false
		}
		return strings.Contains(rec.Body.String(), "wakeup_member")
	}, "member assignment wakeup notification")
}
