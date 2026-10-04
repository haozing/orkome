package workers

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/orchestration/adapters/engine"
	orchapp "orkome/internal/orchestration/app"
)

func TestB12_SquadModeBChoreography(t *testing.T) {
	e := newEnv(t)
	e.engine.inner.SquadLoader = func(ctx context.Context, wsID, squadID string) (orchapp.SquadInfo, bool, error) {
		return e.repo.GetSquad(ctx, wsID, squadID)
	}
	user := e.createUser(t, "b12@example.com")
	ws := e.createWS(t, user, "b12")
	wi := e.createWorkItem(t, ws, user, "proposal review")

	leader := e.createAgent(t, ws, "b12-leader")
	m1 := e.createAgent(t, ws, "reviewer-a")
	m2 := e.createAgent(t, ws, "reviewer-b")
	_, err := e.pool.Exec(ctx0(), `
INSERT INTO squad (workspace_id, name, instructions, leader_agent_id, choreography)
VALUES ($1,'  review squad','review from two perspectives then summarize',$2,true)`, ws, leader.ID)
	require.NoError(t, err)
	var squadID string
	require.NoError(t, e.pool.QueryRow(ctx0(),
		`SELECT id::text FROM squad WHERE workspace_id=$1`, ws).Scan(&squadID))
	_, err = e.pool.Exec(ctx0(), `
UPDATE agent SET description='perspective A reviewer' WHERE id=$1`, m1.ID)
	require.NoError(t, err)
	_, err = e.pool.Exec(ctx0(), `
UPDATE agent SET description='perspective B reviewer' WHERE id=$1`, m2.ID)
	require.NoError(t, err)
	_, err = e.pool.Exec(ctx0(), `
INSERT INTO squad_member (squad_id, agent_id, role) VALUES ($1,$2,'member'),($1,$3,'member')`,
		squadID, m1.ID, m2.ID)
	require.NoError(t, err)

	var lastPrompt string
	e.setToolsScript(func(hist []*schema.Message) (engine.FakeStep, schema.TokenUsage, error) {
		all := ""
		for _, m := range hist {
			all += m.Content + " "
		}
		lastPrompt = all
		for _, m := range hist {
			if m.Role == schema.Tool {

				return engine.FakeStep{Text: "Summary: combining member opinions, the proposal is feasible."}, schema.TokenUsage{PromptTokens: 10, CompletionTokens: 10}, nil
			}
		}
		if containsStr(all, "[Squad Briefing]") {

			return engine.FakeStep{ToolName: "member-reviewer-a", ToolArgs: `{"task":"review the proposal"}`},
				schema.TokenUsage{PromptTokens: 5, CompletionTokens: 5}, nil
		}

		return engine.FakeStep{Text: "Member A's opinion: cost is controllable."}, schema.TokenUsage{PromptTokens: 3, CompletionTokens: 7}, nil
	})

	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	go d.Run(dctx)
	defer cancel()

	task, err := e.enq.EnqueueSquadTask(ctx0(), ws, wi, squadID, user)
	require.NoError(t, err)
	require.True(t, task.IsLeaderTask)
	require.Equal(t, squadID, task.SquadID)

	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTask(ctx0(), ws, task.ID)
		return ok && tt.Status == "completed"
	}, "squad leader the task should be done")

	tt, _, _ := e.repo.GetTask(ctx0(), ws, task.ID)
	assert.Contains(t, tt.Result, "Summary", "leader final draft should contain the summary conclusion")
	assert.Contains(t, lastPrompt, "[Squad Briefing]", "leader prompt should contain the squad briefing")
	assert.Contains(t, lastPrompt, "review squad")

	var label string
	var pt int64
	require.NoError(t, e.pool.QueryRow(ctx0(), `
SELECT agent_label, prompt_tokens FROM task_usage WHERE task_id=$1 AND agent_label='reviewer-a'`, task.ID).
		Scan(&label, &pt))
	assert.Equal(t, "reviewer-a", label)
	assert.Greater(t, pt, int64(0), "member usage non-zero (metering wrapper effective)")

	var memberRows int
	require.NoError(t, e.pool.QueryRow(ctx0(), `
SELECT count(*) FROM task_usage WHERE task_id=$1 AND agent_label LIKE 'reviewer-%'`, task.ID).
		Scan(&memberRows))
	assert.Equal(t, 1, memberRows, "members not consulted produce no usage rows")
}
