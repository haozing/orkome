package main

// dispatch 端点冒烟（P1-6a）：不碰真 DB——用真 EnqueueService + stub
// orchapp.Repo（嵌入接口只覆写 EnqueueAssignment 会触达的方法，先例
// router_contract_test.go 的 stubRepo）。钉死三档契约：
//
//	201 {"task_id","result":"queued"} / 409 E_ACTIVE_DUPLICATE+active_task_id /
//	422 E_VALIDATION+reason_code（blocked/model_not_configured）。
//
// 关键回归点：EnqueueAssignment 对 Blocked/AlreadyActive 返回 (nil, reason,
// nil)——handler 绝不解引用 nil task（生产实证过的 nil panic 缝）。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/events"
	orchapp "orkome/internal/orchestration/app"
)

type dispatchStubRepo struct {
	orchapp.Repo

	agent      orchapp.Agent
	agentFound bool
	fence      int
	active     *orchapp.Task
	inserted   orchapp.Task
}

func (f *dispatchStubRepo) GetAgent(context.Context, string, string) (orchapp.Agent, bool, error) {
	return f.agent, f.agentFound, nil
}
func (f *dispatchStubRepo) CountPendingFence(context.Context, string, string) (int, error) {
	return f.fence, nil
}
func (f *dispatchStubRepo) GetActiveTask(context.Context, string, string) (orchapp.Task, bool, error) {
	if f.active == nil {
		return orchapp.Task{}, false, nil
	}
	return *f.active, true, nil
}
func (f *dispatchStubRepo) InsertTask(_ context.Context, t orchapp.Task) (orchapp.Task, error) {
	t.ID = "t-inserted"
	f.inserted = t
	return t, nil
}
func (f *dispatchStubRepo) GetLastSessionRef(context.Context, string, string, string) (string, bool, error) {
	return "", false, nil
}

type noopEmitter struct{}

func (noopEmitter) Emit(context.Context, events.Envelope) {}

func newDispatchTestServer(repo *dispatchStubRepo) http.Handler {
	mux := http.NewServeMux()
	enq := orchapp.NewEnqueueService(repo, noopEmitter{})
	// SetOutbox 不调用：无 outbox 时 emitCommitted 直写直发；断言在
	// handler 响应面（入队成功 → 201 契约）。
	mountDispatch(mux, func(next http.Handler) http.Handler { return next },
		dispatchDeps{
			Enq:         enq,
			Repo:        repo,
			MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil },
		})
	return mux
}

func doDispatch(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/work-items/wi-1/dispatch", strings.NewReader(body))
	req.Header.Set("X-Workspace-Id", "ws-1")
	req = req.WithContext(webx.WithPrincipal(req.Context(), &webx.Principal{UserID: "u-1"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDispatchEndpointSmoke(t *testing.T) {
	externalAgent := orchapp.Agent{ID: "ag-1", Name: "nova", RuntimeKind: "external"}

	t.Run("queued → 201 task_id+result", func(t *testing.T) {
		repo := &dispatchStubRepo{agent: externalAgent, agentFound: true}
		rec := doDispatch(t, newDispatchTestServer(repo), `{"agent_id":"ag-1"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("code = %d, want 201; body=%s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, want := range []string{`"task_id":"t-inserted"`, `"result":"queued"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("body %s missing %s", body, want)
			}
		}
		if repo.inserted.AgentID != "ag-1" || repo.inserted.WorkItemID != "wi-1" {
			t.Fatalf("inserted task = %+v, want agent ag-1 on wi-1", repo.inserted)
		}
	})

	t.Run("already_active → 409 with active_task_id", func(t *testing.T) {
		repo := &dispatchStubRepo{
			agent: externalAgent, agentFound: true, fence: 1,
			active: &orchapp.Task{ID: "t-active"},
		}
		rec := doDispatch(t, newDispatchTestServer(repo), `{"agent_id":"ag-1"}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("code = %d, want 409; body=%s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, want := range []string{`"error":"E_ACTIVE_DUPLICATE"`, `"active_task_id":"t-active"`, `"reason":"already_active"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("body %s missing %s", body, want)
			}
		}
	})

	t.Run("agent archived/blocked → 422 reason_code=blocked", func(t *testing.T) {
		repo := &dispatchStubRepo{agent: orchapp.Agent{ID: "ag-1", Archived: true}, agentFound: true}
		rec := doDispatch(t, newDispatchTestServer(repo), `{"agent_id":"ag-1"}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("code = %d, want 422; body=%s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, want := range []string{`"error":"E_VALIDATION"`, `"reason_code":"blocked"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("body %s missing %s", body, want)
			}
		}
	})

	t.Run("model not configured → 422 reason_code=model_not_configured", func(t *testing.T) {
		// RuntimeKind 空（非 external）且 runtime_config 无 model → 配置闸。
		repo := &dispatchStubRepo{agent: orchapp.Agent{ID: "ag-1"}, agentFound: true}
		rec := doDispatch(t, newDispatchTestServer(repo), `{"agent_id":"ag-1"}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("code = %d, want 422; body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"reason_code":"model_not_configured"`) {
			t.Fatalf("body %s missing reason_code=model_not_configured", rec.Body.String())
		}
	})

	t.Run("missing agent_id → 400", func(t *testing.T) {
		repo := &dispatchStubRepo{agent: externalAgent, agentFound: true}
		rec := doDispatch(t, newDispatchTestServer(repo), `{}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400", rec.Code)
		}
	})

	t.Run("no workspace header → 400", func(t *testing.T) {
		repo := &dispatchStubRepo{agent: externalAgent, agentFound: true}
		h := newDispatchTestServer(repo)
		req := httptest.NewRequest(http.MethodPost, "/api/work-items/wi-1/dispatch", nil)
		req = req.WithContext(webx.WithPrincipal(req.Context(), &webx.Principal{UserID: "u-1"}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, want 400", rec.Code)
		}
	})
}
