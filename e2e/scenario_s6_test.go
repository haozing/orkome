package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenario_S6_SkillManage(t *testing.T) {
	runScenario(t, "S6 skill management", []step{
		{name: "owner logs in, creates workspace and agent", do: func(w *world) {
			w.loginAs("owner", "owner@s6.e2e")
			w.vars["wsID"] = w.createWSAs("owner", "s6skill")
			rec := w.st.do("owner", http.MethodPost, "/api/agents", map[string]any{
				"name": "s6-worker", "runtime_config": map[string]any{"provider": "fake", "model": "fake-1"},
			}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["agentID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "create skill", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/skills", map[string]string{
				"name": "weekly-report", "description": "weekly report skill",
			}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			w.vars["skillID"] = decodeJSON(t, rec)["id"].(string)
		}},
		{name: "upload SKILL.md main file (upsert)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPut,
				fmt.Sprintf("/api/skills/%s/files", w.str("skillID")), map[string]string{
					"path": "SKILL.md",

					"content": "---\nname: weekly-report\ndescription: weekly report skill\n---\n\n# Weekly Report\nSummarize this week's work items using the template.",
				}, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			rec = w.st.do("owner", http.MethodGet,
				fmt.Sprintf("/api/skills/%s/files", w.str("skillID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "SKILL.md")
		}},
		{name: "attachment upload/download round-trip (content domain, after B1 hookup)", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/work-items",
				map[string]string{"title": "S6 attachment host", "status": "todo"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			host := decodeJSON(t, rec)["id"].(string)

			rec = w.doMultipart("owner", "/api/upload-file", "weekly-report.md", "# weekly report content",
				map[string]string{"host_kind": "work_item", "host_id": host})
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			att := decodeJSON(t, rec)["id"].(string)

			rec = w.st.do("owner", http.MethodGet, "/api/attachments/"+att, nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			rec = w.st.do("owner", http.MethodGet,
				fmt.Sprintf("/api/work-items/%s/attachments", host), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), att, "host attachment list should contain the upload")
		}},
		{name: "create and attach label", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost, "/api/labels",
				map[string]string{"name": "s6skilltag"}, nil)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			labelID := decodeJSON(t, rec)["id"].(string)
			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/skills/%s/labels", w.str("skillID")),
				map[string]string{"label_id": labelID}, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		}},
		{name: "bound to the agent and visible on the binding face", do: func(w *world) {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/agents/%s/skills/add", w.str("agentID")),
				map[string]string{"skill_id": w.str("skillID")}, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			rec = w.st.do("owner", http.MethodGet,
				fmt.Sprintf("/api/agents/%s/skills", w.str("agentID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "weekly-report", "binding face should see the skill")
		}},
		{name: "delete skill: files and bindings cascade cleaned", do: func(w *world) {
			rec := w.st.do("owner", http.MethodDelete,
				fmt.Sprintf("/api/skills/%s", w.str("skillID")), nil, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var n int
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM agent_skill WHERE skill_id=$1`, w.str("skillID")).Scan(&n))
			require.Zero(t, n, "agent bindings should cascade clean")
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM skill_file WHERE skill_id=$1`, w.str("skillID")).Scan(&n))
			require.Zero(t, n, "skill files should cascade clean")
			require.NoError(t, w.st.pool.QueryRow(t.Context(),
				`SELECT count(*) FROM skill_to_label WHERE skill_id=$1`, w.str("skillID")).Scan(&n))
			require.Zero(t, n, "label associations should cascade clean")
		}},
	})
}
