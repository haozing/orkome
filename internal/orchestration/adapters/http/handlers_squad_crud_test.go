package http

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	orchapp "orkome/internal/orchestration/app"
)

type squadUpdateCall struct {
	squadID, name, instructions, leader string
	joinLeader                          bool
}

type squadFakeRepo struct {
	orchapp.Repo
	squads  map[string]map[string]any
	members map[string][]orchapp.SquadMemberRow
	agents  map[string]bool
	updates []squadUpdateCall
	deletes []string
	delRes  orchapp.SquadDeleteResult
}

func (f *squadFakeRepo) GetSquadRow(_ context.Context, _, squadID string) (map[string]any, bool, error) {
	row, ok := f.squads[squadID]
	return row, ok, nil
}

func (f *squadFakeRepo) SquadNameTaken(_ context.Context, _, name, exclude string) (bool, error) {
	for id, row := range f.squads {
		if id != exclude && row["name"] == name {
			return true, nil
		}
	}
	return false, nil
}

func (f *squadFakeRepo) UpdateSquad(_ context.Context, _, squadID, name, instructions, leader string, join bool) (map[string]any, bool, error) {
	f.updates = append(f.updates, squadUpdateCall{squadID, name, instructions, leader, join})
	row, ok := f.squads[squadID]
	if !ok {
		return nil, false, nil
	}
	row["name"], row["instructions"], row["leader_agent_id"] = name, instructions, leader
	if join {
		if f.members == nil {
			f.members = map[string][]orchapp.SquadMemberRow{}
		}
		f.members[squadID] = append(f.members[squadID], orchapp.SquadMemberRow{AgentID: leader, Role: "leader"})
		m, _ := row["members"].([]string)
		row["members"] = append(m, leader)
	}
	return row, true, nil
}

func (f *squadFakeRepo) DeleteSquad(_ context.Context, _, squadID string) (orchapp.SquadDeleteResult, error) {
	f.deletes = append(f.deletes, squadID)
	if f.delRes == orchapp.SquadDeleted {
		delete(f.squads, squadID)
	}
	return f.delRes, nil
}

func (f *squadFakeRepo) GetAgent(_ context.Context, _, agentID string) (orchapp.Agent, bool, error) {
	return orchapp.Agent{}, f.agents[agentID], nil
}

func (f *squadFakeRepo) ListSquadMembers(_ context.Context, _, squadID string) ([]orchapp.SquadMemberRow, error) {
	return f.members[squadID], nil
}

func newSquadCRUDRouter(repo orchapp.Repo, withService bool) http.Handler {
	d := Deps{Repo: repo,
		MemberCheck: func(context.Context, string, string) (bool, error) { return true, nil }}
	if withService {
		d.Squads = orchapp.NewSquadService(repo, nil)
	}
	r := http.NewServeMux()
	Mount(r, d, passAuth)
	return r
}

func TestSquadCRUD_GetAndPut(t *testing.T) {
	repo := &squadFakeRepo{
		squads: map[string]map[string]any{"sq1": {
			"id": "sq1", "name": "old name", "instructions": "old instruction",
			"leader_agent_id": "a1", "members": []string{"a1"},
		}},
		members: map[string][]orchapp.SquadMemberRow{"sq1": {{AgentID: "a1", Role: "leader"}}},
		agents:  map[string]bool{"a1": true, "a2": true},
	}
	r := newSquadCRUDRouter(repo, true)

	rec := agentLabelsReq(r, "GET", "/api/squads/sq1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "old name")
	assert.Equal(t, http.StatusNotFound, agentLabelsReq(r, "GET", "/api/squads/none", "").Code)

	rec = agentLabelsReq(r, "PUT", "/api/squads/sq1",
		`{"name":"new name","instructions":"new instruction","leader_agent_id":"a1"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, repo.updates, 1)
	assert.Equal(t, "new name", repo.updates[0].name)
	assert.False(t, repo.updates[0].joinLeader, "leader unchanged → no member entry needed")

	rec = agentLabelsReq(r, "PUT", "/api/squads/sq1", `{"name":"new name","leader_agent_id":"a2"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, repo.updates, 2)
	assert.True(t, repo.updates[1].joinLeader, "non-member new leader → joins in the same transaction")
	assert.Contains(t, repo.squads["sq1"]["members"], "a2")

	assert.Equal(t, http.StatusBadRequest,
		agentLabelsReq(r, "PUT", "/api/squads/sq1", `{"name":"x","leader_agent_id":"ghost"}`).Code)
	require.Len(t, repo.updates, 2)

	assert.Equal(t, http.StatusBadRequest, agentLabelsReq(r, "PUT", "/api/squads/sq1", `{"name":""}`).Code)
	assert.Equal(t, http.StatusNotFound, agentLabelsReq(r, "PUT", "/api/squads/none", `{"name":"x"}`).Code)

	repo.squads["sq2"] = map[string]any{"id": "sq2", "name": "occupied", "members": []string{}}
	assert.Equal(t, http.StatusConflict, agentLabelsReq(r, "PUT", "/api/squads/sq1", `{"name":"occupied"}`).Code)

	rec = agentLabelsReq(r, "PUT", "/api/squads/sq1", `{"name":"new name","leader_agent_id":""}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "", repo.squads["sq1"]["leader_agent_id"])
}

func TestSquadCRUD_DeleteGates(t *testing.T) {
	cases := []struct {
		res  orchapp.SquadDeleteResult
		code int
	}{
		{orchapp.SquadNotFound, http.StatusNotFound},
		{orchapp.SquadHasMembers, http.StatusConflict},
		{orchapp.SquadHasActiveTasks, http.StatusConflict},
	}
	for _, c := range cases {
		repo := &squadFakeRepo{
			squads: map[string]map[string]any{"sq1": {"id": "sq1", "name": "n", "members": []string{}}},
			delRes: c.res,
		}
		rec := agentLabelsReq(newSquadCRUDRouter(repo, true), "DELETE", "/api/squads/sq1", "")
		assert.Equal(t, c.code, rec.Code, "repo rulings %d", c.res)
		require.Len(t, repo.deletes, 1)
	}

	repo := &squadFakeRepo{
		squads: map[string]map[string]any{"sq1": {"id": "sq1", "name": "n", "members": []string{}}},
		delRes: orchapp.SquadDeleted,
	}
	r := newSquadCRUDRouter(repo, true)
	rec := agentLabelsReq(r, "DELETE", "/api/squads/sq1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "deleted")
	assert.Equal(t, http.StatusNotFound, agentLabelsReq(r, "GET", "/api/squads/sq1", "").Code)
}

func TestSquadCRUD_NoServiceFallback(t *testing.T) {
	repo := &squadFakeRepo{squads: map[string]map[string]any{
		"sq1": {"id": "sq1", "name": "n", "members": []string{}}}}
	r := newSquadCRUDRouter(repo, false)
	assert.Equal(t, http.StatusOK, agentLabelsReq(r, "GET", "/api/squads/sq1", "").Code)
	assert.Equal(t, http.StatusNotFound, agentLabelsReq(r, "GET", "/api/squads/none", "").Code)
	assert.Equal(t, http.StatusInternalServerError, agentLabelsReq(r, "PUT", "/api/squads/sq1", `{"name":"x"}`).Code)
	assert.Equal(t, http.StatusInternalServerError, agentLabelsReq(r, "DELETE", "/api/squads/sq1", "").Code)
}
