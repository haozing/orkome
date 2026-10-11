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

	identityhttp "github.com/haozing/ploykit/identity/adapters/http"
	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	fwapp "github.com/haozing/ploykit/identity/app"
	"github.com/haozing/ploykit/platform/logx"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/events"
	idhttp "orkome/internal/identity/adapters/http"
	idpgrepo "orkome/internal/identity/adapters/pgrepo"
	"orkome/internal/platform/eventbus"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	"orkome/internal/work/app"
	"orkome/migrations"
)

type m2h struct {
	t       *testing.T
	handler http.Handler
	pool    *pgxpool.Pool
	wsID    string
	jar     map[string][]*http.Cookie
	csrf    map[string]string
}

func newM2(t *testing.T) *m2h {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	dsn := testDB(t, base, "orkome_m2")
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `TRUNCATE activity_log, comment_reaction, comment, work_item_source_context,
		work_item_reaction, work_item_child_event, work_item_dependency, work_item_subscriber,
		work_item_view_preference, work_item_view, work_item_to_label, label, work_item_property_def,
		work_item, project_resource, project, work_item_status,
		pinned_item, personal_access_token, workspace_share_link, workspace_invitation,
		verification_code, member, workspace, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	db := pg.WrapPool(pool)
	log := logx.New(logx.Options{Level: "error"})
	bus := eventbus.New(log)

	idRepo := idpgrepo.New(db)
	secret := []byte("m2-integration-secret-32-bytes!!")
	auth := &webx.AuthConfig{CookieName: "orkome_auth", SessionTTL: 720 * time.Hour}
	mail := &captureMail{codes: map[string]string{}}
	fwRepo := identitypg.New(pool, identitypg.Config{SessionTTL: 720 * time.Hour})
	sessions := fwapp.NewSessionService(fwRepo, mail, fwapp.SessionConfig{
		AllowSignup: true, DevCode: "123456", SecretPepper: string(secret),
	}, 720*time.Hour, 0, time.Now)
	tokens := fwapp.NewTokenService(fwRepo, nil, time.Now)
	idDeps := idhttp.Deps{Repo: idRepo, Tokens: tokens}

	wRepo := &workpgrepo.Repo{DB: db}
	workDeps := Deps{
		WorkItems:   app.NewWorkItemService(wRepo, bus, time.Now),
		Comments:    app.NewCommentService(wRepo, bus, time.Now),
		Catalog:     app.NewCatalogService(wRepo, bus),
		Views:       app.NewViewService(wRepo),
		Search:      app.NewSearchService(wRepo),
		Timeline:    wRepo.ListTimeline,
		Subscribers: wRepo.ListSubscribers,
		MemberCheck: func(ctx context.Context, wsID, userID string) (bool, error) {
			_, ok, err := idRepo.GetMember(ctx, wsID, userID)
			return ok, err
		},
	}

	bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		switch e.Event {
		case events.WorkItemCreated, events.WorkItemUpdated, events.CommentCreated:
		default:
			return
		}
		wiID := ""
		if p, ok := e.Payload.(events.WorkItemPayload); ok {
			wiID = p.ID
		}
		if p, ok := e.Payload.(events.CommentPayload); ok {
			wiID = p.WorkItemID
		}
		_ = wRepo.AppendActivity(ctx, e.Event+":"+wiID+":"+e.OccurredAt.Format("150405.000000000"),
			e.WorkspaceID, wiID, "user", e.ActorUserID, e.Event, map[string]any{})
	})

	full := http.NewServeMux()
	identityhttp.Mount(full, identityhttp.Deps{
		AuthCfg: *auth, Sessions: fwRepo, PATs: fwRepo, SessionsSvc: sessions, Tokens: tokens,
	})
	authMw := webx.Authenticate(auth, fwRepo, fwRepo)
	idhttp.MountExtensions(full, authMw, idDeps)
	Mount(full, workDeps, authMw)

	return &m2h{t: t, handler: full, pool: pool, jar: map[string][]*http.Cookie{}, csrf: map[string]string{}}
}

type captureMail struct {
	mu    sync.Mutex
	codes map[string]string
}

func (c *captureMail) SendLoginCode(_ context.Context, to, code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codes[to] = code
	return nil
}
func (c *captureMail) SendInvite(_ context.Context, to, _, _ string) error        { return nil }
func (c *captureMail) SendPasswordReset(_ context.Context, _, _ string) error     { return nil }
func (c *captureMail) SendEmailVerification(_ context.Context, _, _ string) error { return nil }

type failOpen struct{}

func (failOpen) Allow(context.Context, string, int) (bool, time.Duration) { return true, 0 }

func (h *m2h) do(client, method, path string, body any) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(h.t, err)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "192.0.2.7:5555"
	for _, c := range h.jar[client] {
		req.AddCookie(c)
	}
	if h.csrf[client] != "" {
		req.Header.Set("X-CSRF-Token", h.csrf[client])
	}
	if h.wsID != "" {
		req.Header.Set("X-Workspace-Id", h.wsID)
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

func TestM2_GoldenPath(t *testing.T) {
	h := newM2(t)

	rec := h.do("u", http.MethodGet, "/config", nil)
	require.Equal(t, 200, rec.Code)
	var cfgOut struct {
		CSRFToken string `json:"csrf_token"`
	}
	jsonOf(t, rec, &cfgOut)
	h.csrf["u"] = cfgOut.CSRFToken
	rec = h.do("u", http.MethodPost, "/auth/verify-code", map[string]string{"email": "alice@m2.dev", "code": "123456"})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var vout struct {
		CSRFToken string `json:"csrf_token"`
	}
	jsonOf(t, rec, &vout)
	h.csrf["u"] = vout.CSRFToken

	rec = h.do("b", http.MethodGet, "/config", nil)
	require.Equal(t, 200, rec.Code)
	var bcfg struct {
		CSRFToken string `json:"csrf_token"`
	}
	jsonOf(t, rec, &bcfg)
	h.csrf["b"] = bcfg.CSRFToken
	rec = h.do("b", http.MethodPost, "/auth/verify-code", map[string]string{"email": "bob@m2.dev", "code": "123456"})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	rec = h.do("b", http.MethodGet, "/auth/me", nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var bme struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	jsonOf(t, rec, &bme)
	bobID := bme.User.ID
	require.NotEmpty(t, bobID)

	rec = h.do("u", http.MethodPost, "/api/workspaces", map[string]string{"name": "M2", "slug": "m2-golden"})
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var ws struct {
		ID string `json:"id"`
	}
	jsonOf(t, rec, &ws)
	h.wsID = ws.ID

	rec = h.do("u", http.MethodGet, "/api/work-item-statuses", nil)
	require.Equal(t, 200, rec.Code)
	var defs []map[string]any
	jsonOf(t, rec, &defs)
	assert.Len(t, defs, 7, "7 built-in statuses should be seeded")

	rec = h.do("u", http.MethodPost, "/api/work-items", map[string]any{"title": "Alpha", "status": "todo"})
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var wi struct {
		ID     string `json:"id"`
		Number int    `json:"number"`
	}
	jsonOf(t, rec, &wi)
	assert.EqualValues(t, 1, wi.Number)

	rec = h.do("u", http.MethodPost, "/api/work-items/preview-trigger", nil)
	require.Equal(t, 200, rec.Code)
	var pv struct {
		Result     string `json:"result"`
		ReasonCode string `json:"reason_code"`
	}
	jsonOf(t, rec, &pv)
	assert.Equal(t, "blocked", pv.Result)
	assert.Equal(t, "trigger_engine_not_wired", pv.ReasonCode)

	rec = h.do("u", http.MethodPost, "/api/work-items", map[string]any{"title": "alpha", "status": "todo"})
	assert.Equal(t, 409, rec.Code, "normalized title duplicate should be rejected: "+rec.Body.String())

	st := "done"
	rec = h.do("u", http.MethodPut, "/api/work-items/"+wi.ID, map[string]any{"status": st, "expected_revision": 1})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	rec = h.do("u", http.MethodPost, "/api/work-items", map[string]any{"title": "Alpha", "status": "todo"})
	assert.Equal(t, 201, rec.Code, "same name after terminal state should be allowed")

	rec = h.do("u", http.MethodPut, "/api/work-items/"+wi.ID, map[string]any{"title": "X", "expected_revision": 1})
	assert.Equal(t, 409, rec.Code, "old revision should be 409")

	rec = h.do("u", http.MethodPost, "/api/work-items/"+wi.ID+"/comments",
		map[string]any{"content": "first comment [@Bob](mention://user/" + bobID + ")"})
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var cout struct {
		Comment struct {
			ID string `json:"id"`
		} `json:"comment"`
		Admission struct {
			Result     string `json:"result"`
			ReasonCode string `json:"reason_code"`
		} `json:"admission"`
	}
	jsonOf(t, rec, &cout)
	assert.Equal(t, "blocked", cout.Admission.Result, "M2 trigger placeholder")
	root1 := cout.Comment.ID

	rec = h.do("u", http.MethodPost, "/api/work-items/"+wi.ID+"/comments",
		map[string]any{"content": "reply one", "parent_id": root1})
	require.Equal(t, 201, rec.Code)
	var r1 struct {
		Comment struct {
			ID string `json:"id"`
		} `json:"comment"`
	}
	jsonOf(t, rec, &r1)
	rec = h.do("u", http.MethodPost, "/api/work-items/"+wi.ID+"/comments",
		map[string]any{"content": "reply two", "parent_id": r1.Comment.ID})
	require.Equal(t, 201, rec.Code)

	rec = h.do("u", http.MethodPost, "/api/comments/"+root1+"/resolve", nil)
	require.Equal(t, 204, rec.Code, rec.Body.String())
	rec = h.do("u", http.MethodPost, "/api/work-items/"+wi.ID+"/comments",
		map[string]any{"content": "a new reply after resolve should auto-unresolve", "parent_id": r1.Comment.ID})
	require.Equal(t, 201, rec.Code)

	rec = h.do("u", http.MethodGet, "/api/work-items/"+wi.ID+"/comments", nil)
	require.Equal(t, 200, rec.Code)
	var threadOut struct {
		Items []struct {
			ID       string `json:"id"`
			ParentID string `json:"parent_id"`
			Resolved bool   `json:"resolved"`
		} `json:"items"`
	}
	jsonOf(t, rec, &threadOut)
	thread := threadOut.Items
	assert.NotEmpty(t, thread)
	assert.Equal(t, root1, thread[0].ID, "roots should sort first")
	for _, c := range thread[1:] {
		assert.NotEmpty(t, c.ParentID, "the remaining rows should be descendants")
	}

	rec = h.do("u", http.MethodGet, "/api/work-items/"+wi.ID+"/subscribers", nil)
	require.Equal(t, 200, rec.Code)
	var subsOut struct {
		Items []struct {
			UserID string `json:"user_id"`
			Source string `json:"source"`
		} `json:"items"`
	}
	jsonOf(t, rec, &subsOut)
	subs := subsOut.Items
	sources := map[string]string{}
	for _, s := range subs {
		sources[s.UserID] = s.Source
	}
	assert.Contains(t, sources, bobID, "@-mentioned users should subscribe")

	rec = h.do("u", http.MethodPost, "/api/work-items/table/rows",
		map[string]any{"filters": map[string]any{"status": []string{"todo"}}})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var rows struct {
		Items       []map[string]any `json:"items"`
		Fingerprint string           `json:"fingerprint"`
	}
	jsonOf(t, rec, &rows)
	assert.NotEmpty(t, rows.Items, "todo row should exist")
	assert.NotEmpty(t, rows.Fingerprint)

	rec = h.do("u", http.MethodPost, "/api/work-items/table/groups",
		map[string]any{"group_by": "status", "filters": map[string]any{}})
	require.Equal(t, 200, rec.Code)
	var groups []struct {
		K string `json:"k"`
		N int    `json:"n"`
	}
	jsonOf(t, rec, &groups)
	gm := map[string]int{}
	for _, g := range groups {
		gm[g.K] = g.N
	}
	assert.Equal(t, 1, gm["done"], "done group should be 1")
	assert.Equal(t, 1, gm["todo"], "todo group should be 1")

	rec = h.do("u", http.MethodPost, "/api/work-items/table/facets", map[string]any{"filters": map[string]any{}})
	require.Equal(t, 200, rec.Code)
	var facets map[string][]struct {
		K string `json:"k"`
		N int    `json:"n"`
	}
	jsonOf(t, rec, &facets)
	assert.Contains(t, facets, "status")
	assert.Contains(t, facets, "assignee")

	rec = h.do("u", http.MethodGet, "/api/work-items/search?q=Alpha", nil)
	require.Equal(t, 200, rec.Code)
	var sr struct {
		Items []map[string]any `json:"items"`
	}
	jsonOf(t, rec, &sr)
	assert.Len(t, sr.Items, 1, "done Alpha excluded, only the todo namesake remains")

	rec = h.do("u", http.MethodGet, "/api/work-items/"+wi.ID+"/timeline", nil)
	require.Equal(t, 200, rec.Code)
	var tlOut struct {
		Items []struct {
			Kind string `json:"kind"`
		} `json:"items"`
	}
	jsonOf(t, rec, &tlOut)
	tl := tlOut.Items
	kinds := map[string]int{}
	for _, e := range tl {
		kinds[e.Kind]++
	}
	assert.NotZero(t, kinds["comment"], "timeline should contain comments")
	assert.NotZero(t, kinds["activity"], "timeline should contain activity projections (eventbus PhaseProject effective)")
}
