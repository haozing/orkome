package http

import (
	"encoding/json"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	workapp "orkome/internal/work/app"
)

const clientUsageBodyLimit = 16 * 1024

func (d Deps) handleClientUsage(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p == nil {
		webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "authentication required", nil)
		return
	}
	if p.Source != webx.SourceSession {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "client usage reporting is human-only", nil)
		return
	}
	var in struct {
		InstallID string `json:"install_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, clientUsageBodyLimit)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	clientType, clientVersion, clientOS := webx.ClientMetadataFrom(r.Context())
	err := d.ClientUsage.Report(r.Context(), workapp.ClientUsageReport{
		WorkspaceID:   wsID(r),
		UserID:        p.UserID,
		ClientType:    clientType,
		InstallID:     in.InstallID,
		ClientVersion: clientVersion,
		OS:            clientOS,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
