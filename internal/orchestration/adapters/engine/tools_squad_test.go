package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

type fakeLeaderDispatcher struct {
	calls  []orchapp.LeaderDispatchInput
	taskID string
	reason string
	err    error
}

func (f *fakeLeaderDispatcher) EnqueueLeaderDispatch(_ context.Context, in orchapp.LeaderDispatchInput) (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	f.calls = append(f.calls, in)
	if f.reason == "" {
		f.reason = "queued"
	}
	return f.taskID, f.reason, nil
}

type fakeChildCreator struct {
	calls []orchapp.ChildItemInput
	item  orchapp.WorkItemInfo
	err   error
}

func (f *fakeChildCreator) CreateChildWorkItem(_ context.Context, in orchapp.ChildItemInput) (orchapp.WorkItemInfo, error) {
	if f.err != nil {
		return orchapp.WorkItemInfo{}, f.err
	}
	f.calls = append(f.calls, in)
	return f.item, nil
}

type fakeEvalRecorder struct {
	calls []orchapp.SquadEvaluation
	err   error
}

func (f *fakeEvalRecorder) RecordSquadEvaluation(_ context.Context, e orchapp.SquadEvaluation) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, e)
	return nil
}

func leaderTaskFixture() (orchapp.Task, orchapp.Agent) {
	return orchapp.Task{
		ID: "task-1", WorkspaceID: "ws-1", WorkItemID: "wi-1",
		IsLeaderTask: true, SquadID: "sq-1",
		OriginatorUserID: "human-1", AccountableUserID: "human-1",
		OriginatorSource: "direct_human",
	}, orchapp.Agent{ID: "leader-1", Name: "captain"}
}

func rosterFixture() []orchapp.SquadMember {
	return []orchapp.SquadMember{
		{AgentID: "m-1", Name: "scout"},
		{AgentID: "m-2", Name: "builder"},
	}
}

func TestDispatchToMember_AttributionAndRoster(t *testing.T) {
	task, agent := leaderTaskFixture()
	d := &fakeLeaderDispatcher{taskID: "member-task-9"}
	tl := &dispatchToMemberTool{dispatch: d, task: task, agent: agent, members: rosterFixture()}

	out, err := tl.InvokableRun(context.Background(),
		`{"member":"scout","instruction":"scan attachments"}`)
	require.NoError(t, err)
	require.Len(t, d.calls, 1)
	c := d.calls[0]
	assert.Equal(t, "ws-1", c.WorkspaceID)
	assert.Equal(t, "wi-1", c.WorkItemID, "work item with no leader task attached")
	assert.Equal(t, "sq-1", c.SquadID)
	assert.Equal(t, "task-1", c.LeaderTaskID)
	assert.Equal(t, "leader-1", c.LeaderAgentID)
	assert.Equal(t, "m-1", c.MemberAgentID, "members resolved by name")
	assert.Equal(t, "scout", c.MemberName)
	assert.Equal(t, "scan attachments", c.Instruction)

	assert.Equal(t, "human-1", c.OriginatorUserID)
	assert.Equal(t, "human-1", c.AccountableUserID)
	assert.Contains(t, out, "Dispatched to scout")
	assert.Contains(t, out, "member-task-9")
}

func TestDispatchToMember_ByIDAndUnknownMember(t *testing.T) {
	task, agent := leaderTaskFixture()
	d := &fakeLeaderDispatcher{taskID: "t"}
	tl := &dispatchToMemberTool{dispatch: d, task: task, agent: agent, members: rosterFixture()}

	out, err := tl.InvokableRun(context.Background(),
		`{"member":"m-2","instruction":"x"}`)
	require.NoError(t, err)
	require.Len(t, d.calls, 1)
	assert.Equal(t, "m-2", d.calls[0].MemberAgentID)

	out, err = tl.InvokableRun(context.Background(),
		`{"member":"nobody","instruction":"x"}`)
	require.NoError(t, err, "unknown member produces no Go error (protocol does not interrupt)")
	assert.Empty(t, d.calls[1:])
	assert.Contains(t, out, "not on the squad roster")
	assert.Contains(t, out, "scout", "hint carries available members")
}

func TestDispatchToMember_AdmissionReasons(t *testing.T) {
	task, agent := leaderTaskFixture()
	d := &fakeLeaderDispatcher{reason: "already_active"}
	tl := &dispatchToMemberTool{dispatch: d, task: task, agent: agent, members: rosterFixture()}
	out, err := tl.InvokableRun(context.Background(),
		`{"member":"scout","instruction":"x"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "already_active")
	assert.Contains(t, out, "Try another member")
}

func TestDispatchToMember_PortErrorNormalized(t *testing.T) {
	task, agent := leaderTaskFixture()
	d := &fakeLeaderDispatcher{err: errors.New("db down")}
	tl := &dispatchToMemberTool{dispatch: d, task: task, agent: agent, members: rosterFixture()}
	out, err := tl.InvokableRun(context.Background(),
		`{"member":"scout","instruction":"x"}`)
	require.NoError(t, err, "protocol does not interrupt after normalization")
	assert.Contains(t, out, "agent_error.tool_failed")
	assert.Contains(t, out, "db down")
}

func TestCreateChildWorkItem_ChainRootResolution(t *testing.T) {
	task, agent := leaderTaskFixture()
	cc := &fakeChildCreator{item: orchapp.WorkItemInfo{ID: "child-1", Title: "research"}}
	tl := &createChildWorkItemTool{children: cc, task: task, agent: agent}

	_, err := tl.InvokableRun(context.Background(),
		`{"title":"subtask","description":"breakdown"}`)
	require.NoError(t, err)
	require.Len(t, cc.calls, 1)
	c := cc.calls[0]
	assert.Equal(t, "ws-1", c.WorkspaceID)
	assert.Equal(t, "wi-1", c.ParentID)
	assert.Equal(t, "leader-1", c.CreatorAgentID, "creator = engine-built agent Principal")
	assert.Equal(t, "subtask", c.Title)
	assert.Equal(t, orchapp.ChildDelegation{
		OriginType: "agent_create", ChainRootUserID: "human-1", ChainRootDirectHuman: true,
	}, c.Delegation, "chain root = originator authorization column, direct_human ⇔ column valid")

	cc2 := &fakeChildCreator{item: orchapp.WorkItemInfo{ID: "child-2"}}
	noRoot := task
	noRoot.OriginatorUserID = ""
	noRoot.AccountableUserID = "auditor-only"
	noRoot.OriginatorSource = "rule_owner"
	tl2 := &createChildWorkItemTool{children: cc2, task: noRoot, agent: agent}
	_, err = tl2.InvokableRun(context.Background(), `{"title":"x"}`)
	require.NoError(t, err)
	assert.Equal(t, orchapp.ChildDelegation{
		OriginType: "agent_create", ChainRootUserID: "", ChainRootDirectHuman: false,
	}, cc2.calls[0].Delegation, "no direct_human chain root: no audit-column fallback (rule triggers must not be wrongly subscribed)")
}

func TestCreateChildWorkItem_PortErrorNormalized(t *testing.T) {
	task, agent := leaderTaskFixture()
	cc := &fakeChildCreator{err: errors.New("fence")}
	tl := &createChildWorkItemTool{children: cc, task: task, agent: agent}
	out, err := tl.InvokableRun(context.Background(), `{"title":"x"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "agent_error.tool_failed")
}

func TestRecordEvaluation_ProvenanceAntiForgery(t *testing.T) {
	task, agent := leaderTaskFixture()
	rec := &fakeEvalRecorder{}
	tl := &recordEvaluationTool{recorder: rec, task: task, agent: agent}

	out, err := tl.InvokableRun(context.Background(),
		`{"member":"scout","verdict":"great","rationale":"r"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "action, no_action, or failed")
	assert.Empty(t, rec.calls)

	_, err = tl.InvokableRun(context.Background(),
		`{"member":"scout","verdict":"no_action","rationale":"no action needed"}`)
	require.NoError(t, err)
	require.Len(t, rec.calls, 1)
	e := rec.calls[0]
	assert.Equal(t, "task-1", e.TaskID, "provenance taken from the task row")
	assert.Equal(t, "sq-1", e.SquadID)
	assert.Equal(t, "leader-1", e.LeaderAgentID, "actor = the task row's agent, not a parameter")
	assert.Equal(t, "wi-1", e.WorkItemID)
	assert.Equal(t, "scout", e.Member)
	assert.Equal(t, "no_action", e.Verdict)

	rec.err = errors.New("activity write failed")
	out, err = tl.InvokableRun(context.Background(),
		`{"member":"scout","verdict":"action","rationale":"r"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "agent_error.tool_failed")
}

func TestChainRootOf_Table(t *testing.T) {
	cases := []struct {
		name string
		task orchapp.Task
		want orchapp.ChildDelegation
	}{
		{"direct_human", orchapp.Task{OriginatorUserID: "u1", OriginatorSource: "direct_human"},
			orchapp.ChildDelegation{OriginType: "agent_create", ChainRootUserID: "u1", ChainRootDirectHuman: true}},
		{"delegation copies the chain", orchapp.Task{OriginatorUserID: "u1", OriginatorSource: "delegation"},
			orchapp.ChildDelegation{OriginType: "agent_create", ChainRootUserID: "u1", ChainRootDirectHuman: true}},
		{"rule_owner unauthorized", orchapp.Task{AccountableUserID: "u2", OriginatorSource: "rule_owner"},
			orchapp.ChildDelegation{OriginType: "agent_create"}},
		{"unattributed", orchapp.Task{AccountableUserID: "u3", OriginatorSource: "unattributed"},
			orchapp.ChildDelegation{OriginType: "agent_create"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chainRootOf(c.task)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestSquadToolArgValidation(t *testing.T) {
	task, agent := leaderTaskFixture()
	ctx := context.Background()

	d := &fakeLeaderDispatcher{}
	dt := &dispatchToMemberTool{dispatch: d, task: task, agent: agent, members: rosterFixture()}
	out, err := dt.InvokableRun(ctx, `not-json`)
	require.NoError(t, err)
	assert.True(t, strings.Contains(out, "invalid args") || strings.Contains(out, "member"), out)

	cc := &fakeChildCreator{}
	ct := &createChildWorkItemTool{children: cc, task: task, agent: agent}
	out, err = ct.InvokableRun(ctx, `{}`)
	require.NoError(t, err)
	assert.Contains(t, out, "title")

	rec := &fakeEvalRecorder{}
	rt := &recordEvaluationTool{recorder: rec, task: task, agent: agent}
	out, err = rt.InvokableRun(ctx, `{"verdict":"action"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "member")
}
