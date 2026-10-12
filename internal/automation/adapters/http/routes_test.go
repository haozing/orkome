package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/automation/app"
	"orkome/internal/automation/domain"
)

func TestClientIP_DefaultIgnoresProxyHeaders(t *testing.T) {

	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "5.6.7.8")
	require.Equal(t, "203.0.113.5", ClientIP(req, nil))
}

func TestClientIP_TrustedProxyRightmostNonTrusted(t *testing.T) {
	trusted := ParseTrustedProxies("10.0.0.0/8")
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "10.1.2.3:9999"
	req.Header.Set("X-Forwarded-For", "5.5.5.5, 10.0.0.7")

	require.Equal(t, "5.5.5.5", ClientIP(req, trusted))
}

func TestClientIP_UntrustedSourceIgnoresXFF(t *testing.T) {

	trusted := ParseTrustedProxies("10.0.0.0/8")
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Forwarded-For", "5.5.5.5")
	require.Equal(t, "203.0.113.5", ClientIP(req, trusted))
}

func TestClientIP_IPv6RemoteAddr(t *testing.T) {
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "[2001:db8::1]:443"
	require.Equal(t, "2001:db8::1", ClientIP(req, nil))
	require.Equal(t, "2001:db8::1", RemoteAddrHost(req))
}

func TestParseTrustedProxies(t *testing.T) {
	require.Nil(t, ParseTrustedProxies(""))
	require.Nil(t, ParseTrustedProxies("   "))
	got := ParseTrustedProxies("10.0.0.0/8, nope, 192.168.0.0/16")
	require.Len(t, got, 2, "invalid entries skipped (trust less)")
}

func unlimited() LayerLimit { return LayerLimit{Limit: 1 << 30, Window: time.Minute} }

func TestAllow_GlobalLayer(t *testing.T) {
	l := NewWebhookRateLimiter(nil, RateLimitConfig{
		Global: LayerLimit{Limit: 2, Window: time.Minute}, PerIP: unlimited(), AbsIP: unlimited(),
	}, nil)
	post := func(remote string) bool {
		req := httptest.NewRequest("POST", "/webhooks/autopilots/t", nil)
		req.RemoteAddr = remote
		ok, _ := l.Allow(context.Background(), req)
		return ok
	}
	require.True(t, post("1.1.1.1:1"))
	require.True(t, post("2.2.2.2:2"))
	require.False(t, post("3.3.3.3:3"), "global bucket over limit must be rejected regardless of source")
}

func TestAllow_PerIPLayer(t *testing.T) {
	l := NewWebhookRateLimiter(nil, RateLimitConfig{
		Global: unlimited(), PerIP: LayerLimit{Limit: 1, Window: time.Minute}, AbsIP: unlimited(),
	}, nil)
	post := func(remote string) bool {
		req := httptest.NewRequest("POST", "/", nil)
		req.RemoteAddr = remote
		ok, _ := l.Allow(context.Background(), req)
		return ok
	}
	require.True(t, post("9.9.9.9:9"))
	require.False(t, post("9.9.9.9:9"), "same IP second attempt must be rejected")
	require.True(t, post("8.8.8.8:8"), "different IPs unaffected")
}

func TestAllow_AbsIPLayer_BlocksXFFRotation(t *testing.T) {
	l := NewWebhookRateLimiter(nil, RateLimitConfig{
		Global: unlimited(), PerIP: unlimited(), AbsIP: LayerLimit{Limit: 2, Window: time.Minute},
	}, ParseTrustedProxies("10.0.0.0/8"))
	post := func(clientIP string) bool {
		req := httptest.NewRequest("POST", "/", nil)
		req.RemoteAddr = "10.0.0.9:1000"
		req.Header.Set("X-Forwarded-For", clientIP)
		ok, _ := l.Allow(context.Background(), req)
		return ok
	}
	require.True(t, post("5.5.5.5"))
	require.True(t, post("6.6.6.6"))
	require.False(t, post("7.7.7.7"), "the absolute IP layer must catch XFF rotation bypass")
}

func TestMemorySlidingWindow_RecoverAfterWindow(t *testing.T) {
	m := newMemoryStore()
	m.nowFn = func() time.Time { return time.Unix(1000, 0) }
	ok1, _ := m.allow(context.Background(), "k", 1, time.Minute)
	ok2, _ := m.allow(context.Background(), "k", 1, time.Minute)
	require.True(t, ok1)
	require.False(t, ok2)
	m.nowFn = func() time.Time { return time.Unix(1000, 0).Add(61 * time.Second) }
	ok3, _ := m.allow(context.Background(), "k", 1, time.Minute)
	require.True(t, ok3, "must recover after the window slides out")
}

type fakeRepo struct {
	app.Repo
	ws, apID, trigID string
	token, secret    string
	quotaErr         error
}

func (f *fakeRepo) GetTriggerByToken(_ context.Context, _ string) (app.Trigger, bool, error) {
	return app.Trigger{}, false, nil
}

func (f *fakeRepo) InsertDelivery(_ context.Context, _, _, _ string, _ []byte) (string, bool, error) {
	return "dlv-1", false, nil
}

func (f *fakeRepo) SetTriggerSigningSecret(_ context.Context, ws, apID, trigID, secret string) (bool, error) {
	if ws != f.ws || apID != f.apID || trigID != f.trigID {
		return false, nil
	}
	f.secret = secret
	return true, nil
}

func (f *fakeRepo) SetTriggerWebhookToken(_ context.Context, ws, apID, trigID, token string) (bool, error) {
	if ws != f.ws || apID != f.apID || trigID != f.trigID {
		return false, nil
	}
	f.token = token
	return true, nil
}

func (f *fakeRepo) GetAutopilot(_ context.Context, _, id string) (app.Autopilot, bool, error) {
	if id != f.apID {
		return app.Autopilot{}, false, nil
	}
	return app.Autopilot{ID: id, WorkspaceID: f.ws, Enabled: true, ExecMode: "task_only", DailyLimit: 1}, true, nil
}

func (f *fakeRepo) ReserveQuota(_ context.Context, _ string, _, _ time.Time, _ int) (string, error) {
	return "", f.quotaErr
}

func (f *fakeRepo) InsertRun(_ context.Context, r app.Run) (app.Run, error) {
	r.ID = "run-1"
	return r, nil
}

func newTestDeps(fr *fakeRepo) Deps {
	auto := app.NewAutopilotService(fr, nil, nil)
	return Deps{Autopilots: auto, Webhooks: app.NewWebhookService(fr, auto),

		MemberCheck: func(ctx context.Context, workspaceID, userID string) (bool, error) {
			return true, nil
		}}
}

func doJSON(r *http.ServeMux, method, target, ws string, body string) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body == "" {
		rd = bytes.NewReader(nil)
	} else {
		rd = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, target, rd)
	if ws != "" {
		req.Header.Set("X-Workspace-Id", ws)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func mountedDeps(d Deps) *http.ServeMux {
	r := http.NewServeMux()
	Mount(r, d, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := &webx.Principal{UserID: "user-1", Source: webx.SourceSession}
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(), p)))
		})
	})
	return r
}

func TestMountRequiresAuth(t *testing.T) {
	r := http.NewServeMux()
	Mount(r, newTestDeps(&fakeRepo{ws: "ws-1"}), func(next http.Handler) http.Handler { return next })
	w := doJSON(r, http.MethodGet, "/api/autopilots/ap-1/runs", "ws-1", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "E_UNAUTHENTICATED")
}

func TestPutSigningSecret_Handler(t *testing.T) {
	fr := &fakeRepo{ws: "ws-1", apID: "ap-1", trigID: "trig-1"}
	r := mountedDeps(newTestDeps(fr))

	w := doJSON(r, http.MethodPut, "/api/autopilots/ap-1/triggers/trig-1/signing-secret", "ws-1",
		`{"secret":"short"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "E_VALIDATION")

	secret := strings.Repeat("s", 48)
	w = doJSON(r, http.MethodPut, "/api/autopilots/ap-1/triggers/trig-1/signing-secret", "ws-1",
		fmt.Sprintf(`{"secret":%q}`, secret))
	require.Equal(t, http.StatusOK, w.Code)
	var cred app.TriggerCredential
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cred))
	require.Equal(t, domain.SecretFingerprint(secret), cred.SecretFingerprint)
	require.True(t, cred.HasSigningSecret)
	require.Equal(t, secret, fr.secret, "plaintext must actually be persisted (masked only in responses)")

	w = doJSON(r, http.MethodPut, "/api/autopilots/ap-1/triggers/trig-1/signing-secret", "ws-2",
		fmt.Sprintf(`{"secret":%q}`, secret))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "E_NOT_FOUND")
}

func TestRotateWebhookToken_Handler(t *testing.T) {
	fr := &fakeRepo{ws: "ws-1", apID: "ap-1", trigID: "trig-1", token: "old"}
	r := mountedDeps(newTestDeps(fr))

	w := doJSON(r, http.MethodPost, "/api/autopilots/ap-1/triggers/trig-1/rotate-webhook-token", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	var cred app.TriggerCredential
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cred))
	require.NotEmpty(t, cred.WebhookToken, "rotation responses must show the new token exactly once")
	require.NotEqual(t, "old", cred.WebhookToken)
	require.Equal(t, cred.WebhookToken, fr.token)

	w = doJSON(r, http.MethodPost, "/api/autopilots/ap-1/triggers/trig-1/rotate-webhook-token", "ws-2", "")
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestTriggerAutopilot_QuotaExceeded_ReturnsSkippedRun(t *testing.T) {
	fr := &fakeRepo{ws: "ws-1", apID: "ap-1"}
	fr.quotaErr = fmt.Errorf("quota window: %w", domain.ErrQuotaExceeded)
	r := mountedDeps(newTestDeps(fr))

	w := doJSON(r, http.MethodPost, "/api/autopilots/ap-1/trigger", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code, "quota_exceeded is an admission state, not an error")
	var run app.Run
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &run))
	require.Equal(t, domain.RunSkipped, run.Status)
	require.Equal(t, domain.ReasonQuotaExceeded, run.ReasonCode)
}

func TestMountPublic_RateLimitPrecedesLookup(t *testing.T) {
	fr := &fakeRepo{}
	d := newTestDeps(fr)
	d.PublicRL = NewWebhookRateLimiter(nil, RateLimitConfig{
		Global: unlimited(), PerIP: LayerLimit{Limit: 1, Window: time.Minute}, AbsIP: unlimited(),
	}, nil)
	r := http.NewServeMux()
	MountPublic(r, d)

	w1 := doJSON(r, http.MethodPost, "/webhooks/autopilots/whatever", "", `{"x":1}`)
	require.Equal(t, http.StatusNotFound, w1.Code, "first request allowed into business logic (token unknown → 404)")

	w2 := doJSON(r, http.MethodPost, "/webhooks/autopilots/whatever", "", `{"x":1}`)
	require.Equal(t, http.StatusTooManyRequests, w2.Code)
	require.NotEmpty(t, w2.Header().Get("Retry-After"), "  429 must carry Retry-After (internal archive 04 §5)")
	require.Contains(t, w2.Body.String(), "E_RATE_LIMITED")
}

type wakeupRepoFake struct {
	app.Repo
	ws         string
	wi         string
	wakeups    map[string]app.Wakeup
	sysRules   map[string]app.Wakeup
	sysDefault app.SystemWakeupDefault
	hasDefault bool
	receipts   int
	instr      string
	deleted    bool
	deliveries map[string]app.Delivery
	nextID     int
}

func newWakeupRepoFake() *wakeupRepoFake {
	return &wakeupRepoFake{ws: "ws-1", wi: "wi-1", wakeups: map[string]app.Wakeup{},
		sysRules: map[string]app.Wakeup{}, deliveries: map[string]app.Delivery{}}
}

func (f *wakeupRepoFake) CreateWakeup(_ context.Context, w app.Wakeup) (app.Wakeup, error) {
	w.ID = f.id("wk")
	f.wakeups[w.ID] = w
	return w, nil
}

func (f *wakeupRepoFake) id(p string) string {
	f.nextID++
	return fmt.Sprintf("%s-%d", p, f.nextID)
}

func (f *wakeupRepoFake) GetWakeup(_ context.Context, ws, id string) (app.Wakeup, bool, error) {
	w, ok := f.wakeups[id]
	if !ok || w.WorkspaceID != ws {
		return app.Wakeup{}, false, nil
	}
	return w, true, nil
}

func (f *wakeupRepoFake) LatestWakeupChain(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}

func (f *wakeupRepoFake) SetWakeupInstruction(_ context.Context, ws, id, instr string) (bool, error) {
	if w, ok := f.wakeups[id]; ok && w.WorkspaceID == ws {
		w.Instruction = instr
		w.Revision++
		f.wakeups[id] = w
		f.instr = instr
		return true, nil
	}
	return false, nil
}

func (f *wakeupRepoFake) SetWakeupEnabled(_ context.Context, ws, id string, en bool) error {
	if w, ok := f.wakeups[id]; ok {
		w.Enabled = en
		f.wakeups[id] = w
	}
	return nil
}

func (f *wakeupRepoFake) DeleteWakeup(_ context.Context, ws, id string) error {
	delete(f.wakeups, id)
	f.deleted = true
	return nil
}

func (f *wakeupRepoFake) InsertReceipt(_ context.Context, rc app.Receipt) error {
	f.receipts++
	return nil
}

func (f *wakeupRepoFake) GetSystemWakeupDefault(_ context.Context, _ string) (app.SystemWakeupDefault, bool, error) {
	return f.sysDefault, f.hasDefault, nil
}

func (f *wakeupRepoFake) SetSystemWakeupDefault(_ context.Context, _ string, d app.SystemWakeupDefault) error {
	f.sysDefault = d
	f.hasDefault = true
	return nil
}

func (f *wakeupRepoFake) ListSystemWakeups(_ context.Context, ws string) ([]app.Wakeup, error) {
	var out []app.Wakeup
	for _, w := range f.sysRules {
		if w.WorkspaceID == ws {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *wakeupRepoFake) ListSystemWakeupsByWorkItem(_ context.Context, ws, wi string) ([]app.Wakeup, error) {
	var out []app.Wakeup
	for _, w := range f.sysRules {
		if w.WorkspaceID == ws && w.WorkItemID == wi {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *wakeupRepoFake) EnsureSystemWakeup(_ context.Context, w app.Wakeup) (app.Wakeup, error) {
	for _, ex := range f.sysRules {
		if ex.WorkItemID == w.WorkItemID {
			return ex, nil
		}
	}
	w.ID = f.id("sys")
	f.sysRules[w.ID] = w
	return w, nil
}

func (f *wakeupRepoFake) UpdateSystemWakeup(_ context.Context, ws, id string, instr *string, en *bool) (app.Wakeup, bool, error) {
	w, ok := f.sysRules[id]
	if !ok {
		return w, false, nil
	}
	if instr != nil {
		w.Instruction = *instr
	}
	if en != nil {
		w.Enabled = *en
	}
	f.sysRules[id] = w
	return w, true, nil
}

func (f *wakeupRepoFake) ListPausedWakeups(_ context.Context, ws string) ([]app.Wakeup, error) {
	return []app.Wakeup{{ID: "wk-paused", WorkspaceID: ws, PausedReason: "burst"}}, nil
}

func (f *wakeupRepoFake) ListWorkspaceWakeups(_ context.Context, ws string) ([]app.Wakeup, error) {
	var out []app.Wakeup
	for _, w := range f.wakeups {
		if w.WorkspaceID == ws {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *wakeupRepoFake) ListWakeupSummaries(_ context.Context, ws string) ([]app.WakeupSummary, error) {
	return []app.WakeupSummary{{WorkItemID: f.wi, Total: 2, Enabled: 1, Paused: 1}}, nil
}

func (f *wakeupRepoFake) LockTaskOwner(context.Context, string) error { return nil }

func (f *wakeupRepoFake) LockWakeup(_ context.Context, id string) (app.Wakeup, bool, error) {
	w, ok := f.wakeups[id]
	return w, ok, nil
}

func (f *wakeupRepoFake) FireWakeup(_ context.Context, id string, rev int64, next *time.Time, cond *string, taskID string) (bool, error) {
	w, ok := f.wakeups[id]
	if !ok || w.Revision != rev {
		return false, nil
	}
	w.FireCount++
	w.Revision++
	f.wakeups[id] = w
	return true, nil
}

func (f *wakeupRepoFake) SetDispatchLockTimeout(context.Context) error { return nil }

func (f *wakeupRepoFake) GetDelivery(_ context.Context, ws, id string) (app.Delivery, bool, error) {
	d, ok := f.deliveries[id]
	return d, ok, nil
}

func (f *wakeupRepoFake) ReplayDelivery(_ context.Context, ws, id string) (bool, error) {
	if d, ok := f.deliveries[id]; ok {
		d.Status = "queued"
		f.deliveries[id] = d
		return true, nil
	}
	return false, nil
}

func (f *wakeupRepoFake) ListDeliveries(_ context.Context, ws string, limit int) ([]app.Delivery, error) {
	var out []app.Delivery
	for _, d := range f.deliveries {
		out = append(out, d)
	}
	return out, nil
}

func newTailDeps(fr *wakeupRepoFake) Deps {
	wu := app.NewWakeupService(fr, tailEnq{}, tailFacts{})
	auto := app.NewAutopilotService(fr, nil, tailEnq{})
	return Deps{Wakeups: wu, Autopilots: auto, Webhooks: app.NewWebhookService(fr, auto),
		MemberCheck: func(ctx context.Context, workspaceID, userID string) (bool, error) {
			return true, nil
		}}
}

type tailEnq struct{}

func (tailEnq) EnqueueForWakeup(context.Context, string, string, string, string, string) (string, bool, error) {
	return "task-1", false, nil
}

type tailFacts struct{}

func (tailFacts) WorkItemFacts(context.Context, string, string) (string, int, error) {
	return "todo", 0, nil
}

func (tailFacts) ChildStageFacts(context.Context, string, string) ([]domain.ChildStageFact, error) {
	return nil, nil
}

func (tailFacts) WakeupTargetAgent(context.Context, string, string) (string, error) {
	return "ag-1", nil
}

func TestWakeupTailEndpoints(t *testing.T) {
	fr := newWakeupRepoFake()
	fr.wakeups["wk-1"] = app.Wakeup{ID: "wk-1", WorkspaceID: "ws-1", WorkItemID: "wi-1",
		AgentID: "ag-1", Kind: domain.WakeupTime, Enabled: true, Instruction: "check it", Revision: 1}
	r := mountedDeps(newTailDeps(fr))

	w := doJSON(r, http.MethodGet, "/api/work-items/wi-1/wakeups/wk-1", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "wk-1")
	w = doJSON(r, http.MethodGet, "/api/work-items/wi-9/wakeups/wk-1", "ws-1", "")
	require.Equal(t, http.StatusNotFound, w.Code, "path work item not matching the rule = invisible")

	w = doJSON(r, http.MethodPut, "/api/work-items/wi-1/wakeups/wk-1/instruction", "ws-1", `{"instruction":"new instruction"}`)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, "new instruction", fr.instr)
	w = doJSON(r, http.MethodPut, "/api/work-items/wi-1/wakeups/wk-1/instruction", "ws-1", `{"instruction":""}`)
	require.Equal(t, http.StatusBadRequest, w.Code)

	w = doJSON(r, http.MethodPost, "/api/work-items/wi-1/wakeups/wk-1/checkin", "ws-1", `{"task_id":"tk-1","note":"checked, all clear"}`)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, 1, fr.receipts)
	w = doJSON(r, http.MethodPost, "/api/work-items/wi-1/wakeups/wk-1/checkin", "ws-1",
		fmt.Sprintf(`{"task_id":"tk-1","note":"%s"}`, strings.Repeat("x", 501)))
	require.Equal(t, http.StatusBadRequest, w.Code)

	w = doJSON(r, http.MethodPost, "/api/work-items/wi-1/wakeups/wk-1/trigger", "ws-1", "")
	require.Equal(t, http.StatusNoContent, w.Code)

	w = doJSON(r, http.MethodGet, "/api/work-items/wi-1/wakeups/wk-1/tasks", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "[]", strings.TrimSpace(w.Body.String()))

	w = doJSON(r, http.MethodPost, "/api/work-items/wi-1/wakeups/wk-1/disable", "ws-1", "")
	require.Equal(t, http.StatusNoContent, w.Code)
	w = doJSON(r, http.MethodDelete, "/api/work-items/wi-1/wakeups/wk-1", "ws-1", "")
	require.Equal(t, http.StatusNoContent, w.Code)
	require.True(t, fr.deleted)
}

// FP-13 P0-2 回归：createWakeup 曾从不设置 CreatedBy（Wakeup.CreatedBy 无 json
// tag 暴露，body 也传不进来）→ fire 时补零 UUID 撞 agent_task_queue 的
// originator_user_id FK → InsertTask 失败、next_fire_at 永不推进、due 扫描每秒
// 重试同一失败（PG 错误洪水，无日志无自愈）。修复后 handler 必须从认证
// principal 回填 created_by。
func TestCreateWakeup_BackfillsCreatedByFromPrincipal(t *testing.T) {
	fr := newWakeupRepoFake()
	r := mountedDeps(newTailDeps(fr))

	w := doJSON(r, http.MethodPost, "/api/work-items/wi-1/wakeups", "ws-1",
		`{"kind":"event","instruction":"check it","event_types":["comment.created"]}`)
	require.Equal(t, http.StatusCreated, w.Code)
	var created app.Wakeup
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	stored, ok := fr.wakeups[created.ID]
	require.True(t, ok)
	require.Equal(t, "user-1", stored.CreatedBy,
		"handler must backfill created_by from the authenticated principal (else fire hits the originator FK)")
}

// principal 没有 UserID（坏会话/装配缺失）时不许落库——那正是"永不 fire + 每秒
// 刷 FK 错误"的死循环入口，必须在创建入口 400 拦下。
func TestCreateWakeup_MissingPrincipalUserID_Returns400(t *testing.T) {
	fr := newWakeupRepoFake()
	d := newTailDeps(fr)
	r := http.NewServeMux()
	Mount(r, d, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(webx.WithPrincipal(req.Context(),
				&webx.Principal{Source: webx.SourceSession})))
		})
	})

	w := doJSON(r, http.MethodPost, "/api/work-items/wi-1/wakeups", "ws-1",
		`{"kind":"event","instruction":"check it","event_types":["comment.created"]}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "E_VALIDATION")
	require.Empty(t, fr.wakeups, "must not persist a wakeup that can never fire")
}

func TestWorkspaceWakeupViews(t *testing.T) {
	fr := newWakeupRepoFake()
	r := mountedDeps(newTailDeps(fr))

	w := doJSON(r, http.MethodGet, "/api/work-item-wakeups", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	w = doJSON(r, http.MethodGet, "/api/work-item-wakeup-paused", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "burst")
	w = doJSON(r, http.MethodGet, "/api/work-item-wakeup-summaries", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"total":2`)
}

func TestSystemWakeupEndpoints(t *testing.T) {
	fr := newWakeupRepoFake()
	r := mountedDeps(newTailDeps(fr))

	w := doJSON(r, http.MethodGet, "/api/system-wakeups", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"enabled":true`)

	w = doJSON(r, http.MethodPut, "/api/system-wakeups", "ws-1", `{"enabled":false,"instruction":"turn it off"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, fr.sysDefault.Enabled)
	require.Equal(t, "turn it off", fr.sysDefault.Instruction)

	w = doJSON(r, http.MethodPost, "/api/work-items/wi-1/system-wakeups", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "children_done")
	w = doJSON(r, http.MethodPost, "/api/work-items/wi-1/system-wakeups", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, fr.sysRules, 1, "idempotent: no duplicate creation")

	w = doJSON(r, http.MethodGet, "/api/work-items/wi-1/system-wakeups", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	var rules []app.Wakeup
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rules))
	require.Len(t, rules, 1)
	w = doJSON(r, http.MethodPut, "/api/system-wakeups/"+rules[0].ID, "ws-1", `{"instruction":"single instruction"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "single instruction")
	w = doJSON(r, http.MethodPut, "/api/system-wakeups/nope", "ws-1", `{"instruction":"x"}`)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestCronPreviewEndpoint(t *testing.T) {
	fr := newWakeupRepoFake()
	r := mountedDeps(newTailDeps(fr))

	w := doJSON(r, http.MethodPost, "/api/autopilots/cron-preview", "ws-1",
		`{"cron_expression":"0 9 * * *","timezone":"Asia/Shanghai","count":2}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "next_runs")

	require.Regexp(t, `\d{4}-\d{2}-\d{2}T09:00:00\+08:00`, w.Body.String())

	w = doJSON(r, http.MethodPost, "/api/autopilots/cron-preview", "ws-1", `{"cron_expression":"bad"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeliveryReplayEndpoint(t *testing.T) {
	fr := newWakeupRepoFake()
	fr.deliveries["dlv-1"] = app.Delivery{ID: "dlv-1", WorkspaceID: "ws-1", Status: "failed"}
	r := mountedDeps(newTailDeps(fr))

	w := doJSON(r, http.MethodGet, "/api/deliveries", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	w = doJSON(r, http.MethodGet, "/api/deliveries/dlv-1", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	w = doJSON(r, http.MethodPost, "/api/deliveries/dlv-1/replay", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "queued")
	w = doJSON(r, http.MethodPost, "/api/deliveries/dlv-x/replay", "ws-1", "")
	require.Equal(t, http.StatusNotFound, w.Code)
}

type autopilotRepoFake struct {
	app.Repo
	ws            string
	autopilots    map[string]app.Autopilot
	triggers      map[string]app.Trigger
	runs          []app.Run
	collaborators map[string][]string
	lastListLimit int
	deletedTrig   []string
	deletedCollab []string
}

func newAutopilotRepoFake() *autopilotRepoFake {
	return &autopilotRepoFake{ws: "ws-1", autopilots: map[string]app.Autopilot{},
		triggers: map[string]app.Trigger{}, collaborators: map[string][]string{}}
}

func newAPDeps(fr *autopilotRepoFake) Deps {
	return Deps{Autopilots: app.NewAutopilotService(fr, nil, nil),
		MemberCheck: func(ctx context.Context, workspaceID, userID string) (bool, error) {
			return true, nil
		}}
}

// triggerWouldFire 与 pgrepo.DeleteAutopilot 的守卫 SQL 保持同一语义：
// 只挡会真正触发的 trigger（webhook / 带非空 cron 的 schedule）。
// 空 cron 的默认兜底 trigger 永不触发，不挡删除（FP-13 P0-3）。
// 教训（FP-13 审计 §六）：fake 与真库守卫语义漂移正是 P0 漏网的原因，改一处必须同步另一处。
func triggerWouldFire(t app.Trigger) bool {
	switch t.Kind {
	case domain.TriggerWebhook:
		return true
	case domain.TriggerSchedule:
		return t.CronExpression != ""
	default: // manual 从不自行触发
		return false
	}
}

func (f *autopilotRepoFake) GetAutopilot(_ context.Context, ws, id string) (app.Autopilot, bool, error) {
	a, ok := f.autopilots[id]
	if !ok || a.WorkspaceID != ws {
		return app.Autopilot{}, false, nil
	}
	return a, true, nil
}

func (f *autopilotRepoFake) ListAutopilots(_ context.Context, ws string, limit int) ([]app.Autopilot, error) {
	f.lastListLimit = limit
	var out []app.Autopilot
	for _, a := range f.autopilots {
		if a.WorkspaceID == ws && len(out) < limit {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *autopilotRepoFake) UpdateAutopilot(_ context.Context, ws, id string, name, execMode *string,
	template map[string]any, targetAgentID *string, enabled *bool) (app.Autopilot, bool, error) {
	a, ok := f.autopilots[id]
	if !ok || a.WorkspaceID != ws {
		return app.Autopilot{}, false, nil
	}
	if name != nil {
		a.Name = *name
	}
	if execMode != nil {
		a.ExecMode = *execMode
	}
	if template != nil {
		a.Template = template
	}
	if targetAgentID != nil {
		a.TargetAgentID = *targetAgentID
	}
	if enabled != nil {
		a.Enabled = *enabled
	}
	f.autopilots[id] = a
	return a, true, nil
}

func (f *autopilotRepoFake) DeleteAutopilot(_ context.Context, ws, id string) (app.DeleteOutcome, error) {
	a, ok := f.autopilots[id]
	if !ok || a.WorkspaceID != ws {
		return app.DeleteOutcome{}, nil
	}
	out := app.DeleteOutcome{Found: true}
	for _, t := range f.triggers {
		if t.AutopilotID == id && t.Enabled && triggerWouldFire(t) {
			out.BlockedTriggers = true
		}
	}
	for _, run := range f.runs {
		if run.AutopilotID == id && (run.Status == domain.RunPending || run.Status == domain.RunRunning) {
			out.BlockedRuns = true
		}
	}
	if out.BlockedTriggers || out.BlockedRuns {
		return out, nil
	}
	runs := f.runs[:0]
	for _, run := range f.runs {
		if run.AutopilotID != id {
			runs = append(runs, run)
		}
	}
	f.runs = runs
	for tid, t := range f.triggers {
		if t.AutopilotID == id {
			f.deletedTrig = append(f.deletedTrig, tid)
			delete(f.triggers, tid)
		}
	}
	if uids, ok := f.collaborators[id]; ok {
		f.deletedCollab = append(f.deletedCollab, uids...)
		delete(f.collaborators, id)
	}
	delete(f.autopilots, id)
	out.Deleted = true
	return out, nil
}

func TestAutopilotListEndpoint(t *testing.T) {
	fr := newAutopilotRepoFake()
	fr.autopilots["ap-1"] = app.Autopilot{ID: "ap-1", WorkspaceID: "ws-1", Name: "daily standup report", ExecMode: "create_work_item"}
	fr.autopilots["ap-2"] = app.Autopilot{ID: "ap-2", WorkspaceID: "ws-1", Name: "inspection", ExecMode: "task_only"}
	fr.autopilots["ap-x"] = app.Autopilot{ID: "ap-x", WorkspaceID: "ws-2", Name: "other region", ExecMode: "task_only"}
	r := mountedDeps(newAPDeps(fr))

	w := doJSON(r, http.MethodGet, "/api/autopilots", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	var list []app.Autopilot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list, 2, "only replies from the current workspace (others invisible)")
	require.Equal(t, 100, fr.lastListLimit, "no limit parameter defaults to 100")

	w = doJSON(r, http.MethodGet, "/api/autopilots?limit=1", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list, 1)
	require.Equal(t, 1, fr.lastListLimit)

	w = doJSON(r, http.MethodGet, "/api/autopilots", "ws-3", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "[]", strings.TrimSpace(w.Body.String()))
}

func TestAutopilotGetEndpoint(t *testing.T) {
	fr := newAutopilotRepoFake()
	fr.autopilots["ap-1"] = app.Autopilot{ID: "ap-1", WorkspaceID: "ws-1", Name: "inspection", ExecMode: "task_only"}
	r := mountedDeps(newAPDeps(fr))

	w := doJSON(r, http.MethodGet, "/api/autopilots/ap-1", "ws-1", "")
	require.Equal(t, http.StatusOK, w.Code)
	var got app.Autopilot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "ap-1", got.ID)
	require.Equal(t, "task_only", got.ExecMode)

	w = doJSON(r, http.MethodGet, "/api/autopilots/ap-none", "ws-1", "")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "E_NOT_FOUND")

	w = doJSON(r, http.MethodGet, "/api/autopilots/ap-1", "ws-2", "")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "E_NOT_FOUND")
}

func TestAutopilotPatchEndpoint(t *testing.T) {
	fr := newAutopilotRepoFake()
	fr.autopilots["ap-1"] = app.Autopilot{ID: "ap-1", WorkspaceID: "ws-1", Name: "old name", ExecMode: "create_work_item",
		Template: map[string]any{"title": "old title"}, TargetAgentID: "ag-1", Enabled: true, DailyLimit: 5}
	fr.triggers["trig-1"] = app.Trigger{ID: "trig-1", AutopilotID: "ap-1",
		Kind: domain.TriggerSchedule, CronExpression: "0 9 * * *", Enabled: true}
	r := mountedDeps(newAPDeps(fr))
	var got app.Autopilot

	w := doJSON(r, http.MethodPatch, "/api/autopilots/ap-1", "ws-1", `{"name":"new name"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "new name", got.Name)
	require.Equal(t, "create_work_item", got.ExecMode, "uncommitted fields must not change")
	require.Equal(t, "ag-1", got.TargetAgentID)
	require.True(t, got.Enabled)
	require.Equal(t, map[string]any{"title": "old title"}, got.Template)
	require.Equal(t, 5, got.DailyLimit)

	got = app.Autopilot{}
	w = doJSON(r, http.MethodPatch, "/api/autopilots/ap-1", "ws-1",
		`{"exec_mode":"task_only","template":{"work_item_id":"wi-9"},"target_agent_id":"","enabled":false}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "task_only", got.ExecMode)
	require.Equal(t, map[string]any{"work_item_id": "wi-9"}, got.Template)
	require.Equal(t, "", got.TargetAgentID)
	require.False(t, got.Enabled)
	require.Equal(t, "0 9 * * *", fr.triggers["trig-1"].CronExpression, "exec_mode switch must not touch triggers")
	require.True(t, fr.triggers["trig-1"].Enabled)

	got = app.Autopilot{}
	w = doJSON(r, http.MethodPatch, "/api/autopilots/ap-1", "ws-1", `{"template":{}}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Empty(t, got.Template)

	w = doJSON(r, http.MethodPatch, "/api/autopilots/ap-1", "ws-1", `{"exec_mode":"run_only"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "E_VALIDATION")

	w = doJSON(r, http.MethodPatch, "/api/autopilots/ap-1", "ws-1", `{"name":"   "}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "E_VALIDATION")

	w = doJSON(r, http.MethodPatch, "/api/autopilots/ap-1", "ws-1", `{bad`)
	require.Equal(t, http.StatusBadRequest, w.Code)

	w = doJSON(r, http.MethodPatch, "/api/autopilots/ap-none", "ws-1", `{"name":"x"}`)
	require.Equal(t, http.StatusNotFound, w.Code)
	w = doJSON(r, http.MethodPatch, "/api/autopilots/ap-1", "ws-2", `{"name":"x"}`)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestAutopilotDeleteEndpoint(t *testing.T) {
	fr := newAutopilotRepoFake()
	fr.autopilots["ap-1"] = app.Autopilot{ID: "ap-1", WorkspaceID: "ws-1", Name: "inspection"}
	fr.triggers["trig-1"] = app.Trigger{ID: "trig-1", AutopilotID: "ap-1", Kind: domain.TriggerWebhook, Enabled: true}
	fr.triggers["trig-2"] = app.Trigger{ID: "trig-2", AutopilotID: "ap-1", Kind: domain.TriggerSchedule, Enabled: false}
	// FP-13 P0-3 回归：创建必产的空 cron 兜底 trigger（enabled、永不触发）不挡删除。
	fr.triggers["trig-3"] = app.Trigger{ID: "trig-3", AutopilotID: "ap-1", Kind: domain.TriggerSchedule, Enabled: true}
	fr.collaborators["ap-1"] = []string{"user-2"}
	fr.runs = []app.Run{{ID: "run-1", AutopilotID: "ap-1", Status: domain.RunCompleted}}
	r := mountedDeps(newAPDeps(fr))

	w := doJSON(r, http.MethodDelete, "/api/autopilots/ap-1", "ws-1", "")
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "E_REVISION_CONFLICT")
	require.Contains(t, w.Body.String(), "enabled triggers")
	require.Contains(t, fr.autopilots, "ap-1", "  409 rejection must not delete the autopilot row")
	require.Contains(t, fr.triggers, "trig-1")

	disabled := fr.triggers["trig-1"]
	disabled.Enabled = false
	fr.triggers["trig-1"] = disabled
	fr.runs = append(fr.runs, app.Run{ID: "run-2", AutopilotID: "ap-1", Status: domain.RunPending})
	w = doJSON(r, http.MethodDelete, "/api/autopilots/ap-1", "ws-1", "")
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "pending/running")
	require.Contains(t, fr.autopilots, "ap-1")

	fr.runs = fr.runs[:1]
	w = doJSON(r, http.MethodDelete, "/api/autopilots/ap-1", "ws-1", "")
	require.Equal(t, http.StatusNoContent, w.Code,
		"only the inert empty-cron fallback trigger remains; delete must succeed")
	require.NotContains(t, fr.autopilots, "ap-1")
	require.NotContains(t, fr.triggers, "trig-1")
	require.NotContains(t, fr.triggers, "trig-2")
	require.NotContains(t, fr.collaborators, "ap-1")
	require.ElementsMatch(t, []string{"trig-1", "trig-2", "trig-3"}, fr.deletedTrig, "deletion order is not contractual (map iteration order)")
	require.Equal(t, []string{"user-2"}, fr.deletedCollab)

	w = doJSON(r, http.MethodDelete, "/api/autopilots/ap-none", "ws-1", "")
	require.Equal(t, http.StatusNotFound, w.Code)
	fr.autopilots["ap-2"] = app.Autopilot{ID: "ap-2", WorkspaceID: "ws-2", Name: "other region"}
	w = doJSON(r, http.MethodDelete, "/api/autopilots/ap-2", "ws-1", "")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, fr.autopilots, "ap-2", "cannot delete in another workspace")
}
