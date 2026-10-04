package http

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	convapp "orkome/internal/conversation/app"
)

const qaWorkItem = "00000000-0000-0000-0000-0000000000wi"

type qaRunPorts struct {
	agentID string
	enqErr  error
	prompt  string
}

func (p *qaRunPorts) wire(h *b4h) {
	h.qa.SetResolveTargetAgent(func(_ context.Context, _, _ string) (string, error) {
		return p.agentID, nil
	})
	h.qa.SetEnqueueWorkItemTask(func(_ context.Context, _, _, _, _, prompt string) (string, error) {
		p.prompt = prompt
		if p.enqErr != nil {
			return "", p.enqErr
		}
		return "task-qa-run", nil
	})
}

func mkQA(t *testing.T, h *b4h, label, prompt string) string {
	t.Helper()
	code, out := h.do("POST", "/quick-actions", map[string]any{"label": label, "prompt": prompt})
	require.Equal(t, 201, code, "create: %v", out)
	id, _ := out["id"].(string)
	require.NotEmpty(t, id)
	return id
}

func TestQARun_NilPortsUnavailablePlaceholder(t *testing.T) {
	h := newB4H(t)
	qaID := mkQA(t, h, "check risks", "check this week's delay risks")

	code, out := h.do("POST", "/work-items/"+qaWorkItem+"/quick-actions/"+qaID+"/run", nil)
	assert.Equal(t, 503, code, "run: %v", out)
	assert.Equal(t, convapp.CodeQuickActionUnavailable, errCodeOf(t, out))

	code, out = h.do("POST", "/work-items/"+qaWorkItem+"/quick-actions/"+qaID+"/render", nil)
	require.Equal(t, 200, code, "render: %v", out)
	assert.Equal(t, "check this week's delay risks", out["prompt"])
}

func TestQARun_SuccessAndRejectionMapping(t *testing.T) {
	h := newB4H(t)
	qaID := mkQA(t, h, "check risks", "check this week's delay risks")
	ports := &qaRunPorts{agentID: h.agentID}
	ports.wire(h)

	code, out := h.do("POST", "/work-items/"+qaWorkItem+"/quick-actions/"+qaID+"/run", nil)
	require.Equal(t, 201, code, "run: %v", out)
	assert.Equal(t, "task-qa-run", out["task_id"])
	assert.Equal(t, "check this week's delay risks", ports.prompt, "prompt as task input")

	ports.agentID = ""
	code, out = h.do("POST", "/work-items/"+qaWorkItem+"/quick-actions/"+qaID+"/run", nil)
	assert.Equal(t, 400, code)
	assert.Equal(t, apierr.Validation, errCodeOf(t, out))

	ports.agentID = h.agentID
	ports.enqErr = apierr.New(apierr.DuplicatePendingTask, "fence")
	code, out = h.do("POST", "/work-items/"+qaWorkItem+"/quick-actions/"+qaID+"/run", nil)
	assert.Equal(t, 409, code)
	assert.Equal(t, apierr.DuplicatePendingTask, errCodeOf(t, out))

	ports.enqErr = apierr.New(apierr.Forbidden, "agent archived")
	code, out = h.do("POST", "/work-items/"+qaWorkItem+"/quick-actions/"+qaID+"/run", nil)
	assert.Equal(t, 403, code)
	assert.Equal(t, apierr.Forbidden, errCodeOf(t, out))
}

func TestQARun_PrivateRowInvisibleToOthers(t *testing.T) {
	h := newB4H(t)
	qaID := mkQA(t, h, "private action", "private prompt")
	called := false
	h.qa.SetResolveTargetAgent(func(context.Context, string, string) (string, error) {
		called = true
		return h.agentID, nil
	})
	h.qa.SetEnqueueWorkItemTask(func(context.Context, string, string, string, string, string) (string, error) {
		called = true
		return "task-x", nil
	})

	code, out := h.doAs(h.userB, "POST", "/work-items/"+qaWorkItem+"/quick-actions/"+qaID+"/run", nil)
	assert.Equal(t, 404, code, "someone else's private row: 404 (I6): %v", out)
	assert.False(t, called, "invisible rows do not hit the port")

	code, out = h.doAs(h.userB, "POST", "/work-items/"+qaWorkItem+"/quick-actions/"+qaID+"/render", nil)
	assert.Equal(t, 404, code)

	code, out = h.do("POST", "/work-items/"+qaWorkItem+"/quick-actions/"+qaID+"/run", nil)
	assert.Equal(t, 201, code, "own private row executable: %v", out)
}
