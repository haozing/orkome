package http

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"orkome/internal/contract/apierr"
	"orkome/internal/identity/app"
	"orkome/internal/platform/webx"
)

type Deps struct {
	Sessions   *app.SessionService
	Tokens     *app.TokenService
	Workspaces *app.WorkspaceService
	Pins       *app.PinService
	Repo       app.Repo
	Auth       *webx.AuthConfig

	UITokens *app.UITokenService
	CSRFKey  []byte
	Limiter  webx.Limiter

	SendCodePerMinute int

	VerifyCodePerMinute int

	TrustedProxies []*net.IPNet
	Version        string
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
		return
	}
	status := http.StatusInternalServerError
	switch ae.Code {
	case apierr.Unauthenticated:
		status = http.StatusUnauthorized
	case apierr.Forbidden:
		status = http.StatusForbidden
	case apierr.NotFound:
		status = http.StatusNotFound
	case apierr.Validation, apierr.Conflict:
		status = http.StatusBadRequest
	case apierr.RateLimited:
		status = http.StatusTooManyRequests
	}
	webx.WriteError(w, status, ae.Code, ae.Message, ae.Details)
}

func decode[T any](w http.ResponseWriter, r *http.Request) (*T, bool) {
	var in T
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return nil, false
	}
	return &in, true
}

func principal(r *http.Request) *webx.Principal { return webx.PrincipalFrom(r.Context()) }

func (d Deps) AuthCfg() *webx.AuthConfig { return d.Auth }

func (d Deps) PAT() webx.PATLookup { return d.Tokens }

func requirePrincipal(w http.ResponseWriter, r *http.Request) (*webx.Principal, bool) {
	p := principal(r)
	if p == nil {
		webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "authentication required", nil)
		return nil, false
	}
	return p, true
}

func (d Deps) handleSendCode(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Email string `json:"email"`
	}](w, r)
	if !ok {
		return
	}
	if err := d.Sessions.SendCode(r.Context(), in.Email); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"sent": true})
}

func (d Deps) handleVerifyCode(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}](w, r)
	if !ok {
		return
	}
	res, err := d.Sessions.VerifyCode(r.Context(), in.Email, in.Code)
	if err != nil {
		writeErr(w, err)
		return
	}
	d.Auth.SetSessionCookie(w, res.Token, res.Expires)
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":       res.User,
		"csrf_token": webx.CSRFMaskedToken(r),
	})
}

func (d Deps) handleRefresh(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	token, exp, err := d.Sessions.Reissue(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	d.Auth.SetSessionCookie(w, token, exp)
	webx.WriteJSON(w, http.StatusOK, map[string]any{"csrf_token": webx.CSRFMaskedToken(r)})
}

func (d Deps) handleLogout(w http.ResponseWriter, r *http.Request) {
	d.Auth.ClearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleConfig(w http.ResponseWriter, r *http.Request) {
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"version":    d.Version,
		"csrf_token": webx.CSRFMaskedToken(r),
	})
}

func (d Deps) handleCLIToken(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	in, _ := decode[struct {
		Name string `json:"name"`
		TTLS int    `json:"ttl_seconds"`
	}](w, r)
	var ttl *time.Duration
	if in != nil && in.TTLS > 0 {
		t := time.Duration(in.TTLS) * time.Second
		ttl = &t
	}
	name := "cli"
	if in != nil && in.Name != "" {
		name = in.Name
	}
	res, err := d.Tokens.Create(r.Context(), p.UserID, name, ttl)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{
		"token": res.Plaintext,
		"id":    res.PAT.ID, "name": res.PAT.Name, "prefix": res.PAT.Prefix,
	})
}

func (d Deps) handleMe(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	user, wss, err := d.Sessions.Me(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"user": user, "workspaces": wss})
}

func (d Deps) handlePatchMe(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	in, ok := decode[struct {
		DisplayName string `json:"display_name"`
		Language    string `json:"language"`
		Timezone    string `json:"timezone"`
	}](w, r)
	if !ok {
		return
	}
	user, err := d.Sessions.UpdateMe(r.Context(), p, in.DisplayName, in.Language, in.Timezone)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, user)
}

func (d Deps) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	wss, err := d.Repo.ListMyWorkspaces(r.Context(), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, wss)
}

func (d Deps) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	in, ok := decode[struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}](w, r)
	if !ok {
		return
	}
	ws, err := d.Workspaces.Create(r.Context(), p.UserID, in.Name, in.Slug)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, ws)
}

func (d Deps) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	ws, err := d.Workspaces.Get(r.Context(), chi.URLParam(r, "workspaceId"), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, ws)
}

func (d Deps) handlePatchWorkspace(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	in, ok := decode[struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}](w, r)
	if !ok {
		return
	}
	ws, err := d.Workspaces.Update(r.Context(), chi.URLParam(r, "workspaceId"), p.UserID, in.Name, in.Slug)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, ws)
}

func (d Deps) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	if err := d.Workspaces.Delete(r.Context(), chi.URLParam(r, "workspaceId"), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleListMembers(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	wsID := chi.URLParam(r, "workspaceId")
	ms, err := d.Workspaces.ListMembers(r.Context(), wsID, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, ms)
}

func (d Deps) handleInviteMember(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}](w, r)
	if !ok {
		return
	}
	inv, err := d.Workspaces.InviteMember(r.Context(), chi.URLParam(r, "workspaceId"), p.UserID, in.Email, in.Role)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, inv)
}

func (d Deps) handleUpdateMember(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		Role string `json:"role"`
	}](w, r)
	if !ok {
		return
	}
	m, err := d.Workspaces.UpdateMemberRole(r.Context(), chi.URLParam(r, "workspaceId"), chi.URLParam(r, "memberId"), in.Role, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, m)
}

func (d Deps) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := d.Workspaces.RemoveMember(r.Context(), chi.URLParam(r, "workspaceId"), chi.URLParam(r, "memberId"), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleLeave(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := d.Workspaces.Leave(r.Context(), chi.URLParam(r, "workspaceId"), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleMyInvitations(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	invs, err := d.Workspaces.ListMyInvitations(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, invs)
}

func (d Deps) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	invs, err := d.Workspaces.ListWorkspaceInvitations(r.Context(), chi.URLParam(r, "workspaceId"), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, invs)
}

func (d Deps) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := d.Workspaces.RevokeInvitation(r.Context(), chi.URLParam(r, "workspaceId"), chi.URLParam(r, "invitationId"), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	m, err := d.Workspaces.AcceptInvitation(r.Context(), chi.URLParam(r, "invitationId"), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, m)
}

func (d Deps) handleDeclineInvitation(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	if err := d.Workspaces.DeclineInvitation(r.Context(), chi.URLParam(r, "invitationId"), p); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleCreateShareLink(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		Role    string `json:"role"`
		MaxUses int    `json:"max_uses"`
		TTLHrs  int    `json:"ttl_hours"`
	}](w, r)
	if !ok {
		return
	}
	ttl := time.Duration(in.TTLHrs) * time.Hour
	sl, err := d.Workspaces.CreateShareLink(r.Context(), chi.URLParam(r, "workspaceId"), p.UserID, in.Role, in.MaxUses, ttl)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, sl)
}

func (d Deps) handleListShareLinks(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	ls, err := d.Workspaces.ListShareLinks(r.Context(), chi.URLParam(r, "workspaceId"), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, ls)
}

func (d Deps) handleDeleteShareLink(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := d.Workspaces.DeleteShareLink(r.Context(), chi.URLParam(r, "workspaceId"), chi.URLParam(r, "linkId"), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handlePeekShareLink(w http.ResponseWriter, r *http.Request) {
	sl, err := d.Workspaces.PeekShareLink(r.Context(), chi.URLParam(r, "code"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_name": sl.WorkspaceName, "role": sl.Role, "expires_at": sl.ExpiresAt,
	})
}

func (d Deps) handleJoinByCode(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	in, ok := decode[struct {
		Code string `json:"code"`
	}](w, r)
	if !ok {
		return
	}
	m, err := d.Workspaces.JoinByCode(r.Context(), in.Code, p)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, m)
}

func (d Deps) handleListTokens(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	toks, err := d.Tokens.List(r.Context(), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, toks)
}

func (d Deps) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	if p.AgentID != "" {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "runtime credential cannot mint tokens", nil)
		return
	}
	in, ok := decode[struct {
		Name string `json:"name"`
		TTLS int    `json:"ttl_seconds"`
	}](w, r)
	if !ok {
		return
	}
	var ttl *time.Duration
	if in.TTLS > 0 {
		t := time.Duration(in.TTLS) * time.Second
		ttl = &t
	}
	name := "token"
	if in.Name != "" {
		name = in.Name
	}
	res, err := d.Tokens.Create(r.Context(), p.UserID, name, ttl)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{
		"token": res.Plaintext, "id": res.PAT.ID, "name": res.PAT.Name, "prefix": res.PAT.Prefix,
	})
}

func (d Deps) handleRenewToken(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok || p.Source != webx.SourcePAT || p.PATID == "" {
		return
	}
	bearer := bearerToken(r)
	in, _ := decode[struct {
		Name string `json:"name"`
	}](w, r)
	name := "token"
	if in != nil && in.Name != "" {
		name = in.Name
	}
	res, err := d.Tokens.Renew(r.Context(), bearer, name)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"token": res.Plaintext, "id": res.PAT.ID, "name": res.PAT.Name, "prefix": res.PAT.Prefix,
	})
}

func (d Deps) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	if err := d.Tokens.Revoke(r.Context(), chi.URLParam(r, "tokenId"), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func bearerToken(r *http.Request) string {
	const p = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(p) && h[:len(p)] == p {
		return h[len(p):]
	}
	return ""
}

func (d Deps) pinsService() *app.PinService {
	if d.Pins != nil {
		return d.Pins
	}
	return app.NewPinService(d.Repo)
}

func (d Deps) handleListPins(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	pins, err := d.pinsService().List(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if pins == nil {
		pins = []app.Pin{}
	}
	webx.WriteJSON(w, http.StatusOK, pins)
}

func (d Deps) handleAddPin(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		ItemType string `json:"item_type"`
		ItemID   string `json:"item_id"`
	}](w, r)
	if !ok {
		return
	}
	pin, created, err := d.pinsService().Add(r.Context(), p.WorkspaceID, p.UserID, in.ItemType, in.ItemID)
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	webx.WriteJSON(w, status, pin)
}

func (d Deps) handleReorderPins(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		Order []string `json:"order"`
	}](w, r)
	if !ok {
		return
	}
	if err := d.pinsService().Reorder(r.Context(), p.WorkspaceID, p.UserID, in.Order); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleRemovePin(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := d.pinsService().Remove(r.Context(), p.WorkspaceID, p.UserID,
		chi.URLParam(r, "itemType"), chi.URLParam(r, "itemId")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
