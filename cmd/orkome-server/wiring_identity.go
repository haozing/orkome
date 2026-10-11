package main

import (
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/resend/resend-go/v2"

	"github.com/haozing/ploykit/platform/pg"
	idadapters "orkome/internal/identity/adapters"
	"orkome/internal/identity/adapters/email"
	idhttp "orkome/internal/identity/adapters/http"
	"orkome/internal/identity/adapters/pgrepo"
	idworkers "orkome/internal/identity/adapters/workers"
	"orkome/internal/identity/app"
	"orkome/internal/platform/webx"
)

func wiringIdentity(log *slog.Logger, db *pg.DB, redisCli redis.UniversalClient, cfg *Config, reg *Workers) (idhttp.Deps, func()) {
	repo := &pgrepo.Repo{DB: db}
	if reg != nil {
		reg.Add(&idworkers.CodeSweeper{
			Repo: repo, Log: log, Every: time.Hour, Retention: time.Hour, BatchLimit: 500,
		})
	}

	auth := &webx.AuthConfig{
		Secret:     []byte(cfg.JWTSecret),
		SessionTTL: cfg.TokenTTL,
		Secure:     cfg.IsProd(),
	}
	csrfKey := webx.DeriveCSRFKey(auth.Secret)

	var sender app.EmailSender = email.DevLog{Log: log}
	if cfg.ResendKey != "" {
		sender = email.Resend{Client: resend.NewClient(cfg.ResendKey), From: cfg.ResendFrom}
		log.Info("email channel: resend")
	} else if cfg.SMTPHost != "" {
		sender = email.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser,
			Pass: cfg.SMTPPass, From: cfg.SMTPFrom, TLS: cfg.SMTPTLS}
		log.Info("email channel: smtp", "host", cfg.SMTPHost)
	} else {
		log.Info("email channel: dev log (verification code logged)")
	}

	sessions := app.NewSessionService(repo, sender, auth, app.SessionConfig{
		AllowSignup: cfg.AllowSignup, AllowedEmails: cfg.AllowedEmails,
		AllowedDomains: cfg.AllowedDomains, DevCode: cfg.DevCode,
	}, time.Now)
	var patCache app.Cache = app.NoopCache{}
	if redisCli != nil {
		patCache = idadapters.RedisStringCache{C: redisCli}
	}
	tokens := app.NewTokenService(repo, patCache, time.Now)
	workspaces := app.NewWorkspaceService(repo, sender, tokens, time.Now)

	var limiter webx.Limiter = webx.FailOpenLimiter{}
	if redisCli != nil {
		limiter = webx.NewGCRALimiter(redisCli)
	}

	deps := idhttp.Deps{
		Sessions: sessions, Tokens: tokens, Workspaces: workspaces, Repo: repo,
		Auth: auth, CSRFKey: csrfKey, Limiter: limiter,
		UITokens:          app.NewUITokenService(repo, auth, time.Now),
		SendCodePerMinute: 10, Version: "0.1.0-m1",
	}
	return deps, func() {}
}
