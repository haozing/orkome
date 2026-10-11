package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestM5_ChatGolden_SendMessageToAssistantRow(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m5a@example.com")
	ws := e.createWS(t, user, "m5-a")
	a := e.createAgent(t, ws, "chatter")

	sess, err := e.conv.CreateSession(ctx0(), ws, a.ID, user, "test session")
	require.NoError(t, err)

	var lastPrompt string
	e.SetReply(func(prompt string) (string, int, int, error) {
		lastPrompt = prompt
		if !strings.Contains(prompt, "hello, please take a look") {
			return "[error] model did not hear the user message", 8, 8, nil
		}
		return "This is the assistant reply.", 8, 8, nil
	})

	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	ddone := make(chan struct{})
	go func() { defer close(ddone); d.Run(dctx) }()
	defer func() { cancel(); <-ddone }()

	msg, taskID, err := e.conv.SendMessage(ctx0(), ws, sess.ID, user, "hello, please take a look")
	require.NoError(t, err)
	require.NotEmpty(t, taskID)
	assert.Equal(t, "user", msg.Role)

	waitFor(t, func() bool {
		ms, _ := e.listMessages(sess.ID)
		for _, m := range ms {
			if m.Role == "assistant" && m.Content == "This is the assistant reply." {
				return true
			}
		}
		return false
	}, "assistant rows should be persisted (internal archive 05 §2.4)")

	assert.Contains(t, lastPrompt, "User's new message: hello, please take a look", "the prompt should contain this user message")

	_, _, err = e.conv.SendMessage(ctx0(), ws, sess.ID, user, "second question")
	require.NoError(t, err)
	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTaskByChatSession(ctx0(), ws, sess.ID)
		_ = tt
		_ = ok
		return strings.Contains(lastPrompt, "This is the assistant reply.") && strings.Contains(lastPrompt, "second question")
	}, "second-round prompt should contain history and the new message")
	assert.Contains(t, lastPrompt, "user：hello, please take a look", "history contains the first-round user message")
}

func TestM5_Cancel_RestoredOutcome(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m5b@example.com")
	ws := e.createWS(t, user, "m5-b")
	a := e.createAgent(t, ws, "canceller")

	sess, _ := e.conv.CreateSession(ctx0(), ws, a.ID, user, "cancel-restore")
	_, _, err := e.conv.SendMessage(ctx0(), ws, sess.ID, user, "stop running")
	require.NoError(t, err)

	outcome, err := e.conv.Cancel(ctx0(), ws, sess.ID, user, taskCancellerAdapter{settle: e.settle})
	require.NoError(t, err)
	assert.Equal(t, "restored", outcome, "empty transcript cancel → restored (internal archive 05 §2.5)")

	ms, _ := e.listMessages(sess.ID)
	assert.Empty(t, ms, "user messages should be deleted")

	draft, ok, err := e.conv.RestoreDraft(ctx0(), ws, sess.ID, user)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "stop running", draft)
}

func TestM5_Cancel_StoppedOutcome(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m5c@example.com")
	ws := e.createWS(t, user, "m5-c")
	a := e.createAgent(t, ws, "stopper")

	sess, _ := e.conv.CreateSession(ctx0(), ws, a.ID, user, "cancel-stop")
	_, taskID, err := e.conv.SendMessage(ctx0(), ws, sess.ID, user, "run one step first")
	require.NoError(t, err)

	require.NoError(t, e.pointers.AppendAssistantMessage(ctx0(), sess.ID, taskID, "partial reply", "message"))
	_ = taskID

	outcome, err := e.conv.Finalize(ctx0(), ws, sess.ID, user, taskID)
	require.NoError(t, err)
	assert.Equal(t, "stopped", outcome)

	ms, _ := e.listMessages(sess.ID)
	kinds := map[string]bool{}
	for _, m := range ms {
		kinds[m.Kind] = true
	}
	assert.True(t, kinds["system_stopped"], "should insert a 'stopped' row")
	assert.True(t, kinds["message"], "existing messages kept")
}

func TestM5_Privacy_DraftNeverBroadcast(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m5d@example.com")
	ws := e.createWS(t, user, "m5-d")
	a := e.createAgent(t, ws, "private")

	var broadcastLog []string
	e.bus.OnBroadcast(func(ev string, payload any) {
		broadcastLog = append(broadcastLog, ev+":"+stringify(payload))
	})

	sess, _ := e.conv.CreateSession(ctx0(), ws, a.ID, user, "privacy")
	_, _, _ = e.conv.SendMessage(ctx0(), ws, sess.ID, user, "private content XYZPDQ")
	_, err := e.conv.Cancel(ctx0(), ws, sess.ID, user, taskCancellerAdapter{settle: e.settle})
	require.NoError(t, err)

	for _, b := range broadcastLog {
		assert.NotContains(t, b, "XYZPDQ", "draft content must not enter any broadcast payload (internal archive 05 §2.5)")
	}
}

func TestM5_NoResponseKind(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "m5e@example.com")
	ws := e.createWS(t, user, "m5-e")
	a := e.createAgent(t, ws, "silent")
	sess, _ := e.conv.CreateSession(ctx0(), ws, a.ID, user, "empty reply")
	require.NoError(t, e.pointers.AppendAssistantMessage(ctx0(), sess.ID, "", "", "message"))
	ms, _ := e.listMessages(sess.ID)
	require.Len(t, ms, 1)
	assert.Equal(t, "no_response", ms[0].Kind, "empty output normalized to no_response")
}

func stringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
