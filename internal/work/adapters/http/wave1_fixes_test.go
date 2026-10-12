package http

// Wave-1 contract-fix regressions (docs/fix-wave1-e-work-backend-2026-10-12.md):
//   - fix 1: exhausted list pages omit next_cursor instead of sending ""
//   - fix 3: PUT /api/labels/{id} updates by path id (404 on unknown id)
//   - fix 4: batch-update rejects non-whitelisted fields with 400, refuses an
//     empty patch, and reports per-id failures
//   - fix 5: unknown query/table filter keys are rejected with 400
// (fix 2 / fix 6 need a real database: see wave1_integration_test.go)

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
	"orkome/internal/work/app"
)

// --- fake repo additions -------------------------------------------------

func (f *fakeCatalogRepo) UpdateWorkItem(_ context.Context, wsID, id string, p app.WorkItemPatch, _ int64) (app.WorkItem, error) {
	w, ok := f.items[id]
	if !ok || w.WorkspaceID != wsID {
		return app.WorkItem{}, apierr.New(apierr.NotFound, "work item not found")
	}
	if p.Title != nil {
		w.Title = *p.Title
	}
	if p.StatusKey != nil {
		w.StatusKey = *p.StatusKey
	}
	if p.Priority != nil {
		w.Priority = *p.Priority
	}
	if p.AssigneeID != nil {
		w.AssigneeID = *p.AssigneeID
	}
	if p.AssigneeType != nil {
		w.AssigneeType = *p.AssigneeType
	}
	if p.Properties != nil {
		w.Properties = p.Properties
	}
	w.Revision++
	f.items[id] = w
	return w, nil
}

func (f *fakeCatalogRepo) DeleteWorkItem(_ context.Context, wsID, id string) error {
	w, ok := f.items[id]
	if !ok || w.WorkspaceID != wsID {
		return apierr.New(apierr.NotFound, "work item not found")
	}
	delete(f.items, id)
	return nil
}

func (f *fakeCatalogRepo) UpdateLabel(_ context.Context, wsID, id, name, color string) (app.Label, bool, error) {
	l, ok := f.labels[id]
	if !ok {
		return app.Label{}, false, nil
	}
	for lid, other := range f.labels {
		if lid != id && other.Name == name {
			return app.Label{}, false, apierr.NewValidation("label name already exists")
		}
	}
	l.Name = name
	if color != "" {
		l.Color = color
	}
	f.labels[id] = l
	return l, true, nil
}

// --- fix 1: exhausted next_cursor is omitted ------------------------------

func assertNoKey(t *testing.T, key string, obj map[string]json.RawMessage, msg string) {
	t.Helper()
	if _, ok := obj[key]; ok {
		t.Errorf("%s: key %q must be absent (an empty-string cursor survived `?? undefined` and broke hasNextPage)", msg, key)
	}
}

func TestListPageExhaustedCursorOmitted(t *testing.T) {
	r, repo := newCatalogRouter(t)
	repo.items["wi-2"] = app.WorkItem{ID: "wi-2", WorkspaceID: "ws-1", Title: "second"}
	repo.items["wi-3"] = app.WorkItem{ID: "wi-3", WorkspaceID: "ws-1", Title: "third"}

	// Page full (limit cuts the 3 items down) + a next cursor: key must exist.
	repo.nextCursor = "2026-10-12T00:00:00Z|wi-2"
	rec := doCat(r, http.MethodGet, "/api/work-items?limit=2", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var page map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.NotNil(t, page["next_cursor"], "non-exhausted page keeps next_cursor")
	var cur string
	require.NoError(t, json.Unmarshal(page["next_cursor"], &cur))
	assert.Equal(t, "2026-10-12T00:00:00Z|wi-2", cur)

	// Exhausted page: next_cursor must be absent, never "".
	repo.nextCursor = ""
	rec = doCat(r, http.MethodGet, "/api/work-items?limit=2", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var fresh map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &fresh))
	assertNoKey(t, "next_cursor", fresh, "exhausted list page")

	// children endpoint obeys the same shape.
	rec = doCat(r, http.MethodGet, "/api/work-items/children?parent=wi-1&root_only=true", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var freshChildren map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &freshChildren))
	assertNoKey(t, "next_cursor", freshChildren, "exhausted children page")
}

// --- fix 3: PUT /api/labels/{id} updates by path id -----------------------

func TestLabelsPutByID(t *testing.T) {
	r, repo := newCatalogRouter(t)

	rec := doCat(r, http.MethodPut, "/api/labels/"+lab1ID, `{"name":"core-renamed","color":"#0f0"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var l app.Label
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &l))
	assert.Equal(t, lab1ID, l.ID, "id must stay the path id — no phantom upsert")
	assert.Equal(t, "core-renamed", l.Name)
	assert.Equal(t, "#0f0", l.Color)
	assert.Len(t, repo.labels, 2, "rename must not create a new label")

	// empty name rejected like create
	assert.Equal(t, http.StatusBadRequest, doCat(r, http.MethodPut, "/api/labels/"+lab1ID, `{"name":"  "}`).Code)

	// name collision with the other label → 400
	rec = doCat(r, http.MethodPut, "/api/labels/"+lab1ID, `{"name":"peripheral"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "E_VALIDATION", codeOf(t, rec))

	// unknown id → 404 (valid-shape ghost hits the repo miss; garbage id is
	// rejected before storage and must 404, not 500)
	rec = doCat(r, http.MethodPut, "/api/labels/"+labGhost, `{"name":"ghost"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "E_NOT_FOUND", codeOf(t, rec))
	rec = doCat(r, http.MethodPut, "/api/labels/lab-x", `{"name":"ghost"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "E_NOT_FOUND", codeOf(t, rec))
}

// --- fix 4: batch-update contract -----------------------------------------

func TestBatchUpdateRejectsUnsupportedFields(t *testing.T) {
	r, repo := newCatalogRouter(t)

	rec := doCat(r, http.MethodPost, "/api/work-items/batch-update",
		`{"ids":["wi-1"],"patch":{"description":"sneaky"}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "whitelist-external field must 400, not be dropped")
	assert.Equal(t, "E_VALIDATION", codeOf(t, rec))
	var e struct {
		Details []apierr.FieldError `json:"details"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
	require.Len(t, e.Details, 1)
	assert.Equal(t, "patch.description", e.Details[0].Field)
	assert.Equal(t, "todo", repo.items["wi-1"].StatusKey, "no write may happen on rejection")

	rec = doCat(r, http.MethodPost, "/api/work-items/batch-update",
		`{"ids":["wi-1"],"patch":{"description":"x","due_date":"2030-01-01","title":"ok"}}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
	var fields []string
	for _, d := range e.Details {
		fields = append(fields, d.Field)
	}
	assert.ElementsMatch(t, []string{"patch.description", "patch.due_date"}, fields, "rejected fields are enumerated")
}

func TestBatchUpdateEmptyPatchRejected(t *testing.T) {
	r, repo := newCatalogRouter(t)

	rec := doCat(r, http.MethodPost, "/api/work-items/batch-update", `{"ids":["wi-1"],"patch":{}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "empty patch must 400 instead of ghost-bumping revision")
	assert.Equal(t, "E_VALIDATION", codeOf(t, rec))
	assert.EqualValues(t, 3, repo.items["wi-1"].Revision, "revision untouched")

	rec = doCat(r, http.MethodPost, "/api/work-items/batch-update", `{"ids":["wi-1"],"patch":{"priority":"NaN"}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "patch with no applicable value is an empty patch")
	assert.EqualValues(t, 3, repo.items["wi-1"].Revision)
}

func TestBatchUpdatePartialFailureReported(t *testing.T) {
	r, repo := newCatalogRouter(t)
	repo.items["wi-2"] = app.WorkItem{ID: "wi-2", WorkspaceID: "ws-1", Title: "second", StatusKey: "todo"}

	rec := doCat(r, http.MethodPost, "/api/work-items/batch-update",
		`{"ids":["wi-1","not-a-uuid"],"patch":{"status":"todo"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Updated int            `json:"updated"`
		Failed  []batchFailure `json:"failed"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, 1, out.Updated)
	require.Len(t, out.Failed, 1, "failed ids must be listed, not silently skipped")
	assert.Equal(t, "not-a-uuid", out.Failed[0].ID)
	assert.Contains(t, out.Failed[0].Reason, "E_NOT_FOUND")

	// unknown status now surfaces per-item as a failure entry
	rec = doCat(r, http.MethodPost, "/api/work-items/batch-update",
		`{"ids":["wi-1"],"patch":{"status":"no_such"}}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, 0, out.Updated)
	require.Len(t, out.Failed, 1)
	assert.Contains(t, out.Failed[0].Reason, "E_VALIDATION")

	// empty id set with a valid patch stays a legit no-op 200
	rec = doCat(r, http.MethodPost, "/api/work-items/batch-update",
		`{"ids":[],"patch":{"status":"todo"}}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, 0, out.Updated)
	assert.Empty(t, out.Failed)
}

func TestBatchDeletePartialFailureReported(t *testing.T) {
	r, repo := newCatalogRouter(t)
	repo.items["wi-2"] = app.WorkItem{ID: "wi-2", WorkspaceID: "ws-1", Title: "second"}

	rec := doCat(r, http.MethodPost, "/api/work-items/batch-delete", `{"ids":["wi-1","wi-ghost"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Deleted int            `json:"deleted"`
		Failed  []batchFailure `json:"failed"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, 1, out.Deleted)
	require.Len(t, out.Failed, 1)
	assert.Equal(t, "wi-ghost", out.Failed[0].ID)
	assert.Contains(t, out.Failed[0].Reason, "E_NOT_FOUND")
}

// --- fix 5: unknown filter keys are rejected ------------------------------

func TestUnknownFilterKeysRejected(t *testing.T) {
	r, _ := newCatalogRouter(t)

	cases := []struct {
		name string
		path string
		body string
	}{
		{"query", "/api/work-items/query", `{"filters":{"banana":1}}`},
		{"rows", "/api/work-items/table/rows", `{"filters":{"banana":1}}`},
		{"groups", "/api/work-items/table/groups", `{"group_by":"status","filters":{"labels_x":[]}}`},
		{"facets", "/api/work-items/table/facets", `{"filters":{"foo":{"bar":1}}}`},
	}
	for _, tc := range cases {
		rec := doCat(r, http.MethodPost, tc.path, tc.body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%s: unsupported filter must 400, was silent no-op", tc.name)
		assert.Equal(t, "E_VALIDATION", codeOf(t, rec), tc.name)
		var e struct {
			Details []apierr.FieldError `json:"details"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e), tc.name)
		require.NotEmpty(t, e.Details, tc.name)
		assert.Contains(t, e.Details[0].Field, "filters.", tc.name)
	}

	// the declared filters still decode (execution itself needs a real DB,
	// covered by wave1_integration_test.go)
	rec := doCat(r, http.MethodPost, "/api/work-items/query",
		`{"filters":{"labels":["core"],"priority_min":3,"status":["todo"]}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
