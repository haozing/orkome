package main

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/haozing/ploykit/admin"
	adminhttp "github.com/haozing/ploykit/admin/adapters/http"
	adminpg "github.com/haozing/ploykit/admin/adapters/pgrepo"
	adminapp "github.com/haozing/ploykit/admin/app"
	analyticspg "github.com/haozing/ploykit/analytics/adapters/pgrepo"
	analyticsapp "github.com/haozing/ploykit/analytics/app"
	"github.com/haozing/ploykit/audit"
	audithttp "github.com/haozing/ploykit/audit/adapters/http"
	"github.com/haozing/ploykit/authz"
	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	"github.com/haozing/ploykit/platform/pg"
	"github.com/haozing/ploykit/platform/webx"
	"github.com/haozing/ploykit/quota"
	quotahttp "github.com/haozing/ploykit/quota/adapters/http"
)

// commercial holds the framework's free-tier commercial surfaces: audit
// trail, usage quota readout, admin operations, and analytics tracking.
// Billing is DEFERRED (D7 conflict): the framework billing domain hard-depends
// on platform/events (River substrate) — adopt both together when 商业化 starts.
// P6-2 scope: wiring only, no product development.
type commercial struct {
	pool     *pgxpool.Pool
	Audit    *audit.Recorder
	Tracks   *analyticsapp.TrackService
	AdminSvc *adminapp.AdminService
}

func wiringCommercial(log *slog.Logger, db *pg.DB, pool *pgxpool.Pool, az *authz.Authorizer) *commercial {
	return &commercial{
		pool:     pool,
		Audit:    audit.NewRecorder(pool, log),
		Tracks:   analyticsapp.NewTrackService(analyticspg.New(pool, log), log),
		AdminSvc: adminapp.NewAdminService(adminpg.New(pool), time.Now),
	}
}

// MountAPI mounts the workspace-scoped commercial routes (/api prefix).
// wsMW is the workspace-header middleware; the /api workspaces/{wsId}/...
// billing/audit routes need it while quota's /api/usage reads the header too.
func (c *commercial) MountAPI(mux *http.ServeMux, wsMW func(http.Handler) http.Handler, az *authz.Authorizer) {
	quotahttp.Mount(mux, quotahttp.Deps{
		Svc: quota.NewService(c.pool), WsMW: wsMW, Authz: az,
	})
	audithttp.Mount(mux, audithttp.Deps{Rec: c.Audit, WsMW: wsMW, Authz: az})
}

// MountAdmin mounts platform-admin routes; impersonation mints framework
// sessions through the identity repo. Hook callback is wired product-side.
func (c *commercial) MountAdmin(mux *http.ServeMux, idRepo *identitypg.Repo, auth webx.AuthConfig, onImpersonation admin.Hooks) {
	adminhttp.Mount(mux, adminhttp.Deps{
		Svc: c.AdminSvc, Rec: c.Audit, Sessions: idRepo, AuthCfg: &auth, Hooks: &onImpersonation,
	})
}
