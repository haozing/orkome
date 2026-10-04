package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/webx"
)

func newAgentLabelsRouter(t *testing.T, mounted []string, notFoundFor string) (*chi.Mux, *map[string]int) {
	t.Helper()
	calls := map[string]int{}
	labels := func(ids ...string) []AgentLabelView {
		out := make([]AgentLabelView, 0, len(ids))
		for _, id := range ids {
			out = append(out, AgentLabelView{ID: id, Name: "L" + id, Color: "#111"})
		}
		return out
	}
	ep := &AgentLabelEndpoints{
		List: func(_ context.Context, ws, agentID string) ([]AgentLabelView, error) {
			calls["list"]++
			return labels(mounted...), nil
		},
		Attach: func(_ context.Context, ws, agentID, labelID string) ([]AgentLabelView, error) {
			calls["attach"]++
			if agentID == notFoundFor {
				return nil, apierr.New(apierr.NotFound, "agent not found")
			}
			return labels(append(append([]string{}, mounted...), labelID)...), nil
		},
		Detach: func(_ context.Context, ws, agentID, labelID string) ([]AgentLabelView, error) {
			calls["detach"]++
			return labels(), nil
		},
	}
	deps := Deps{
		Agents:      &orchapp.AgentService{},
		AgentLabels: ep,
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(),
				&webx.Principal{UserID: "u-1", Source: webx.SourceSession})))
		})
	}
	r := chi.NewRouter()
	Mount(r, deps, auth)
	return r, &calls
}

func agentLabelsReq(r http.Handler, method, target, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	req.Header.Set("X-Workspace-Id", "ws-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAgentLabelsEndpoints_MountedListAttachDetach(t *testing.T) {
	r, calls := newAgentLabelsRouter(t, []string{"lab-1"}, "")

	rec := agentLabelsReq(r, "GET", "/agents/ag-1/labels", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var got []AgentLabelView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "lab-1", got[0].ID)

	rec = agentLabelsReq(r, "POST", "/agents/ag-1/labels", `{"label_id":"lab-2"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got, 2, "attach response equals updated catalog")

	rec = agentLabelsReq(r, "DELETE", "/agents/ag-1/labels/lab-2", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
	assert.Equal(t, 1, (*calls)["list"], "GET once each")
	assert.Equal(t, 1, (*calls)["attach"])
	assert.Equal(t, 1, (*calls)["detach"])
}

func TestAgentLabelsEndpoints_ValidationAndNotFound(t *testing.T) {
	r, _ := newAgentLabelsRouter(t, nil, "ag-404")
	assert.Equal(t, http.StatusBadRequest, agentLabelsReq(r, "POST", "/agents/ag-1/labels", `{}`).Code)
	assert.Equal(t, http.StatusBadRequest, agentLabelsReq(r, "POST", "/agents/ag-1/labels", `not-json`).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, agentLabelsReq(r, "DELETE", "/agents/ag-1/labels", "").Code)
	assert.Equal(t, http.StatusNotFound, agentLabelsReq(r, "POST", "/agents/ag-404/labels", `{"label_id":"x"}`).Code)
}

func TestAgentLabelsEndpoints_NilNotMounted(t *testing.T) {
	deps := Deps{
		Agents:      &orchapp.AgentService{},
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	r := chi.NewRouter()
	Mount(r, deps, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r2 *http.Request) {
			next.ServeHTTP(w, r2.WithContext(webx.WithPrincipal(r2.Context(),
				&webx.Principal{UserID: "u-1", Source: webx.SourceSession})))
		})
	})
	rec := agentLabelsReq(r, "GET", "/agents/ag-1/labels", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "nil injection = routes not registered")
}
