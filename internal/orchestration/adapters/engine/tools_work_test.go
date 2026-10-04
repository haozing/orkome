package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

type fakeStatusUpdater struct {
	calls []struct{ ws, wi, agent, status string }
	err   error
}

func (f *fakeStatusUpdater) UpdateStatusByAgent(_ context.Context, ws, wi, agent, status string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, struct{ ws, wi, agent, status string }{ws, wi, agent, status})
	return nil
}

type checkinCall struct{ ws, wi, wakeupID, taskID, note string }

type fakeCheckiner struct {
	calls []checkinCall
	err   error
}

func (f *fakeCheckiner) CheckInWakeup(_ context.Context, ws, wi, wakeupID, taskID, note string) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, checkinCall{ws, wi, wakeupID, taskID, note})
	return nil
}

func TestUpdateWorkItemStatusTool(t *testing.T) {
	task := orchapp.Task{ID: "t1", WorkspaceID: "ws-1", WorkItemID: "wi-1"}
	agent := orchapp.Agent{ID: "a-1"}
	ctx := context.Background()

	up := &fakeStatusUpdater{}
	tl := &updateWorkItemStatusTool{update: up, task: task, agent: agent}
	out, err := tl.InvokableRun(ctx, `{"status":"in_progress"}`)
	require.NoError(t, err)
	require.Len(t, up.calls, 1)
	assert.Equal(t, "ws-1", up.calls[0].ws)
	assert.Equal(t, "wi-1", up.calls[0].wi)
	assert.Equal(t, "a-1", up.calls[0].agent, "executed with the engine-built agent Principal (I-E5)")
	assert.Equal(t, "in_progress", up.calls[0].status)
	assert.Contains(t, out, "in_progress")

	up.err = apierr.NewValidation("work item in triage: status locked")
	out, err = tl.InvokableRun(ctx, `{"status":"done"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "rejected")
	assert.Contains(t, out, "triage")

	up.err = errors.New("connection refused")
	out, err = tl.InvokableRun(ctx, `{"status":"done"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "agent_error.tool_failed")

	out, err = tl.InvokableRun(ctx, `{}`)
	require.NoError(t, err)
	assert.Contains(t, out, "status")
}

func TestCheckinWakeupTool(t *testing.T) {
	task := orchapp.Task{
		ID: "t9", WorkspaceID: "ws-1", WorkItemID: "wi-1",
		Context: map[string]any{"wakeup_id": "wk-7"},
	}
	ctx := context.Background()

	ck := &fakeCheckiner{}
	tl := &checkinWakeupTool{checkiner: ck, task: task, wakeupID: "wk-7"}
	out, err := tl.InvokableRun(ctx, `{"note":"checked, no action needed"}`)
	require.NoError(t, err)
	require.Len(t, ck.calls, 1)
	c := ck.calls[0]
	assert.Equal(t, "ws-1", c.ws)
	assert.Equal(t, "wi-1", c.wi)
	assert.Equal(t, "wk-7", c.wakeupID, "receipt anchored to the wakeup rule (context.wakeup_id)")
	assert.Equal(t, "t9", c.taskID, "idempotent key = once per task (automation side)")
	assert.Equal(t, "checked, no action needed", c.note)
	assert.Contains(t, out, "Check-in recorded.")

	ck.err = apierr.NewValidation("checkin note must be <= 500 runes")
	out, err = tl.InvokableRun(ctx, `{"note":"n"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "rejected")
	assert.Contains(t, out, "500")

	ck.err = errors.New("boom")
	out, err = tl.InvokableRun(ctx, `{"note":"n"}`)
	require.NoError(t, err)
	assert.Contains(t, out, "agent_error.tool_failed")
}
