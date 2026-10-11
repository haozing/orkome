package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func subEnv(t *testing.T) *m2h {
	t.Helper()
	h := newM2(t)

	rec := h.do("u", http.MethodGet, "/config", nil)
	require.Equal(t, 200, rec.Code)
	var cfg struct {
		CSRFToken string `json:"csrf_token"`
	}
	jsonOf(t, rec, &cfg)
	h.csrf["u"] = cfg.CSRFToken

	rec = h.do("u", http.MethodPost, "/auth/verify-code",
		map[string]string{"email": "sub@m2.dev", "code": "123456"})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var vout struct {
		CSRFToken string `json:"csrf_token"`
	}
	jsonOf(t, rec, &vout)
	h.csrf["u"] = vout.CSRFToken

	rec = h.do("u", http.MethodPost, "/api/workspaces",
		map[string]string{"name": "SubRes", "slug": "sub-res"})
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var ws struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &ws)
	require.NotEmpty(t, ws.ID)
	h.wsID = ws.ID
	return h
}

func (h *m2h) createWI(t *testing.T, title, status, parent string) string {
	t.Helper()
	body := map[string]any{"title": title}
	if status != "" {
		body["status"] = status
	}
	if parent != "" {
		body["parent_work_item_id"] = parent
	}
	rec := h.do("u", http.MethodPost, "/api/work-items", body)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var out struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &out)
	require.NotEmpty(t, out.ID)
	return out.ID
}

func (h *m2h) doRaw(client, method, path, raw string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(raw))
	req.RemoteAddr = "192.0.2.7:5555"
	for _, c := range h.jar[client] {
		req.AddCookie(c)
	}
	if h.csrf[client] != "" {
		req.Header.Set("X-CSRF-Token", h.csrf[client])
	}
	if h.wsID != "" {
		req.Header.Set("X-Workspace-Id", h.wsID)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "orkome_auth" || c.Name == "orkome_csrf" {
			h.jar[client] = append(h.jar[client], c)
		}
	}
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e), "body: %s", rec.Body.String())
	return e.Error
}

func reactionCounts(t *testing.T, rec *httptest.ResponseRecorder) map[string]int64 {
	t.Helper()
	var out struct {
		Reactions []struct {
			Emoji string `json:"emoji"`
			Count int64  `json:"count"`
		} `json:"reactions"`
	}
	jsonOf(t, rec, &out)
	m := map[string]int64{}
	for _, r := range out.Reactions {
		m[r.Emoji] = r.Count
	}
	return m
}

func subInviteBob(t *testing.T, h *m2h) string {
	t.Helper()
	rec := h.do("b", http.MethodGet, "/config", nil)
	require.Equal(t, 200, rec.Code)
	var cfg struct {
		CSRFToken string `json:"csrf_token"`
	}
	jsonOf(t, rec, &cfg)
	h.csrf["b"] = cfg.CSRFToken
	rec = h.do("b", http.MethodPost, "/auth/verify-code",
		map[string]string{"email": "bob@m2.dev", "code": "123456"})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var vout struct {
		CSRFToken string `json:"csrf_token"`
	}
	jsonOf(t, rec, &vout)
	h.csrf["b"] = vout.CSRFToken

	rec = h.do("u", http.MethodPost, "/api/workspaces/"+h.wsID+"/members",
		map[string]string{"email": "bob@m2.dev", "role": "member"})
	require.Equal(t, 201, rec.Code, rec.Body.String())

	rec = h.do("b", http.MethodGet, "/api/invitations", nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var invs []struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &invs)
	require.NotEmpty(t, invs, "bob should have a pending invitation")
	rec = h.do("b", http.MethodPost, "/api/invitations/"+invs[0].ID+"/accept", nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())

	rec = h.do("b", http.MethodGet, "/api/me", nil)
	require.Equal(t, 200, rec.Code)
	var me struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	jsonOf(t, rec, &me)
	require.NotEmpty(t, me.User.ID)
	return me.User.ID
}

func TestM2_UsageEndpoint(t *testing.T) {
	h := subEnv(t)
	wi := h.createWI(t, "usage host", "todo", "")

	ctx := context.Background()
	var agentID, task1, task2 string
	require.NoError(t, h.pool.QueryRow(ctx,
		`INSERT INTO agent (workspace_id, name) VALUES ($1,'usage-agent') RETURNING id::text`,
		h.wsID).Scan(&agentID))
	require.NoError(t, h.pool.QueryRow(ctx, `
INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status)
VALUES ($1,$2,$3,'completed') RETURNING id::text`, h.wsID, agentID, wi).Scan(&task1))
	require.NoError(t, h.pool.QueryRow(ctx, `
INSERT INTO agent_task_queue (workspace_id, agent_id, work_item_id, status)
VALUES ($1,$2,$3,'failed') RETURNING id::text`, h.wsID, agentID, wi).Scan(&task2))
	_, err := h.pool.Exec(ctx, `
INSERT INTO task_usage (workspace_id, task_id, agent_id, provider, model,
  prompt_tokens, completion_tokens, cost_usd_ticks) VALUES
  ($1,$2,$3,'openai','gpt-x',100,50,10),
  ($1,$2,$3,'openai','gpt-x',20,30,5),
  ($1,$4,$3,'anthropic','claude-3',10,5,2)`, h.wsID, task1, agentID, task2)
	require.NoError(t, err)

	rec := h.do("u", http.MethodGet, "/api/work-items/"+wi+"/usage", nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var out struct {
		Items []struct {
			Provider         string `json:"provider"`
			Model            string `json:"model"`
			TaskCount        int64  `json:"task_count"`
			PromptTokens     int64  `json:"prompt_tokens"`
			CompletionTokens int64  `json:"completion_tokens"`
			CostUSDTicks     int64  `json:"cost_usd_ticks"`
		} `json:"items"`
	}
	jsonOf(t, rec, &out)
	got := map[string]struct {
		task, prompt, completion, cost int64
	}{}
	for _, it := range out.Items {
		got[it.Provider+"/"+it.Model] = struct {
			task, prompt, completion, cost int64
		}{it.TaskCount, it.PromptTokens, it.CompletionTokens, it.CostUSDTicks}
	}
	assert.Equal(t, struct{ task, prompt, completion, cost int64 }{1, 120, 80, 15},
		got["openai/gpt-x"], "two rows of the same task should aggregate: tokens summed, task_count=DISTINCT")
	assert.Equal(t, struct{ task, prompt, completion, cost int64 }{1, 10, 5, 2},
		got["anthropic/claude-3"])
	assert.Len(t, out.Items, 2)

	empty := h.createWI(t, "no-usage item", "todo", "")
	rec = h.do("u", http.MethodGet, "/api/work-items/"+empty+"/usage", nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	jsonOf(t, rec, &out)
	assert.Empty(t, out.Items, "no tasks should return empty items")

	rec = h.do("u", http.MethodGet, "/api/work-items/00000000-0000-0000-0000-000000000000/usage", nil)
	assert.Equal(t, 404, rec.Code, rec.Body.String())
	assert.Equal(t, "E_NOT_FOUND", errCode(t, rec))
}

func TestM2_DuplicatesEndpoint(t *testing.T) {
	h := subEnv(t)
	orig := h.createWI(t, "original task", "todo", "")
	dup1 := h.createWI(t, "duplicate mark one", "todo", "")
	dup2 := h.createWI(t, "duplicate mark two", "todo", "")

	for _, d := range []string{dup1, dup2} {
		rec := h.do("u", http.MethodPut, "/api/work-items/"+d,
			map[string]any{"duplicate_of_work_item_id": orig})
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var body struct {
			DuplicateOf string `json:"duplicate_of_work_item_id"`
		}
		jsonOf(t, rec, &body)
		assert.Equal(t, orig, body.DuplicateOf)
	}

	rec := h.do("u", http.MethodGet, "/api/work-items/"+orig+"/duplicates", nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var out struct {
		Items []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			DuplicateOf string `json:"duplicate_of_work_item_id"`
		} `json:"items"`
	}
	jsonOf(t, rec, &out)
	ids := map[string]bool{}
	for _, it := range out.Items {
		ids[it.ID] = true
		assert.Equal(t, orig, it.DuplicateOf, "each row should echo its duplicate_of target")
		assert.NotEmpty(t, it.Title, "should return the full work item")
	}
	assert.True(t, ids[dup1] && ids[dup2], "both marked items should appear")
	assert.Len(t, out.Items, 2)

	fresh := h.createWI(t, "no duplicates", "todo", "")
	rec = h.do("u", http.MethodGet, "/api/work-items/"+fresh+"/duplicates", nil)
	require.Equal(t, 200, rec.Code)
	jsonOf(t, rec, &out)
	assert.Empty(t, out.Items)

	rec = h.do("u", http.MethodGet, "/api/work-items/00000000-0000-0000-0000-000000000000/duplicates", nil)
	assert.Equal(t, 404, rec.Code, rec.Body.String())
	assert.Equal(t, "E_NOT_FOUND", errCode(t, rec))

	rec = h.do("u", http.MethodPut, "/api/work-items/"+dup1,
		map[string]any{"duplicate_of_work_item_id": "00000000-0000-0000-0000-000000000000"})
	assert.Equal(t, 400, rec.Code, "missing duplicate_of should be validation-rejected not 500: "+rec.Body.String())
	assert.Equal(t, "E_VALIDATION", errCode(t, rec))

	rec = h.do("u", http.MethodPut, "/api/work-items/"+dup1,
		map[string]any{"duplicate_of_work_item_id": dup1})
	assert.Equal(t, 400, rec.Code, rec.Body.String())
	assert.Equal(t, "E_VALIDATION", errCode(t, rec))
}

func TestM2_MetadataEndpoint(t *testing.T) {
	h := subEnv(t)
	wi := h.createWI(t, "metadata host", "todo", "")
	base := "/api/work-items/" + wi + "/metadata/"

	rec := h.doRaw("u", http.MethodPut, base+"spec", `{"kind":"spec","v":2}`)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var putOut struct {
		Metadata map[string]any `json:"metadata"`
		Revision int64          `json:"revision"`
	}
	jsonOf(t, rec, &putOut)
	assert.Equal(t, map[string]any{"kind": "spec", "v": float64(2)}, putOut.Metadata["spec"])
	rev1 := putOut.Revision

	rec = h.do("u", http.MethodGet, base+"spec", nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var got struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	jsonOf(t, rec, &got)
	assert.Equal(t, "spec", got.Key)
	assert.JSONEq(t, `{"kind":"spec","v":2}`, string(got.Value), "GET should read back the PUT value")

	rec = h.doRaw("u", http.MethodPut, base+"editor", `"alice"`)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	jsonOf(t, rec, &putOut)
	assert.Equal(t, rev1+1, putOut.Revision, "metadata PUT should advance revision")
	rec = h.do("u", http.MethodGet, base+"editor", nil)
	require.Equal(t, 200, rec.Code)
	jsonOf(t, rec, &got)
	assert.JSONEq(t, `"alice"`, string(got.Value))

	rec = h.do("u", http.MethodGet, "/api/work-items/"+wi, nil)
	require.Equal(t, 200, rec.Code)
	var wiOut struct {
		Metadata map[string]any `json:"metadata"`
	}
	jsonOf(t, rec, &wiOut)
	assert.Contains(t, wiOut.Metadata, "spec")
	assert.Contains(t, wiOut.Metadata, "editor")

	rec = h.doRaw("u", http.MethodPut, base+"spec", `{"kind":"spec","v":3}`)
	require.Equal(t, 200, rec.Code)
	rec = h.do("u", http.MethodGet, base+"spec", nil)
	require.Equal(t, 200, rec.Code)
	jsonOf(t, rec, &got)
	assert.JSONEq(t, `{"kind":"spec","v":3}`, string(got.Value))

	rec = h.do("u", http.MethodGet, base+"missing", nil)
	assert.Equal(t, 404, rec.Code, rec.Body.String())
	assert.Equal(t, "E_NOT_FOUND", errCode(t, rec))

	rec = h.do("u", http.MethodDelete, base+"editor", nil)
	require.Equal(t, 204, rec.Code, rec.Body.String())
	rec = h.do("u", http.MethodGet, base+"editor", nil)
	assert.Equal(t, 404, rec.Code, "GET should be 404 after key deletion")

	rec = h.do("u", http.MethodDelete, base+"never-existed", nil)
	assert.Equal(t, 204, rec.Code, "deleting a missing key should be idempotent 204: "+rec.Body.String())

	rec = h.doRaw("u", http.MethodPut, base+"bad", `{"broken":`)
	assert.Equal(t, 400, rec.Code, rec.Body.String())
	assert.Equal(t, "E_VALIDATION", errCode(t, rec))

	rec = h.doRaw("u", http.MethodPut, base+"empty", ``)
	assert.Equal(t, 400, rec.Code, rec.Body.String())

	rec = h.doRaw("u", http.MethodPut, base+strings.Repeat("k", 129), `"v"`)
	assert.Equal(t, 400, rec.Code, rec.Body.String())
	assert.Equal(t, "E_VALIDATION", errCode(t, rec))

	rec = h.doRaw("u", http.MethodPut, base+"big", `"`+strings.Repeat("a", 17_000)+`"`)
	assert.Equal(t, 400, rec.Code, "over-limit values should be 400: "+rec.Body.String())
	assert.Equal(t, "E_VALIDATION", errCode(t, rec))

	missing := "/api/work-items/00000000-0000-0000-0000-000000000000/metadata/k"
	rec = h.do("u", http.MethodGet, missing, nil)
	assert.Equal(t, 404, rec.Code)
	rec = h.doRaw("u", http.MethodPut, missing, `"v"`)
	assert.Equal(t, 404, rec.Code, rec.Body.String())
	rec = h.do("u", http.MethodDelete, missing, nil)
	assert.Equal(t, 404, rec.Code)
}

func TestM2_ReactionsEndpoint(t *testing.T) {
	h := subEnv(t)
	wi := h.createWI(t, "emoji host", "todo", "")
	base := "/api/work-items/" + wi + "/reactions"
	subInviteBob(t, h)

	rec := h.do("u", http.MethodPost, base, map[string]string{"emoji": "👍"})
	require.Equal(t, 201, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]int64{"👍": 1}, reactionCounts(t, rec))

	rec = h.do("u", http.MethodPost, base, map[string]string{"emoji": "👍"})
	require.Equal(t, 201, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]int64{"👍": 1}, reactionCounts(t, rec), "(user,emoji) primary key should dedup")

	rec = h.do("u", http.MethodPost, base, map[string]string{"emoji": "🎉"})
	require.Equal(t, 201, rec.Code)
	assert.Equal(t, map[string]int64{"👍": 1, "🎉": 1}, reactionCounts(t, rec))

	rec = h.do("b", http.MethodPost, base, map[string]string{"emoji": "👍"})
	require.Equal(t, 201, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]int64{"👍": 2, "🎉": 1}, reactionCounts(t, rec))

	rec = h.do("u", http.MethodDelete, base+"?emoji="+url.QueryEscape("👍"), nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]int64{"👍": 1, "🎉": 1}, reactionCounts(t, rec))

	rec = h.do("u", http.MethodDelete, base+"?emoji="+url.QueryEscape("🤖"), nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]int64{"👍": 1, "🎉": 1}, reactionCounts(t, rec))

	rec = h.do("u", http.MethodDelete, base, map[string]string{"emoji": "🎉"})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]int64{"👍": 1}, reactionCounts(t, rec))

	rec = h.do("u", http.MethodPost, base, map[string]string{"emoji": ""})
	assert.Equal(t, 400, rec.Code, rec.Body.String())
	assert.Equal(t, "E_VALIDATION", errCode(t, rec))

	rec = h.do("u", http.MethodPost, base, map[string]string{"emoji": strings.Repeat("🎉", 20)})
	assert.Equal(t, 400, rec.Code, rec.Body.String())

	missing := "/api/work-items/00000000-0000-0000-0000-000000000000/reactions"
	rec = h.do("u", http.MethodPost, missing, map[string]string{"emoji": "👍"})
	assert.Equal(t, 404, rec.Code)
	rec = h.do("u", http.MethodDelete, missing+"?emoji=👍", nil)
	assert.Equal(t, 404, rec.Code)
}

func TestM2_ChildProgressEndpoint(t *testing.T) {
	h := subEnv(t)
	parent := h.createWI(t, "progress parent item", "todo", "")
	h.createWI(t, "sub-item one", "todo", parent)
	h.createWI(t, "sub-item two", "todo", parent)
	h.createWI(t, "sub-item three", "done", parent)
	h.createWI(t, "sub-item four", "in_progress", parent)

	isolParent := h.createWI(t, "isolated parent item", "todo", "")
	h.createWI(t, "isolated sub-item", "todo", isolParent)

	rec := h.do("u", http.MethodGet, "/api/work-items/child-progress?parent="+parent, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var rows []struct {
		K string `json:"k"`
		N int64  `json:"n"`
	}
	jsonOf(t, rec, &rows)
	m := map[string]int64{}
	for _, r := range rows {
		m[r.K] = r.N
	}
	assert.Equal(t, map[string]int64{"todo": 2, "done": 1, "in_progress": 1}, m)

	bare := h.createWI(t, "no sub parent item", "todo", "")
	rec = h.do("u", http.MethodGet, "/api/work-items/child-progress?parent="+bare, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	jsonOf(t, rec, &rows)
	assert.Empty(t, rows, "no sub-items should return empty grouping")

	rec = h.do("u", http.MethodGet, "/api/work-items/child-progress", nil)
	assert.Equal(t, 400, rec.Code, rec.Body.String())
	assert.Equal(t, "E_VALIDATION", errCode(t, rec))

	rec = h.do("u", http.MethodGet,
		"/api/work-items/child-progress?parent=00000000-0000-0000-0000-000000000000", nil)
	assert.Equal(t, 404, rec.Code, rec.Body.String())
	assert.Equal(t, "E_NOT_FOUND", errCode(t, rec))
}
