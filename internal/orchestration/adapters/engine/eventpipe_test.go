package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock      { return &fakeClock{t: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time { return c.t }

type sinkRecorder struct{ frames []string }

func (s *sinkRecorder) push(kind, content string) { s.frames = append(s.frames, content) }

func TestInterimThrottle_Table(t *testing.T) {
	type action struct {
		op      string
		kind    string
		content string
		adv     time.Duration
	}
	cases := []struct {
		name    string
		actions []action
		want    []string
	}{
		{
			name:    "first frame sent immediately",
			actions: []action{{op: "add", kind: "text", content: "a"}},
			want:    []string{"a"},
		},
		{
			name: "32-chunk arrives first and sends (32nd buffered frame triggers)",
			actions: func() []action {
				a := []action{{op: "add", kind: "text", content: "c0"}}
				for i := 1; i <= 31; i++ {
					a = append(a, action{op: "add", kind: "text", content: fmt.Sprintf("c%d", i)})
				}
				a = append(a, action{op: "add", kind: "text", content: "c32"})
				a = append(a, action{op: "add", kind: "text", content: "c33"})
				return a
			}(),
			want: []string{"c0", "c1c2c3c4c5c6c7c8c9c10c11c12c13c14c15c16c17c18c19c20c21c22c23c24c25c26c27c28c29c30c31c32"},
		},
		{
			name: "500ms arrives first and sends (chunk count far from full)",
			actions: []action{
				{op: "add", kind: "text", content: "a"},
				{op: "add", kind: "text", content: "b", adv: 100 * time.Millisecond},
				{op: "add", kind: "text", content: "c", adv: 401 * time.Millisecond},
			},
			want: []string{"a", "bc"},
		},
		{
			name: "multiple frames within the window not sent early",
			actions: []action{
				{op: "add", kind: "text", content: "a"},
				{op: "add", kind: "text", content: "b", adv: 10 * time.Millisecond},
				{op: "add", kind: "text", content: "c", adv: 10 * time.Millisecond},
			},
			want: []string{"a"},
		},
		{
			name: "tool-call boundary forces a flush (internal archive 09 §9)",
			actions: []action{
				{op: "add", kind: "text", content: "a"},
				{op: "add", kind: "text", content: "b"},
				{op: "add", kind: "text", content: "c"},
				{op: "boundary"},
				{op: "boundary"},
			},
			want: []string{"a", "bc"},
		},
		{
			name: "run closing tail-frame flush",
			actions: []action{
				{op: "add", kind: "text", content: "a"},
				{op: "add", kind: "text", content: "tail"},
				{op: "tail"},
				{op: "tail"},
			},
			want: []string{"a", "tail"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := newFakeClock()
			sink := &sinkRecorder{}
			th := newInterimThrottle(clock.Now, sink.push)
			for _, a := range tc.actions {
				clock.t = clock.t.Add(a.adv)
				switch a.op {
				case "add":
					th.add(a.kind, a.content)
				case "boundary":
					th.flushBoundary()
				case "tail":
					th.flushTail()
				}
			}
			assert.Equal(t, tc.want, sink.frames)
		})
	}
}

func TestInterimThrottle_NilPushSafe(t *testing.T) {
	th := newInterimThrottle(nil, nil)
	th.add("text", "a")
	th.flushBoundary()
	th.flushTail()
}

type fakeMsgSink struct {
	seq   int64
	rows  []orchapp.TaskMessage
	fail  bool
	calls int
}

func (f *fakeMsgSink) AppendTaskMessage(_ context.Context, _, taskID, kind, callID, content string, truncated bool) (int64, error) {
	f.calls++
	if f.fail {
		return 0, fmt.Errorf("sink down")
	}
	f.seq++
	f.rows = append(f.rows, orchapp.TaskMessage{TaskID: taskID, Seq: f.seq, Kind: kind, CallID: callID,
		Content: content, Truncated: truncated})
	return f.seq, nil
}

type progressRecord struct {
	workspaceID, taskID, stage, callID string
	seq                                int64
}

func TestEventPipe_Emit_PersistsAndPushesProgress(t *testing.T) {
	clock := newFakeClock()
	sink := &sinkRecorder{}
	msgs := &fakeMsgSink{}
	var prog []progressRecord
	e := &EinoEngine{
		InterimPush: func(ws, taskID, kind, content string) { sink.push(kind, content) },
		ProgressPush: func(ws, taskID, stage, callID string, seq int64) {
			prog = append(prog, progressRecord{ws, taskID, stage, callID, seq})
		},
		TaskMessages: msgs,
		Now:          clock.Now,
	}
	task := orchapp.Task{ID: "tk1", WorkspaceID: "ws1"}
	pipe := newEventPipe(e, task)

	pipe.emit("tool_use", "call_9", "post_comment {}")
	pipe.emit("tool_result", "call_9", "ok")
	pipe.emit("text", "", strings.Repeat("x", summaryMax+5))
	pipe.flushTail()

	require.Len(t, msgs.rows, 3)
	assert.Equal(t, orchapp.TaskMessage{TaskID: "tk1", Seq: 1, Kind: "tool_use", CallID: "call_9", Content: "post_comment {}"}, msgs.rows[0])
	assert.True(t, msgs.rows[2].Truncated, "rows beyond summaryMax must set truncated")
	assert.Equal(t, summaryMax, len([]rune(msgs.rows[2].Content)))

	require.Len(t, prog, 3, "one task:progress frame per summary-level boundary")
	assert.Equal(t, progressRecord{"ws1", "tk1", "tool_use", "call_9", 1}, prog[0])
	assert.Equal(t, progressRecord{"ws1", "tk1", "tool_result", "call_9", 2}, prog[1])
	assert.Equal(t, progressRecord{"ws1", "tk1", "text", "", 3}, prog[2])

	assert.Equal(t, []string{"post_comment {}", "ok" + strings.Repeat("x", summaryMax+5)}, sink.frames)
}

func TestEventPipe_Emit_NoProgressWhenPersistFails(t *testing.T) {
	e := &EinoEngine{TaskMessages: &fakeMsgSink{fail: true}}
	var progCount int
	e.ProgressPush = func(string, string, string, string, int64) { progCount++ }
	pipe := newEventPipe(e, orchapp.Task{ID: "tk1", WorkspaceID: "ws1"})
	pipe.emit("text", "", "hello")
	assert.Zero(t, progCount)
}

func TestEventPipe_InterimOnlyNeverProgress(t *testing.T) {
	e := &EinoEngine{}
	progCount := 0
	e.ProgressPush = func(string, string, string, string, int64) { progCount++ }
	task := orchapp.Task{ID: "tk1", WorkspaceID: "ws1"}
	pipe := newEventPipe(e, task)
	for i := 0; i < 100; i++ {
		pipe.interim.add("text", "chunk")
	}
	assert.Zero(t, progCount, "transcript level produces no task:progress (internal archive 09 §9 two-level separation)")
}

func TestEngineRun_EmissionFace(t *testing.T) {
	RegisterFakeProviderForTest(func(string) (string, schema.TokenUsage, error) {
		return "final conclusion", schema.TokenUsage{PromptTokens: 1, CompletionTokens: 2}, nil
	})
	clock := newFakeClock()
	sink := &sinkRecorder{}
	msgs := &fakeMsgSink{}
	var prog []progressRecord
	e := &EinoEngine{
		InterimPush: func(ws, taskID, kind, content string) { sink.push(kind, content) },
		ProgressPush: func(ws, taskID, stage, callID string, seq int64) {
			prog = append(prog, progressRecord{ws, taskID, stage, callID, seq})
		},
		TaskMessages: msgs,
		Now:          clock.Now,
	}
	task := orchapp.Task{ID: "00000000-0000-0000-0000-000000000001", WorkspaceID: "ws1",
		WorkItemID: "00000000-0000-0000-0000-000000000002",
		Context:    map[string]any{"trigger": "assignment", "work_item_title": "T"}}
	agent := orchapp.Agent{ID: "agent-0001", WorkspaceID: "ws1",
		RuntimeConfig: map[string]any{"provider": "fake", "model": "m"}}

	out, err := e.Execute(context.Background(), task, agent, "")
	require.NoError(t, err)
	assert.Equal(t, "final conclusion", out.Output)

	require.Len(t, prog, 1, "single-turn final draft → one task:progress(text) frame")
	assert.Equal(t, progressRecord{"ws1", task.ID, "text", "", 1}, prog[0])
	require.Len(t, msgs.rows, 1, "one-line summary persistent row")
	assert.Equal(t, "text", msgs.rows[0].Kind)
	assert.Equal(t, []string{"final conclusion"}, sink.frames, "interim: first frame sent immediately + tail frames have no residue")
}
