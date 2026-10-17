package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenario_S4_AutomationLoop(t *testing.T) {
	runScenario(t, "S4 automation loop", []step{
		{name: "owner logs in and creates the workspace", do: func(w *world) {
			w.loginAs("owner", "owner@s4.e2e")
			w.vars["wsID"] = w.createWSAs("owner", "s4auto")
		}},
		{name: "HTTP create agent (fake runtime)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/agents", map[string]any{
				"name":        "s4-reporter",
				"description": "S4 weekly report generation",
				"runtime_config": map[string]any{
					"provider": "fake", "model": "fake-1",
				},
			}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["agentID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "create the carrier work item", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/work-items",
				map[string]string{"title": "generate a weekly report every Friday", "status": "todo"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["wiID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "create wakeup rule (time type + manual trigger)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/wakeups", w.str("wiID")), map[string]any{
					"agent_id":    w.str("agentID"),
					"kind":        "time",
					"instruction": "summarize this week's work items and generate the weekly report",
					"timezone":    "UTC",
					"enabled":     true,
				}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			out := decodeJSON(t, rec)
			w.vars["wkID"] = out["id"].(string)
			require.Equal(t, true, out["enabled"], "newly created rules should be enabled")
		}},
		{name: "manual wakeup trigger", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/wakeups/%s/trigger", w.str("wiID"), w.str("wkID")),
				nil, nil)
			require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		}},
		{name: "task enqueued and reached terminal state (engine executes, polling)", do: func(w *world) {
			waitFor(t, func() bool {
				var done int
				_ = w.st.pool.QueryRow(t.Context(), `
SELECT count(*) FROM agent_task_queue
WHERE workspace_id=$1 AND work_item_id=$2 AND status IN ('completed','failed')`,
					w.str("wsID"), w.str("wiID")).Scan(&done)
				return done > 0
			}, "wakeup task should reach terminal state")
			var status string
			require.NoError(t, w.st.pool.QueryRow(t.Context(), `
SELECT status FROM agent_task_queue WHERE workspace_id=$1 AND work_item_id=$2`,
				w.str("wsID"), w.str("wiID")).Scan(&status))
			require.Equal(t, "completed", status, "fake engine should run successfully")
		}},
		{name: "outcome settles to the work item (delivery visible)", do: func(w *world) {
			// 66c4afe：结算不再写兜底评论——交付可见面 = work_item.outcome。
			var oc []byte
			waitFor(t, func() bool {
				_ = w.st.pool.QueryRow(t.Context(), `
SELECT outcome FROM work_item WHERE id=$1`,
					w.str("wiID")).Scan(&oc)
				return oc != nil
			}, "settle should land outcome on the work item")
			rec := w.st.do("owner", http.MethodGet,
				fmt.Sprintf("/api/work-items/%s", w.str("wiID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"outcome":{`, "the work item API should surface the outcome")
		}},
		{name: "wakeup view and linked task backfill", do: func(w *world) {
			var fires int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT fire_count FROM work_item_wakeup WHERE id=$1`, w.str("wkID")).Scan(&fires))
			require.GreaterOrEqual(t, fires, 1, "fire_count should be backfilled")

			rec := w.st.do("owner", http.MethodGet,
				fmt.Sprintf("/api/work-items/%s/wakeups/%s/tasks", w.str("wiID"), w.str("wkID")),
				nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `completed`,
				"wakeup-linked tasks should contain terminal records")
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM work_item_wakeup WHERE workspace_id=$1`, w.str("wsID")).Scan(&n))
			require.Equal(t, 1, n)
		}},
	})
}
