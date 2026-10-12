package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/identity/app"
)

// consume 反转（FP-15 P1-1）的 HTTP 级锚点，免 DB：MountExtensions 挂真实路由，
// consume 侧零鉴权壳——匿名请求直达 handler，票即凭证。
type consumeRouteHarness struct {
	t    *testing.T
	repo *uitFakeTokenRepo
	svc  *app.UITokenService
	mux  *http.ServeMux
}

type uitFakeTokenRepo struct {
	store map[string]uitHTTPRow
}

type uitHTTPRow struct {
	ws, user, plugin string
	expiresAt        time.Time
	consumed         bool
}

func (f *uitFakeTokenRepo) InsertUIToken(_ context.Context, jti, ws, user, plugin string, expiresAt time.Time) error {
	f.store[jti] = uitHTTPRow{ws: ws, user: user, plugin: plugin, expiresAt: expiresAt}
	return nil
}

func (f *uitFakeTokenRepo) ConsumeUIToken(_ context.Context, jti string, now time.Time) (string, string, string, bool, error) {
	row, found := f.store[jti]
	if !found || row.consumed || !row.expiresAt.After(now) {
		return "", "", "", false, nil
	}
	row.consumed = true
	f.store[jti] = row
	return row.ws, row.user, row.plugin, true, nil
}

// uitNoRepo 仅满足 Deps.Repo 接口（consume/匿名 mint 路径不触 pins 仓储）。
type uitNoRepo struct{ app.PinRepo }

func newConsumeRouteHarness(t *testing.T) *consumeRouteHarness {
	t.Helper()
	repo := &uitFakeTokenRepo{store: map[string]uitHTTPRow{}}
	svc := uitService(repo, func() time.Time { return uitNow })
	h := &consumeRouteHarness{t: t, repo: repo, svc: svc}
	h.mux = uitMount(svc)
	return h
}

// uitNow 固定夹具时钟：取真实时间截到分钟——固定历史日期会让 JWT exp
// （now+60s）在第二天被真实校验路径判过期（时间炸弹），相对时钟永不过期。
var uitNow = time.Now().UTC().Truncate(time.Minute)

func uitService(repo *uitFakeTokenRepo, now func() time.Time) *app.UITokenService {
	return app.NewUITokenService(repo, []byte("uit-route-secret-0123456789"), now)
}

func uitMount(svc *app.UITokenService) *http.ServeMux {
	mux := http.NewServeMux()
	MountExtensions(mux, func(next http.Handler) http.Handler { return next },
		Deps{Repo: uitNoRepo{}, UITokens: svc})
	return mux
}

func (h *consumeRouteHarness) post(target, token string) *httptest.ResponseRecorder {
	h.t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	require.NoError(h.t, err)
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

// TestUITokenConsume_AnonymousFourStates 四态反转：匿名 + 有效票 = 200；
// 二次使用 / 过期 / 伪造票拒绝；铸票仍要会话。
func TestUITokenConsume_AnonymousFourStates(t *testing.T) {
	h := newConsumeRouteHarness(t)
	ctx := context.Background()

	// 前置：匿名 mint 必须 401——会话门只从 consume 摘除，铸票不放开。
	req := httptest.NewRequest(http.MethodPost, "/api/ui-tokens",
		bytes.NewReader([]byte(`{"plugin":"fakeblog","workspace_id":"ws-1"}`)))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "anonymous mint stays 401")

	// ① 匿名 + 有效票 = 200（此前被 CSRF/RequireAuth 挡死——本断言即反转锚）。
	tok, err := h.svc.Mint(ctx, "ws-1", "u-1", "fakeblog", "")
	require.NoError(t, err)
	rec = h.post("/api/ui-tokens/consume", tok)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		UserID      string `json:"user_id"`
		WorkspaceID string `json:"workspace_id"`
		Plugin      string `json:"plugin"`
		WorkItemID  string `json:"work_item_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "u-1", out.UserID)
	assert.Equal(t, "ws-1", out.WorkspaceID)
	assert.Equal(t, "fakeblog", out.Plugin)
	assert.Empty(t, out.WorkItemID, "未绑定票不透出 work_item_id")

	// ② 票二次使用 = 401（jti 单次，与会话无关）。
	rec = h.post("/api/ui-tokens/consume", tok)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// ③ 过期票 = 401（JWT exp 与行级 expires_at 双门槛，用推进时钟的服务走真路由）。
	tok2, err := h.svc.Mint(ctx, "ws-1", "u-1", "fakeblog", "")
	require.NoError(t, err)
	expired := uitMount(uitService(h.repo, func() time.Time { return uitNow.Add(app.UITokenTTL + time.Second) }))
	rec2 := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"token": tok2})
	expired.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/api/ui-tokens/consume", bytes.NewReader(body)))
	assert.Equal(t, http.StatusUnauthorized, rec2.Code, "expired ticket must be rejected")

	// ④ 无票 = 401（伪造/未知票验签即拒）；空体 = 400（形状错误，非鉴权语义）。
	rec = h.post("/api/ui-tokens/consume", "garbage-token")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	rec2 = httptest.NewRecorder()
	h.mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/api/ui-tokens/consume",
		bytes.NewReader([]byte(`{}`))))
	assert.Equal(t, http.StatusBadRequest, rec2.Code)
}

// TestUITokenConsume_BoundWorkItemExposed 绑定票的 consume 响应透出
// work_item_id——pack 侧拿它与 URL 参数比对（FP-15 P1-2 的消费原语）。
func TestUITokenConsume_BoundWorkItemExposed(t *testing.T) {
	repo := &uitFakeTokenRepo{store: map[string]uitHTTPRow{}}
	svc := uitService(repo, func() time.Time { return uitNow }).
		WithWorkItemLocator(app.WorkItemLocatorFunc(
			func(_ context.Context, ws, wi string) (bool, error) {
				return ws == "ws-1" && wi == "11111111-1111-1111-1111-111111111111", nil
			}))
	h := &consumeRouteHarness{t: t, repo: repo, svc: svc, mux: uitMount(svc)}
	ctx := context.Background()

	tok, err := svc.Mint(ctx, "ws-1", "u-1", "fakeblog", "11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	rec := h.post("/api/ui-tokens/consume", tok)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		WorkItemID string `json:"work_item_id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", out.WorkItemID)
}
