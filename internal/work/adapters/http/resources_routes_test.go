package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/work/app"
	"orkome/internal/work/domain"
)

func (f *fakeCatalogRepo) ListProjectResources(_ context.Context, _, projectID string) ([]app.ProjectResource, error) {
	var out []app.ProjectResource
	for _, res := range f.resources {
		if res.ProjectID == projectID {
			out = append(out, res)
		}
	}
	return out, nil
}

func (f *fakeCatalogRepo) CreateProjectResource(_ context.Context, wsID, projectID, kind, uri string) (app.ProjectResource, bool, error) {
	p, ok := f.projects[projectID]
	if !ok || p.ws != wsID {
		return app.ProjectResource{}, false, nil
	}
	res := app.ProjectResource{ID: "res-" + strconv.Itoa(len(f.resources)+1), ProjectID: projectID, Kind: kind, URI: uri}
	f.resources[res.ID] = res
	return res, true, nil
}

func (f *fakeCatalogRepo) UpdateProjectResource(_ context.Context, wsID, projectID, resourceID, kind, uri string) (app.ProjectResource, bool, error) {
	res, ok := f.resources[resourceID]
	if !ok || res.ProjectID != projectID {
		return app.ProjectResource{}, false, nil
	}
	if p, ok := f.projects[projectID]; !ok || p.ws != wsID {
		return app.ProjectResource{}, false, nil
	}
	res.Kind, res.URI = kind, uri
	f.resources[resourceID] = res
	return res, true, nil
}

func (f *fakeCatalogRepo) DeleteProjectResource(_ context.Context, wsID, projectID, resourceID string) error {
	res, ok := f.resources[resourceID]
	if !ok || res.ProjectID != projectID {
		return nil
	}
	if p, ok := f.projects[projectID]; !ok || p.ws != wsID {
		return nil
	}
	delete(f.resources, resourceID)
	return nil
}

func (f *fakeCatalogRepo) ListWorkItems(_ context.Context, q app.ListQuery) ([]app.WorkItem, string, error) {
	var out []app.WorkItem
	for _, wi := range f.items {
		if wi.WorkspaceID != q.WorkspaceID {
			continue
		}
		if len(q.StatusKeys) > 0 && !strIn(q.StatusKeys, wi.StatusKey) {
			continue
		}
		if q.AssigneeID != "" && wi.AssigneeID != q.AssigneeID {
			continue
		}
		if q.ProjectID != "" && wi.ProjectID != q.ProjectID {
			continue
		}
		if q.Involves != "" && !f.involvesHit(wi, q.Involves) {
			continue
		}
		if !propFiltersMatch(q.PropFilters, wi.Properties) {
			continue
		}
		out = append(out, wi)
	}
	if len(out) > q.Limit && q.Limit > 0 {
		out = out[:q.Limit]
		return out, f.nextCursor, nil
	}
	return out, "", nil
}

func (f *fakeCatalogRepo) involvesHit(wi app.WorkItem, user string) bool {
	if wi.AssigneeID == user || wi.CreatorID == user {
		return true
	}
	_, subscribed := f.subs[[2]string{wi.ID, user}]
	return subscribed
}

func propFiltersMatch(filters []domain.PropFilter, props map[string]any) bool {
	for _, pf := range filters {
		raw, set := props[pf.Key]
		text := ""
		if set {
			switch v := raw.(type) {
			case string:
				text = v
			case float64:
				text = strconv.FormatFloat(v, 'f', -1, 64)
			default:
				b, _ := json.Marshal(v)
				text = string(b)
			}
		}
		switch pf.Op {
		case domain.PropOpEmpty:
			if set && text != "" {
				return false
			}
		case domain.PropOpContains:
			if !set || !strings.Contains(strings.ToLower(text), strings.ToLower(pf.Value)) {
				return false
			}
		case domain.PropOpEq, domain.PropOpNe:
			if !set {
				return false
			}
			eq := text == pf.Value
			if pf.Numeric {
				a, e1 := strconv.ParseFloat(text, 64)
				b, e2 := strconv.ParseFloat(pf.Value, 64)
				eq = e1 == nil && e2 == nil && a == b
			}
			if (pf.Op == domain.PropOpEq) != eq {
				return false
			}
		case domain.PropOpGt, domain.PropOpLt:
			if !set {
				return false
			}
			gt, lt := text > pf.Value, text < pf.Value
			if pf.Numeric {
				a, _ := strconv.ParseFloat(text, 64)
				b, _ := strconv.ParseFloat(pf.Value, 64)
				gt, lt = a > b, a < b
			}
			if pf.Op == domain.PropOpGt && !gt {
				return false
			}
			if pf.Op == domain.PropOpLt && !lt {
				return false
			}
		}
	}
	return true
}

func strIn(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func TestProjectResourcesEndpoints(t *testing.T) {
	r, repo := newCatalogRouter(t)
	base := "/api/projects/p-1/resources"
	repo.projects["p-9"] = fakeProject{ws: "ws-2", p: app.Project{ID: "p-9", Name: "other-region project"}}

	rec := doCat(r, http.MethodGet, base, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Items []app.ProjectResource `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Empty(t, out.Items)

	for _, body := range []string{
		`{"kind":"git","uri":"https://x.example/r"}`,
		`{"kind":"repo","uri":"http://x.example/r"}`,
		`{"kind":"repo","uri":"git@x.example:org/r.git"}`,
		`{"kind":"local_dir","uri":"relative/path"}`,
		`{"kind":"local_dir","uri":"D:relative"}`,
		`{"uri":"https://x.example/r"}`,
	} {
		rec := doCat(r, http.MethodPost, base, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s should be 400", body)
		assert.Equal(t, "E_VALIDATION", codeOf(t, rec))
	}

	for _, m := range []string{http.MethodGet, http.MethodPost} {
		assert.Equal(t, http.StatusNotFound, doCat(r, m, "/api/projects/p-404/resources", `{"kind":"repo","uri":"https://x.example/r"}`).Code)
		assert.Equal(t, http.StatusNotFound, doCat(r, m, "/api/projects/p-9/resources", `{"kind":"repo","uri":"https://x.example/r"}`).Code, "cross-workspace 404")
	}

	rec = doCat(r, http.MethodPost, base, `{"kind":"repo","uri":"https://github.com/org/repo.git"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var r1 app.ProjectResource
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r1))
	assert.Equal(t, "repo", r1.Kind)
	assert.Equal(t, "https://github.com/org/repo.git", r1.URI)
	assert.Equal(t, "p-1", r1.ProjectID)

	rec = doCat(r, http.MethodPost, base, `{"kind":"local_dir","uri":"D:\\code\\miniCrew"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var r2 app.ProjectResource
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r2))
	assert.Equal(t, "local_dir", r2.Kind)

	rec = doCat(r, http.MethodGet, base, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Len(t, out.Items, 2)

	rec = doCat(r, http.MethodPut, base+"/"+r1.ID, `{"kind":"local_dir","uri":"/srv/data/repo"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var upd app.ProjectResource
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &upd))
	assert.Equal(t, "local_dir", upd.Kind)
	assert.Equal(t, "/srv/data/repo", upd.URI)
	assert.Equal(t, http.StatusBadRequest, doCat(r, http.MethodPut, base+"/"+r1.ID, `{"kind":"repo","uri":"ftp://x"}`).Code)
	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodPut, base+"/res-404", `{"kind":"repo","uri":"https://x.example/r"}`).Code)

	assert.Equal(t, http.StatusNoContent, doCat(r, http.MethodDelete, base+"/"+r1.ID, "").Code)
	assert.Equal(t, http.StatusNoContent, doCat(r, http.MethodDelete, base+"/"+r1.ID, "").Code, "idempotent re-delete 204")
	rec = doCat(r, http.MethodGet, base, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Len(t, out.Items, 1)

	assert.Equal(t, http.StatusNotFound, doCat(r, http.MethodDelete, "/api/projects/p-404/resources/res-1", "").Code)
}

func TestListInvolvesThreeForms(t *testing.T) {
	r, repo := newCatalogRouter(t)
	u := "00000000-0000-4000-8000-00000000000F"
	repo.items["wi-a"] = app.WorkItem{ID: "wi-a", WorkspaceID: "ws-1", Title: "assignment hit", AssigneeType: "user", AssigneeID: u}
	repo.items["wi-b"] = app.WorkItem{ID: "wi-b", WorkspaceID: "ws-1", Title: "creator hit", CreatorType: "user", CreatorID: u}
	repo.items["wi-c"] = app.WorkItem{ID: "wi-c", WorkspaceID: "ws-1", Title: "subscription hit", CreatorType: "user", CreatorID: "u-other"}
	repo.subs[[2]string{"wi-c", u}] = app.Subscriber{WorkItemID: "wi-c", UserID: u, Source: "commenter"}
	repo.items["wi-d"] = app.WorkItem{ID: "wi-d", WorkspaceID: "ws-1", Title: "irrelevant"}
	repo.items["wi-e"] = app.WorkItem{ID: "wi-e", WorkspaceID: "ws-2", Title: "other region"}

	list := func(query string) []app.WorkItem {
		rec := doCat(r, http.MethodGet, "/api/work-items"+query, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out struct {
			Items []app.WorkItem `json:"items"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out.Items
	}

	items := list("?involves=" + u)
	require.Len(t, items, 3, "assignee+creator+subscriber three forms hit")
	got := map[string]bool{}
	for _, wi := range items {
		got[wi.ID] = true
	}
	assert.True(t, got["wi-a"] && got["wi-b"] && got["wi-c"], "three forms each named")

	assert.Empty(t, list("?involves=00000000-0000-4000-8000-00000000000E"))

	rec := doCat(r, http.MethodGet, "/api/work-items?involves=not-a-uuid", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "E_VALIDATION", codeOf(t, rec))
}

func TestListPropOperators(t *testing.T) {
	r, repo := newCatalogRouter(t)
	repo.propDefs["stack"] = fakePropDef{ws: "ws-1", def: app.PropertyDef{
		Key: "stack", Name: "Stack", Type: "multi_select", Options: []string{"go", "pg"},
	}}
	repo.items = map[string]app.WorkItem{
		"wi-1": {ID: "wi-1", WorkspaceID: "ws-1", Title: "big task", Properties: map[string]any{
			"estimate": 8.0, "notes": "fix login bug", "stack": []any{"go"},
		}},
		"wi-2": {ID: "wi-2", WorkspaceID: "ws-1", Title: "small task", Properties: map[string]any{
			"estimate": 2.0, "notes": "docs",
		}},
		"wi-3": {ID: "wi-3", WorkspaceID: "ws-1", Title: "no properties", Properties: map[string]any{}},
	}

	ids := func(query string) []string {
		rec := doCat(r, http.MethodGet, "/api/work-items"+query, "")
		require.Equal(t, http.StatusOK, rec.Code, "query=%s: %s", query, rec.Body.String())
		var out struct {
			Items []app.WorkItem `json:"items"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		ids := make([]string, 0, len(out.Items))
		for _, wi := range out.Items {
			ids = append(ids, wi.ID)
		}
		return ids
	}

	assert.ElementsMatch(t, []string{"wi-1"}, ids("?prop_estimate=gt:5"), "number gt")
	assert.ElementsMatch(t, []string{"wi-2"}, ids("?prop_estimate=lt:5"), "number lt")
	assert.ElementsMatch(t, []string{"wi-1"}, ids("?prop_estimate=eq:8"), "number eq")
	assert.ElementsMatch(t, []string{"wi-2"}, ids("?prop_estimate=ne:8"), "number ne")
	assert.ElementsMatch(t, []string{"wi-1"}, ids("?prop_notes=contains:bug"), "text contains")
	assert.ElementsMatch(t, []string{"wi-3"}, ids("?prop_notes=empty:"), "empty (unset hit)")
	assert.ElementsMatch(t, []string{"wi-1"}, ids("?prop_stack=contains:go"), "multi_select contains (jsonb text form)")

	assert.ElementsMatch(t, []string{"wi-1"}, ids("?prop_estimate=gt:5&prop_notes=contains:bug"))

	for _, q := range []string{
		"?prop_stack=gt:go",
		"?prop_estimate=gt:abc",
		"?prop_ghost=eq:x",
		"?prop_estimate=gte:5",
	} {
		rec := doCat(r, http.MethodGet, "/api/work-items"+q, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, "query=%s should be 400", q)
		assert.Equal(t, "E_VALIDATION", codeOf(t, rec))
	}
}

func TestQueryCompileRichDimensions(t *testing.T) {
	r, repo := newCatalogRouter(t)
	repo.propDefs["stack"] = fakePropDef{ws: "ws-1", def: app.PropertyDef{
		Key: "stack", Name: "Stack", Type: "multi_select", Options: []string{"go", "pg"},
	}}
	uid := "00000000-0000-4000-8000-00000000000A"

	rec := doCat(r, http.MethodPost, "/api/work-items/query", `{
		"filters": {"involves": "`+uid+`", "props": {"estimate": "gt:5", "notes": "contains:bug"}},
		"group_by": "status", "sort": ["priority"], "limit": 30
	}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Fingerprint string `json:"fingerprint"`
		Parsed      struct {
			Filters struct {
				Involves string            `json:"involves"`
				Props    map[string]string `json:"props"`
			} `json:"filters"`
			GroupBy string   `json:"group_by"`
			Sort    []string `json:"sort"`
			Limit   int      `json:"limit"`
		} `json:"parsed"`
		Errors []string `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.NotEmpty(t, out.Fingerprint)
	assert.Equal(t, uid, out.Parsed.Filters.Involves)
	assert.Equal(t, map[string]string{"estimate": "gt:5", "notes": "contains:bug"}, out.Parsed.Filters.Props)
	assert.Equal(t, []string{}, out.Errors)

	bad := []string{
		`{"filters": {"involves": "not-a-uuid"}}`,
		`{"filters": {"props": {"stack": "gt:go"}}}`,
		`{"filters": {"props": {"estimate": "gt:abc"}}}`,
		`{"filters": {"props": {"ghost": "eq:x"}}}`,
		`{"filters": {"props": {"estimate": "gte:5"}}}`,
	}
	for _, body := range bad {
		rec := doCat(r, http.MethodPost, "/api/work-items/query", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s should be 400", body)
		assert.Equal(t, "E_VALIDATION", codeOf(t, rec))
	}
}
