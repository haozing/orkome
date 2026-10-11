package main

import (
	"context"
	"log/slog"

	"github.com/haozing/ploykit/platform/pg"
	"github.com/haozing/ploykit/platform/storagex"
	contenthttp "orkome/internal/content/adapters/http"
	contentpgrepo "orkome/internal/content/adapters/pgrepo"
	contentapp "orkome/internal/content/app"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	idpgrepo "orkome/internal/identity/adapters/pgrepo"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
)

func wiringContent(log *slog.Logger, db *pg.DB, cfg *Config,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
) (contenthttp.Deps, *contentapp.IntentSweeper, error) {
	store, local, err := buildStore(cfg)
	if err != nil {
		return contenthttp.Deps{}, nil, err
	}
	if local != nil {
		log.Info("storagex: local backend", "root", cfg.LocalUploadDir)
	} else {
		log.Info("storagex: s3 backend", "bucket", cfg.S3Bucket)
	}
	secret := []byte(cfg.JWTSecret)
	repo := contentpgrepo.New(db)
	attachments := contentapp.NewAttachmentService(repo, store, secret, nil)
	attachments.SetMemberGate(memberCheck)

	attachments.SetHostChecker(hostCheckOf(db))
	sweeper := contentapp.NewIntentSweeper(repo, store, nil)
	return contenthttp.Deps{
		Attachments: attachments,
		Avatars:     contentapp.NewAvatarService(store, secret, nil),
		Local:       local,
		MemberCheck: memberCheck,
		RoleOf:      roleOf(db),
	}, sweeper, nil
}

func buildStore(cfg *Config) (contentapp.Store, *storagex.LocalBackend, error) {
	if cfg.S3Bucket != "" {
		s3, err := storagex.NewS3(context.Background(), storagex.S3Options{
			Endpoint: cfg.S3Endpoint, Region: cfg.S3Region,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey,
			Bucket: cfg.S3Bucket, Prefix: "attachments/", PublicBase: cfg.S3PublicBase,
			PathStyle: true,
		})
		if err != nil {
			return nil, nil, err
		}
		return s3, nil, nil
	}
	dir := cfg.LocalUploadDir
	if dir == "" {
		dir = "./uploads"
	}
	local, err := storagex.NewLocal(dir)
	if err != nil {
		return nil, nil, err
	}
	return local, local, nil
}

func roleOf(db *pg.DB) func(ctx context.Context, workspaceID, userID string) (string, error) {
	repo := idpgrepo.New(db)
	return func(ctx context.Context, wsID, userID string) (string, error) {
		m, ok, err := repo.GetMember(ctx, wsID, userID)
		if err != nil || !ok {
			return "", err
		}
		return m.Role, nil
	}
}

func hostCheckOf(db *pg.DB) func(ctx context.Context, ws, kind, hid string) (bool, error) {
	workRepo := &workpgrepo.Repo{DB: db}
	convRepo := convpgrepo.New(db)
	orchRepo := orchpgrepo.New(db)
	return func(ctx context.Context, ws, kind, hid string) (bool, error) {
		switch kind {
		case "work_item":
			_, ok, err := workRepo.GetWorkItem(ctx, ws, hid)
			return ok, err
		case "comment":
			c, ok, err := workRepo.GetComment(ctx, hid)
			_ = c
			if err != nil || !ok {
				return ok, err
			}
			return c.WorkspaceID == ws, nil
		case "chat_session":
			_, ok, err := convRepo.GetSession(ctx, ws, hid)
			return ok, err
		case "chat_message":
			ok, err := convRepo.GetMessage(ctx, hid)
			return ok, err
		case "task":
			_, ok, err := orchRepo.GetTask(ctx, ws, hid)
			return ok, err
		}
		return true, nil
	}
}
