package workers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/events"
	"orkome/internal/contract/wswire"
	convapp "orkome/internal/conversation/app"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
)

func TestB9_EngineGenerateAndParsing(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b9a@example.com")
	ws := e.createWS(t, user, "b9-a")
	a := e.agentWithRuntime(t, ws, map[string]any{"provider": "fake", "model": "m"})

	e.SetReply(func(prompt string) (string, int, int, error) {
		return `  [{"label":"continue research","prompt":"dig deeper into this direction"}]`, 3, 7, nil
	})
	out, usage, err := e.engine.Generate(ctx0(), a, "", "suggester", "assistant reply: conclusion")
	require.NoError(t, err)
	assert.Contains(t, out, "continue research")
	assert.Equal(t, "fake", usage.Provider)
	assert.EqualValues(t, 3, usage.PromptTokens)
	assert.EqualValues(t, 7, usage.CompletionTokens)

	qs := convapp.ParseSuggestions(out)
	require.Len(t, qs, 1)
	assert.Equal(t, "continue research", qs[0].Label)

	assert.Len(t, convapp.ParseSuggestions("suggestions:\n[{\"label\":\"A\",\"prompt\":\"p\"}] done"), 1)
	assert.Empty(t, convapp.ParseSuggestions("not JSON"))
	assert.Len(t, convapp.ParseSuggestions(
		`  [{"label":"a","prompt":"p"},{"label":"missing prompt"}]`), 1, "invalid rows silently trimmed")
}

func TestB9_QuickActionGenerationChain(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b9b@example.com")
	ws := e.createWS(t, user, "b9-b")
	a := e.createAgent(t, ws, "b9-suggester")
	sess, err := e.conv.CreateSession(ctx0(), ws, a.ID, user, "suggested session")
	require.NoError(t, err)

	qaSeen := make(chan struct{}, 4)
	e.bus.On(eventbus.PhaseBroadcast, func(_ context.Context, ev events.Envelope) {
		if ev.Event == "chat.quick_actions" {
			qaSeen <- struct{}{}
		}
	})

	e.SetReply(func(prompt string) (string, int, int, error) {
		if len(prompt) >= 6 && prompt[:6] == "You are in a conversation with the user" {
			return "This is the final answer.", 1, 1, nil
		}
		return `  [{"label":"ask for details","prompt":"expand point two"},{"label":"bad"},{"label":"three","prompt":"p3"}]`, 1, 1, nil
	})

	gen := convapp.NewQuickActionGenerator(e.convRepo)
	gen.Engine = qaGenEngine{inner: e.engine}
	gen.TaskLookup = func(ctx context.Context, taskID string) (string, string, string, bool) {
		var wsID, agentID, creator string
		err := e.pool.QueryRow(ctx, `
SELECT workspace_id::text, agent_id::text, coalesce(originator_user_id::text,'') FROM agent_task_queue
WHERE id=$1`, taskID).Scan(&wsID, &agentID, &creator)
		return wsID, agentID, creator, err == nil
	}
	gen.AgentLookup = func(ctx context.Context, wsID, agentID string) (convapp.AgentLite, bool) {
		ag, ok, err := e.repo.GetAgent(ctx, wsID, agentID)
		if err != nil || !ok {
			return convapp.AgentLite{}, false
		}
		return convapp.AgentLite{WorkspaceID: ag.WorkspaceID, ID: ag.ID,
			Description: ag.Description, RuntimeConfig: ag.RuntimeConfig}, true
	}
	gen.AuxUsage = func(ctx context.Context, wsID string, u convapp.UsageLite) error {
		return e.repo.InsertAuxUsage(ctx, wsID, orchapp.UsageReport{
			Provider: u.Provider, Model: u.Model, AgentLabel: "aux",
			PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens,
		})
	}
	gen.Emit = func(ctx context.Context, wsID, sessionID string, created []convapp.QuickAction) {
		e.bus.Emit(ctx, events.Envelope{Event: "chat.quick_actions", WorkspaceID: wsID,
			Payload: map[string]any{"session_id": sessionID, "quick_actions": created}})
	}
	e.bus.On(eventbus.PhaseBroadcast, func(_ context.Context, ev events.Envelope) {
		if ev.Event == events.ChatDone {
			if p, ok := ev.Payload.(wswire.ChatDonePayload); ok {
				gen.OnChatDone(context.Background(), p.SessionID, p.TaskID, p.Content)
			}
		}
	})

	d := e.newDispatcher()
	dctx, cancel := context.WithCancel(ctx0())
	go d.Run(dctx)
	defer cancel()

	_, taskID, err := e.conv.SendMessage(ctx0(), ws, sess.ID, user, "help me analyze")
	require.NoError(t, err)
	require.NotEmpty(t, taskID)

	<-qaSeen

	var rows int
	require.NoError(t, e.pool.QueryRow(ctx0(),
		`SELECT count(*) FROM quick_action WHERE workspace_id=$1`, ws).Scan(&rows))
	assert.Equal(t, 2, rows, "bad rows (missing prompt) trimmed, exactly 2 left")

	qrows, err := e.convRepo.ListQuickActions(ctx0(), ws, user)
	require.NoError(t, err)
	found := map[string]bool{}
	for _, q := range qrows {
		found[q.Label] = true
		assert.True(t, q.IsPrivate, "suggestion rows private (03: private rows belong to the creator)")
	}
	assert.True(t, found["ask for details"])
	assert.True(t, found["three"])

	var aux int
	require.NoError(t, e.pool.QueryRow(ctx0(), `
SELECT count(*) FROM task_usage WHERE workspace_id=$1 AND task_id IS NULL AND agent_id IS NULL`, ws).Scan(&aux))
	assert.Equal(t, 1, aux)
}

type qaGenEngine struct{ inner *fakeOrRealEngine }

func (q qaGenEngine) Generate(ctx context.Context, a convapp.AgentLite, secret, system, input string) (string, convapp.UsageLite, error) {
	out, u, err := q.inner.Generate(ctx, orchapp.Agent{
		WorkspaceID: a.WorkspaceID, ID: a.ID,
		Description: a.Description, RuntimeConfig: a.RuntimeConfig,
	}, secret, system, input)
	if err != nil {
		return "", convapp.UsageLite{}, err
	}
	return out, convapp.UsageLite{Provider: u.Provider, Model: u.Model,
		PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens}, nil
}

func TestB9_AgentBuilderDraft(t *testing.T) {
	e := newEnv(t)
	user := e.createUser(t, "b9c@example.com")
	ws := e.createWS(t, user, "b9-c")
	svc := orchapp.NewAgentBuilderService(e.repo)

	d1, err := svc.Create(ctx0(), orchapp.BuilderDraft{
		WorkspaceID: ws, UserID: user, Title: "researcher",
		Draft: map[string]any{"name": "researcher", "description": "do research"},
	})
	require.NoError(t, err)

	upd, err := svc.UpsertDraft(ctx0(), orchapp.BuilderDraft{
		ID: d1.ID, WorkspaceID: ws, UserID: user, Title: "senior researcher",
		Draft: map[string]any{"name": "researcher2", "description": "deep research"},
	})
	require.NoError(t, err)
	assert.Equal(t, "senior researcher", upd.Title)

	got, err := svc.Get(ctx0(), ws, d1.ID)
	require.NoError(t, err)
	assert.Equal(t, "researcher2", got.Draft["name"], "last-write-wins on the draft body (internal archive 03 §2)")

	_, err = svc.UpsertDraft(ctx0(), orchapp.BuilderDraft{
		ID: d1.ID, WorkspaceID: ws, UserID: "00000000-0000-0000-0000-000000000009"})
	assert.Error(t, err)

	list, err := svc.List(ctx0(), ws, user)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}
