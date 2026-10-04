package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"
)

func ptrTargets(ts ...InvocationTarget) *[]InvocationTarget {
	if ts == nil {
		ts = []InvocationTarget{}
	}
	return &ts
}

type agentAccessFakeRepo struct {
	Repo
	byID    map[string]Agent
	byName  map[string]Agent
	targets map[string][]InvocationTarget
	seq     int

	replaced  []replaceCall
	createdID string
}

type replaceCall struct {
	agentID string
	targets []InvocationTarget
}

func newAgentAccessFakeRepo() *agentAccessFakeRepo {
	return &agentAccessFakeRepo{
		byID: map[string]Agent{}, byName: map[string]Agent{}, targets: map[string][]InvocationTarget{},
	}
}

func (f *agentAccessFakeRepo) GetAgentByName(_ context.Context, _, name string) (Agent, bool, error) {
	a, ok := f.byName[name]
	return a, ok, nil
}

func (f *agentAccessFakeRepo) GetAgent(_ context.Context, _, id string) (Agent, bool, error) {
	a, ok := f.byID[id]
	return a, ok, nil
}

func (f *agentAccessFakeRepo) CreateAgent(_ context.Context, a Agent) (Agent, error) {
	f.seq++
	a.ID = fmt.Sprintf("a-%d", f.seq)
	if a.ConversationStarters == nil {
		a.ConversationStarters = []ConversationStarter{}
	}
	f.byID[a.ID] = a
	f.byName[a.Name] = a
	f.createdID = a.ID
	return a, nil
}

func (f *agentAccessFakeRepo) UpdateAgent(_ context.Context, a Agent) (Agent, bool, error) {
	if _, ok := f.byID[a.ID]; !ok {
		return Agent{}, false, nil
	}
	if a.ConversationStarters == nil {
		a.ConversationStarters = []ConversationStarter{}
	}
	f.byID[a.ID] = a
	f.byName[a.Name] = a
	return a, true, nil
}

func (f *agentAccessFakeRepo) ReplaceInvocationTargets(_ context.Context, _, agentID string, targets []InvocationTarget) error {
	f.replaced = append(f.replaced, replaceCall{agentID: agentID, targets: targets})
	f.targets[agentID] = targets
	return nil
}

func newAgentAccessSvc(repo Repo) *AgentService {
	svc := NewAgentService(repo, nil)
	svc.MemberCheck = func(_ context.Context, _, userID string) (bool, error) { return userID == "u-in", nil }
	return svc
}

func TestCreateAgent_StartersOverThreeRejected(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := newAgentAccessSvc(repo)
	_, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a",
		ConversationStarters: []ConversationStarter{{Label: "1"}, {Label: "2"}, {Label: "3"}, {Label: "4"}}})
	requireValidationError(t, err)
	assert.Empty(t, repo.byID, "validation failure must not create rows")
}

func TestCreateAgent_StartersLabelRuneLimit(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := newAgentAccessSvc(repo)
	base := strings.Repeat("あ", 80)
	in := Agent{WorkspaceID: "ws", Name: "a", ConversationStarters: []ConversationStarter{{Label: base}}}
	_, err := svc.Create(context.Background(), in)
	require.NoError(t, err)

	in.Name = "b"
	in.ConversationStarters[0].Label = strings.Repeat("あ", 81)
	_, err = svc.Create(context.Background(), in)
	requireValidationError(t, err)
}

func TestCreateAgent_StartersTrimAndPersist(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := newAgentAccessSvc(repo)
	_, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a",
		ConversationStarters: []ConversationStarter{{Label: "   "}}})
	requireValidationError(t, err)

	out, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "b",
		ConversationStarters: []ConversationStarter{{Label: "summarize for me  "}, {Label: "check it"}}})
	require.NoError(t, err)
	assert.Equal(t, []ConversationStarter{{Label: "summarize for me"}, {Label: "check it"}}, out.ConversationStarters)
	assert.Equal(t, []ConversationStarter{{Label: "summarize for me"}, {Label: "check it"}},
		repo.byID[repo.createdID].ConversationStarters, "persisted after trim")
}

func TestUpdateAgent_StartersNilKeepsEmptyClears(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := newAgentAccessSvc(repo)
	created, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a",
		ConversationStarters: []ConversationStarter{{Label: "old"}}})
	require.NoError(t, err)

	updated, err := svc.Update(context.Background(), Agent{ID: created.ID, WorkspaceID: "ws",
		Name: "a", Visibility: "public"})
	require.NoError(t, err)
	assert.Equal(t, []ConversationStarter{{Label: "old"}}, updated.ConversationStarters)

	updated, err = svc.Update(context.Background(), Agent{ID: created.ID, WorkspaceID: "ws",
		Name: "a", Visibility: "public", ConversationStarters: []ConversationStarter{}})
	require.NoError(t, err)
	assert.Empty(t, updated.ConversationStarters)
	assert.Empty(t, repo.byID[created.ID].ConversationStarters)
}

func TestUpdateAgent_StartersInvalidKeepsRow(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := newAgentAccessSvc(repo)
	created, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a",
		ConversationStarters: []ConversationStarter{{Label: "old"}}})
	require.NoError(t, err)

	_, err = svc.Update(context.Background(), Agent{ID: created.ID, WorkspaceID: "ws", Name: "a",
		Visibility: "public", ConversationStarters: []ConversationStarter{{Label: strings.Repeat("x", 81)}}})
	requireValidationError(t, err)
	assert.Equal(t, []ConversationStarter{{Label: "old"}}, repo.byID[created.ID].ConversationStarters)
}

func TestUpdateAgent_StartersNilMissingAgent404(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := newAgentAccessSvc(repo)
	_, err := svc.Update(context.Background(), Agent{ID: "nope", WorkspaceID: "ws", Name: "a", Visibility: "public"})
	assertErrorCode(t, err, apierr.NotFound)
}

func TestCreateAgent_TargetsRequirePublicTo(t *testing.T) {
	for _, vis := range []string{"public", "private"} {
		repo := newAgentAccessFakeRepo()
		svc := newAgentAccessSvc(repo)
		_, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a", Visibility: vis,
			InvocationTargets: ptrTargets(InvocationTarget{Type: "workspace"})})
		requireValidationError(t, err, "visibility="+vis)
		assert.Empty(t, repo.replaced)
	}
}

func TestCreateAgent_TargetsWorkspaceRowPersisted(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := newAgentAccessSvc(repo)
	out, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "workspace"})})
	require.NoError(t, err)
	assert.Equal(t, ptrTargets(InvocationTarget{Type: "workspace"}), out.InvocationTargets)
	require.Len(t, repo.replaced, 1)
	assert.Equal(t, repo.createdID, repo.replaced[0].agentID)
	assert.Equal(t, []InvocationTarget{{Type: "workspace"}}, repo.targets[repo.createdID])

	repo2 := newAgentAccessFakeRepo()
	svc2 := newAgentAccessSvc(repo2)
	_, err = svc2.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "b", Visibility: "public_to"})
	require.NoError(t, err)
	assert.Empty(t, repo2.replaced, "default targets must not trigger replacement write")
}

func TestCreateAgent_TargetsMemberValidation(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := newAgentAccessSvc(repo)

	_, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "member"})})
	requireValidationError(t, err)

	_, err = svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "b", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "member", UserID: "u-out"})})
	requireValidationError(t, err)

	out, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "c", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "member", UserID: "u-in"})})
	require.NoError(t, err)
	assert.Equal(t, []InvocationTarget{{Type: "member", UserID: "u-in"}}, *out.InvocationTargets)
}

func TestCreateAgent_TargetsMemberCheckErrorPropagates(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := NewAgentService(repo, nil)
	boom := errors.New("member store down")
	svc.MemberCheck = func(context.Context, string, string) (bool, error) { return false, boom }
	_, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "member", UserID: "u-in"})})
	assert.ErrorIs(t, err, boom)
}

func TestCreateAgent_TargetsMemberCheckNilFailClosed(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := NewAgentService(repo, nil)
	_, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "member", UserID: "u-in"})})
	requireValidationError(t, err)

	_, err = svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "b", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "workspace"})})
	require.NoError(t, err)
}

func TestCreateAgent_TargetsTypeAndDuplicateRules(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := newAgentAccessSvc(repo)

	_, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "org"})})
	requireValidationError(t, err)

	_, err = svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "b", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "workspace", UserID: "u-in"})})
	requireValidationError(t, err)

	_, err = svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "c", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "member", UserID: "u-in"},
			InvocationTarget{Type: "member", UserID: "u-in"})})
	requireValidationError(t, err)
}

func TestUpdateAgent_TargetsNilKeepsEmptyClears(t *testing.T) {
	repo := newAgentAccessFakeRepo()
	svc := newAgentAccessSvc(repo)
	created, err := svc.Create(context.Background(), Agent{WorkspaceID: "ws", Name: "a", Visibility: "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "workspace"})})
	require.NoError(t, err)
	require.Len(t, repo.replaced, 1)

	_, err = svc.Update(context.Background(), Agent{ID: created.ID, WorkspaceID: "ws",
		Name: "a", Visibility: "public_to"})
	require.NoError(t, err)
	assert.Len(t, repo.replaced, 1, "default targets must not trigger replacement write")
	assert.Equal(t, []InvocationTarget{{Type: "workspace"}}, repo.targets[created.ID], "current allowlist preserved")

	_, err = svc.Update(context.Background(), Agent{ID: created.ID, WorkspaceID: "ws",
		Name: "a", Visibility: "public_to", InvocationTargets: ptrTargets()})
	require.NoError(t, err)
	assert.Empty(t, repo.targets[created.ID])

	_, err = svc.Update(context.Background(), Agent{ID: created.ID, WorkspaceID: "ws", Name: "a",
		Visibility:        "public_to",
		InvocationTargets: ptrTargets(InvocationTarget{Type: "member", UserID: "u-in"})})
	require.NoError(t, err)
	assert.Equal(t, []InvocationTarget{{Type: "member", UserID: "u-in"}}, repo.targets[created.ID])
}

func requireValidationError(t *testing.T, err error, msgAndArgs ...interface{}) {
	t.Helper()
	require.Error(t, err, msgAndArgs...)
	var ae *apierr.Error
	require.ErrorAs(t, err, &ae, msgAndArgs...)
	assert.Equal(t, apierr.Validation, ae.Code, msgAndArgs...)
}

func assertErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae *apierr.Error
	if assert.ErrorAs(t, err, &ae) {
		assert.Equal(t, code, ae.Code)
	}
}
