package workers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/orchestration/adapters/engine"
)

func TestB13_MultimodalOutputParts(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b13@example.com")
	ws := e.createWS(t, user, "b13")
	a := e.createAgent(t, ws, "b13-artist")
	sess, err := e.conv.CreateSession(ctx0(), ws, a.ID, user, "multimodal session")
	require.NoError(t, err)

	e.setToolsScript(func(hist []*schema.Message) (engine.FakeStep, schema.TokenUsage, error) {
		return engine.FakeStep{
			Text: "This is the generated diagram description.",
			MultiContent: []schema.ChatMessagePart{
				{Type: schema.ChatMessagePartTypeText, Text: "This is the generated diagram description."},
				{Type: schema.ChatMessagePartTypeImageURL, ImageURL: &schema.ChatMessageImageURL{
					URL: "https://cdn.example/gen/chart-1.png"}},
			},
		}, schema.TokenUsage{PromptTokens: 2, CompletionTokens: 9}, nil
	})

	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	go d.Run(dctx)
	defer cancel()

	_, taskID, err := e.conv.SendMessage(ctx0(), ws, sess.ID, user, "draw a picture")
	require.NoError(t, err)

	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTaskByChatSession(ctx0(), ws, sess.ID)
		return ok && tt.Status == "completed"
	}, "multimodal the task should be done")

	var content string
	var partsRaw []byte
	require.NoError(t, e.pool.QueryRow(ctx0(), `
SELECT content, parts FROM chat_message WHERE session_id=$1 AND role='assistant' AND task_id=$2
ORDER BY seq DESC LIMIT 1`, sess.ID, taskID).Scan(&content, &partsRaw))
	assert.Equal(t, "This is the generated diagram description.", content)
	var parts []map[string]any
	require.NoError(t, json.Unmarshal(partsRaw, &parts))
	require.Len(t, parts, 2)
	assert.Equal(t, "text", parts[0]["type"])
	assert.Equal(t, "image", parts[1]["type"])
	assert.Contains(t, parts[1]["url"], "chart-1.png")
}

func TestB13_InputAttachmentManifest(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b13b@example.com")
	ws := e.createWS(t, user, "b13-b")
	a := e.createAgent(t, ws, "b13b")
	sess, err := e.conv.CreateSession(ctx0(), ws, a.ID, user, "session with attachments")
	require.NoError(t, err)

	var lastPrompt string
	e.SetReply(func(prompt string) (string, int, int, error) {
		lastPrompt = prompt
		return "Attachment received.", 1, 1, nil
	})

	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	go d.Run(dctx)
	defer cancel()

	msg, taskID, err := e.conv.SendMessage(ctx0(), ws, sess.ID, user, "take a look at this material")
	require.NoError(t, err)
	_, err = e.pool.Exec(ctx0(), `
UPDATE agent_task_queue SET context = context || jsonb_build_object('attachments',
  jsonb_build_array(jsonb_build_object('name','  quarterly-data.xlsx','content_type','application/vnd.ms-excel')))
WHERE id=$1`, taskID)
	require.NoError(t, err)

	waitFor(t, func() bool {
		tt, ok, _ := e.repo.GetTaskByChatSession(ctx0(), ws, sess.ID)
		return ok && tt.Status == "completed"
	}, "the task should be done")

	assert.Contains(t, lastPrompt, "This message has attachments", "attachment list section")
	assert.Contains(t, lastPrompt, "quarterly-data.xlsx", "attachment names into the prompt")
	_ = msg
}
