package http

import (
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

func (d Deps) handleGetSquad(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("squadId")
	var row map[string]any
	var err error
	if d.Squads != nil {
		row, err = d.Squads.Get(r.Context(), d.ws(r), id)
	} else {
		var ok bool
		row, ok, err = d.Repo.GetSquadRow(r.Context(), d.ws(r), id)
		if err == nil && !ok {
			err = apierr.New(apierr.NotFound, "squad not found")
		}
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, row)
}

func (d Deps) handleUpdateSquad(w http.ResponseWriter, r *http.Request) {
	if d.Squads == nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "squad service not wired", nil)
		return
	}
	in, ok := decode[struct {
		Name          string `json:"name"`
		Instructions  string `json:"instructions"`
		LeaderAgentID string `json:"leader_agent_id"`
	}](w, r)
	if !ok {
		return
	}
	out, err := d.Squads.Update(r.Context(), d.ws(r), r.PathValue("squadId"), orchapp.SquadUpdateInput{
		Name: in.Name, Instructions: in.Instructions,
		LeaderAgentID: in.LeaderAgentID,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) handleDeleteSquad(w http.ResponseWriter, r *http.Request) {
	if d.Squads == nil {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "squad service not wired", nil)
		return
	}
	if err := d.Squads.Delete(r.Context(), d.ws(r), r.PathValue("squadId")); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}
