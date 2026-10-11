package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

type agentAccessRepo struct {
	orchapp.Repo
	byID    map[string]orchapp.Agent
	byName  map[string]orchapp.Agent
	targets map[string][]orchapp.InvocationTarget
	seq     int
}

func newAgentAccessHTTPDeps() (*Deps, *agentAccessRepo) {
	repo := &agentAccessRepo{
		byID: map[string]orchapp.Agent{}, byName: map[string]orchapp.Agent{},
		targets: map[string][]orchapp.InvocationTarget{},
	}

	memberCheck := func(_ context.Context, _, userID string) (bool, error) {
		return userID == "u-in" || userID == "sqh-user", nil
	}
	agents := orchapp.NewAgentService(repo, nil)
	d := &Deps{Agents: agents, Repo: repo, MemberCheck: memberCheck}
	return d, repo
}

func (f *agentAccessRepo) GetAgentByName(_ context.Context, _, name string) (orchapp.Agent, bool, error) {
	a, ok := f.byName[name]
	return a, ok, nil
}

func (f *agentAccessRepo) GetAgent(_ context.Context, _, id string) (orchapp.Agent, bool, error) {
	a, ok := f.byID[id]
	return a, ok, nil
}

func (f *agentAccessRepo) CreateAgent(_ context.Context, a orchapp.Agent) (orchapp.Agent, error) {
	f.seq++
	a.ID = fmt.Sprintf("ag-%d", f.seq)
	if a.ConversationStarters == nil {
		a.ConversationStarters = []orchapp.ConversationStarter{}
	}
	f.byID[a.ID] = a
	f.byName[a.Name] = a
	return a, nil
}

func (f *agentAccessRepo) UpdateAgent(_ context.Context, a orchapp.Agent) (orchapp.Agent, bool, error) {
	if _, ok := f.byID[a.ID]; !ok {
		return orchapp.Agent{}, false, nil
	}
	if a.ConversationStarters == nil {
		a.ConversationStarters = []orchapp.ConversationStarter{}
	}
	f.byID[a.ID] = a
	f.byName[a.Name] = a
	return a, true, nil
}

func (f *agentAccessRepo) ReplaceInvocationTargets(_ context.Context, _, agentID string, targets []orchapp.InvocationTarget) error {
	f.targets[agentID] = targets
	return nil
}

func (f *agentAccessRepo) GetInvocationTargets(_ context.Context, _, agentID string) ([]orchapp.InvocationTarget, error) {
	return f.targets[agentID], nil
}

func agentAccessRouter(d *Deps) http.Handler {
	mux := http.NewServeMux()
	Mount(mux, *d, passAuth)
	return mux
}

func agentAccessDo(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("X-Workspace-Id", "ws-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAgentAccessHTTP_CreateValidation(t *testing.T) {
	d, repo := newAgentAccessHTTPDeps()
	h := agentAccessRouter(d)

	rec := agentAccessDo(h, "POST", "/api/agents", `{"name":"a","visibility":"public_to",
		"conversation_starters":[{"label":"1"},{"label":"2"},{"label":"3"},{"label":"4"}]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, apierr.Validation, errCodeOfHTTP(rec))

	rec = agentAccessDo(h, "POST", "/api/agents", `{"name":"a","visibility":"public",
		"invocation_targets":[{"type":"workspace"}]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "non-public_to with non-empty targets → 400")
	assert.Equal(t, apierr.Validation, errCodeOfHTTP(rec))

	rec = agentAccessDo(h, "POST", "/api/agents", `{"name":"a","visibility":"public_to",
		"invocation_targets":[{"type":"member","user_id":"u-out"}]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "member type non-local member → 400 (Mount-wired MemberCheck)")
	assert.Empty(t, repo.byID, "validation failure creates no rows")
}

func TestAgentAccessHTTP_CreateGetWithTargetsAndStarters(t *testing.T) {
	d, _ := newAgentAccessHTTPDeps()
	h := agentAccessRouter(d)

	rec := agentAccessDo(h, "POST", "/api/agents", `{"name":"guard","visibility":"public_to",
		"conversation_starters":[{"label":" summarize for me "}],
		"invocation_targets":[{"type":"workspace"},{"type":"member","user_id":"u-in"}]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created orchapp.Agent
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, []orchapp.ConversationStarter{{Label: "summarize for me"}}, created.ConversationStarters)

	rec = agentAccessDo(h, "GET", "/api/agents/"+created.ID, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	targets, ok := got["invocation_targets"].([]any)
	require.True(t, ok, "GET response must carry invocation_targets: %v", got)
	require.Len(t, targets, 2)
	first, _ := targets[0].(map[string]any)
	assert.Equal(t, "workspace", first["type"])
	second, _ := targets[1].(map[string]any)
	assert.Equal(t, "u-in", second["user_id"])
	starters, _ := got["conversation_starters"].([]any)
	require.Len(t, starters, 1)

	rec = agentAccessDo(h, "POST", "/api/agents", `{"name":"plain"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	var plain orchapp.Agent
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &plain))
	rec = agentAccessDo(h, "GET", "/api/agents/"+plain.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	got = map[string]any{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	empty, ok := got["invocation_targets"].([]any)
	require.True(t, ok, "empty allowlist renders []: %v", got)
	assert.Empty(t, empty)
}

func TestAgentAccessHTTP_UpdateClearsTargets(t *testing.T) {
	d, repo := newAgentAccessHTTPDeps()
	h := agentAccessRouter(d)

	rec := agentAccessDo(h, "POST", "/api/agents", `{"name":"guard","visibility":"public_to",
		"conversation_starters":[{"label":"old"}],
		"invocation_targets":[{"type":"workspace"}]}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created orchapp.Agent
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

	rec = agentAccessDo(h, "PUT", "/api/agents/"+created.ID, `{"name":"guard","visibility":"public_to",
		"invocation_targets":[]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, repo.targets[created.ID], "empty array = clear")

	rec = agentAccessDo(h, "GET", "/api/agents/"+created.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	empty, _ := got["invocation_targets"].([]any)
	assert.Empty(t, empty)
	starters, _ := got["conversation_starters"].([]any)
	assert.Len(t, starters, 1, "starters default inherits current column")
}

func errCodeOfHTTP(rec *httptest.ResponseRecorder) string {
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	c, _ := out["error"].(string)
	return c
}
