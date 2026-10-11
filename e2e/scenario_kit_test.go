package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type world struct {
	t    *testing.T
	st   *stack
	vars map[string]any
}

func newWorld(t *testing.T) *world {
	return &world{t: t, st: newStack(t), vars: map[string]any{}}
}

func (w *world) str(k string) string {
	w.t.Helper()
	v, ok := w.vars[k].(string)
	require.Truef(w.t, ok, "scenario variable %q unset or not a string (currently %#v)", k, w.vars[k])
	return v
}

type step struct {
	name string
	do   func(w *world)
}

func runScenario(t *testing.T, name string, steps []step) {
	t.Helper()
	w := newWorld(t)
	for i, s := range steps {
		t.Logf("scenario[%s] step %d/%d: %s", name, i+1, len(steps), s.name)
		s.do(w)
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "response is not a JSON object: %s", rec.Body.String())
	return out
}

func (w *world) memberRole(email, wsID string) string {
	w.t.Helper()
	var role string
	err := w.st.pool.QueryRow(context.Background(), `
SELECT m.role FROM member m JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = $1 AND u.email = $2 AND m.removed_at IS NULL`, wsID, email).Scan(&role)
	require.NoErrorf(w.t, err, "user %s should have a member record in workspace %s", email, wsID)
	return role
}

func (w *world) memberIDOf(email, wsID string) string {
	w.t.Helper()
	var id string
	err := w.st.pool.QueryRow(context.Background(), `
SELECT u.id::text FROM member m JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = $1 AND u.email = $2 AND m.removed_at IS NULL`, wsID, email).Scan(&id)
	require.NoError(w.t, err)
	return id
}

func (w *world) loginAs(client, email string) {
	w.t.Helper()
	w.st.login(client, email)
}

func (w *world) bootstrapCSRF(client string) {
	w.t.Helper()
	rec := w.st.do(client, http.MethodGet, "/config", nil, nil)
	require.Equal(w.t, http.StatusOK, rec.Code, rec.Body.String())
	var cfg struct {
		CSRFToken string `json:"csrf_token"`
	}
	require.NoError(w.t, json.Unmarshal(rec.Body.Bytes(), &cfg))
	w.st.csrf[client] = cfg.CSRFToken
}

func (w *world) createWSAs(client, slug string) string {
	w.t.Helper()
	wsID := w.st.createWS(w.t, client, slug)
	w.st.wsID = wsID
	return wsID
}

func (w *world) ensureWorkItem() string {
	w.t.Helper()
	if id, ok := w.vars["wiID"].(string); ok && id != "" {
		return id
	}
	id := w.st.createWorkItem(w.t, "owner", "L1 template work item")
	w.vars["wiID"] = id
	return id
}

var _ = http.MethodGet

func (w *world) doMultipart(client, path, filename, content string, fields map[string]string) *httptest.ResponseRecorder {
	w.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.RemoteAddr = "192.0.2.9:5555"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, c := range w.st.jar[client] {
		req.AddCookie(c)
	}
	if w.st.csrf[client] != "" {
		req.Header.Set("X-CSRF-Token", w.st.csrf[client])
	}
	if w.st.wsID != "" {
		req.Header.Set("X-Workspace-Id", w.st.wsID)
	}
	rec := httptest.NewRecorder()
	w.st.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "orkome_auth" || c.Name == "orkome_csrf" {
			w.st.jar[client] = append(w.st.jar[client], c)
		}
	}
	return rec
}

func unmarshalInto(body string, v any) error {
	return json.Unmarshal([]byte(body), v)
}

func (w *world) ensureLabel() string {
	if id, ok := w.vars["labelID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost, "/api/labels", map[string]string{"name": "recipe signature"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["labelID"] = id
	return id
}

func (w *world) ensureProject() string {
	if id, ok := w.vars["projID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost, "/api/projects", map[string]string{"name": "recipe project"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["projID"] = id
	return id
}

func (w *world) ensureComment() string {
	if id, ok := w.vars["cmID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost,
		"/api/work-items/"+w.ensureWorkItem()+"/comments",
		map[string]string{"content": "recipe comment"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["comment"].(map[string]any)["id"].(string)
	w.vars["cmID"] = id
	return id
}

func (w *world) ensureAgent() string {
	if id, ok := w.vars["agentID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost, "/api/agents", map[string]any{
		"name": "recipe agent", "runtime_config": map[string]any{"provider": "fake", "model": "fake-1"},
	}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["agentID"] = id
	return id
}

func (w *world) ensureToken() string {
	if id, ok := w.vars["tokID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost, "/api/tokens", map[string]string{"name": "recipe token"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["tokID"] = id
	return id
}

func (w *world) ensureSkill() string {
	if id, ok := w.vars["skillID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost, "/api/skills",
		map[string]string{"name": "recipe-skill", "description": "recipe skill"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)

	rec = w.st.do("owner", http.MethodPut, "/api/skills/"+id+"/files", map[string]string{
		"path":    "SKILL.md",
		"content": "---\nname: recipe-skill\ndescription: recipe skill\n---\n\n# Recipe",
	}, nil)
	require.Equal(w.t, http.StatusOK, rec.Code, rec.Body.String())
	w.vars["skillID"] = id
	return id
}

func (w *world) ensureAutopilot() string {
	if id, ok := w.vars["apID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost, "/api/autopilots", map[string]any{
		"name": "recipe autopilot", "exec_mode": "create_work_item",
		"target_agent_id": w.ensureAgent(), "template": map[string]any{"title": "recipe {{date}}"},
		"enabled": true,
	}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["apID"] = id
	return id
}

func (w *world) ensureWakeup() string {
	if id, ok := w.vars["wkID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost,
		"/api/work-items/"+w.ensureWorkItem()+"/wakeups", map[string]any{
			"agent_id": w.ensureAgent(), "kind": "time", "instruction": "recipe instruction",
			"timezone": "UTC", "enabled": true,
		}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["wkID"] = id
	return id
}

func (w *world) ensureChatSession() string {
	if id, ok := w.vars["sessID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost, "/api/chat/sessions", map[string]string{
		"agent_id": w.ensureAgent(), "title": "recipe session",
	}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["sessID"] = id
	return id
}

func (w *world) ensureWIExplicit(title string) string {
	rec := w.st.do("owner", http.MethodPost, "/api/work-items",
		map[string]string{"title": title, "status": "todo"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeJSON(w.t, rec)["id"].(string)
}

func (w *world) ensureBatch() string {
	if id, ok := w.vars["batchID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost,
		"/api/work-items/"+w.ensureWorkItem()+"/batches",
		map[string]any{"kind": "generic", "payloads": []map[string]any{{"title": "recipe batch sub-items"}}}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["batchID"] = id
	return id
}

func (w *world) ensureQuickAction() string {
	if id, ok := w.vars["qaID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost, "/api/quick-actions",
		map[string]any{"label": "recipe shortcut", "prompt": "do something"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["qaID"] = id
	return id
}

func (w *world) ensureWsMcp() string {
	if id, ok := w.vars["wmcpID"].(string); ok && id != "" {
		return id
	}

	rec := w.st.do("owner", http.MethodGet,
		"/api/workspaces/"+w.str("wsID")+"/mcp-servers", nil, nil)
	if rec.Code == http.StatusOK {
		var out struct {
			Servers []map[string]any `json:"servers"`
		}
		if unmarshalInto(rec.Body.String(), &out) == nil {
			for _, it := range out.Servers {
				if n, _ := it["name"].(string); n == "recipe ws-mcp" {
					if id, ok := it["id"].(string); ok {
						w.vars["wmcpID"] = id
						return id
					}
				}
			}
		}
	}
	rec = w.st.do("owner", http.MethodPost,
		"/api/workspaces/"+w.str("wsID")+"/mcp-servers",
		map[string]any{"name": "recipe ws-mcp", "url": "https://127.0.0.1:19999/sse"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["wmcpID"] = id
	return id
}

func (w *world) ensureAgentMcp() string {
	if id, ok := w.vars["amcpID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost,
		"/api/agents/"+w.ensureAgent()+"/mcp-servers",
		map[string]any{"name": "recipe agent-mcp", "url": "https://127.0.0.1:19999/sse"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["amcpID"] = id
	return id
}

func (w *world) ensureSquad() string {
	if id, ok := w.vars["squadID"].(string); ok && id != "" {
		return id
	}

	rec := w.st.do("owner", http.MethodGet, "/api/squads", nil, nil)
	if rec.Code == http.StatusOK {
		var out struct {
			Squads []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"squads"`
		}
		if unmarshalInto(rec.Body.String(), &out) == nil {
			for _, it := range out.Squads {
				if it.Name == "recipe squad" {
					w.vars["squadID"] = it.ID
					return it.ID
				}
			}
		}
	}
	rec = w.st.do("owner", http.MethodPost, "/api/squads",
		map[string]any{"name": "recipe squad"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["squadID"] = id
	return id
}

func (w *world) ensureTrigger() string {
	if id, ok := w.vars["trgID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost,
		"/api/autopilots/"+w.ensureAutopilot()+"/triggers",
		map[string]string{"kind": "schedule", "cron_expression": "0 9 * * 1", "timezone": "UTC"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["trgID"] = id
	return id
}

func (w *world) ensurePropDef() string {
	if id, ok := w.vars["propKey"].(string); ok && id != "" {
		return id
	}

	rec := w.st.do("owner", http.MethodGet, "/api/properties/recipe_prop", nil, nil)
	if rec.Code == http.StatusOK {
		w.vars["propKey"] = "recipe_prop"
		return "recipe_prop"
	}
	rec = w.st.do("owner", http.MethodPost, "/api/properties",
		map[string]any{"key": "recipe_prop", "type": "text"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	w.vars["propKey"] = "recipe_prop"
	return "recipe_prop"
}

func (w *world) ensureInvitation() string {
	if id, ok := w.vars["invID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost,
		"/api/workspaces/"+w.str("wsID")+"/invitations",
		map[string]string{"email": "invite-p3@e2e.test", "role": "member"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["invID"] = id
	return id
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (w *world) ensureBuilderSession() string {
	if id, ok := w.vars["bldrID"].(string); ok && id != "" {
		return id
	}
	rec := w.st.do("owner", http.MethodPost, "/api/agent-builder/sessions", map[string]any{}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeJSON(w.t, rec)["id"].(string)
	w.vars["bldrID"] = id
	return id
}
