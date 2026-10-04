package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenario_S7_FailureContinuity(t *testing.T) {
	runScenario(t, "S7 interruption and continuity", []step{
		{name: "owner logs in and creates the workspace", do: func(w *world) {
			w.loginAs("owner", "owner@s7.e2e")
			w.vars["wsID"] = w.createWSAs("owner", "s7fail")
		}},
		{name: "create a normal agent and work item", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/agents", map[string]any{
				"name": "s7-agent", "runtime_config": map[string]any{"provider": "fake", "model": "fake-1"},
			}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["agentID"] = decodeJSON(t, rec)["id"].(string)
			rec = w.st.do("owner", http.MethodPost, "/api/work-items",
				map[string]string{"title": "S7 deletion items", "status": "todo"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["wiID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "attach wakeup rule", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/wakeups", w.str("wiID")), map[string]any{
					"agent_id": w.str("agentID"), "kind": "time",
					"instruction": "any", "timezone": "UTC", "enabled": true,
				}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		}},
		{name: "delete work item with wakeup rules (cascade integrity)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodDelete,
				fmt.Sprintf("/api/work-items/%s", w.str("wiID")), nil, nil)
			require.Equal(t, http.StatusNoContent, rec.Code,
				"work item with wakeup must not 500 on delete (wakeup placeholders are cascade-clean intent), body=%s", rec.Body.String())
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM work_item_wakeup WHERE work_item_id=$1`, w.str("wiID")).Scan(&n))
			require.Zero(t, n, "wakeup placeholders should cascade-clean with the work item")
		}},
		{name: "failed agent: tasks healthy-terminal after bad runtime trigger", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/agents", map[string]any{
				"name": "s7-bad-agent", "runtime_config": map[string]any{"provider": "nonexistent", "model": "x"},
			}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			badAgent := decodeJSON(t, rec)["id"].(string)

			rec = w.st.do("owner", http.MethodPost, "/api/work-items",
				map[string]string{"title": "S7 failure-path items", "status": "in_progress"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			wi := decodeJSON(t, rec)["id"].(string)

			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/wakeups", wi), map[string]any{
					"agent_id": badAgent, "kind": "time", "instruction": "must fail",
					"timezone": "UTC", "enabled": true,
				}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			wk := decodeJSON(t, rec)["id"].(string)

			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/wakeups/%s/trigger", wi, wk), nil, nil)
			require.Equal(t, http.StatusNoContent, rec.Code)

			waitFor(t, func() bool {
				var done int
				_ = w.st.pool.QueryRow(t.Context(), `
SELECT count(*) FROM agent_task_queue WHERE work_item_id=$1 AND status IN ('completed','failed')`,
					wi).Scan(&done)
				return done > 0
			}, "failed agents' tasks should also reach terminal state")
			var status, wiStatus string
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT status FROM agent_task_queue WHERE work_item_id=$1`, wi).Scan(&status))
			require.Equal(t, "failed", status, "bad provider should fail, not hang")
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT status_key FROM work_item WHERE id=$1`, wi).Scan(&wiStatus))
			require.Equal(t, "in_progress", wiStatus, "task failure must not wrongly advance the work item status")
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM comment WHERE work_item_id=$1 AND author_type='agent'`, wi).Scan(&n))
			require.Zero(t, n, "failed tasks should not produce agent comments")
		}},
		{name: "034 ruling check: work item deletion with task history (tasks cascade / sub-items detach / run history kept)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/work-items",
				map[string]string{"title": "S7 cross-domain parent deletion", "status": "todo"}, nil)
			parent := decodeJSON(t, rec)["id"].(string)
			rec = w.st.do("owner", http.MethodPost, "/api/work-items",
				map[string]string{"title": "S7 sub-items", "status": "todo", "parent_id": parent}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			child := decodeJSON(t, rec)["id"].(string)

			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/wakeups", parent), map[string]any{
					"agent_id": w.str("agentID"), "kind": "time", "instruction": "history produced",
					"timezone": "UTC", "enabled": true,
				}, nil)
			wk := decodeJSON(t, rec)["id"].(string)
			_ = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/wakeups/%s/trigger", parent, wk), nil, nil)
			waitFor(t, func() bool {
				var n int
				_ = w.st.pool.QueryRow(t.Context(),
					`SELECT count(*) FROM agent_task_queue WHERE work_item_id=$1`, parent).Scan(&n)
				return n > 0
			}, "parent tasks should be produced")

			rec = w.st.do("owner", http.MethodDelete,
				fmt.Sprintf("/api/work-items/%s", parent), nil, nil)
			require.Equal(t, http.StatusNoContent, rec.Code,
				"deletion with task history must not 500 (034 ruling), body=%s", rec.Body.String())

			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM agent_task_queue WHERE work_item_id=$1`, parent).Scan(&n))
			require.Zero(t, n, "task rows should follow parent deletion (CASCADE ruling)")
			var childParent interface{}
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT parent_work_item_id FROM work_item WHERE id=$1`, child).Scan(&childParent))
			require.Nil(t, childParent, "sub-items should detach and stay (SET NULL ruling)")
		}},
		{name: "delete work item with queued task (pre-cancel does not block deletion)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/work-items",
				map[string]string{"title": "S7 running-state deletion", "status": "todo"}, nil)
			wi := decodeJSON(t, rec)["id"].(string)
			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/wakeups", wi), map[string]any{
					"agent_id": w.str("agentID"), "kind": "time", "instruction": "running state deleted",
					"timezone": "UTC", "enabled": true,
				}, nil)
			wk := decodeJSON(t, rec)["id"].(string)
			_ = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/wakeups/%s/trigger", wi, wk), nil, nil)
			waitFor(t, func() bool {
				var n int
				_ = w.st.pool.QueryRow(t.Context(),
					`SELECT count(*) FROM agent_task_queue WHERE work_item_id=$1`, wi).Scan(&n)
				return n > 0
			}, "task should be enqueued")

			rec = w.st.do("owner", http.MethodDelete,
				fmt.Sprintf("/api/work-items/%s", wi), nil, nil)
			require.Equal(t, http.StatusNoContent, rec.Code,
				"deletion after pre-cancel should succeed (aligned), body=%s", rec.Body.String())
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM agent_task_queue WHERE work_item_id=$1`, wi).Scan(&n))
			require.Zero(t, n, "task rows should follow deletion (cancel first then cascade)")
		}},
		{name: "reaction idempotent resend: side effects do not double", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/work-items",
				map[string]string{"title": "S7 idempotency items", "status": "todo"}, nil)
			wi := decodeJSON(t, rec)["id"].(string)
			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/comments", wi),
				map[string]string{"content": "idempotency baseline"}, nil)
			cm := decodeJSON(t, rec)["comment"].(map[string]any)["id"].(string)

			for i := 0; i < 2; i++ {
				rec = w.st.do("owner", http.MethodPost,
					fmt.Sprintf("/api/comments/%s/reactions", cm),
					map[string]string{"emoji": "🎉"}, nil)
				require.Equal(t, http.StatusCreated, rec.Code, "append %d should succeed", i+1)
			}
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM comment_reaction WHERE comment_id=$1 AND user_id=(SELECT id FROM "user" WHERE email='owner@s7.e2e')`,
				cm).Scan(&n))
			require.Equal(t, 1, n, "same user same emoji resend must not double (idempotent upsert)")
		}},
	})
}
