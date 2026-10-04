package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenario_S2_WorkItemLifecycle(t *testing.T) {
	runScenario(t, "S2 requirement delivery mainline", []step{
		{name: "owner logs in and creates the workspace", do: func(w *world) {
			w.loginAs("owner", "owner@s2.e2e")
			w.vars["wsID"] = w.createWSAs("owner", "s2life")
		}},
		{name: "create project", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/projects",
				map[string]string{"name": "S2 delivery project"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["projID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "create requirement work item (attached to project)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/work-items", map[string]any{
				"title": "support exporting weekly reports", "status": "todo", "project_id": w.str("projID"),
			}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["wiID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "DB assert: work item persisted and linked to project", do: func(w *world) {
			var title, proj string
			err := w.st.pool.QueryRow(t.Context(),
				`SELECT title, coalesce(project_id::text,'') FROM work_item WHERE id=$1 AND workspace_id=$2`,
				w.str("wiID"), w.str("wsID")).Scan(&title, &proj)
			require.NoError(t, err)
			require.Equal(t, "support exporting weekly reports", title)
			require.Equal(t, w.str("projID"), proj)
		}},
		{name: "create and attach label", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/labels",
				map[string]string{"name": "s2tag"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["labelID"] = decodeJSON(t, rec)["id"].(string)

			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/labels", w.str("wiID")),
				map[string]string{"label_id": w.str("labelID")}, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		}},
		{name: "update: assignment + status transition to in_progress", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPut,
				fmt.Sprintf("/api/work-items/%s", w.str("wiID")), map[string]any{
					"status": "in_progress", "priority": 2,
				}, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		}},
		{name: "DB assert: status transitioned", do: func(w *world) {
			var st string
			err := w.st.pool.QueryRow(t.Context(),
				`SELECT status_key FROM work_item WHERE id=$1`, w.str("wiID")).Scan(&st)
			require.NoError(t, err)
			require.Equal(t, "in_progress", st)
		}},
		{name: "post a comment", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/comments", w.str("wiID")),
				map[string]string{"content": "this week done first version"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		}},
		{name: "explicit subscription", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/work-items/%s/subscribers", w.str("wiID")), nil, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		}},
		{name: "list/detail/search caps consistent", do: func(w *world) {
			var list, search struct {
				Items []map[string]any `json:"items"`
			}
			rec := w.st.do("owner", http.MethodGet,
				"/api/work-items?status=in_progress&project="+w.str("projID"), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.NoError(t, unmarshalInto(rec.Body.String(), &list))
			require.Len(t, list.Items, 1, "the filtered list should hit exactly the target work item")

			rec = w.st.do("owner", http.MethodGet, "/api/work-items/search?q=exporting", nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.NoError(t, unmarshalInto(rec.Body.String(), &search))
			require.NotEmpty(t, search.Items, "search should hit title")
		}},
		{name: "status transitions to done", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPut,
				fmt.Sprintf("/api/work-items/%s", w.str("wiID")), map[string]any{"status": "done"}, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		}},
		{name: "delete work item (cascade assertions)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodDelete,
				fmt.Sprintf("/api/work-items/%s", w.str("wiID")), nil, nil)
			require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM comment WHERE work_item_id=$1`, w.str("wiID")).Scan(&n))
			require.Zero(t, n, "comments should cascade clean")
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM work_item_to_label WHERE work_item_id=$1`, w.str("wiID")).Scan(&n))
			require.Zero(t, n, "label associations should cascade clean")
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM work_item_subscriber WHERE work_item_id=$1`, w.str("wiID")).Scan(&n))
			require.Zero(t, n, "subscriptions should cascade clean")
		}},
		{name: "journey terminal state: projects kept, work items zeroed", do: func(w *world) {
			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM work_item WHERE workspace_id=$1`, w.str("wsID")).Scan(&n))
			require.Zero(t, n)
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM project WHERE id=$1`, w.str("projID")).Scan(&n))
			require.Equal(t, 1, n, "projects not deleted with work items")
		}},
	})
}
