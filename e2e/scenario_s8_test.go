package e2e

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenario_S8_Concurrency(t *testing.T) {
	runScenario(t, "S8 concurrent continuity", []step{
		{name: "owner logs in and creates the workspace", do: func(w *world) {
			w.loginAs("owner", "owner@s8.e2e")
			w.vars["wsID"] = w.createWSAs("owner", "s8conc")
		}},
		{name: "concurrently create same-title work items (duplicate guard deterministic)", do: func(w *world) {
			const n = 4
			codes := make([]int, n)
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					rec := w.st.do("owner", http.MethodPost, "/api/work-items",
						map[string]string{"title": "S8 concurrent same title", "status": "todo"}, nil)
					codes[i] = rec.Code
				}(i)
			}
			wg.Wait()
			var rows int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM work_item WHERE workspace_id=$1 AND title='S8 concurrent same title'`,
				w.str("wsID")).Scan(&rows))
			created := 0
			for _, c := range codes {
				require.Less(t, c, 500, "concurrent creation must not 500")
				if c == 201 {
					created++
				}
			}
			require.Equal(t, rows, created, "DB row count should match the 201s (terminal state determined, no phantom rows)")
		}},
		{name: "concurrent wakeup trigger (confluent single task)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/agents", map[string]any{
				"name": "s8-agent", "runtime_config": map[string]any{"provider": "fake", "model": "fake-1"},
			}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			agent := decodeJSON(t, rec)["id"].(string)

			rec = w.st.do("owner", http.MethodPost, "/api/work-items",
				map[string]string{"title": "S8 wakeup concurrency", "status": "todo"}, nil)
			wi := decodeJSON(t, rec)["id"].(string)
			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/wakeups", wi), map[string]any{
					"agent_id": agent, "kind": "time", "instruction": "concurrent trigger",
					"timezone": "UTC", "enabled": true,
				}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			wk := decodeJSON(t, rec)["id"].(string)

			var wg sync.WaitGroup
			for i := 0; i < 3; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_ = w.st.do("owner", http.MethodPost,
						fmt.Sprintf("/api/work-items/%s/wakeups/%s/trigger", wi, wk), nil, nil)
				}()
			}
			wg.Wait()

			waitFor(t, func() bool {
				var running int
				_ = w.st.pool.QueryRow(t.Context(), `
SELECT count(*) FROM agent_task_queue WHERE work_item_id=$1 AND status IN ('queued','dispatched','running')`,
					wi).Scan(&running)
				return running == 0
			}, "concurrently triggered tasks should all settle")
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM agent_task_queue WHERE work_item_id=$1`, wi).Scan(&n))
			require.Equal(t, 1, n, "concurrent triggers should confluence into one task (MergeIntoPending)")
		}},
		{name: "concurrent same-emoji reaction (idempotent, no doubling)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/work-items",
				map[string]string{"title": "S8 idempotent concurrency", "status": "todo"}, nil)
			wi := decodeJSON(t, rec)["id"].(string)
			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/comments", wi),
				map[string]string{"content": "concurrency baseline"}, nil)
			cm := decodeJSON(t, rec)["comment"].(map[string]any)["id"].(string)

			var wg sync.WaitGroup
			for i := 0; i < 5; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_ = w.st.do("owner", http.MethodPost,
						fmt.Sprintf("/api/comments/%s/reactions", cm),
						map[string]string{"emoji": "🚀"}, nil)
				}()
			}
			wg.Wait()
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM comment_reaction WHERE comment_id=$1`, cm).Scan(&n))
			require.Equal(t, 1, n, "same user same emoji concurrent append must not double")
		}},
	})
}
