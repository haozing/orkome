package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

func (f *fakeRepo) InsertRuleVersion(_ context.Context, v RuleVersion) (RuleVersion, error) {
	f.ruleVersions = append(f.ruleVersions, v)
	v.ID = f.seq("rv")
	return v, nil
}

func (f *fakeRepo) ListCollaborators(_ context.Context, _, autopilotID string) ([]Collaborator, error) {
	return f.collaborators[autopilotID], nil
}

func (f *fakeRepo) AddCollaborator(_ context.Context, _, autopilotID, userID, grantedBy string) (bool, error) {
	if _, ok := f.autopilots[autopilotID]; !ok {
		return false, nil
	}
	list := f.collaborators[autopilotID]
	for _, c := range list {
		if c.UserID == userID {
			return true, nil
		}
	}
	f.collaborators[autopilotID] = append(list, Collaborator{
		AutopilotID: autopilotID, UserID: userID, UserType: "member", GrantedBy: grantedBy})
	return true, nil
}

func (f *fakeRepo) RemoveCollaborator(_ context.Context, _, autopilotID, userID string) (bool, error) {
	list := f.collaborators[autopilotID]
	for i, c := range list {
		if c.UserID == userID {
			f.collaborators[autopilotID] = append(list[:i:i], list[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func TestCreateAutopilot_LandsRuleVersionRow(t *testing.T) {
	repo := newFakeRepo()
	svc := NewAutopilotService(repo, nil, &fakeEnqueuer{})

	ap, _, err := svc.Create(context.Background(), Autopilot{
		WorkspaceID: "ws-1", Name: "daily report", ExecMode: "create_work_item",
		Template: map[string]any{"title": "daily report {{date}}"}, TargetAgentID: "ag-1",
		CreatedBy: "u-creator",
	}, "0 9 * * *", "Asia/Shanghai")
	require.NoError(t, err)
	require.NotNil(t, ap)

	require.Len(t, repo.ruleVersions, 1, "creation is the first publish: exactly one audit row")
	rv := repo.ruleVersions[0]
	assert.Equal(t, ap.ID, rv.AutopilotID)
	assert.Equal(t, "ws-1", rv.WorkspaceID)
	assert.Equal(t, "member", rv.PublishedByType)
	assert.Equal(t, "u-creator", rv.PublishedByID)
	assert.Contains(t, string(rv.ConfigSummary), `"exec_mode":"create_work_item"`)
	assert.Contains(t, string(rv.ConfigSummary), `"target_agent_id":"ag-1"`)
	assert.Contains(t, string(rv.ConfigSummary), `"daily_limit":`)
	assert.Contains(t, string(rv.ConfigSummary), `daily report {{date}}`, "template into the summary")
}

func TestCollaboratorCRUD(t *testing.T) {
	repo := newFakeRepo()
	repo.autopilots["ap-1"] = Autopilot{ID: "ap-1", WorkspaceID: "ws-1", Name: "x", Enabled: true}
	svc := NewAutopilotService(repo, nil, &fakeEnqueuer{})
	ctx := context.Background()

	_, err := svc.AddCollaborator(ctx, "ws-1", "ap-1", "", "granter")
	require.Error(t, err)

	got, err := svc.AddCollaborator(ctx, "ws-1", "ap-1", "u-9", "granter")
	require.NoError(t, err)
	assert.Equal(t, "u-9", got.UserID)
	assert.Equal(t, "granter", got.GrantedBy)
	_, err = svc.AddCollaborator(ctx, "ws-1", "ap-1", "u-9", "granter-2")
	require.NoError(t, err)
	list, err := svc.ListCollaborators(ctx, "ws-1", "ap-1")
	require.NoError(t, err)
	assert.Len(t, list, 1, "repeated grants idempotent (ON CONFLICT semantics)")

	_, err = svc.AddCollaborator(ctx, "ws-1", "ap-404", "u-9", "granter")
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.NotFound, ae.Code)
	_, err = svc.ListCollaborators(ctx, "ws-1", "ap-404")
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, apierr.NotFound, ae.Code)

	require.NoError(t, svc.RemoveCollaborator(ctx, "ws-1", "ap-1", "u-none"))
	require.NoError(t, svc.RemoveCollaborator(ctx, "ws-1", "ap-1", "u-9"))
	list, err = svc.ListCollaborators(ctx, "ws-1", "ap-1")
	require.NoError(t, err)
	assert.Empty(t, list)
}
