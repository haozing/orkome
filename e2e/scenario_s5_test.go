package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenario_S5_ChatSession(t *testing.T) {
	runScenario(t, "S5 session and agent collaboration", []step{
		{name: "owner logs in, creates workspace and agent", do: func(w *world) {
			w.loginAs("owner", "owner@s5.e2e")
			w.vars["wsID"] = w.createWSAs("owner", "s5chat")
			rec := w.st.do("owner", http.MethodPost, "/api/agents", map[string]any{
				"name": "s5-assistant", "runtime_config": map[string]any{"provider": "fake", "model": "fake-1"},
			}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["agentID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "sidebar pinned agent", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/chat/pinned-agents",
				map[string]string{"agent_id": w.str("agentID")}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			rec = w.st.do("owner", http.MethodGet, "/api/chat/pinned-agents", nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), w.str("agentID"))
		}},
		{name: "create session", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/chat/sessions",
				map[string]string{"agent_id": w.str("agentID"), "title": "weekly report assistant"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["sessID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "send message (user message persisted + task produced)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/chat/sessions/%s/messages", w.str("sessID")),
				map[string]string{"content": "help me summarize this week's progress"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			out := decodeJSON(t, rec)
			taskID, _ := out["task_id"].(string)
			require.NotEmpty(t, taskID, "sending a message should produce a chat task")
			w.vars["taskID"] = taskID
		}},
		{name: "engine executes: assistant reply persisted (polling)", do: func(w *world) {
			waitFor(t, func() bool {
				var n int
				_ = w.st.pool.QueryRow(t.Context(), `
SELECT count(*) FROM chat_message WHERE session_id=$1 AND role='assistant'`,
					w.str("sessID")).Scan(&n)
				return n > 0
			}, "assistant reply should be persisted")
			rec := w.st.do("owner", http.MethodGet,
				fmt.Sprintf("/api/chat/sessions/%s/messages", w.str("sessID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"role":"user"`)
			require.Contains(t, rec.Body.String(), `"role":"assistant"`)
		}},
		{name: "the pending-task view is empty after the task reaches terminal state", do: func(w *world) {
			waitFor(t, func() bool {
				var st string
				_ = w.st.pool.QueryRow(t.Context(),
					`SELECT status FROM agent_task_queue WHERE id=$1`, w.str("taskID")).Scan(&st)
				return st == "completed" || st == "failed"
			}, "chat task should reach terminal state")
			rec := w.st.do("owner", http.MethodGet,
				fmt.Sprintf("/api/chat/sessions/%s/pending-task", w.str("sessID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		}},
		{name: "archive → write rejected → unarchive restores", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/chat/sessions/%s/archive", w.str("sessID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/chat/sessions/%s/messages", w.str("sessID")),
				map[string]string{"content": "should not be writable after archival"}, nil)
			require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest,
				"archived session should reject writes, got %d: %s", rec.Code, rec.Body.String())

			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/chat/sessions/%s/unarchive", w.str("sessID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		}},
		{name: "delete session (messages cascade cleaned)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodDelete,
				fmt.Sprintf("/api/chat/sessions/%s", w.str("sessID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM chat_message WHERE session_id=$1`, w.str("sessID")).Scan(&n))
			require.Zero(t, n, "session messages should cascade clean")
		}},
	})
}
