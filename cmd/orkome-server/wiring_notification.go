package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	bellpg "github.com/haozing/ploykit/notify/adapters/bell"
	notifyhttp "github.com/haozing/ploykit/notify/adapters/http"
	fwnotifapp "github.com/haozing/ploykit/notify/app"
	"github.com/haozing/ploykit/platform/pg"
	"github.com/haozing/ploykit/platform/webx"

	notifhttp "orkome/internal/notification/adapters/http"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	notifworkers "orkome/internal/notification/adapters/workers"
	notifapp "orkome/internal/notification/app"
)

// NotificationWiring bundles the two notify surfaces the composition root
// mounts side by side (ploykit rebase P4):
//
//	notifW := wiringNotification(db, log, frames, workersReg, memberCheck)
//	notifyhttp.Mount(notifW.FrameworkRouter(api, authGuardMw), notifW.Framework) // framework subset
//	notifhttp.Mount(api, notifW.Product, authGuardMw)                            // product extensions
type NotificationWiring struct {
	// Framework carries the deps for ploykit's notifyhttp.Mount. It MUST be
	// mounted through FrameworkRouter, never directly onto the shared api
	// mux: four of the framework patterns are superseded by richer product
	// versions and a std ServeMux panics on the duplicate registration.
	Framework notifyhttp.Deps
	// Product carries the deps for orkome's notification extension Mount
	// (adapters/http.Mount); it applies the auth guard itself.
	Product notifhttp.Deps
}

func wiringNotification(db *pg.DB, log *slog.Logger, frames notifapp.UserFrames, reg *Workers,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
) NotificationWiring {
	repo := notifpgrepo.New(db)

	// Framework bell repo + NotifyService over the same notification table the
	// product projection writes. Product inserts fill user_id alongside
	// workspace_id/recipient_user_id, so the framework read surface
	// (badge / read-all / per-type preference toggles) sees product rows.
	// Product writes themselves stay on the repo's direct inserts: the
	// framework NotifyInput cannot carry workspace/work-item/agent-recipient
	// fields and the bell Insert does not set the NOT NULL workspace_id
	// (product migration 1005) — see internal/notification/README.md.
	fwRepo := bellpg.New(db.Pool())
	fwSvc := fwnotifapp.NewNotifyService(fwRepo, nil, time.Now).WithPrefGate(fwRepo)

	notification := notifapp.NewNotificationService(repo)
	if frames != nil {
		notification.SetUserFrames(frames)
	}
	if reg != nil {
		reg.Add(&notifworkers.ArchiveSweeper{
			Repo: repo, Log: log, Every: time.Hour, Retention: 30 * 24 * time.Hour, BatchLimit: 500,
		})
	}
	return NotificationWiring{
		Framework: notifyhttp.Deps{Svc: fwSvc},
		Product: notifhttp.Deps{
			Notification: notification,
			Prefs:        notifapp.NewPreferenceService(repo),
			MemberCheck:  memberCheck,
		},
	}
}

// supersededNotifyPatterns are the framework notify patterns whose product
// counterparts win — the framework response shapes lack workspace scoping,
// cursor pagination, work_item_id and the WS frame push, so the product
// versions stay registered at these paths and the framework Mount must not
// register them (ServeMux duplicate-pattern panic).
var supersededNotifyPatterns = map[string]bool{
	"GET /api/notifications":               true,
	"POST /api/notifications/{id}/read":    true,
	"POST /api/notifications/{id}/archive": true,
	"GET /api/notification-preferences":    true,
}

// FrameworkRouter adapts the shared api mux for the framework notifyhttp.Mount:
// it drops the superseded patterns and runs every kept framework route behind
// the composition root's auth guard (the framework Mount relies on the caller
// for auth, like every product Mount here):
//
//	notifyhttp.Mount(notifW.FrameworkRouter(api, authGuardMw), notifW.Framework)
//
// The framework routes that survive the filter: GET /api/notifications/badge,
// POST /api/notifications/read-all, PUT /api/notification-preferences/{type}.
func (w NotificationWiring) FrameworkRouter(api webx.Router, auth func(http.Handler) http.Handler) webx.Router {
	return filteredNotifyRouter{inner: api, auth: auth}
}

type filteredNotifyRouter struct {
	inner webx.Router
	auth  func(http.Handler) http.Handler
}

func (r filteredNotifyRouter) Handle(pattern string, h http.Handler) {
	if supersededNotifyPatterns[pattern] {
		return
	}
	r.inner.Handle(pattern, r.auth(h))
}

func (r filteredNotifyRouter) HandleFunc(pattern string, f func(http.ResponseWriter, *http.Request)) {
	r.Handle(pattern, http.HandlerFunc(f))
}
