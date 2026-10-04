package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"orkome/internal/platform/webx"
)

func TestRuntimeAgentWriteGuard(t *testing.T) {
	cases := []struct {
		name    string
		agentID string
		method  string
		path    string
		want    int
	}{
		{"loop claim allowed", "ag1", http.MethodPost, "/api/tasks/abc/claim", 200},
		{"loop message allowed", "ag1", http.MethodPost, "/api/tasks/abc/messages", 200},
		{"loop done allowed", "ag1", http.MethodPost, "/api/tasks/abc/complete", 200},
		{"loop fail allowed", "ag1", http.MethodPost, "/api/tasks/abc/fail", 200},
		{"comment write allowed", "ag1", http.MethodPost, "/api/work-items/abc/comments", 200},
		{"read face unrestricted", "ag1", http.MethodGet, "/api/agents", 200},
		{"ordinary principals unrestricted", "", http.MethodPost, "/api/agents", 200},
		{"mint PAT rejected", "ag1", http.MethodPost, "/api/tokens", 403},
		{"create work item rejected", "ag1", http.MethodPost, "/api/work-items", 403},
		{"change work item rejected", "ag1", http.MethodPut, "/api/work-items/abc", 403},
		{"delete comment rejected", "ag1", http.MethodDelete, "/api/comments/abc", 403},
		{"create automation rejected", "ag1", http.MethodPost, "/api/autopilots", 403},
		{"unauthorized path variant rejected", "ag1", http.MethodPost, "/api/tasks/abc/claim/extra", 403},
		{"non-/api rejected", "ag1", http.MethodPost, "/webhooks/autopilots/x", 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := runtimeAgentWriteGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req = req.WithContext(webx.WithPrincipal(req.Context(), &webx.Principal{AgentID: tc.agentID}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s %s (agent=%q) = %d, want %d", tc.method, tc.path, tc.agentID, rec.Code, tc.want)
			}
		})
	}

	h := runtimeAgentWriteGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/agents", nil).WithContext(context.Background())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("no principal = %d, want 200", rec.Code)
	}
}
