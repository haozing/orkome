package pgrepo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	autopapp "orkome/internal/automation/app"
)

func TestRuleVersion_AppendOnlyActiveIsLatest(t *testing.T) {
	f := newQuotaFixture(t)

	v1, err := f.repo.InsertRuleVersion(f.ctx, autopapp.RuleVersion{
		WorkspaceID: f.wsID, AutopilotID: f.apID,
		PublishedByType: "member", PublishedByID: "00000000-0000-0000-0000-0000000000aa",
		ConfigSummary: []byte(`{"name":"v1"}`),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, v1.ID)
	assert.False(t, v1.CreatedAt.IsZero(), "RETURNING backfills created_at")
	v2, err := f.repo.InsertRuleVersion(f.ctx, autopapp.RuleVersion{
		WorkspaceID: f.wsID, AutopilotID: f.apID,
		PublishedByType: "member", PublishedByID: "00000000-0000-0000-0000-0000000000bb",
		ConfigSummary: []byte(`{"name":"v2"}`),
	})
	require.NoError(t, err)

	var activeName string
	require.NoError(t, f.pool.QueryRow(f.ctx, `
SELECT config_summary->>'name' FROM autopilot_rule_version
WHERE workspace_id=$1 AND autopilot_id=$2
ORDER BY created_at DESC LIMIT 1`, f.wsID, f.apID).Scan(&activeName))
	assert.Equal(t, "v2", activeName, "the live version = latest published row")
	assert.NotEqual(t, v1.ID, v2.ID)

	v3, err := f.repo.InsertRuleVersion(f.ctx, autopapp.RuleVersion{
		WorkspaceID: f.wsID, AutopilotID: f.apID, PublishedByType: "member"})
	require.NoError(t, err)
	var raw []byte
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`SELECT config_summary FROM autopilot_rule_version WHERE id=$1`, v3.ID).Scan(&raw))
	assert.Equal(t, "{}", string(raw))
}

func TestCollaborators_CRUDAndPredicate(t *testing.T) {
	f := newQuotaFixture(t)
	var granter, member string
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('granter@x.dev','g') RETURNING id::text`).Scan(&granter))
	require.NoError(t, f.pool.QueryRow(f.ctx,
		`INSERT INTO "user" (email, display_name) VALUES ('member@x.dev','m') RETURNING id::text`).Scan(&member))

	ok, err := f.repo.AddCollaborator(f.ctx, f.wsID, f.apID, member, granter)
	require.NoError(t, err)
	assert.True(t, ok, "ownership hit grants")

	ok, err = f.repo.AddCollaborator(f.ctx, f.wsID, f.apID, member, granter)
	require.NoError(t, err)
	assert.True(t, ok, "already authorized = idempotent success (0-row ambiguity resolved by ownership existence)")
	list, err := f.repo.ListCollaborators(f.ctx, f.wsID, f.apID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, member, list[0].UserID)
	assert.Equal(t, granter, list[0].GrantedBy)

	otherWS := "00000000-0000-0000-0000-0000000000ff"
	ok, err = f.repo.AddCollaborator(f.ctx, otherWS, f.apID, member, granter)
	require.NoError(t, err)
	assert.False(t, ok)
	ls, err := f.repo.ListCollaborators(f.ctx, otherWS, f.apID)
	require.NoError(t, err)
	assert.Empty(t, ls)

	ok, err = f.repo.RemoveCollaborator(f.ctx, f.wsID, f.apID, member)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = f.repo.RemoveCollaborator(f.ctx, f.wsID, f.apID, member)
	require.NoError(t, err)
	assert.False(t, ok)
	list, err = f.repo.ListCollaborators(f.ctx, f.wsID, f.apID)
	require.NoError(t, err)
	assert.Empty(t, list)
}
