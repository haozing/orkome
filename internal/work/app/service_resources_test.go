package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

type fakeResourcesRepo struct {
	Repo
	ws        map[string]string
	projects  map[string]Project
	resources map[string]map[string]ProjectResource
	nextID    int
}

func newFakeResourcesRepo() *fakeResourcesRepo {
	return &fakeResourcesRepo{
		ws: map[string]string{"p-1": "ws-1", "p-2": "ws-2"},
		projects: map[string]Project{
			"p-1": {ID: "p-1", Name: "Alpha"},
			"p-2": {ID: "p-2", Name: "other region"},
		},
		resources: map[string]map[string]ProjectResource{},
	}
}

func (f *fakeResourcesRepo) GetProject(_ context.Context, wsID, id string) (Project, bool, error) {
	if f.ws[id] != wsID {
		return Project{}, false, nil
	}
	return f.projects[id], true, nil
}

func (f *fakeResourcesRepo) ListProjectResources(_ context.Context, _, projectID string) ([]ProjectResource, error) {
	var out []ProjectResource
	for _, res := range f.resources[projectID] {
		out = append(out, res)
	}
	return out, nil
}

func (f *fakeResourcesRepo) CreateProjectResource(_ context.Context, _, projectID, kind, uri string) (ProjectResource, bool, error) {
	if _, ok := f.projects[projectID]; !ok {
		return ProjectResource{}, false, nil
	}
	f.nextID++
	res := ProjectResource{ID: "r-" + string(rune('0'+f.nextID)), ProjectID: projectID, Kind: kind, URI: uri}
	if f.resources[projectID] == nil {
		f.resources[projectID] = map[string]ProjectResource{}
	}
	f.resources[projectID][res.ID] = res
	return res, true, nil
}

func (f *fakeResourcesRepo) UpdateProjectResource(_ context.Context, _, projectID, resourceID, kind, uri string) (ProjectResource, bool, error) {
	res, ok := f.resources[projectID][resourceID]
	if !ok {
		return ProjectResource{}, false, nil
	}
	res.Kind, res.URI = kind, uri
	f.resources[projectID][resourceID] = res
	return res, true, nil
}

func (f *fakeResourcesRepo) DeleteProjectResource(_ context.Context, _, projectID, resourceID string) error {
	delete(f.resources[projectID], resourceID)
	return nil
}

func TestProjectResourcesCRUD(t *testing.T) {
	repo := newFakeResourcesRepo()
	svc := NewCatalogService(repo, nil)
	ctx := context.Background()

	r1, err := svc.CreateProjectResource(ctx, "ws-1", "p-1", "repo", "https://github.com/org/repo.git")
	require.NoError(t, err)
	assert.Equal(t, "repo", r1.Kind)
	assert.Equal(t, "https://github.com/org/repo.git", r1.URI)

	r2, err := svc.CreateProjectResource(ctx, "ws-1", "p-1", "local_dir", `D:\code\miniCrew`)
	require.NoError(t, err)
	assert.NotEqual(t, r1.ID, r2.ID)

	items, err := svc.ListProjectResources(ctx, "ws-1", "p-1")
	require.NoError(t, err)
	assert.Len(t, items, 2)

	upd, err := svc.UpdateProjectResource(ctx, "ws-1", "p-1", r1.ID, "local_dir", "/srv/data/repo")
	require.NoError(t, err)
	assert.Equal(t, "local_dir", upd.Kind)
	assert.Equal(t, "/srv/data/repo", upd.URI)

	require.NoError(t, svc.DeleteProjectResource(ctx, "ws-1", "p-1", r1.ID))
	require.NoError(t, svc.DeleteProjectResource(ctx, "ws-1", "p-1", r1.ID), "duplicate deletion idempotent")
	items, err = svc.ListProjectResources(ctx, "ws-1", "p-1")
	require.NoError(t, err)
	assert.Len(t, items, 1)

	_, err = svc.UpdateProjectResource(ctx, "ws-1", "p-1", "r-404", "repo", "https://x.example/r")
	assertNotFound(t, err)
}

func TestProjectResourcesProjectNotFound(t *testing.T) {
	svc := NewCatalogService(newFakeResourcesRepo(), nil)
	ctx := context.Background()
	for _, projectID := range []string{"p-404", "p-2"} {
		_, err := svc.ListProjectResources(ctx, "ws-1", projectID)
		assertNotFound(t, err)
		_, err = svc.CreateProjectResource(ctx, "ws-1", projectID, "repo", "https://x.example/r")
		assertNotFound(t, err)
		_, err = svc.UpdateProjectResource(ctx, "ws-1", projectID, "r-1", "repo", "https://x.example/r")
		assertNotFound(t, err)
		err = svc.DeleteProjectResource(ctx, "ws-1", projectID, "r-1")
		assertNotFound(t, err)
	}
}

func TestProjectResourcesValidation(t *testing.T) {
	svc := NewCatalogService(newFakeResourcesRepo(), nil)
	ctx := context.Background()

	bad := []struct{ kind, uri string }{
		{"", "https://x.example/r"},
		{"git", "https://x.example/r"},
		{"repo", "http://x.example/r"},
		{"repo", "git@x.example:org/r.git"},
		{"local_dir", "relative/path"},
		{"local_dir", `D:relative`},
		{"repo", ""},
	}
	for _, tc := range bad {
		_, err := svc.CreateProjectResource(ctx, "ws-1", "p-1", tc.kind, tc.uri)
		assertValidation(t, err, "create kind=%q uri=%q should be 400", tc.kind, tc.uri)
		_, err = svc.UpdateProjectResource(ctx, "ws-1", "p-1", "r-1", tc.kind, tc.uri)
		assertValidation(t, err, "update kind=%q uri=%q should be 400", tc.kind, tc.uri)
	}
}

func assertNotFound(t *testing.T, err error) {
	t.Helper()
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.NotFound, ae.Code)
}

func assertValidation(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Validation, ae.Code, msgAndArgs...)
}
