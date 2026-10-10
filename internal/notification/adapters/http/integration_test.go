package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"orkome/internal/notification/adapters/pgrepo"
	"orkome/internal/notification/app"
	"orkome/internal/platform/webx"
	"orkome/migrations"
)

type nenv struct {
	t            *testing.T
	handler      http.Handler
	repo         *pgrepo.Repo
	notification *app.NotificationService
	prefs        *app.PreferenceService
	pool         *pgxpool.Pool
	wsID         string
	userID       string
}

func newNenv(t *testing.T) *nenv {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	const name = "orkome_notif"
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name)
	_, err = admin.Exec(context.Background(), "CREATE DATABASE "+name)
	require.NoError(t, err)
	require.NoError(t, admin.Close(context.Background()))

	u, err := url.Parse(base)
	require.NoError(t, err)
	u.Path = "/" + name
	pool, err := pgxpool.New(context.Background(), u.String())
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(context.Background(), 0)
	require.NoError(t, err)

	ctx := context.Background()
	var userID, wsID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('notif@x.io','Notif') RETURNING id::text`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ('notif','notif-ws') RETURNING id::text`).Scan(&wsID))
	_, err = pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1,$2,'owner')`, wsID, userID)
	require.NoError(t, err)

	db := pg.WrapPool(pool)
	repo := pgrepo.New(db)
	notification := app.NewNotificationService(repo)
	prefs := app.NewPreferenceService(repo)

	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(), &webx.Principal{
				UserID: userID, Source: webx.SourceSession,
			})))
		})
	}
	memberCheck := func(ctx context.Context, ws, uid string) (bool, error) {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM member WHERE workspace_id=$1 AND user_id=$2)`, ws, uid).Scan(&exists)
		return exists, err
	}
	r := http.NewServeMux()
	Mount(r, Deps{Notification: notification, Prefs: prefs, MemberCheck: memberCheck}, auth)
	return &nenv{t: t, handler: r, repo: repo, notification: notification, prefs: prefs,
		pool: pool, wsID: wsID, userID: userID}
}

func (h *nenv) do(method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var rd *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		rd = httptest.NewRequest(method, "/api"+path, strings.NewReader(string(b)))
	} else {
		rd = httptest.NewRequest(method, "/api"+path, nil)
	}
	rd.Header.Set("X-Workspace-Id", h.wsID)
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, rd)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestPrefs_PatchMergesKeepUnprovided(t *testing.T) {
	h := newNenv(t)
	wi := "11111111-1111-1111-1111-111111111111"
	wi2 := "22222222-2222-2222-2222-222222222222"

	code, out := h.do("PUT", "/notification-preferences", map[string]any{
		"muted_types":         []string{"task_completed", "eval_report"},
		"muted_work_item_ids": []string{wi},
	})
	require.Equal(t, 200, code, "put: %v", out)

	code, out = h.do("PATCH", "/notification-preferences", map[string]any{
		"muted_types": []string{"approval_request"},
	})
	require.Equal(t, 200, code, "patch: %v", out)
	assert.ElementsMatch(t, []string{"approval_request"}, toStrings(t, out["muted_types"]),
		"PATCH-provided fields should overwrite whole-field")
	assert.ElementsMatch(t, []string{wi}, toStrings(t, out["muted_work_item_ids"]),
		"PATCH-unprovided fields must keep current values, not clear")

	code, out = h.do("GET", "/notification-preferences", nil)
	require.Equal(t, 200, code)
	assert.ElementsMatch(t, []string{"approval_request"}, toStrings(t, out["muted_types"]))
	assert.ElementsMatch(t, []string{wi}, toStrings(t, out["muted_work_item_ids"]))

	code, out = h.do("PATCH", "/notification-preferences", map[string]any{})
	require.Equal(t, 200, code)
	assert.ElementsMatch(t, []string{wi}, toStrings(t, out["muted_work_item_ids"]))

	code, out = h.do("PATCH", "/notification-preferences", map[string]any{
		"muted_types": []string{},
	})
	require.Equal(t, 200, code)
	assert.Empty(t, toStrings(t, out["muted_types"]), "explicit empty array should clear the field")
	assert.ElementsMatch(t, []string{wi}, toStrings(t, out["muted_work_item_ids"]))

	code, out = h.do("PUT", "/notification-preferences", map[string]any{
		"muted_work_item_ids": []string{wi2},
	})
	require.Equal(t, 200, code, "put2: %v", out)
	code, out = h.do("GET", "/notification-preferences", nil)
	require.Equal(t, 200, code)
	assert.Empty(t, toStrings(t, out["muted_types"]), "PUT-unprovided fields should be cleared")
	assert.ElementsMatch(t, []string{wi2}, toStrings(t, out["muted_work_item_ids"]))

	var big []string
	for i := 0; i < 51; i++ {
		big = append(big, "t"+strings.Repeat("x", i+1))
	}
	code, out = h.do("PATCH", "/notification-preferences", map[string]any{"muted_types": big})
	assert.Equal(t, 400, code)
	errObj, _ := out["error"].(map[string]any)
	require.NotNil(t, errObj)
	assert.Equal(t, "E_VALIDATION", errObj["code"])
}

func TestNotification_SummaryFacetsArchived(t *testing.T) {
	h := newNenv(t)
	ctx := context.Background()

	seed := []struct{ typ, dedup string }{
		{"task_completed", "it:A"},
		{"comment_created", "it:B"},
		{"approval_request", "it:C"},
	}
	for _, s := range seed {
		require.NoError(t, h.repo.InsertNotification(ctx, h.wsID, h.userID, nil, s.typ, s.dedup, "b", "", s.dedup))
	}

	_, err := h.pool.Exec(ctx, `UPDATE notification SET created_at = now() - interval '2 days'
		WHERE dedup_key IN ('it:B','it:C')`)
	require.NoError(t, err)

	ids := map[string]string{}
	for _, s := range seed {
		var id string
		require.NoError(t, h.pool.QueryRow(ctx,
			`SELECT id::text FROM notification WHERE dedup_key=$1`, s.dedup).Scan(&id))
		ids[s.dedup] = id
	}

	code, _ := h.do("POST", "/notifications/"+ids["it:A"]+"/read", nil)
	require.Equal(t, 204, code)

	code, out := h.do("GET", "/notifications/summary", nil)
	require.Equal(t, 200, code, "summary: %v", out)
	assert.Equal(t, float64(2), out["unread"], "B/C unread")
	assert.Equal(t, float64(1), out["today_new"], "only A newly added today")

	code, out = h.do("GET", "/notifications/facets", nil)
	require.Equal(t, 200, code, "facets: %v", out)
	facets, _ := out["facets"].([]any)
	require.Len(t, facets, 3)
	got := map[string]float64{}
	for _, f := range facets {
		m, _ := f.(map[string]any)
		got[m["type"].(string)] = m["count"].(float64)
	}
	assert.Equal(t, map[string]float64{
		"task_completed": 1, "comment_created": 1, "approval_request": 1,
	}, got)

	code, out = h.do("POST", "/notifications/"+ids["it:C"]+"/archive", nil)
	require.Equal(t, 204, code)
	code, out = h.do("GET", "/notifications/archived", nil)
	require.Equal(t, 200, code, "archived: %v", out)
	arch, _ := out["items"].([]any)
	require.Len(t, arch, 1)
	first, _ := arch[0].(map[string]any)
	assert.Equal(t, ids["it:C"], first["id"])
	assert.Equal(t, "approval_request", first["type"])

	code, out = h.do("GET", "/notifications", nil)
	require.Equal(t, 200, code)
	active, _ := out["items"].([]any)
	assert.Len(t, active, 2, "main list should keep only A/B")
	for _, it := range active {
		m, _ := it.(map[string]any)
		assert.NotEqual(t, ids["it:C"], m["id"])
	}

	code, out = h.do("GET", "/notifications/summary", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, float64(1), out["unread"])
	assert.Equal(t, float64(1), out["today_new"])
	code, out = h.do("GET", "/notifications/facets", nil)
	require.Equal(t, 200, code)
	facets, _ = out["facets"].([]any)
	assert.Len(t, facets, 3, "full grouping should include archived")

	code, out = h.do("GET", "/notifications?archived=true", nil)
	require.Equal(t, 200, code)
	arch2, _ := out["items"].([]any)
	assert.Len(t, arch2, 1)
}

func toStrings(t *testing.T, v any) []string {
	t.Helper()
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("want an array, got %T: %v", v, v)
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, e.(string))
	}
	return out
}

func TestDirectWrite_MuteFiltered(t *testing.T) {
	h := newNenv(t)
	ctx := context.Background()

	require.NoError(t, h.repo.UpsertPreference(ctx, h.wsID, h.userID, app.Preference{
		MutedTypes: []string{"task_completed", "approval_request", "eval_report"},
	}))
	require.NoError(t, h.repo.WriteTaskCompleted(ctx, h.wsID, h.userID, "", "agent", "done"))
	require.NoError(t, h.repo.WriteApprovalRequest(ctx, h.wsID, h.userID, "task-1", "payload"))
	require.NoError(t, h.repo.WriteEvalReport(ctx, h.wsID, h.userID, "task-2", "t", "b"))
	var n int
	require.NoError(t, h.pool.QueryRow(ctx,
		`SELECT count(*) FROM notification WHERE workspace_id=$1`, h.wsID).Scan(&n))
	assert.Zero(t, n, "all three direct-write methods must pass the mute filter (D19②)")

	require.NoError(t, h.repo.UpsertPreference(ctx, h.wsID, h.userID, app.Preference{}))
	require.NoError(t, h.repo.WriteTaskCompleted(ctx, h.wsID, h.userID, "", "agent", "done"))
	require.NoError(t, h.pool.QueryRow(ctx,
		`SELECT count(*) FROM notification WHERE workspace_id=$1 AND type='task_completed'`, h.wsID).Scan(&n))
	assert.Equal(t, 1, n, "direct writes recover after unmute")

	sess := "33333333-3333-3333-3333-333333333333"
	require.NoError(t, h.repo.UpsertPreference(ctx, h.wsID, h.userID, app.Preference{
		MutedTypes: []string{"comment_created"}, MutedConversationIDs: []string{sess},
	}))
	got, err := h.repo.GetPreference(ctx, h.wsID, h.userID)
	require.NoError(t, err)
	assert.Equal(t, []string{"comment_created"}, got.MutedTypes)
	assert.Equal(t, []string{sess}, got.MutedConversationIDs, "session mute group round-trips losslessly")
}

func TestDirectWrite_TruncationUnified(t *testing.T) {
	h := newNenv(t)
	ctx := context.Background()
	runeLen := func(s string) int { return len([]rune(s)) }

	require.NoError(t, h.repo.WriteTaskCompleted(ctx, h.wsID, h.userID, "",
		"agent", strings.Repeat("done", 3000)))
	var body string
	require.NoError(t, h.pool.QueryRow(ctx,
		`SELECT body FROM notification WHERE workspace_id=$1 AND type='task_completed'`, h.wsID).Scan(&body))
	assert.Equal(t, 2000, runeLen(body), "task completed body = summary cap 2000")
	assert.True(t, utf8.ValidString(body), "CJK truncation does not split UTF-8")
	assert.True(t, strings.HasSuffix(body, "…"), "over the limit ends with an ellipsis (counted in the cap)")

	require.NoError(t, h.repo.WriteApprovalRequest(ctx, h.wsID, h.userID, "t-1",
		strings.Repeat("a", 5000)))
	require.NoError(t, h.pool.QueryRow(ctx,
		`SELECT body FROM notification WHERE workspace_id=$1 AND type='approval_request'`, h.wsID).Scan(&body))
	assert.Equal(t, 4000, runeLen(body), "approval payload = body cap 4000")

	require.NoError(t, h.repo.WriteEvalReport(ctx, h.wsID, h.userID, "t-2",
		strings.Repeat("🎉", 300), strings.Repeat("b", 4500)))
	var title string
	require.NoError(t, h.pool.QueryRow(ctx,
		`SELECT title, body FROM notification WHERE workspace_id=$1 AND type='eval_report'`, h.wsID).Scan(&title, &body))
	assert.Equal(t, 200, runeLen(title), "title cap 200")
	assert.True(t, utf8.ValidString(title), "emoji truncation does not split")
	assert.Equal(t, 4000, runeLen(body))
}

func TestArchive_DeleteArchivedBefore(t *testing.T) {
	h := newNenv(t)
	ctx := context.Background()
	for _, dedup := range []string{"old-a", "old-b", "new-a"} {
		require.NoError(t, h.repo.InsertNotification(ctx, h.wsID, h.userID, nil,
			"task_completed", dedup, "t", "", dedup))
		age := "40 days"
		if strings.HasPrefix(dedup, "new") {
			age = "2 days"
		}
		_, err := h.pool.Exec(ctx, `UPDATE notification
			SET archived_at = now() - interval '`+age+`'
			WHERE dedup_key=$1`, dedup)
		require.NoError(t, err)
	}

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)

	n, err := h.repo.DeleteArchivedBefore(ctx, cutoff, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "batch cap 1: one row deleted per batch")
	n, err = h.repo.DeleteArchivedBefore(ctx, cutoff, 500)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only one row left due for archival")
	n, err = h.repo.DeleteArchivedBefore(ctx, cutoff, 500)
	require.NoError(t, err)
	assert.Zero(t, n, "due rows drained")
	var left string
	require.NoError(t, h.pool.QueryRow(ctx,
		`SELECT dedup_key FROM notification WHERE workspace_id=$1`, h.wsID).Scan(&left))
	assert.Equal(t, "new-a", left, "archived rows not yet due must be kept")
}
