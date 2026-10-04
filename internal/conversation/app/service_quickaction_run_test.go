package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

type fakeQARepo struct {
	rows   []QuickAction
	insert []QuickAction
}

func (f *fakeQARepo) InsertQuickAction(_ context.Context, q QuickAction) (QuickAction, error) {
	f.insert = append(f.insert, q)
	return q, nil
}

func (f *fakeQARepo) ListQuickActions(_ context.Context, wsID, userID string) ([]QuickAction, error) {
	var out []QuickAction
	for _, q := range f.rows {
		if q.WorkspaceID == wsID && (q.UserID == userID || !q.IsPrivate) {
			out = append(out, q)
		}
	}
	return out, nil
}

func (f *fakeQARepo) UpdateQuickAction(_ context.Context, _, _, _, _, _ string) (QuickAction, bool, error) {
	return QuickAction{}, false, nil
}

func (f *fakeQARepo) DeleteQuickAction(_ context.Context, _, _, _ string) error { return nil }

type fakeRunPorts struct {
	agentID    string
	resolveErr error
	enqErr     error
	resolved   []struct{ ws, wi string }
	enqueued   []struct{ ws, wi, agent, user, prompt string }
}

func (f *fakeRunPorts) resolveFn() ResolveTargetAgentFunc {
	return func(_ context.Context, ws, wi string) (string, error) {
		f.resolved = append(f.resolved, struct{ ws, wi string }{ws, wi})
		if f.resolveErr != nil {
			return "", f.resolveErr
		}
		return f.agentID, nil
	}
}

func (f *fakeRunPorts) enqueueFn() EnqueueWorkItemTaskFunc {
	return func(_ context.Context, ws, wi, agent, user, prompt string) (string, error) {
		f.enqueued = append(f.enqueued, struct{ ws, wi, agent, user, prompt string }{ws, wi, agent, user, prompt})
		if f.enqErr != nil {
			return "", f.enqErr
		}
		return "task-qa", nil
	}
}

func qaRow(ws, user string, private bool) QuickAction {
	return QuickAction{ID: "qa1", WorkspaceID: ws, UserID: user,
		Label: "check risks", Prompt: "check this week's delay risks", IsPrivate: private}
}

func TestQuickActionRun_VisibilityOwnerOrPublic(t *testing.T) {
	privateRow := qaRow("ws1", "u1", true)
	publicRow := qaRow("ws1", "u1", false)
	publicRow.ID = "qa2"
	repo := &fakeQARepo{rows: []QuickAction{privateRow, publicRow}}
	ports := &fakeRunPorts{agentID: "ag1"}
	s := NewQuickActionService(repo)
	s.SetResolveTargetAgent(ports.resolveFn())
	s.SetEnqueueWorkItemTask(ports.enqueueFn())

	_, err := s.Run(context.Background(), "ws1", "wi1", "qa1", "u2")
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))
	assert.Empty(t, ports.resolved)

	taskID, err := s.Run(context.Background(), "ws1", "wi1", "qa2", "u2")
	require.NoError(t, err)
	assert.Equal(t, "task-qa", taskID)

	taskID, err = s.Run(context.Background(), "ws1", "wi1", "qa1", "u1")
	require.NoError(t, err)
	assert.Equal(t, "task-qa", taskID)
}

func TestQuickActionRun_NoAgentAssigneeRejected(t *testing.T) {
	repo := &fakeQARepo{rows: []QuickAction{qaRow("ws1", "u1", true)}}
	ports := &fakeRunPorts{agentID: ""}
	s := NewQuickActionService(repo)
	s.SetResolveTargetAgent(ports.resolveFn())
	s.SetEnqueueWorkItemTask(ports.enqueueFn())

	_, err := s.Run(context.Background(), "ws1", "wi1", "qa1", "u1")
	require.Error(t, err)
	assert.Equal(t, "E_VALIDATION", errCode(t, err))
	assert.Equal(t, "work item has no agent assignee", err.Error())
	assert.Empty(t, ports.enqueued, "no agent, no enqueue")
}

func TestQuickActionRun_NilPortsAreUnavailablePlaceholder(t *testing.T) {
	repo := &fakeQARepo{rows: []QuickAction{qaRow("ws1", "u1", true)}}

	s := NewQuickActionService(repo)
	_, err := s.Run(context.Background(), "ws1", "wi1", "qa1", "u1")
	require.Error(t, err)
	assert.Equal(t, CodeQuickActionUnavailable, errCode(t, err), "unwired placeholder code")

	_, err = s.Run(context.Background(), "ws1", "wi1", "qa1", "u2")
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))

	ports := &fakeRunPorts{agentID: "ag1"}
	s2 := NewQuickActionService(repo)
	s2.SetResolveTargetAgent(ports.resolveFn())
	_, err = s2.Run(context.Background(), "ws1", "wi1", "qa1", "u1")
	require.Error(t, err)
	assert.Equal(t, CodeQuickActionUnavailable, errCode(t, err))
}

func TestQuickActionRun_SuccessPassesPromptAsInput(t *testing.T) {
	repo := &fakeQARepo{rows: []QuickAction{qaRow("ws1", "u1", true)}}
	ports := &fakeRunPorts{agentID: "ag9"}
	s := NewQuickActionService(repo)
	s.SetResolveTargetAgent(ports.resolveFn())
	s.SetEnqueueWorkItemTask(ports.enqueueFn())

	taskID, err := s.Run(context.Background(), "ws1", "wi7", "qa1", "u1")
	require.NoError(t, err)
	assert.Equal(t, "task-qa", taskID)
	require.Len(t, ports.resolved, 1)
	assert.Equal(t, []struct{ ws, wi string }{{"ws1", "wi7"}}, ports.resolved)
	require.Len(t, ports.enqueued, 1)
	got := ports.enqueued[0]
	assert.Equal(t, "ws1", got.ws)
	assert.Equal(t, "wi7", got.wi)
	assert.Equal(t, "ag9", got.agent, "enqueue agent = resolved result")
	assert.Equal(t, "u1", got.user)
	assert.Equal(t, "check this week's delay risks", got.prompt, "prompt as task input")
}

func TestQuickActionRun_EnqueueRejectionPassesThrough(t *testing.T) {
	repo := &fakeQARepo{rows: []QuickAction{qaRow("ws1", "u1", true)}}
	ports := &fakeRunPorts{agentID: "ag1",
		enqErr: apierr.New(apierr.DuplicatePendingTask, "I1 fence")}
	s := NewQuickActionService(repo)
	s.SetResolveTargetAgent(ports.resolveFn())
	s.SetEnqueueWorkItemTask(ports.enqueueFn())

	_, err := s.Run(context.Background(), "ws1", "wi1", "qa1", "u1")
	require.Error(t, err)
	assert.Equal(t, apierr.DuplicatePendingTask, errCode(t, err), "I1 fence code passthrough (http 409)")

	ports.enqErr = apierr.New(apierr.Forbidden, "agent archived")
	_, err = s.Run(context.Background(), "ws1", "wi1", "qa1", "u1")
	require.Error(t, err)
	assert.Equal(t, apierr.Forbidden, errCode(t, err), "rejection code passthrough (http 403)")

	ports.resolveErr = errors.New("work repo down")
	_, err = s.Run(context.Background(), "ws1", "wi1", "qa1", "u1")
	require.Error(t, err)
	assert.EqualError(t, err, "work repo down", "parse port error passthrough")
}

func TestQuickActionRender_ReturnsPromptAsIs(t *testing.T) {
	repo := &fakeQARepo{rows: []QuickAction{{ID: "qa1", WorkspaceID: "ws1", UserID: "u1",
		Label: "summary", Prompt: "summarize progress of {{work_item.title}}", IsPrivate: false}}}
	s := NewQuickActionService(repo)

	q, err := s.Render(context.Background(), "ws1", "qa1", "u2")
	require.NoError(t, err)
	assert.Equal(t, "summarize progress of {{work_item.title}}", q.Prompt, "template variable undefined — returned as-is")

	_, err = s.Render(context.Background(), "ws1", "nope", "u1")
	require.Error(t, err)
	assert.Equal(t, "E_NOT_FOUND", errCode(t, err))
}
