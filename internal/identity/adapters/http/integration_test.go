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

	"github.com/go-chi/chi/v5"

	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/internal/identity/adapters/pgrepo"
	"orkome/internal/identity/app"
	"orkome/internal/platform/webx"
	"orkome/migrations"
)

type captureSender struct {
	mu      sync.Mutex
	codes   map[string]string
	invited []string
}

func newCapture() *captureSender { return &captureSender{codes: map[string]string{}} }

func (c *captureSender) SendVerificationCode(_ context.Context, to, code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codes[to] = code
	return nil
}

func (c *captureSender) SendInvitation(_ context.Context, to, _, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invited = append(c.invited, to)
	return nil
}

func (c *captureSender) code(t *testing.T, email string) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	code, ok := c.codes[email]
	require.True(t, ok, "verification code not captured for %s", email)
	return code
}

type countingLimiter struct {
	mu    sync.Mutex
	deny  map[string]int
	count map[string]int
}

func newLimiter() *countingLimiter {
	return &countingLimiter{deny: map[string]int{}, count: map[string]int{}}
}

func (c *countingLimiter) Allow(_ context.Context, key string, _ int) (bool, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count[key]++
	if d, ok := c.deny[key]; ok && c.count[key] >= d {
		return false, 30 * time.Second
	}
	return true, 0
}

type harness struct {
	t       *testing.T
	handler http.Handler
	auth    *webx.AuthConfig
	mail    *captureSender
	limiter *countingLimiter
	repo    *pgrepo.Repo
	jar     map[string][]*http.Cookie
	csrf    map[string]string
}

func newHarness(t *testing.T) *harness {
	return newHarnessCfg(t, app.SessionConfig{AllowSignup: true})
}

func newHarnessCfg(t *testing.T, cfg app.SessionConfig) *harness {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping integration test")
	}
	dsn := testDB(t, base, "orkome_m1")
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `TRUNCATE pinned_item, personal_access_token, workspace_share_link,
		workspace_invitation, verification_code, member, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	repo := &pgrepo.Repo{DB: pg.WrapPool(pool)}

	auth := &webx.AuthConfig{Secret: []byte("integration-secret-32-bytes-xxxx"), SessionTTL: 720 * time.Hour}
	mail := newCapture()
	limiter := newLimiter()

	sessions := app.NewSessionService(repo, mail, auth, cfg, time.Now)
	tokens := app.NewTokenService(repo, app.NoopCache{}, time.Now)
	workspaces := app.NewWorkspaceService(repo, mail, tokens, time.Now)
	pins := app.NewPinService(repo)

	deps := Deps{
		Sessions: sessions, Tokens: tokens, Workspaces: workspaces, Pins: pins, Repo: repo,
		Auth: auth, CSRFKey: webx.DeriveCSRFKey(auth.Secret), Limiter: limiter,
		SendCodePerMinute: 100, Version: "test",
	}
	return &harness{
		t: t, handler: Routes(deps, false), auth: auth, mail: mail, limiter: limiter,
		repo: repo, jar: map[string][]*http.Cookie{}, csrf: map[string]string{},
	}
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

	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		if req.Header.Get("Origin") == "" {
			req.Header.Set("Origin", "http://"+req.Host)
		}
	}
	for _, c := range h.jar[client] {
		req.AddCookie(c)
	}
	if h.csrf[client] != "" {
		req.Header.Set("X-CSRF-Token", h.csrf[client])
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
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

func (h *harness) login(client, email string) {
	h.t.Helper()

	rec := h.do(client, http.MethodGet, "/config", nil, nil)
	require.Equal(h.t, 200, rec.Code, rec.Body.String())
	var cfgOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	require.NoError(h.t, json.Unmarshal(rec.Body.Bytes(), &cfgOut))
	h.csrf[client] = cfgOut.CSRFToken

	rec = h.do(client, http.MethodPost, "/auth/send-code", map[string]string{"email": email}, nil)
	require.Equal(h.t, 200, rec.Code, "send-code: "+rec.Body.String())

	code := h.mail.code(h.t, email)
	rec = h.do(client, http.MethodPost, "/auth/verify-code", map[string]string{"email": email, "code": code}, nil)
	require.Equal(h.t, 200, rec.Code, "verify-code: "+rec.Body.String())
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	require.NoError(h.t, json.Unmarshal(rec.Body.Bytes(), &out))
	h.csrf[client] = out.CSRFToken
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

func TestM1_GoldenPath_BrowserAndCLI(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	h.login("alice", "alice@example.com")

	rec := h.do("alice", http.MethodGet, "/api/me", nil, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var me struct {
		User struct{ Email string } `json:"user"`
	}
	jsonOf(t, rec, &me)
	assert.Equal(t, "alice@example.com", me.User.Email)

	rec = h.do("alice", http.MethodPatch, "/api/me", map[string]string{"display_name": "Alice A"}, nil)
	require.Equal(t, 200, rec.Code)

	rec = h.do("alice", http.MethodPost, "/api/workspaces", map[string]string{"name": "Acme", "slug": "acme-ws"}, nil)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var ws struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	jsonOf(t, rec, &ws)
	require.NotEmpty(t, ws.ID)

	rec = h.do("alice", http.MethodPost, "/api/workspaces/"+ws.ID+"/members",
		map[string]string{"email": "bob@example.com", "role": "member"}, nil)
	require.Equal(t, 201, rec.Code, "invite: "+rec.Body.String())
	var inv struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &inv)
	require.NotEmpty(t, inv.ID)
	assert.Contains(t, h.mail.invited, "bob@example.com", "invitation emails should be sent out")

	h.login("bob", "bob@example.com")
	rec = h.do("bob", http.MethodGet, "/api/invitations", nil, nil)
	require.Equal(t, 200, rec.Code)
	rec = h.do("bob", http.MethodPost, "/api/invitations/"+inv.ID+"/accept", nil, nil)
	require.Equal(t, 200, rec.Code, "accept: "+rec.Body.String())

	rec = h.do("alice", http.MethodGet, "/api/workspaces/"+ws.ID+"/members", nil, nil)
	require.Equal(t, 200, rec.Code)
	var members []map[string]any
	jsonOf(t, rec, &members)
	assert.Len(t, members, 2)

	rec = h.do("bob", http.MethodPost, "/cli-token", map[string]string{"name": "ci"}, nil)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var cliTok struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	jsonOf(t, rec, &cliTok)
	require.True(t, len(cliTok.Token) > 40, "PAT plaintext should be returned")

	bearer := map[string]string{"Authorization": "Bearer " + cliTok.Token}
	rec = h.do("bob-cli", http.MethodGet, "/api/me", nil, bearer)
	require.Equal(t, 200, rec.Code, "PAT calls /me: "+rec.Body.String())

	rec = h.do("bob-cli", http.MethodPost, "/api/tokens", map[string]string{"name": "another"}, bearer)
	require.Equal(t, 201, rec.Code, "PAT CSRF-exempt write operations: "+rec.Body.String())

	rec = h.do("bob-cli", http.MethodPost, "/api/tokens/current/renew", map[string]string{"name": "rotated"}, bearer)
	require.Equal(t, 200, rec.Code, "renew: "+rec.Body.String())
	var renewed struct {
		Token string `json:"token"`
	}
	jsonOf(t, rec, &renewed)
	rec = h.do("bob-cli2", http.MethodGet, "/api/me", nil, map[string]string{"Authorization": "Bearer " + cliTok.Token})
	assert.Equal(t, 401, rec.Code, "old tokens invalidated immediately after rotation")
	rec = h.do("bob-cli3", http.MethodGet, "/api/me", nil, map[string]string{"Authorization": "Bearer " + renewed.Token})
	assert.Equal(t, 200, rec.Code, "the new token should be usable")

	rec = h.do("alice", http.MethodGet, "/api/workspaces/"+ws.ID+"/members", nil, nil)
	var ms []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	jsonOf(t, rec, &ms)
	var aliceMemberID string
	for _, m := range ms {
		if m.Role == "owner" {
			aliceMemberID = m.ID
		}
	}
	rec = h.do("alice", http.MethodDelete, "/api/workspaces/"+ws.ID+"/members/"+aliceMemberID, nil, nil)
	assert.Equal(t, 400, rec.Code, "removing the sole owner must be rejected")
	rec = h.do("alice", http.MethodPost, "/api/workspaces/"+ws.ID+"/leave", nil, nil)
	assert.Equal(t, 400, rec.Code, "the sole owner's leave must be rejected")

	var bobMemberID string
	for _, m := range ms {
		if m.Role == "member" {
			bobMemberID = m.ID
		}
	}
	rec = h.do("alice", http.MethodDelete, "/api/workspaces/"+ws.ID+"/members/"+bobMemberID, nil, nil)
	require.Equal(t, 204, rec.Code, "remove bob: "+rec.Body.String())
	rec = h.do("bob-cli4", http.MethodGet, "/api/me", nil, map[string]string{"Authorization": "Bearer " + renewed.Token})
	assert.Equal(t, 401, rec.Code, "member removal should revoke all PATs (M1 face)")

	rec = h.do("alice", http.MethodPost, "/api/workspaces/"+ws.ID+"/share-links",
		map[string]any{"role": "member", "max_uses": 1, "ttl_hours": 24}, nil)
	require.Equal(t, 201, rec.Code)
	var sl struct {
		Code string `json:"code"`
	}
	jsonOf(t, rec, &sl)

	h.login("carol", "carol@example.com")
	rec = h.do("carol", http.MethodPost, "/api/share-links/join", map[string]string{"code": sl.Code}, nil)
	require.Equal(t, 200, rec.Code, "join: "+rec.Body.String())

	h.login("dave", "dave@example.com")
	rec = h.do("dave", http.MethodPost, "/api/share-links/join", map[string]string{"code": sl.Code}, nil)
	assert.Equal(t, 404, rec.Code, "max_uses=1 unusable after exhaustion")

	rec = h.do("alice", http.MethodPost, "/auth/logout", nil, nil)
	require.Equal(t, 204, rec.Code)
	_ = ctx
}

type pinRow struct {
	ID       string `json:"id"`
	ItemType string `json:"item_type"`
	ItemID   string `json:"item_id"`
	Position int    `json:"position"`
}

func TestPins_Lifecycle(t *testing.T) {
	h := newHarness(t)
	h.login("alice", "pin-alice@example.com")
	rec := h.do("alice", http.MethodPost, "/api/workspaces",
		map[string]string{"name": "PinWs", "slug": "pin-ws"}, nil)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var ws struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &ws)
	hdr := map[string]string{"X-Workspace-Id": ws.ID}

	wi1 := "11111111-1111-1111-1111-111111111111"
	wi2 := "22222222-2222-2222-2222-222222222222"
	cs1 := "33333333-3333-3333-3333-333333333333"

	rec = h.do("alice", http.MethodGet, "/api/pins", nil, hdr)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var pins []pinRow
	jsonOf(t, rec, &pins)
	assert.Empty(t, pins)

	for i, it := range []struct{ typ, id string }{
		{"work_item", wi1}, {"work_item", wi2}, {"chat_session", cs1},
	} {
		rec = h.do("alice", http.MethodPut, "/api/pins",
			map[string]string{"item_type": it.typ, "item_id": it.id}, hdr)
		require.Equal(t, 201, rec.Code, "add#%d: %s", i, rec.Body.String())
		var pin pinRow
		jsonOf(t, rec, &pin)
		assert.Equal(t, i+1, pin.Position, "position = the user's current MAX+1")
	}

	rec = h.do("alice", http.MethodPut, "/api/pins",
		map[string]string{"item_type": "work_item", "item_id": wi1}, hdr)
	require.Equal(t, 200, rec.Code, "repeated pin idempotently returns: "+rec.Body.String())
	rec = h.do("alice", http.MethodGet, "/api/pins", nil, hdr)
	jsonOf(t, rec, &pins)
	require.Len(t, pins, 3)

	ids := make([]string, 0, len(pins))
	for i := len(pins) - 1; i >= 0; i-- {
		ids = append(ids, pins[i].ID)
	}
	rec = h.do("alice", http.MethodPut, "/api/pins/reorder", map[string]any{"order": ids}, hdr)
	require.Equal(t, 204, rec.Code, "reorder: "+rec.Body.String())
	rec = h.do("alice", http.MethodGet, "/api/pins", nil, hdr)
	jsonOf(t, rec, &pins)
	got := make([]string, 0, len(pins))
	for _, p := range pins {
		got = append(got, p.ID)
	}
	assert.Equal(t, ids, got, "after reordering, ascending by new position")

	rec = h.do("alice", http.MethodPut, "/api/pins/reorder", map[string]any{"order": ids[:2]}, hdr)
	assert.Equal(t, 400, rec.Code, "missing id should be 400")
	rec = h.do("alice", http.MethodPut, "/api/pins/reorder",
		map[string]any{"order": []string{"44444444-4444-4444-4444-444444444444", ids[1], ids[2]}}, hdr)
	assert.Equal(t, 400, rec.Code, "unknown id should be 400")
	rec = h.do("alice", http.MethodPut, "/api/pins/reorder",
		map[string]any{"order": []string{ids[0], ids[0], ids[1]}}, hdr)
	assert.Equal(t, 400, rec.Code, "duplicate id should be 400")

	rec = h.do("alice", http.MethodDelete, "/api/pins/work_item/"+wi1, nil, hdr)
	require.Equal(t, 204, rec.Code)
	rec = h.do("alice", http.MethodDelete, "/api/pins/work_item/"+wi1, nil, hdr)
	require.Equal(t, 204, rec.Code, "detach idempotent")
	rec = h.do("alice", http.MethodGet, "/api/pins", nil, hdr)
	jsonOf(t, rec, &pins)
	assert.Len(t, pins, 2)
}

func TestPins_ValidationAndVisibility(t *testing.T) {
	h := newHarness(t)
	h.login("alice", "pv-alice@example.com")
	rec := h.do("alice", http.MethodPost, "/api/workspaces",
		map[string]string{"name": "PvWs", "slug": "pv-ws"}, nil)
	require.Equal(t, 201, rec.Code)
	var ws struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &ws)
	hdr := map[string]string{"X-Workspace-Id": ws.ID}

	rec = h.do("alice", http.MethodGet, "/api/pins", nil, nil)
	assert.Equal(t, 400, rec.Code, "missing X-Workspace-Id should be 400")

	rec = h.do("anon", http.MethodGet, "/api/pins", nil, hdr)
	assert.Equal(t, 401, rec.Code)

	h.login("bob", "pv-bob@example.com")
	rec = h.do("bob", http.MethodGet, "/api/pins", nil, hdr)
	assert.Equal(t, 404, rec.Code, "non-members cannot see")
	rec = h.do("bob", http.MethodPut, "/api/pins",
		map[string]string{"item_type": "work_item", "item_id": "11111111-1111-1111-1111-111111111111"}, hdr)
	assert.Equal(t, 404, rec.Code, "non-members cannot write")
	rec = h.do("alice", http.MethodGet, "/api/pins", nil,
		map[string]string{"X-Workspace-Id": "55555555-5555-5555-5555-555555555555"})
	assert.Equal(t, 404, rec.Code, "nonexistent workspace same-code 404")

	rec = h.do("alice", http.MethodPut, "/api/pins",
		map[string]string{"item_type": "agent", "item_id": "11111111-1111-1111-1111-111111111111"}, hdr)
	assert.Equal(t, 400, rec.Code, "invalid item_type should be 400")
	rec = h.do("alice", http.MethodPut, "/api/pins",
		map[string]string{"item_type": "work_item", "item_id": "not-a-uuid"}, hdr)
	assert.Equal(t, 400, rec.Code, "invalid item_id should be 400")
	rec = h.do("alice", http.MethodDelete, "/api/pins/agent/11111111-1111-1111-1111-111111111111", nil, hdr)
	assert.Equal(t, 400, rec.Code, "DELETE invalid item_type should be 400")

	rec = h.do("alice", http.MethodPost, "/api/workspaces/"+ws.ID+"/members",
		map[string]string{"email": "pv-bob@example.com", "role": "member"}, nil)
	require.Equal(t, 201, rec.Code)
	var inv struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &inv)
	rec = h.do("bob", http.MethodPost, "/api/invitations/"+inv.ID+"/accept", nil, nil)
	require.Equal(t, 200, rec.Code)

	rec = h.do("alice", http.MethodPut, "/api/pins",
		map[string]string{"item_type": "work_item", "item_id": "11111111-1111-1111-1111-111111111111"}, hdr)
	require.Equal(t, 201, rec.Code)
	rec = h.do("bob", http.MethodGet, "/api/pins", nil, hdr)
	require.Equal(t, 200, rec.Code)
	var bobPins []pinRow
	jsonOf(t, rec, &bobPins)
	assert.Empty(t, bobPins, "pins isolated by (workspace,user)")
}

func TestWorkspaceDelete_RevokesMemberPATs(t *testing.T) {
	h := newHarness(t)
	h.login("own", "wd-own@example.com")
	rec := h.do("own", http.MethodPost, "/api/workspaces",
		map[string]string{"name": "Wd", "slug": "wd-ws"}, nil)
	require.Equal(t, 201, rec.Code)
	var ws struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &ws)

	rec = h.do("own", http.MethodPost, "/api/workspaces/"+ws.ID+"/members",
		map[string]string{"email": "wd-bob@example.com", "role": "member"}, nil)
	require.Equal(t, 201, rec.Code)
	var inv struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &inv)
	h.login("bob", "wd-bob@example.com")
	rec = h.do("bob", http.MethodPost, "/api/invitations/"+inv.ID+"/accept", nil, nil)
	require.Equal(t, 200, rec.Code)
	rec = h.do("bob", http.MethodPost, "/api/tokens", map[string]string{"name": "wd"}, nil)
	require.Equal(t, 201, rec.Code)
	var tok struct {
		Token string `json:"token"`
	}
	jsonOf(t, rec, &tok)
	bearer := map[string]string{"Authorization": "Bearer " + tok.Token}
	rec = h.do("bob-cli", http.MethodGet, "/api/me", nil, bearer)
	require.Equal(t, 200, rec.Code)

	rec = h.do("bob", http.MethodDelete, "/api/workspaces/"+ws.ID, nil, nil)
	assert.Equal(t, 403, rec.Code, "only the owner can delete")

	rec = h.do("own", http.MethodDelete, "/api/workspaces/"+ws.ID, nil, nil)
	require.Equal(t, 204, rec.Code, rec.Body.String())
	rec = h.do("bob-cli", http.MethodGet, "/api/me", nil, bearer)
	assert.Equal(t, 401, rec.Code, "member PATs should be revoked after workspace deletion (two-phase step 2)")

	rec = h.do("own", http.MethodDelete, "/api/workspaces/"+ws.ID, nil, nil)
	assert.Equal(t, 404, rec.Code)
}

func TestM1_CSRFMatrix(t *testing.T) {
	h := newHarness(t)
	h.login("u", "csrf-user@example.com")

	rec := h.do("u", http.MethodPost, "/api/tokens", map[string]string{"name": "x"}, nil)
	require.Equal(t, 201, rec.Code, "double submit should be allowed: "+rec.Body.String())

	csrfSaved := h.csrf["u"]
	h.csrf["u"] = ""
	rec = h.do("u", http.MethodPost, "/api/tokens", map[string]string{"name": "y"}, nil)
	assert.Equal(t, 403, rec.Code, "session writes without a CSRF header should be 403")
	h.csrf["u"] = csrfSaved
}

func TestM1_SendCodeRateLimit(t *testing.T) {
	h := newHarness(t)
	h.login("u0", "rl@example.com")
	h.limiter.deny["rl:sendcode-ip:192.0.2.1"] = 2

	rec := h.do("u0", http.MethodPost, "/auth/send-code", map[string]string{"email": "rl2@example.com"}, nil)
	require.Equal(t, 429, rec.Code, "429 from the 3rd on: "+rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	jsonOf(t, rec, &e)
	assert.Equal(t, "E_RATE_LIMITED", e.Error.Code)
}

func (h *harness) bootstrapCSRF(client string) {
	h.t.Helper()
	rec := h.do(client, http.MethodGet, "/config", nil, nil)
	require.Equal(h.t, 200, rec.Code, rec.Body.String())
	var cfgOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	require.NoError(h.t, json.Unmarshal(rec.Body.Bytes(), &cfgOut))
	h.csrf[client] = cfgOut.CSRFToken
}

func (h *harness) verifyWithDevCode(client, email, devCode string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do(client, http.MethodPost, "/auth/verify-code",
		map[string]string{"email": email, "code": devCode}, nil)
}

func TestM1_AllowSignupDisabled_NewEmailRejected(t *testing.T) {
	h := newHarnessCfg(t, app.SessionConfig{AllowSignup: false, DevCode: "424242"})
	h.bootstrapCSRF("eve")

	rec := h.verifyWithDevCode("eve", "newbie@example.com", "424242")
	require.Equal(t, 403, rec.Code, "verify-code: "+rec.Body.String())
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	jsonOf(t, rec, &e)
	assert.Equal(t, "E_FORBIDDEN", e.Error.Code)
	assert.Equal(t, "signup disabled", e.Error.Message)

	_, exists, err := h.repo.GetUserByEmail(context.Background(), "newbie@example.com")
	require.NoError(t, err)
	assert.False(t, exists, "rejected emails must not create user rows")
}

func TestM1_AllowSignupDisabled_ExistingUserAllowed(t *testing.T) {
	h := newHarnessCfg(t, app.SessionConfig{AllowSignup: false, DevCode: "424242"})
	ctx := context.Background()
	_, err := h.repo.UpsertUserByEmail(ctx, "existing@example.com")
	require.NoError(t, err)

	h.bootstrapCSRF("old")
	rec := h.verifyWithDevCode("old", "existing@example.com", "424242")
	require.Equal(t, 200, rec.Code, "existing user code verification should pass: "+rec.Body.String())
	var out struct {
		User struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	jsonOf(t, rec, &out)
	assert.Equal(t, "existing@example.com", out.User.Email)
}

func TestM1_AllowSignupDisabled_WhitelistedEmailAllowed(t *testing.T) {
	h := newHarnessCfg(t, app.SessionConfig{
		AllowSignup: false, AllowedEmails: []string{"vip@example.com"}, DevCode: "424242",
	})
	h.bootstrapCSRF("vip")

	rec := h.verifyWithDevCode("vip", "vip@example.com", "424242")
	require.Equal(t, 200, rec.Code, "allowlisted new emails should pass: "+rec.Body.String())
	_, exists, err := h.repo.GetUserByEmail(context.Background(), "vip@example.com")
	require.NoError(t, err)
	assert.True(t, exists, "allowlisted email should create the user row")
}

func TestM1_SlidingRenewal(t *testing.T) {
	h := newHarness(t)
	h.login("u", "renew@example.com")

	rec0 := h.do("u", http.MethodGet, "/api/me", nil, nil)
	require.Equal(t, 200, rec0.Code)
	var meOut struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Name  string `json:"display_name"`
		} `json:"user"`
	}
	jsonOf(t, rec0, &meOut)

	h.auth.SessionTTL = 2 * time.Hour
	token, exp, err := h.auth.SignSession(webx.SessionClaims{
		Sub: meOut.User.ID, Email: meOut.User.Email, Name: meOut.User.Name, Sid: "s1",
	}, time.Now().Add(-90*time.Minute))
	require.NoError(t, err)
	h.auth.SessionTTL = 720 * time.Hour
	require.True(t, h.auth.ShouldRenew(exp, time.Now()), "precondition: renewal should trigger")

	h.jar["old"] = []*http.Cookie{{Name: "orkome_auth", Value: token}}
	rec := h.do("old", http.MethodGet, "/api/me", nil, nil)
	require.Equal(t, 200, rec.Code)
	var setCookie []string = rec.Header().Values("Set-Cookie")
	reissued := false
	for _, c := range setCookie {
		if strings.HasPrefix(c, "orkome_auth=") && c != "orkome_auth="+token {
			reissued = true
		}
	}
	assert.True(t, reissued, "sessions with <TTL/2 left should be slidingly re-signed (Set-Cookie carries the new ticket)")
}

func TestM1_RouteAccounting(t *testing.T) {
	h := newHarness(t)
	mux, ok := h.handler.(chi.Router)
	require.True(t, ok, "Routes should return chi.Router")

	got := map[string]bool{}
	require.NoError(t, chi.Walk(mux, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got[method+" "+strings.TrimSuffix(pattern, "/")] = true
		return nil
	}))

	expected := []string{

		"POST /auth/send-code", "POST /auth/verify-code", "POST /auth/logout",
		"GET /config", "POST /cli-token",

		"POST /api/auth/refresh", "GET /api/me", "PATCH /api/me",
		"GET /api/invitations", "POST /api/invitations/{invitationId}/accept", "POST /api/invitations/{invitationId}/decline",
		"GET /api/share-links/{code}", "POST /api/share-links/join",
		"GET /api/tokens", "POST /api/tokens", "POST /api/tokens/current/renew", "DELETE /api/tokens/{tokenId}",

		"GET /api/pins", "PUT /api/pins", "PUT /api/pins/reorder", "DELETE /api/pins/{itemType}/{itemId}",
		"GET /api/workspaces", "POST /api/workspaces",
		"GET /api/workspaces/{workspaceId}", "PATCH /api/workspaces/{workspaceId}", "DELETE /api/workspaces/{workspaceId}",
		"GET /api/workspaces/{workspaceId}/members", "POST /api/workspaces/{workspaceId}/members",
		"PATCH /api/workspaces/{workspaceId}/members/{memberId}", "DELETE /api/workspaces/{workspaceId}/members/{memberId}",
		"POST /api/workspaces/{workspaceId}/leave",
		"GET /api/workspaces/{workspaceId}/invitations", "POST /api/workspaces/{workspaceId}/invitations",
		"DELETE /api/workspaces/{workspaceId}/invitations/{invitationId}",
		"GET /api/workspaces/{workspaceId}/share-links", "POST /api/workspaces/{workspaceId}/share-links",
		"DELETE /api/workspaces/{workspaceId}/share-links/{linkId}",
	}
	for _, e := range expected {
		assert.True(t, got[e], "04 contract route not mounted: %s", e)
	}
	t.Logf("route reconciliation: all %d contract routes mounted (%d actual)", len(expected), len(got))
}
