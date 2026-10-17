package main

// setup-status 端点冒烟（P1-6c）：不碰真 DB——stub orchapp.Repo.ListAgents +
// 真 CredentialService（stub CredentialRepo）。钉死契约形状：
//
//	{"credential_count": N, "agents": [{"id","name","archived","ready"}...]}
//
// ready 判定复用 A2 的 agentRuntimeReadyMe（三键非空）——本文件只钉
// 端点装配（鉴权链、头校验、聚合形状），ready 规则的钉子在
// wiring_mcp_me_config_test.go 的 TestAgentRuntimeReadyMe。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/haozing/ploykit/platform/webx"
	orchapp "orkome/internal/orchestration/app"
)

type setupStubRepo struct {
	orchapp.Repo
	agents []orchapp.Agent
}

func (f *setupStubRepo) ListAgents(context.Context, string) ([]orchapp.Agent, error) {
	return f.agents, nil
}

type setupStubCredRepo struct{}

func (setupStubCredRepo) CreateCredential(context.Context, string, string, string, string) (orchapp.Credential, error) {
	return orchapp.Credential{}, nil
}
func (setupStubCredRepo) ListCredentials(context.Context, string) ([]orchapp.Credential, error) {
	return []orchapp.Credential{{ID: "c-1"}, {ID: "c-2"}}, nil
}
func (setupStubCredRepo) DeleteCredential(context.Context, string, string) (bool, error) {
	return true, nil
}

func TestSetupStatusEndpointSmoke(t *testing.T) {
	mux := http.NewServeMux()
	mountSetupStatus(mux, func(next http.Handler) http.Handler { return next }, setupStatusDeps{
		Repo: &setupStubRepo{agents: []orchapp.Agent{
			{ID: "ag-1", Name: "nova", RuntimeConfig: map[string]any{
				"model": "m", "credential_id": "c-1", "base_url": "https://x"}},
			{ID: "ag-2", Name: "ghost", Archived: true},
		}},
		Credentials: orchapp.NewCredentialService(setupStubCredRepo{}),
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil },
	})

	req := httptest.NewRequest(http.MethodGet, "/api/setup-status", nil)
	req.Header.Set("X-Workspace-Id", "ws-1")
	req = req.WithContext(webx.WithPrincipal(req.Context(), &webx.Principal{UserID: "u-1"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"credential_count":2`,
		`"id":"ag-1"`, `"name":"nova"`, `"archived":false`, `"ready":true`,
		`"id":"ag-2"`, `"archived":true`, `"ready":false`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %s missing %s", body, want)
		}
	}

	t.Run("missing workspace header → 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/setup-status", nil)
		req = req.WithContext(webx.WithPrincipal(req.Context(), &webx.Principal{UserID: "u-1"}))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400", rec.Code)
		}
	})

	t.Run("non-member → 403", func(t *testing.T) {
		denied := http.NewServeMux()
		mountSetupStatus(denied, func(next http.Handler) http.Handler { return next }, setupStatusDeps{
			Repo:        &setupStubRepo{},
			Credentials: orchapp.NewCredentialService(setupStubCredRepo{}),
			MemberCheck: func(context.Context, string, string) (bool, error) { return false, nil },
		})
		req := httptest.NewRequest(http.MethodGet, "/api/setup-status", nil)
		req.Header.Set("X-Workspace-Id", "ws-1")
		req = req.WithContext(webx.WithPrincipal(req.Context(), &webx.Principal{UserID: "u-1"}))
		rec := httptest.NewRecorder()
		denied.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403", rec.Code)
		}
	})
}
