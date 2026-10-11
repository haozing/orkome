package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fwapp "github.com/haozing/ploykit/identity/app"
	pkgmigrations "github.com/haozing/ploykit/migrations"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"github.com/haozing/ploykit/platform/webx"

	"orkome/internal/identity/adapters/pgrepo"
	"orkome/internal/identity/app"
	"orkome/migrations"
)

// ---- stubs for the framework auth surface ----

type stubSessions struct {
	mu     sync.Mutex
	tokens map[string]*webx.Principal
}

func (s *stubSessions) CreateSession(context.Context, webx.SessionCreate) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func (s *stubSessions) VerifySession(_ context.Context, token string, _ time.Time) (*webx.Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.tokens[token]; ok {
		return p, nil
	}
	return nil, nil
}

func (s *stubSessions) RenewSession(context.Context, string, time.Time) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (s *stubSessions) RevokeSession(context.Context, string) error { return nil }

type stubPATs struct{}

func (stubPATs) ResolvePAT(context.Context, string, time.Time) (*webx.Principal, error) {
	return nil, nil
}

type minterCall struct {
	UserID string
	Name   string
	TTL    time.Duration
}

type stubMinter struct {
	mu    sync.Mutex
	calls []minterCall
}

func (m *stubMinter) CreatePAT(_ context.Context, p *webx.Principal, name string, ttl time.Duration, _ *webx.CredentialScope) (fwapp.PAT, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, minterCall{UserID: p.UserID, Name: name, TTL: ttl})
	return fwapp.PAT{ID: "pat-1", Name: name, Prefix: "tk_abcde"}, "tk_plainsecret", nil
}

// ---- harness ----

type harness struct {
	t        *testing.T
	pool     *pgxpool.Pool
	repo     *pgrepo.Repo
	sessions *stubSessions
	minter   *stubMinter
	mux      *http.ServeMux
	handler  http.Handler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping integration test")
	}
	dsn := testDB(t, base, "orkome_ext")
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(ctx, 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `TRUNCATE pinned_item, ui_token, member, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	repo := pgrepo.New(pg.WrapPool(pool))
	sessions := &stubSessions{tokens: map[string]*webx.Principal{}}
	minter := &stubMinter{}

	deps := Deps{
		Repo:     repo,
		Pins:     app.NewPinService(repo),
		UITokens: app.NewUITokenService(repo, []byte("integration-ui-secret-0123456789"), time.Now),
		Tokens:   minter,
	}
	mux := http.NewServeMux()
	MountExtensions(mux, func(next http.Handler) http.Handler { return next }, deps)
	authCfg := &webx.AuthConfig{CookieName: "tk_test", SessionTTL: 720 * time.Hour}
	return &harness{
		t: t, pool: pool, repo: repo, sessions: sessions, minter: minter,
		mux: mux, handler: webx.Authenticate(authCfg, sessions, stubPATs{})(mux),
	}
}

// client registers a session cookie for the named client bound to userID.
func (h *harness) client(name, userID string) string {
	h.t.Helper()
	token := "sess-" + name + "-" + userID
	h.sessions.mu.Lock()
	h.sessions.tokens[token] = &webx.Principal{UserID: userID, Email: name + "@example.com", Source: webx.SourceSession}
	h.sessions.mu.Unlock()
	return token
}

func (h *harness) seedUser(email string) string {
	h.t.Helper()
	var id string
	require.NoError(h.t, h.pool.QueryRow(context.Background(),
		`INSERT INTO "user" (email, display_name) VALUES ($1, $2) RETURNING id`,
		email, "User "+email).Scan(&id))
	return id
}

func (h *harness) seedWorkspace(name, slug, ownerID string) string {
	h.t.Helper()
	var id string
	require.NoError(h.t, h.pool.QueryRow(context.Background(),
		`INSERT INTO workspace (slug, name, created_by) VALUES ($1, $2, $3) RETURNING id`,
		slug, name, ownerID).Scan(&id))
	return id
}

func (h *harness) seedMember(wsID, userID, role string) {
	h.t.Helper()
	_, err := h.pool.Exec(context.Background(),
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3)`, wsID, userID, role)
	require.NoError(h.t, err)
}

func (h *harness) do(client, method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(h.t, err)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "192.0.2.1:5555"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if client != "" {
		req.AddCookie(&http.Cookie{Name: "tk_test", Value: client})
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func testDB(t *testing.T, base, name string) string {
	t.Helper()
	admin, err := pgx.Connect(context.Background(), base)
	if err != nil {
		t.Skipf("cannot connect %s: %v", base, err)
	}
	defer admin.Close(context.Background())

	_, _ = admin.Exec(context.Background(), "CREATE DATABASE "+name)

	cfg, err := pgx.ParseConfig(base)
	require.NoError(t, err)
	return strings.Replace(base, "/"+cfg.Database, "/"+name, 1)
}

func jsonOf(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), v), "body: %s", rec.Body.String())
}

type pinRow struct {
	ID       string `json:"id"`
	ItemType string `json:"item_type"`
	ItemID   string `json:"item_id"`
	Position int    `json:"position"`
}

func TestPins_Lifecycle(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("pin-alice@example.com")
	ws := h.seedWorkspace("PinWs", "pin-ws", alice)
	h.seedMember(ws, alice, "owner")
	ck := h.client("alice", alice)
	hdr := map[string]string{"X-Workspace-Id": ws}

	wi1 := "11111111-1111-1111-1111-111111111111"
	wi2 := "22222222-2222-2222-2222-222222222222"
	cs1 := "33333333-3333-3333-3333-333333333333"

	rec := h.do(ck, http.MethodGet, "/api/pins", nil, hdr)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var pins []pinRow
	jsonOf(t, rec, &pins)
	assert.Empty(t, pins)

	for i, it := range []struct{ typ, id string }{
		{"work_item", wi1}, {"work_item", wi2}, {"chat_session", cs1},
	} {
		rec = h.do(ck, http.MethodPut, "/api/pins",
			map[string]string{"item_type": it.typ, "item_id": it.id}, hdr)
		require.Equal(t, 201, rec.Code, "add#%d: %s", i, rec.Body.String())
		var pin pinRow
		jsonOf(t, rec, &pin)
		assert.Equal(t, i+1, pin.Position, "position = the user's current MAX+1")
	}

	rec = h.do(ck, http.MethodPut, "/api/pins",
		map[string]string{"item_type": "work_item", "item_id": wi1}, hdr)
	require.Equal(t, 200, rec.Code, "repeated pin idempotently returns: "+rec.Body.String())
	rec = h.do(ck, http.MethodGet, "/api/pins", nil, hdr)
	jsonOf(t, rec, &pins)
	require.Len(t, pins, 3)

	ids := make([]string, 0, len(pins))
	for i := len(pins) - 1; i >= 0; i-- {
		ids = append(ids, pins[i].ID)
	}
	rec = h.do(ck, http.MethodPut, "/api/pins/reorder", map[string]any{"order": ids}, hdr)
	require.Equal(t, 204, rec.Code, "reorder: "+rec.Body.String())
	rec = h.do(ck, http.MethodGet, "/api/pins", nil, hdr)
	jsonOf(t, rec, &pins)
	got := make([]string, 0, len(pins))
	for _, p := range pins {
		got = append(got, p.ID)
	}
	assert.Equal(t, ids, got, "after reordering, ascending by new position")

	rec = h.do(ck, http.MethodPut, "/api/pins/reorder", map[string]any{"order": ids[:2]}, hdr)
	assert.Equal(t, 400, rec.Code, "missing id should be 400")
	rec = h.do(ck, http.MethodPut, "/api/pins/reorder",
		map[string]any{"order": []string{"44444444-4444-4444-4444-444444444444", ids[1], ids[2]}}, hdr)
	assert.Equal(t, 400, rec.Code, "unknown id should be 400")
	rec = h.do(ck, http.MethodPut, "/api/pins/reorder",
		map[string]any{"order": []string{ids[0], ids[0], ids[1]}}, hdr)
	assert.Equal(t, 400, rec.Code, "duplicate id should be 400")

	rec = h.do(ck, http.MethodDelete, "/api/pins/work_item/"+wi1, nil, hdr)
	require.Equal(t, 204, rec.Code)
	rec = h.do(ck, http.MethodDelete, "/api/pins/work_item/"+wi1, nil, hdr)
	require.Equal(t, 204, rec.Code, "detach idempotent")
	rec = h.do(ck, http.MethodGet, "/api/pins", nil, hdr)
	jsonOf(t, rec, &pins)
	assert.Len(t, pins, 2)
}

func TestPins_ValidationAndVisibility(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("pv-alice@example.com")
	bob := h.seedUser("pv-bob@example.com")
	ws := h.seedWorkspace("PvWs", "pv-ws", alice)
	h.seedMember(ws, alice, "owner")
	aliceCk := h.client("alice", alice)
	hdr := map[string]string{"X-Workspace-Id": ws}

	rec := h.do(aliceCk, http.MethodGet, "/api/pins", nil, nil)
	assert.Equal(t, 400, rec.Code, "missing X-Workspace-Id should be 400")

	rec = h.do("", http.MethodGet, "/api/pins", nil, hdr)
	assert.Equal(t, 401, rec.Code, "anonymous should be 401")

	bobCk := h.client("bob", bob)
	rec = h.do(bobCk, http.MethodGet, "/api/pins", nil, hdr)
	assert.Equal(t, 404, rec.Code, "non-members cannot see")
	rec = h.do(bobCk, http.MethodPut, "/api/pins",
		map[string]string{"item_type": "work_item", "item_id": "11111111-1111-1111-1111-111111111111"}, hdr)
	assert.Equal(t, 404, rec.Code, "non-members cannot write")
	rec = h.do(aliceCk, http.MethodGet, "/api/pins", nil,
		map[string]string{"X-Workspace-Id": "55555555-5555-5555-5555-555555555555"})
	assert.Equal(t, 404, rec.Code, "nonexistent workspace same-code 404")

	rec = h.do(aliceCk, http.MethodPut, "/api/pins",
		map[string]string{"item_type": "agent", "item_id": "11111111-1111-1111-1111-111111111111"}, hdr)
	assert.Equal(t, 400, rec.Code, "invalid item_type should be 400")
	rec = h.do(aliceCk, http.MethodPut, "/api/pins",
		map[string]string{"item_type": "work_item", "item_id": "not-a-uuid"}, hdr)
	assert.Equal(t, 400, rec.Code, "invalid item_id should be 400")
	rec = h.do(aliceCk, http.MethodDelete, "/api/pins/agent/11111111-1111-1111-1111-111111111111", nil, hdr)
	assert.Equal(t, 400, rec.Code, "DELETE invalid item_type should be 400")

	h.seedMember(ws, bob, "member")
	rec = h.do(aliceCk, http.MethodPut, "/api/pins",
		map[string]string{"item_type": "work_item", "item_id": "11111111-1111-1111-1111-111111111111"}, hdr)
	require.Equal(t, 201, rec.Code)
	rec = h.do(bobCk, http.MethodGet, "/api/pins", nil, hdr)
	require.Equal(t, 200, rec.Code)
	var bobPins []pinRow
	jsonOf(t, rec, &bobPins)
	assert.Empty(t, bobPins, "pins isolated by (workspace,user)")
}

func TestUITokens_MintConsumeLifecycle(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("uit-alice@example.com")
	bob := h.seedUser("uit-bob@example.com")
	ws := h.seedWorkspace("UitWs", "uit-ws", alice)
	h.seedMember(ws, alice, "owner")
	aliceCk := h.client("alice", alice)
	bobCk := h.client("bob", bob)

	rec := h.do(bobCk, http.MethodPost, "/api/ui-tokens",
		map[string]string{"plugin": "fakeblog", "workspace_id": ws}, nil)
	assert.Equal(t, 403, rec.Code, "non-member mint should be 403")

	rec = h.do(aliceCk, http.MethodPost, "/api/ui-tokens",
		map[string]string{"plugin": "", "workspace_id": ws}, nil)
	assert.Equal(t, 400, rec.Code, "missing plugin should be 400")

	rec = h.do(aliceCk, http.MethodPost, "/api/ui-tokens",
		map[string]string{"plugin": "fakeblog", "workspace_id": ws}, nil)
	require.Equal(t, 201, rec.Code, "mint: "+rec.Body.String())
	var minted struct {
		Token     string `json:"token"`
		ExpiresIn int    `json:"expires_in"`
	}
	jsonOf(t, rec, &minted)
	assert.NotEmpty(t, minted.Token)
	assert.Equal(t, 60, minted.ExpiresIn)

	rec = h.do(aliceCk, http.MethodPost, "/api/ui-tokens/consume",
		map[string]string{"token": minted.Token}, nil)
	require.Equal(t, 200, rec.Code, "consume: "+rec.Body.String())
	var claims struct {
		UserID      string `json:"user_id"`
		WorkspaceID string `json:"workspace_id"`
		Plugin      string `json:"plugin"`
	}
	jsonOf(t, rec, &claims)
	assert.Equal(t, alice, claims.UserID)
	assert.Equal(t, ws, claims.WorkspaceID)
	assert.Equal(t, "fakeblog", claims.Plugin)

	rec = h.do(aliceCk, http.MethodPost, "/api/ui-tokens/consume",
		map[string]string{"token": minted.Token}, nil)
	assert.Equal(t, 401, rec.Code, "replay should be rejected (single-use jti)")

	rec = h.do("", http.MethodPost, "/api/ui-tokens/consume",
		map[string]string{"token": minted.Token}, nil)
	assert.Equal(t, 401, rec.Code, "anonymous consume should be 401")
}

func TestCLIToken_ExchangesSessionForPAT(t *testing.T) {
	h := newHarness(t)
	alice := h.seedUser("cli-alice@example.com")
	aliceCk := h.client("alice", alice)

	rec := h.do("", http.MethodPost, "/cli-token", map[string]string{"name": "ci"}, nil)
	assert.Equal(t, 401, rec.Code, "anonymous cli-token should be 401")

	rec = h.do(aliceCk, http.MethodPost, "/cli-token",
		map[string]any{"name": "ci", "ttl_seconds": 3600}, nil)
	require.Equal(t, 201, rec.Code, "exchange: "+rec.Body.String())
	var out struct {
		Token  string `json:"token"`
		ID     string `json:"id"`
		Name   string `json:"name"`
		Prefix string `json:"prefix"`
	}
	jsonOf(t, rec, &out)
	assert.Equal(t, "tk_plainsecret", out.Token)
	assert.Equal(t, "pat-1", out.ID)
	assert.Equal(t, "ci", out.Name)
	assert.Equal(t, "tk_abcde", out.Prefix)

	require.Len(t, h.minter.calls, 1)
	assert.Equal(t, alice, h.minter.calls[0].UserID, "the PAT is minted for the session principal")
	assert.Equal(t, "ci", h.minter.calls[0].Name)
	assert.Equal(t, time.Hour, h.minter.calls[0].TTL)

	// empty body falls back to name "cli", no expiry
	rec = h.do(aliceCk, http.MethodPost, "/cli-token", nil, nil)
	require.Equal(t, 201, rec.Code, "defaults: "+rec.Body.String())
	require.Len(t, h.minter.calls, 2)
	assert.Equal(t, "cli", h.minter.calls[1].Name)
	assert.Zero(t, h.minter.calls[1].TTL)
}

func TestMountExtensions_RouteAccounting(t *testing.T) {
	h := newHarness(t)
	// stdlib ServeMux cannot enumerate patterns; resolve each expected route
	// with a dummy request instead — Handler() only matches, never invokes.
	expected := []string{
		"GET /api/pins", "PUT /api/pins", "PUT /api/pins/reorder",
		"DELETE /api/pins/{itemType}/{itemId}",
		"POST /api/ui-tokens", "POST /api/ui-tokens/consume",
		"POST /cli-token",
	}
	for _, e := range expected {
		method, path, _ := strings.Cut(e, " ")
		req := httptest.NewRequest(method, fillRouteParams(path), nil)
		_, got := h.mux.Handler(req)
		assert.Equal(t, e, got, "product route not mounted: %s", e)
	}
}

func fillRouteParams(path string) string {
	return strings.ReplaceAll(strings.ReplaceAll(path, "{itemType}", "work_item"),
		"{itemId}", "00000000-0000-0000-0000-000000000000")
}
