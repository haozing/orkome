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

	"orkome/internal/platform/webx"
	"orkome/internal/work/app"
)

type fakeCatalogRepo struct {
	app.Repo
	items     map[string]app.WorkItem
	labels    map[string]app.Label
	mounted   map[[2]string]bool
	subs      map[[2]string]app.Subscriber
	comments  map[string]app.Comment
	cReacts   map[[3]string]bool
	statuses  map[string]app.StatusDef
	projects  map[string]fakeProject
	propDefs  map[string]fakePropDef
	resources map[string]app.ProjectResource
	attached  [][2]string
	subCalls  int
}

type fakeProject struct {
	ws    string
	p     app.Project
	items int
}

type fakePropDef struct {
	ws       string
	def      app.PropertyDef
	archived bool
}

func newFakeCatalogRepo() *fakeCatalogRepo {
	return &fakeCatalogRepo{
		items: map[string]app.WorkItem{
			"wi-1": {ID: "wi-1", WorkspaceID: "ws-1", Title: "host", StatusKey: "todo",
				Properties: map[string]any{}, Revision: 3},
		},
		labels: map[string]app.Label{
			"lab-1": {ID: "lab-1", Name: "core", Color: "#f00"},
			"lab-2": {ID: "lab-2", Name: "peripheral"},
		},
		mounted:  map[[2]string]bool{},
		subs:     map[[2]string]app.Subscriber{},
		comments: map[string]app.Comment{},
		cReacts:  map[[3]string]bool{},
		statuses: map[string]app.StatusDef{
			"todo":      {Key: "todo", Name: "Todo", Category: "unstarted", IsBuiltin: true, Position: 1},
			"in_review": {Key: "in_review", Name: "In Review", Category: "started", IsBuiltin: true, Position: 3},
			"verifying": {Key: "verifying", Name: "verifying", Category: "started", Position: 7},
		},
		projects: map[string]fakeProject{
			"p-1": {ws: "ws-1", p: app.Project{ID: "p-1", Name: "Alpha project", Description: "flagship"}},
			"p-2": {ws: "ws-1", p: app.Project{ID: "p-2", Name: "Beta", Description: "tasks remain"}},
		},
		propDefs: map[string]fakePropDef{
			"severity": {ws: "ws-1", def: app.PropertyDef{Key: "severity", Name: "Severity", Type: "select", Options: []string{"low", "high"}}},
			"due":      {ws: "ws-1", def: app.PropertyDef{Key: "due", Name: "Due", Type: "date"}},
			"estimate": {ws: "ws-1", def: app.PropertyDef{Key: "estimate", Name: "Estimate", Type: "number"}},
			"notes":    {ws: "ws-1", def: app.PropertyDef{Key: "notes", Name: "Notes", Type: "text"}},
		},
		resources: map[string]app.ProjectResource{},
	}
}

func (f *fakeCatalogRepo) EnsureStatusSeed(context.Context, string) error { return nil }
func (f *fakeCatalogRepo) ListStatuses(context.Context, string) ([]app.StatusDef, error) {
	var out []app.StatusDef
	for _, d := range f.statuses {
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeCatalogRepo) GetWorkItem(_ context.Context, _, id string) (app.WorkItem, bool, error) {
	w, ok := f.items[id]
	return w, ok, nil
}

func (f *fakeCatalogRepo) GetLabel(_ context.Context, _, id string) (app.Label, bool, error) {
	l, ok := f.labels[id]
	return l, ok, nil
}

func (f *fakeCatalogRepo) AttachWorkItemLabel(_ context.Context, _, wiID, labelID string) error {
	f.attached = append(f.attached, [2]string{wiID, labelID})
	f.mounted[[2]string{wiID, labelID}] = true
	return nil
}

func (f *fakeCatalogRepo) DetachWorkItemLabel(_ context.Context, _, wiID, labelID string) error {
	delete(f.mounted, [2]string{wiID, labelID})
	return nil
}

func (f *fakeCatalogRepo) GetWorkItemLabels(_ context.Context, wiID string) ([]app.Label, error) {
	var out []app.Label
	for pair := range f.mounted {
		if pair[0] == wiID {
			out = append(out, f.labels[pair[1]])
		}
	}
	return out, nil
}

func (f *fakeCatalogRepo) UpsertManualSubscriber(_ context.Context, _, wiID, userID string) (app.Subscriber, bool, error) {
	key := [2]string{wiID, userID}
	s, existed := f.subs[key]
	if !existed {
		s = app.Subscriber{WorkItemID: wiID, UserID: userID, Source: "manual", Level: "all"}
		f.subs[key] = s
	}
	f.subCalls++
	return s, !existed, nil
}

func (f *fakeCatalogRepo) GetComment(_ context.Context, id string) (app.Comment, bool, error) {
	c, ok := f.comments[id]
	return c, ok, nil
}

func (f *fakeCatalogRepo) UpsertCommentReaction(_ context.Context, _, commentID, userID, emoji string) error {
	f.cReacts[[3]string{commentID, userID, emoji}] = true
	return nil
}

func (f *fakeCatalogRepo) DeleteCommentReaction(_ context.Context, _, commentID, userID, emoji string) (bool, error) {
	k := [3]string{commentID, userID, emoji}
	had := f.cReacts[k]
	delete(f.cReacts, k)
	return had, nil
}

func (f *fakeCatalogRepo) ListCommentReactionCounts(_ context.Context, _, commentID string) ([]app.ReactionCount, error) {
	counts := map[string]int64{}
	for k := range f.cReacts {
		if k[0] == commentID {
			counts[k[2]]++
		}
	}
	var out []app.ReactionCount
	for e, n := range counts {
		out = append(out, app.ReactionCount{Emoji: e, Count: n})
	}
	return out, nil
}

func (f *fakeCatalogRepo) ListPropertyDefs(context.Context, string) ([]app.PropertyDef, error) {
	var out []app.PropertyDef
	for _, d := range f.propDefs {
		if !d.archived {
			out = append(out, d.def)
		}
	}
	return out, nil
}

func (f *fakeCatalogRepo) GetPropertyDef(_ context.Context, _, key string) (app.PropertyDef, bool, error) {
	d, ok := f.propDefs[key]
	if !ok || d.archived {
		return app.PropertyDef{}, false, nil
	}
	return d.def, true, nil
}

func (f *fakeCatalogRepo) UpdatePropertyDef(_ context.Context, _, key string, p app.PropertyDefPatch) (app.PropertyDef, bool, error) {
	d, ok := f.propDefs[key]
	if !ok || d.archived {
		return app.PropertyDef{}, false, nil
	}
	if p.Name != nil {
		d.def.Name = *p.Name
	}
	if p.Description != nil {
		d.def.Description = *p.Description
	}
	if p.Options != nil {
		d.def.Options = p.Options
	}
	f.propDefs[key] = d
	return d.def, true, nil
}

func (f *fakeCatalogRepo) ArchivePropertyDef(_ context.Context, _, key string) (bool, bool, error) {
	d, ok := f.propDefs[key]
	if !ok {
		return false, false, nil
	}
	if d.archived {
		return false, true, nil
	}
	d.archived = true
	f.propDefs[key] = d
	return true, true, nil
}

func (f *fakeCatalogRepo) GetPropertyKey(_ context.Context, _, wiID, key string) (json.RawMessage, bool, bool, error) {
	wi, ok := f.items[wiID]
	if !ok {
		return nil, false, false, nil
	}
	v, ok := wi.Properties[key]
	if !ok {
		return nil, true, false, nil
	}
	b, _ := json.Marshal(v)
	return b, true, true, nil
}

func (f *fakeCatalogRepo) SetPropertyKey(_ context.Context, _, wiID, key string, value json.RawMessage) (map[string]any, bool, error) {
	wi, ok := f.items[wiID]
	if !ok {
		return nil, false, nil
	}
	var v any
	_ = json.Unmarshal(value, &v)
	if wi.Properties == nil {
		wi.Properties = map[string]any{}
	}
	wi.Properties[key] = v
	wi.Revision++
	f.items[wiID] = wi
	return wi.Properties, true, nil
}

func (f *fakeCatalogRepo) DeletePropertyKey(_ context.Context, _, wiID, key string) (bool, error) {
	wi, ok := f.items[wiID]
	if !ok {
		return false, nil
	}
	delete(wi.Properties, key)
	wi.Revision++
	f.items[wiID] = wi
	return true, nil
}

func (f *fakeCatalogRepo) GetStatus(_ context.Context, _, key string) (app.StatusDef, bool, error) {
	d, ok := f.statuses[key]
	return d, ok, nil
}

func (f *fakeCatalogRepo) UpdateStatus(_ context.Context, _, key string, p app.StatusDefPatch) (app.StatusDef, bool, error) {
	d, ok := f.statuses[key]
	if !ok {
		return d, false, nil
	}
	if p.Name != nil {
		d.Name = *p.Name
	}
	if p.Icon != nil {
		d.Icon = *p.Icon
	}
	if p.Category != nil {
		d.Category = *p.Category
	}
	f.statuses[key] = d
	return d, true, nil
}

func (f *fakeCatalogRepo) GetProject(_ context.Context, wsID, id string) (app.Project, bool, error) {
	p, ok := f.projects[id]
	if !ok || p.ws != wsID {
		return app.Project{}, false, nil
	}
	return p.p, true, nil
}

func (f *fakeCatalogRepo) UpdateProject(_ context.Context, _, id, name, desc string) (app.Project, bool, error) {
	p, ok := f.projects[id]
	if !ok {
		return app.Project{}, false, nil
	}
	p.p.Name, p.p.Description = name, desc
	f.projects[id] = p
	return p.p, true, nil
}

func (f *fakeCatalogRepo) DeleteProject(_ context.Context, _, id string) error {
	delete(f.projects, id)
	return nil
}

func (f *fakeCatalogRepo) CountWorkItemsInProject(_ context.Context, _, projectID string) (int, error) {
	return f.projects[projectID].items, nil
}

func (f *fakeCatalogRepo) SearchProjects(_ context.Context, _, q string, _ int) ([]app.Project, error) {
	var out []app.Project
	for _, p := range f.projects {
		if strings.Contains(strings.ToLower(p.p.Name), strings.ToLower(q)) ||
			strings.Contains(strings.ToLower(p.p.Description), strings.ToLower(q)) {
			out = append(out, p.p)
		}
	}
	return out, nil
}

func newCatalogRouter(t *testing.T) (*chi.Mux, *fakeCatalogRepo) {
	t.Helper()
	repo := newFakeCatalogRepo()
	repo.comments["c-1"] = app.Comment{ID: "c-1", WorkspaceID: "ws-1", WorkItemID: "wi-1"}
	repo.comments["c-2"] = app.Comment{ID: "c-2", WorkspaceID: "ws-2", WorkItemID: "wi-x"}
	repo.comments["c-3"] = app.Comment{ID: "c-3", WorkspaceID: "ws-1", WorkItemID: "wi-1", Deleted: true}
	p2 := repo.projects["p-2"]
	p2.items = 2
	repo.projects["p-2"] = p2
	deps := Deps{
		WorkItems: app.NewWorkItemService(repo, nil, nil),
		Comments:  app.NewCommentService(repo, nil, nil),
		Catalog:   app.NewCatalogService(repo, nil),
		Views:     app.NewViewService(repo),
		Search:    app.NewSearchService(repo),
		MemberCheck: func(context.Context, string, string) (bool, error) {
			return true, nil
		},
	}
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(webx.WithPrincipal(r.Context(),
				&webx.Principal{UserID: "u-1", Source: webx.SourceSession})))
		})
	}
	r := chi.NewRouter()
	Mount(r, deps, auth)
	return r, repo
}

func doCat(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var rd *strings.Reader = strings.NewReader(body)
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("X-Workspace-Id", "ws-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func codeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e), "body: %s", rec.Body.String())
	return e.Error.Code
}

func TestLabelsIncrementalMount(t *testing.T) {
	r, repo := newCatalogRouter(t)
	base := "/work-items/wi-1/labels"

	assert.Equal(t, http.StatusMethodNotAllowed, doCat(r, http.MethodPut, base, `{"label_ids":[]}`).Code)

	rec := doCat(r, http.MethodPost, base, `{"label_id":"lab-1"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var labels []app.Label
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &labels))
	require.Len(t, labels, 1)
	assert.Equal(t, "lab-1", labels[0].ID)

	rec = doCat(r, http.MethodPost, base, `{"label_id":"lab-1"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &labels))
	assert.Len(t, labels, 1)

	rec = doCat(r, http.MethodGet, base, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &labels))
	assert.Len(t, labels, 1)

	assert.Equal(t, http.StatusBadRequest, doCat(r, http.MethodPost, base, `{}`).Code)
	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodPost, base, `{"label_id":"lab-x"}`).Code)

	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodPost, "/work-items/wi-x/labels", `{"label_id":"lab-1"}`).Code)

	rec = doCat(r, http.MethodDelete, base+"/lab-1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &labels))
	assert.Empty(t, labels)
	rec = doCat(r, http.MethodDelete, base+"/lab-1", "")
	require.Equal(t, http.StatusOK, rec.Code, "re-deleting an unmounted entry should succeed idempotently")
	require.Len(t, repo.attached, 2, "two attaches each hit one repo write")
}

func TestSubscribeEndpoint(t *testing.T) {
	r, repo := newCatalogRouter(t)

	rec := doCat(r, http.MethodPost, "/work-items/wi-1/subscribers", "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var sub app.Subscriber
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sub))
	assert.Equal(t, "u-1", sub.UserID)
	assert.Equal(t, "manual", sub.Source, "source recorded as manual (a seventh type beyond internal archive 05 §3.10)")
	assert.Equal(t, "all", sub.Level)

	rec = doCat(r, http.MethodPost, "/work-items/wi-1/subscribers", "")
	require.Equal(t, http.StatusCreated, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sub))
	assert.Equal(t, "manual", sub.Source)
	assert.Equal(t, 2, repo.subCalls)

	rec = doCat(r, http.MethodPost, "/work-items/wi-x/subscribers", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "E_NOT_FOUND", codeOf(t, rec))
}

func TestCommentReactions(t *testing.T) {
	r, _ := newCatalogRouter(t)
	base := "/comments/c-1/reactions"

	rec := doCat(r, http.MethodPost, base, `{"emoji":"👍"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var out struct {
		Reactions []app.ReactionCount `json:"reactions"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Reactions, 1)
	assert.Equal(t, "👍", out.Reactions[0].Emoji)
	rec = doCat(r, http.MethodPost, base, `{"emoji":"👍"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.EqualValues(t, 1, out.Reactions[0].Count, "(comment,user,emoji) primary key dedup")

	rec = doCat(r, http.MethodDelete, base+"?emoji=%F0%9F%91%8D", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Empty(t, out.Reactions)
	doCat(r, http.MethodPost, base, `{"emoji":"🎉"}`)
	rec = doCat(r, http.MethodDelete, base, `{"emoji":"🎉"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body {emoji} form deletion")

	assert.Equal(t, http.StatusBadRequest, doCat(r, http.MethodPost, base, `{"emoji":""}`).Code)
	assert.Equal(t, http.StatusBadRequest, doCat(r, http.MethodPost, base, `{"emoji":"`+strings.Repeat("🎉", 20)+`"}`).Code)

	for _, c := range []string{"c-2", "c-3", "c-404"} {
		rec := doCat(r, http.MethodPost, "/comments/"+c+"/reactions", `{"emoji":"👍"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code, "comment %s should be 404", c)
		assert.Equal(t, "E_NOT_FOUND", codeOf(t, rec))
	}
}

func TestPropertyKeyEndpoints(t *testing.T) {
	r, _ := newCatalogRouter(t)
	base := "/work-items/wi-1/properties/"

	cases := []struct {
		key  string
		body string
		ok   bool
	}{
		{"severity", `"high"`, true},
		{"severity", `"urgent"`, false},
		{"due", `"2026-10-02"`, true},
		{"due", `"2026-10-02T00:00:00Z"`, false},
		{"estimate", `3.5`, true},
		{"estimate", `"3"`, false},
		{"notes", `"hello"`, true},
		{"notes", `""`, true},
		{"unknown_key", `"x"`, false},
	}
	for _, tc := range cases {
		rec := doCat(r, http.MethodPut, base+tc.key, tc.body)
		if tc.ok {
			assert.Equal(t, http.StatusOK, rec.Code, "PUT %s=%s should pass", tc.key, tc.body)
		} else {
			assert.Equal(t, http.StatusBadRequest, rec.Code, "PUT %s=%s should be 400", tc.key, tc.body)
			assert.Equal(t, "E_VALIDATION", codeOf(t, rec))
		}
	}

	rec := doCat(r, http.MethodPut, base+"severity", `"low"`)
	require.Equal(t, http.StatusOK, rec.Code)
	var put struct {
		Key      string          `json:"key"`
		Value    json.RawMessage `json:"value"`
		Revision int64           `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &put))
	assert.Equal(t, "severity", put.Key)
	assert.JSONEq(t, `"low"`, string(put.Value))
	rev := put.Revision
	rec = doCat(r, http.MethodPut, base+"severity", `"high"`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &put))
	assert.Equal(t, rev+1, put.Revision, "property PUT should advance revision")

	rec = doCat(r, http.MethodGet, base+"severity", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var got struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "severity", got.Key)
	assert.JSONEq(t, `"high"`, string(got.Value))
	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodGet, base+"missing", "").Code)
	assert.Equal(t, http.StatusNoContent, doCat(r, http.MethodDelete, base+"severity", "").Code)
	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodGet, base+"severity", "").Code)
	assert.Equal(t, http.StatusNoContent, doCat(r, http.MethodDelete, base+"severity", "").Code, "key does not exist idempotent 204")

	missing := "/work-items/wi-x/properties/severity"
	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodGet, missing, "").Code)
	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodPut, missing, `"x"`).Code)
	assert.Equal(t, http.StatusBadRequest, doCat(r, http.MethodPut, base+"notes", `{"broken":`).Code)
}

func TestPropertyDefManagement(t *testing.T) {
	r, _ := newCatalogRouter(t)
	base := "/properties/"

	rec := doCat(r, http.MethodGet, base+"severity", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var def app.PropertyDef
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &def))
	assert.Equal(t, "select", def.Type)

	rec = doCat(r, http.MethodPatch, base+"severity",
		`{"name":"critical level","description":"P0-P2","options":["low","mid","high"]}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &def))
	assert.Equal(t, "critical level", def.Name)
	assert.Equal(t, "P0-P2", def.Description)
	assert.Equal(t, []string{"low", "mid", "high"}, def.Options)
	rec = doCat(r, http.MethodPatch, base+"severity", `{}`)
	require.Equal(t, http.StatusOK, rec.Code, "empty patch idempotent")
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &def))
	assert.Equal(t, "critical level", def.Name)

	rec = doCat(r, http.MethodPatch, base+"severity", `{"options":[]}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "E_VALIDATION", codeOf(t, rec))

	assert.Equal(t, http.StatusNoContent, doCat(r, http.MethodDelete, base+"severity", "").Code)
	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodGet, base+"severity", "").Code)
	assert.Equal(t, http.StatusNoContent, doCat(r, http.MethodDelete, base+"severity", "").Code)
	rec = doCat(r, http.MethodGet, "/properties", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var defs []app.PropertyDef
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &defs))
	for _, d := range defs {
		assert.NotEqual(t, "severity", d.Key, "deleted definitions absent from the list")
	}

	for _, m := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		body := ""
		if m == http.MethodPatch {
			body = `{}`
		}
		assert.Equal(t, http.StatusNotFound, doCat(r, m, base+"nope", body).Code)
	}
}

func TestStatusPatch(t *testing.T) {
	r, _ := newCatalogRouter(t)

	rec := doCat(r, http.MethodPatch, "/work-item-statuses/verifying",
		`{"name":"verifying (strict)","icon":"🔬","category":"done"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var def app.StatusDef
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &def))
	assert.Equal(t, "verifying", def.Key, "keys are immutable (the path is the key)")
	assert.Equal(t, "verifying (strict)", def.Name)
	assert.Equal(t, "🔬", def.Icon)
	assert.Equal(t, "done", def.Category)

	rec = doCat(r, http.MethodPatch, "/work-item-statuses/todo", `{"name":"todo","icon":"📥"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &def))
	assert.Equal(t, "todo", def.Name)
	assert.Equal(t, "📥", def.Icon)
	rec = doCat(r, http.MethodPatch, "/work-item-statuses/todo", `{"category":"done"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "built-in status categories immutable")
	assert.Equal(t, "E_VALIDATION", codeOf(t, rec))

	bad := []struct {
		path string
		body string
		code int
	}{
		{"/work-item-statuses/verifying", `{"category":"bogus"}`, http.StatusBadRequest},
		{"/work-item-statuses/verifying", `{"icon":""}`, http.StatusBadRequest},
		{"/work-item-statuses/verifying", `{"icon":"` + strings.Repeat("🔥", 33) + `"}`, http.StatusBadRequest},
		{"/work-item-statuses/verifying", `{"name":""}`, http.StatusBadRequest},
		{"/work-item-statuses/nope", `{"name":"x"}`, http.StatusNotFound},
	}
	for _, tc := range bad {
		assert.Equal(t, tc.code, doCat(r, http.MethodPatch, tc.path, tc.body).Code, "body=%s", tc.body)
	}
}

func TestProjectCRUD(t *testing.T) {
	r, _ := newCatalogRouter(t)

	rec := doCat(r, http.MethodGet, "/projects/p-1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var p app.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "Alpha project", p.Name)
	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodGet, "/projects/p-x", "").Code)

	rec = doCat(r, http.MethodPut, "/projects/p-1", `{"name":"Alpha v2","description":"upgrade"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "Alpha v2", p.Name)
	assert.Equal(t, "upgrade", p.Description)
	assert.Equal(t, http.StatusBadRequest, doCat(r, http.MethodPut, "/projects/p-1", `{"name":" "}`).Code)
	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodPut, "/projects/p-x", `{"name":"x"}`).Code)

	rec = doCat(r, http.MethodDelete, "/projects/p-2", "")
	assert.Equal(t, http.StatusConflict, rec.Code, "linked work items not cleared should be 409: "+rec.Body.String())
	assert.Equal(t, "E_REVISION_CONFLICT", codeOf(t, rec), "task vocabulary has no separate reference conflict code, lands on the existing 409")
	assert.Equal(t, http.StatusNoContent, doCat(r, http.MethodDelete, "/projects/p-1", "").Code)
	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodDelete, "/projects/p-1", "").Code)

	r2, _ := newCatalogRouter(t)
	rec = doCat(r2, http.MethodGet, "/projects/search?q=beta", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var sr struct {
		Items []app.Project `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sr))
	require.Len(t, sr.Items, 1)
	assert.Equal(t, "Beta", sr.Items[0].Name)
	rec = doCat(r2, http.MethodGet, "/projects/search?q=flagship", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sr))
	assert.NotEmpty(t, sr.Items, "description ILIKE also hits")
	assert.Equal(t, http.StatusBadRequest, doCat(r2, http.MethodGet, "/projects/search?q=", "").Code)
}

func TestGetLabel(t *testing.T) {
	r, _ := newCatalogRouter(t)

	rec := doCat(r, http.MethodGet, "/labels/lab-1", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var l app.Label
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &l))
	assert.Equal(t, "lab-1", l.ID)
	assert.Equal(t, "core", l.Name)

	rec = doCat(r, http.MethodGet, "/labels/lab-x", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "missing/cross-workspace → 404 (I6)")
	assert.Equal(t, "E_NOT_FOUND", codeOf(t, rec))
}
