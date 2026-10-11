package http

import (
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
)

func runtimeAgentID(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := webx.PrincipalFrom(r.Context())
	if p == nil || p.AgentID == "" {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "runtime credential required", nil)
		return "", false
	}
	return p.AgentID, true
}

func (d Deps) handleExternalClaim(w http.ResponseWriter, r *http.Request) {
	agentID, ok := runtimeAgentID(w, r)
	if !ok {
		return
	}
	t, err := d.External.Claim(r.Context(), d.ws(r), r.PathValue("taskId"), agentID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"task": t})
}

func (d Deps) handleExternalMessage(w http.ResponseWriter, r *http.Request) {
	agentID, ok := runtimeAgentID(w, r)
	if !ok {
		return
	}
	in, ok := decode[struct {
		Kind    string `json:"kind"`
		CallID  string `json:"call_id"`
		Content string `json:"content"`
	}](w, r)
	if !ok {
		return
	}
	seq, err := d.External.ReportMessage(r.Context(), d.ws(r), r.PathValue("taskId"), agentID,
		in.Kind, in.CallID, in.Content)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"seq": seq})
}

func (d Deps) handleExternalComplete(w http.ResponseWriter, r *http.Request) {
	agentID, ok := runtimeAgentID(w, r)
	if !ok {
		return
	}
	in, ok := decode[struct {
		Result string `json:"result"`
	}](w, r)
	if !ok {
		return
	}
	if err := d.External.Complete(r.Context(), d.ws(r), r.PathValue("taskId"), agentID, in.Result); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "completed"})
}

func (d Deps) handleExternalFail(w http.ResponseWriter, r *http.Request) {
	agentID, ok := runtimeAgentID(w, r)
	if !ok {
		return
	}
	in, ok := decode[struct {
		Reason string `json:"reason"`
		Detail string `json:"detail"`
	}](w, r)
	if !ok {
		return
	}
	if err := d.External.Fail(r.Context(), d.ws(r), r.PathValue("taskId"), agentID, in.Reason, in.Detail); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "failed"})
}

func (d Deps) handleMintRuntimeCredential(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	in, ok := decode[struct {
		Name string `json:"name"`
	}](w, r)
	if !ok {
		return
	}
	token, err := d.MintRuntimeCredential(r.Context(), d.ws(r), r.PathValue("agentId"), in.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{"token": token})
}
