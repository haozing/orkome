package main

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/haozing/ploykit/authz"
	identityhttp "github.com/haozing/ploykit/identity/adapters/http"
	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	fwapp "github.com/haozing/ploykit/identity/app"
	"github.com/haozing/ploykit/platform/pg"
	"github.com/haozing/ploykit/platform/webx"
	workspacehttp "github.com/haozing/ploykit/workspace/adapters/http"
	workspacepg "github.com/haozing/ploykit/workspace/adapters/pgrepo"
	wsapp "github.com/haozing/ploykit/workspace/app"

	"github.com/resend/resend-go/v2"

	"orkome/internal/identity/adapters/email"
	idhttp "orkome/internal/identity/adapters/http"
	idpgrepo "orkome/internal/identity/adapters/pgrepo"
	idapp "orkome/internal/identity/app"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
)

// fwIdentity is the framework-backed identity/workspace wiring plus orkome's
// product account extensions (pins / ui-tokens / cli-token).
type fwIdentity struct {
	Repo     *identitypg.Repo
	Sessions *fwapp.SessionService
	Tokens   *fwapp.TokenService
	Account  *fwapp.AccountService
	WS       *wsapp.WorkspaceService
	WSRepo   *workspacepg.Repo
	Az       *authz.Authorizer
	Auth     webx.AuthConfig
	PAT      webx.PATLookup // agent-binding wrapped
	Ext      idhttp.Deps
}

func wiringIdentity(log *slog.Logger, db *pg.DB, redisCli redis.UniversalClient, cfg *Config) *fwIdentity {
	repo := identitypg.New(db.Pool(), identitypg.Config{
		SessionTTL:  7 * 24 * time.Hour,
		AbsoluteTTL: 30 * 24 * time.Hour,
		PATPrefix:   "ork_", // external MCP/client-kit contract
	})

	var sender fwapp.EmailSender
	switch {
	case cfg.ResendKey != "":
		sender = email.Resend{Client: resend.NewClient(cfg.ResendKey), From: cfg.ResendFrom}
	case cfg.SMTPHost != "":
		sender = email.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser,
			Pass: cfg.SMTPPass, From: cfg.SMTPFrom, TLS: cfg.SMTPTLS}
	default:
		log.Info("email channel: dev log (verification code logged)")
		sender = email.DevLog{Log: log}
	}

	auth := webx.AuthConfig{
		CookieName:   "orkome_auth",
		Secure:       cfg.IsProd(),
		SessionTTL:   7 * 24 * time.Hour,
		AbsoluteTTL:  30 * 24 * time.Hour,
		IPHashSecret: cfg.JWTSecret,
		PATPrefix:    "ork_", // external MCP/client-kit contract
	}

	sessions := fwapp.NewSessionService(repo, sender, fwapp.SessionConfig{
		SecretPepper: cfg.JWTSecret,
		AllowSignup:  cfg.AllowSignup, AllowedEmails: cfg.AllowedEmails,
		AllowedDomains: cfg.AllowedDomains, DevCode: cfg.DevCode,
		Production: cfg.IsProd(),
	}, auth.SessionTTL, 0, time.Now)
	tokens := fwapp.NewTokenService(repo, nil, time.Now).WithPATPrefix("ork_")

	// FP-16 P1-1：AccountService 此前从未装配——forgot-password / reset-password /
	// send-verification / verify-email / DELETE /auth/me 五端点因 Deps.Account
	// 为 nil 全部 500，email_verified 恒 false、忘旧密码即永久锁死。
	// mailer 三通道（Resend/SMTP/DevLog）上方已就绪，此处补构造 + LinkBaseURL。
	account := fwapp.NewAccountService(repo, sender, fwapp.AccountConfig{
		SecretPepper: cfg.JWTSecret,
		LinkBaseURL:  cfg.AccountLinkBaseURL,
	}, time.Now)

	catalog := authz.NewCatalog(append(authz.DefaultPerms(),
		// orkome product permission points (rebase plan Appendix B)
		"work:read", "work:write", "work:delete", "work:move", "work:catalog",
		"comment:write",
		"orch:agent:read", "orch:agent:write", "orch:agent:credentials",
		"orch:squad:read", "orch:squad:write", "orch:batch:write", "orch:approve:decide", "orch:usage:read",
		"conv:read", "conv:write",
		"channel:read", "channel:write", "channel:connection:write",
		"content:upload", "content:avatar:write",
		"skill:read", "skill:write", "skill:import",
		"auto:wakeup:write", "auto:autopilot:read", "auto:autopilot:write",
	)...)
	az := authz.New(catalog, authz.DefaultRoles())
	wsRepo := workspacepg.New(db.Pool())
	ws := wsapp.NewWorkspaceService(wsRepo, az, sender, nil, wsapp.WorkspaceConfig{MaxPerUser: -1}, time.Now)

	repoX := idpgrepo.New(db)
	id := &fwIdentity{
		Repo: repo, Sessions: sessions, Tokens: tokens, WS: ws, WSRepo: wsRepo, Az: az, Auth: auth,
		Account: account,
		Ext: idhttp.Deps{
			Repo:     repoX,
			Pins:     idapp.NewPinService(repoX),
			UITokens: idapp.NewUITokenService(repoX, []byte(cfg.JWTSecret), time.Now).
				WithWorkItemLocator(idapp.WorkItemLocatorFunc(idWorkItemCheck(db))),
			Tokens: tokens,
		},
	}

	// PAT -> agent binding: wrap the framework resolver, then enrich the
	// principal with the orchestration agent mapping (runtime fence + MCP).
	bindRepo := orchpgrepo.New(db)
	id.PAT = agentBoundPAT{resolve: repo.ResolvePAT, agentOf: bindRepo.GetAgentIDByPATID}
	return id
}

type agentBoundPAT struct {
	resolve func(ctx context.Context, token string, now time.Time) (*webx.Principal, error)
	agentOf func(ctx context.Context, patID string) (string, bool, error)
}

func (a agentBoundPAT) ResolvePAT(ctx context.Context, token string, now time.Time) (*webx.Principal, error) {
	p, err := a.resolve(ctx, token, now)
	if err != nil || p == nil {
		return p, err
	}
	if agentID, ok, err := a.agentOf(ctx, p.PATID); err == nil && ok {
		p.AgentID = agentID
	}
	return p, nil
}

func (id *fwIdentity) IdentityMountDeps(proxies []*net.IPNet) identityhttp.Deps {
	return identityhttp.Deps{
		AuthCfg: id.Auth, Sessions: id.Repo, PATs: id.Repo,
		SessionsSvc: id.Sessions, Tokens: id.Tokens,
		Account: id.Account, // FP-16 P1-1：账号五端点的 nil 守卫在此收口
		TrustedProxies: proxies,
	}
}

func (id *fwIdentity) WorkspaceMountDeps() workspacehttp.Deps {
	return workspacehttp.Deps{Svc: id.WS, Members: id.WSRepo, Authz: id.Az}
}
