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
	AdminSvc *adminapp.AdminService
}

func wiringCommercial(log *slog.Logger, db *pg.DB, pool *pgxpool.Pool, az *authz.Authorizer, idRepo *identitypg.Repo) *commercial {
	// FP-19 FP19-A2/A5：平台管理员授予/撤销、用户封禁/解封此前零审计、
	// 解封后旧会话原样复活——AdminService 的 auditor/revoker 从未接线。
	rec := audit.NewRecorder(pool, log)
	adminSvc := adminapp.NewAdminService(adminpg.New(pool), time.Now).
		WithAuditor(rec).        // *audit.Recorder 满足 adminapp.Auditor
		WithSessionRevoker(idRepo) // identitypg.Repo.RevokeAllUserSessions
	return &commercial{
		pool:     pool,
		Audit:    rec,
		AdminSvc: adminSvc,
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
