package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

type fakeCatalogRepo struct {
	Repo
	labels     map[string]Label
	byName     map[string]string
	statuses   []StatusDef
	properties []PropertyDef
	lastLabel  *Label
}

func newFakeCatalogRepo() *fakeCatalogRepo {
	return &fakeCatalogRepo{labels: map[string]Label{}, byName: map[string]string{}}
}

func (f *fakeCatalogRepo) EnsureStatusSeed(context.Context, string) error { return nil }
func (f *fakeCatalogRepo) ListStatuses(context.Context, string) ([]StatusDef, error) {
	return f.statuses, nil
}
func (f *fakeCatalogRepo) CreateStatus(_ context.Context, _, key, name, category string, pos int) (StatusDef, error) {
	d := StatusDef{Key: key, Name: name, Category: category, Position: pos}
	f.statuses = append(f.statuses, d)
	return d, nil
}
func (f *fakeCatalogRepo) UpsertLabel(_ context.Context, _, name, color string) (Label, error) {
	if id, ok := f.byName[name]; ok {
		l := f.labels[id]
		if color != "" {
			l.Color = color
			f.labels[id] = l
		}
		return l, nil
	}
	l := Label{ID: "lab-" + name, Name: name, Color: color}
	f.labels[l.ID] = l
	f.byName[name] = l.ID
	return l, nil
}
func (f *fakeCatalogRepo) ListLabels(context.Context, string) ([]Label, error) {
	var out []Label
	for _, l := range f.labels {
		out = append(out, l)
	}
	return out, nil
}
func (f *fakeCatalogRepo) CreatePropertyDef(_ context.Context, d PropertyDef, _ string) (PropertyDef, error) {
	f.properties = append(f.properties, d)
	return d, nil
}
func (f *fakeCatalogRepo) ListPropertyDefs(context.Context, string) ([]PropertyDef, error) {
	return f.properties, nil
}

func TestCatalogUpsertLabel_ValidationAndIdempotency(t *testing.T) {
	svc := NewCatalogService(newFakeCatalogRepo(), nil)
	ctx := context.Background()

	_, err := svc.UpsertLabel(ctx, "ws-1", "", "#f00")
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Validation, ae.Code)

	_, err = svc.UpsertLabel(ctx, "ws-1", string(make([]byte, 51)), "")
	require.ErrorAs(t, err, &ae, "name ≤50")

	l1, err := svc.UpsertLabel(ctx, "ws-1", "core", "#f00")
	require.NoError(t, err)
	l2, err := svc.UpsertLabel(ctx, "ws-1", "core", "#0f0")
	require.NoError(t, err)
	assert.Equal(t, l1.ID, l2.ID, "same-name upsert idempotent ((workspace,name) unique)")
	assert.Equal(t, "#0f0", l2.Color, "subsequent writes can change the color")
}

func TestCatalogCreateStatus_CategoryWhitelistAndAppend(t *testing.T) {
	repo := newFakeCatalogRepo()
	repo.statuses = []StatusDef{{Key: "backlog", Category: "unstarted", Position: 0}}
	svc := NewCatalogService(repo, nil)
	ctx := context.Background()

	_, err := svc.CreateStatus(ctx, "ws-1", "verifying", "in_verification")
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.Validation, ae.Code, "category must be one of the four lifecycle values (internal archive 05 §3.9)")

	d, err := svc.CreateStatus(ctx, "ws-1", "verifying", "started")
	require.NoError(t, err)
	assert.Equal(t, 1, d.Position, "custom statuses appended to the catalog tail")
	assert.Equal(t, "started", d.Category)
}
