package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	fwapp "github.com/haozing/ploykit/identity/app"
	"github.com/haozing/ploykit/platform/webx"

	"orkome/internal/contract/apierr"
	"orkome/internal/identity/app"
)

// PATMinter issues a personal access token for a principal. The framework
// identity app.TokenService satisfies this (*fwapp.TokenService).
type PATMinter interface {
	CreatePAT(ctx context.Context, p *webx.Principal, name string, ttl time.Duration, scope *webx.CredentialScope) (fwapp.PAT, string, error)
}

// Deps wires the product account-extension endpoints. Everything the
// framework already provides (login, sessions, PAT CRUD, account, and the
// whole workspace face) is intentionally absent.
type Deps struct {
	// Repo backs the X-Workspace-Id membership gate, the ui-token mint
	// membership check and (when Pins is nil) a lazily built PinService.
	Repo app.PinRepo

	// Pins is the sidebar-pin service; nil defaults to app.NewPinService(Repo).
	Pins *app.PinService

	// UITokens mints/consumes one-shot plugin-embedding tickets.
	UITokens *app.UITokenService

	// Tokens mints the PAT returned by POST /cli-token — pass the framework
	// TokenService (fwapp.NewTokenService(fwRepo, auditor, clock)).
	Tokens PATMinter
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

func principal(r *http.Request) *webx.Principal { return webx.PrincipalFromRequest(r) }

func requirePrincipal(w http.ResponseWriter, r *http.Request) (*webx.Principal, bool) {
	p := principal(r)
	if p == nil {
		webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "authentication required", nil)
		return nil, false
	}
	return p, true
}

// maxPATScopeWorkspaces caps how many workspaces one PAT may be scoped to.
// The UI submits exactly one (the current workspace); the cap exists only to
// bound hand-crafted payloads.
const maxPATScopeWorkspaces = 10

// normalizePATScope validates the optional PAT scope before it reaches the
// minter: every workspace id must be a UUID and the list is capped at
// maxPATScopeWorkspaces. A scope carrying no constraint at all (no workspace
// ids, no permissions) is collapsed to nil so a submitted empty allowlist can
// never lock a token out of every workspace — AllowsWorkspace treats nil as
// unrestricted but a non-nil empty list as allow-none.
func normalizePATScope(s *webx.CredentialScope) (*webx.CredentialScope, *apierr.Error) {
	if s == nil || (len(s.WorkspaceIDs) == 0 && len(s.Permissions) == 0) {
		return nil, nil
	}
	if len(s.WorkspaceIDs) > maxPATScopeWorkspaces {
		return nil, apierr.NewValidation("scope.workspace_ids accepts at most 10 entries",
			apierr.FieldError{Field: "scope.workspace_ids", Problem: "too_many"})
	}
	for _, id := range s.WorkspaceIDs {
		if _, err := uuid.Parse(id); err != nil {
			return nil, apierr.NewValidation("scope.workspace_ids entries must be UUIDs",
				apierr.FieldError{Field: "scope.workspace_ids", Problem: "invalid_uuid"})
		}
	}
	return s, nil
}

// handleCLIToken exchanges the caller's authenticated session for a PAT the
// CLI can store: the framework session stays untouched, the returned token
// is an ordinary framework PAT (Bearer tk_..., resolvable by webx.Authenticate).
func (d Deps) handleCLIToken(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}
	var in struct {
		Name  string                `json:"name"`
		TTLS  int                   `json:"ttl_seconds"`
		Scope *webx.CredentialScope `json:"scope"`
	}
	if r.Body != nil {
		err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in)
		if err != nil && !errors.Is(err, io.EOF) {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
			return
		}
	}
	var ttl time.Duration
	if in.TTLS > 0 {
		ttl = time.Duration(in.TTLS) * time.Second
	}
	name := "cli"
	if in.Name != "" {
		name = in.Name
	}
	scope, verr := normalizePATScope(in.Scope)
	if verr != nil {
		writeErr(w, verr)
		return
	}
	pat, token, err := d.Tokens.CreatePAT(r.Context(), p, name, ttl, scope)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{
		"token": token,
		"id":    pat.ID, "name": pat.Name, "prefix": pat.Prefix,
	})
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
		r.PathValue("itemType"), r.PathValue("itemId")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
