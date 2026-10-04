package engine

import (
	"context"

	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

func TestLeaderToolsFullRun_ModeA(t *testing.T) {
	var mu sync.Mutex
	var prompt string
	var dispatches []orchapp.LeaderDispatchInput
	step := 0
	RegisterFakeProviderToolsForTest(func(hist []*schema.Message) (FakeStep, schema.TokenUsage, error) {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range hist {
			if m.Role == "user" {
				prompt = m.Content
			}
		}
		step++
		if step == 1 {
			return FakeStep{ToolName: "dispatch_to_member",
				ToolArgs: `{"member":"scout","instruction":"scan"}`}, schema.TokenUsage{}, nil
		}
		return FakeStep{Text: "dispatched and wrapped up."}, schema.TokenUsage{}, nil
	})

	d := &recordingDispatcher{mu: &mu, calls: &dispatches}
	e := &EinoEngine{
		LeaderDispatch: d,
		SquadLoader: func(context.Context, string, string) (orchapp.SquadInfo, bool, error) {
			return orchapp.SquadInfo{
				ID: "sq-1", Name: "alpha", Instructions: "division of labor",
				Members: []orchapp.SquadMember{{AgentID: "m-1", Name: "scout", Role: "member"}},
			}, true, nil
		},
	}
	task := orchapp.Task{
		ID: "t1", WorkspaceID: "ws-1", WorkItemID: "wi-1", Status: "running",
		IsLeaderTask: true, SquadID: "sq-1",
		OriginatorUserID: "human-1", OriginatorSource: "direct_human",
		Context: map[string]any{"trigger": "squad"},
	}
	out, err := e.Execute(context.Background(), task,
		orchapp.Agent{ID: "leader-1", RuntimeConfig: map[string]any{"provider": "fake", "model": "f"}}, "")
	require.NoError(t, err)
	assert.Equal(t, "completed", out.Outcome)
	assert.Contains(t, out.Output, "dispatched")

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, prompt, "dispatch_to_member", "dispatch duty")
	assert.Contains(t, prompt, "create_child_work_item", "tracking duty")
	assert.Contains(t, prompt, "record_evaluation", "wrap-up duty")
	assert.Contains(t, prompt, "no_action", "no_action forbids substituting with a comment")

	require.Len(t, dispatches, 1)
	assert.Equal(t, "m-1", dispatches[0].MemberAgentID)
	assert.Equal(t, "t1", dispatches[0].LeaderTaskID)
	assert.Equal(t, "human-1", dispatches[0].OriginatorUserID)
}

func TestLeaderToolsNotAssembledForNonLeader(t *testing.T) {
	var mu sync.Mutex
	var lastToolResult string
	step := 0
	RegisterFakeProviderToolsForTest(func(hist []*schema.Message) (FakeStep, schema.TokenUsage, error) {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range hist {
			if m.Role == "tool" {
				lastToolResult = m.Content
			}
		}
		step++
		if step == 1 {
			return FakeStep{ToolName: "dispatch_to_member", ToolArgs: `{}`}, schema.TokenUsage{}, nil
		}
		return FakeStep{Text: "tool does not exist, change it the normal way."}, schema.TokenUsage{}, nil
	})
	var dispatches []orchapp.LeaderDispatchInput
	e := &EinoEngine{
		LeaderDispatch: &recordingDispatcher{mu: &mu, calls: &dispatches},
	}
	task := orchapp.Task{ID: "t2", WorkspaceID: "ws-1", WorkItemID: "wi-1", Status: "running",
		Context: map[string]any{"trigger": "assignment"}}
	_, err := e.Execute(context.Background(), task,
		orchapp.Agent{ID: "agent-9-aaaaaaaa", RuntimeConfig: map[string]any{"provider": "fake", "model": "f"}}, "")
	require.NoError(t, err)
	assert.Empty(t, dispatches, "non-leader task dispatch port zero calls")
	assert.Contains(t, lastToolResult, "does not exist", "unknown tools take the corrective hint")
}

type recordingDispatcher struct {
	mu    *sync.Mutex
	calls *[]orchapp.LeaderDispatchInput
}

func (r *recordingDispatcher) EnqueueLeaderDispatch(_ context.Context, in orchapp.LeaderDispatchInput) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.calls = append(*r.calls, in)
	return "member-task-77", "queued", nil
}
