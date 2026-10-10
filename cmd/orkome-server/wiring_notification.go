package main

import (
	"context"
	"log/slog"
	"time"

	notifhttp "orkome/internal/notification/adapters/http"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	notifworkers "orkome/internal/notification/adapters/workers"
	notifapp "orkome/internal/notification/app"
	"github.com/haozing/ploykit/platform/pg"
)

func wiringNotification(db *pg.DB, log *slog.Logger, frames notifapp.UserFrames, reg *Workers,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
) notifhttp.Deps {
	repo := notifpgrepo.New(db)
	notification := notifapp.NewNotificationService(repo)
	if frames != nil {
		notification.SetUserFrames(frames)
	}
	if reg != nil {
		reg.Add(&notifworkers.ArchiveSweeper{
			Repo: repo, Log: log, Every: time.Hour, Retention: 30 * 24 * time.Hour, BatchLimit: 500,
		})
	}
	return notifhttp.Deps{
		Notification: notification,
		Prefs:        notifapp.NewPreferenceService(repo),
		MemberCheck:  memberCheck,
	}
}
