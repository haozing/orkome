package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/haozing/ploykit/admin"
	identityhttp "github.com/haozing/ploykit/identity/adapters/http"
	pkgmigrations "github.com/haozing/ploykit/migrations"
	fwnotifyhttp "github.com/haozing/ploykit/notify/adapters/http"
	"github.com/haozing/ploykit/platform/logx"
	"github.com/haozing/ploykit/platform/metrics"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"github.com/haozing/ploykit/platform/redisx"
	"github.com/haozing/ploykit/platform/webx"
	"github.com/haozing/ploykit/platform/wswire"
	webhookhttp "github.com/haozing/ploykit/webhooks/adapters/http"
	workspacehttp "github.com/haozing/ploykit/workspace/adapters/http"
	autohttp "orkome/internal/automation/adapters/http"
	chhttp "orkome/internal/channels/adapters/http"
	chpgrepo "orkome/internal/channels/adapters/pgrepo"
	chapp "orkome/internal/channels/app"
	contenthttp "orkome/internal/content/adapters/http"
	"orkome/internal/contract/events"
	convhttp "orkome/internal/conversation/adapters/http"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	idhttp "orkome/internal/identity/adapters/http"
	idpgrepo "orkome/internal/identity/adapters/pgrepo"
	notifhttp "orkome/internal/notification/adapters/http"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	orchhttp "orkome/internal/orchestration/adapters/http"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/flagsx"
	"orkome/internal/platform/outboxx"
	skillhttp "orkome/internal/skills/adapters/http"
	workhttp "orkome/internal/work/adapters/http"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"

	"orkome/migrations"
)

func main() {
	cfg, err := ParseConfig()
	if err != nil {
		fmt_stderr("config: " + err.Error())
		os.Exit(1)
	}
	log := logx.New(logx.Options{Level: cfg.LogLvl})

	if cfg.FeatureFlagsFile != "" {
		if _, err := flagsx.LoadFile(cfg.FeatureFlagsFile); err != nil {
			log.Error("feature flags file invalid", "file", cfg.FeatureFlagsFile, "err", err)
			os.Exit(1)
		}
		log.Info("flagsx: static rules loaded", "file", cfg.FeatureFlagsFile)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := pg.Connect(ctx, cfg.Dsn, pg.Options{ReplicaDSN: cfg.Replica})
	if err != nil {
		log.Error("pg connection failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	redisCli, _ := redisx.New(ctx, cfg.RedisURL)

	metricsReg := metrics.New()
	metricsReg.InstallGlobal()
	bus := eventbus.New(log)
	bus.Metrics = metricsReg
	workersReg := NewWorkers(log)
	idDeps := wiringIdentity(log, db, redisCli, cfg)
	workDeps := wiringWork(log, db, bus, workersReg, idMemberCheck(db), cfg.JWTSecret)
	dispatcher, sweeper, orchHTTPDeps, orchEnq, orchEng, batchSvc := wiringOrchestration(log, db, bus, cfg, idMemberCheck(db))
	sweeper.Metrics = metricsReg

	{
		enq := orchEnq
		wiSvc := workDeps.WorkItems
		orepo := orchpgrepo.New(db)
		bus.On(eventbus.PhaseCascade, func(ctx context.Context, e events.Envelope) {
			switch e.Event {
			case events.WorkItemUpdated:
				p, ok := e.Payload.(events.WorkItemPayload)
				if !ok || e.ActorUserID == "" || p.AssigneeType != "agent" || p.AssigneeID == "" {
					return
				}
				_, _, _ = enq.EnqueueAssignment(ctx, e.WorkspaceID, p.ID, p.AssigneeID, e.ActorUserID)
			case events.TaskCompleted:
				p, ok := e.Payload.(orchapp.TaskPayload)
				if !ok || p.WorkItemID == "" {
					return
				}
				resetIdleWorkItem(ctx, wiSvc, orepo, e.WorkspaceID, p.WorkItemID)
			}
		})
	}

	workapp.SetCommentTaskCanceller(func(ctx context.Context, ws, commentID string) (int, error) {
		ids, err := orchpgrepo.New(db).CancelTasksByTriggerComment(ctx, ws, commentID)
		return len(ids), err
	})

	workapp.SetWorkItemTaskCanceller(orchpgrepo.New(db).CancelActiveTasksByWorkItem)
	if settle := orchHTTPDeps.Settle; settle != nil {
		bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
			_ = settle.HandleCommentEvent(ctx, e)
		})
	}
	orchHTTPDeps.RetrySourceContext = retrySourceContext(db, workDeps.SourceCtx)
	orchHTTPDeps.Builder = orchapp.NewAgentBuilderService(orchpgrepo.New(db))
	convDeps := wiringConversation(log, db, bus, orchEng, orchEnq, orchHTTPDeps.Settle, idMemberCheck(db))

	// FP-20 F1/F3 订阅 fence：task/chat scope 的实体→工作区归属校验。
	orchRepo := orchpgrepo.New(db)
	convRepo := convpgrepo.New(db)
	wsHub, wsCast := wiringWS(ctx, log, bus, idMemberCheck(db), subScopeFence{
		task: func(ctx context.Context, ws, taskID string) (bool, error) {
			_, ok, err := orchRepo.GetTask(ctx, ws, taskID)
			return ok, err
		},
		chat: func(ctx context.Context, ws, sessionID, userID string) (bool, error) {
			sess, ok, err := convRepo.GetSession(ctx, ws, sessionID)
			if err != nil || !ok {
				return false, err
			}
			return sess.CreatorID == userID, nil
		},
	}, idDeps.PAT, cfg, redisCli, workersReg)
	wsHub.Metrics = metricsReg

	// W2-1 chat:done creator 定向：帧不再进 workspace 广播，改经 notification
	// ×6 帧同款 notifUserFrames 适配器 SendToUser 投给会话 creator（结算侧
	// completed/failed + 会话侧 cancel 终态两个发射口共用一个适配器）。
	// 注：本段属 wave2-chat 对 FixD 波次文件（cmd/orkome-server）的最小增量
	// 接线，交接见 docs/fix-wave2-chat-2026-10-12.md §6。
	if settle := orchHTTPDeps.Settle; settle != nil {
		settle.SetChatDonePush(notifUserFrames{cast: wsCast})
	}
	convDeps.Chat.SetUserFrames(notifUserFrames{cast: wsCast})

	orchEng.InterimPush = func(wsID, taskID, kind, content string) {
		b, _ := json.Marshal(map[string]any{"kind": kind, "content": content, "task_id": taskID})
		wsCast.Broadcast(context.Background(), wswire.ScopeKey(wswire.ScopeWorkspace, wsID),
			wswire.Frame{Type: "task:message", Payload: b})
	}

	orchEng.ProgressPush = func(wsID, taskID, stage, callID string, seq int64) {
		b, _ := json.Marshal(map[string]any{"stage": stage, "call_id": callID, "seq": seq, "task_id": taskID})
		wsCast.Broadcast(context.Background(), wswire.ScopeKey(wswire.ScopeWorkspace, wsID),
			wswire.Frame{Type: "task:progress", Payload: b})
	}

	notifW := wiringNotification(db, log, notifUserFrames{cast: wsCast}, workersReg, idMemberCheck(db))
	relay := outboxx.NewRelay(db, func(ctx context.Context, e events.Envelope) error {
		bus.Emit(ctx, e)
		return nil
	}, log)
	relay.Metrics = metricsReg

	relay.DecodePayload = decodeOutboxPayload
	workersReg.AddFunc("outbox-relay", relay.Run)

	root := http.NewServeMux()

	proxies := webx.ParseTrustedProxies(cfg.TrustedProxies)

	_oldRevocation := func(ctx context.Context, workspaceID, userID string) error {
		tasks, err := orchHTTPDeps.Repo.ListWorkspaceActiveTasks(ctx, workspaceID)
		if err != nil {
			log.Error("user revocation: list active tasks", "ws", workspaceID, "err", err)
			return nil
		}
		for _, t := range tasks {
			if _, err := orchHTTPDeps.Settle.Cancel(ctx, workspaceID, t.TaskID); err != nil {
				log.Error("user revocation: cancel task", "task", t.TaskID, "err", err)
			}
		}

		if n, err := (&workpgrepo.Repo{DB: db}).DeleteAllSubscriptionsByUser(ctx, workspaceID, userID); err != nil {
			log.Error("user revocation: clear subscriptions", "err", err)
		} else if n > 0 {
			log.Info("user revocation: subscriptions cleared", "n", n)
		}
		if n, err := convpgrepo.New(db).ArchiveSessionsByUser(ctx, workspaceID, userID); err != nil {
			log.Error("user revocation: archive sessions", "err", err)
		} else if n > 0 {
			log.Info("user revocation: sessions archived", "n", n)
		}
		return nil
	}

	_oldTeardown := func(ctx context.Context, workspaceID string) error {
		tasks, err := orchHTTPDeps.Repo.ListWorkspaceActiveTasks(ctx, workspaceID)
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if _, err := orchHTTPDeps.Settle.Cancel(ctx, workspaceID, t.TaskID); err != nil {
				return err
			}
		}
		return nil
	}

	root.Handle("/metrics", metricsReg.HandlerWithToken(cfg.MetricsToken))

	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	root.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			http.Error(w, "db unreachable", http.StatusServiceUnavailable)
			return
		}
		if missing := missingMigrations(ctx, db); len(missing) > 0 {
			http.Error(w, "migrations pending: "+joinVersions(missing), http.StatusServiceUnavailable)
			return
		}
		if !workersReg.AllHealthy() {
			http.Error(w, "workers unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	api := http.NewServeMux()

	// 工作区硬删接线（FP-02 P1-1 / wave2-ws-delete）：BeforeDelete 预清跨域
	// label join 表，OnTeardown = 取消活跃任务 → FixE CleanupWorkspaceTables
	// （work 域 21 表）→ 跨域业务表清理，随后框架 DELETE workspace。
	// 事务编排与失败语义见 wiring_workspace.go 头注。
	idDeps.WS = idDeps.WS.WithHooks(workspaceHardDeleteHooks(db, _oldRevocation, _oldTeardown))
	authMw := webx.Authenticate(&idDeps.Auth, idDeps.Repo, idDeps.PAT)
	// The chain-level authMw authenticates once for every request; domain
	// mounts only add the runtime-agent write fence on top of it.
	authGuardMw := runtimeAgentWriteGuard

	workspacehttp.Mount(api, idDeps.WorkspaceMountDeps())

	mountAgentMCP(api, authGuardMw, db, bus, orchHTTPDeps.External, idMemberCheck(db))
	workhttp.Mount(api, workDeps, authGuardMw)
	fwnotifyhttp.Mount(notifW.FrameworkRouter(api, authGuardMw), notifW.Framework)
	notifhttp.Mount(api, notifW.Product, authGuardMw)

	// Commercial surfaces (P6-2): audit / quota usage / billing plans / admin.
	comm := wiringCommercial(log, db, db.Pool(), idDeps.Az, idDeps.Repo)
	wsHeaderMW := workspacehttp.WorkspaceHeaderCtx(idDeps.WSRepo)
	comm.MountAPI(api, wsHeaderMW, idDeps.Az)
	comm.MountAdmin(root, idDeps.Repo, idDeps.Auth, admin.Hooks{
		OnImpersonation: func(ctx context.Context, adminUserID, targetUserID string) error {
			log.Info("admin impersonation", "admin", adminUserID, "target", targetUserID)
			return nil
		},
	})
	orchhttp.Mount(api, orchHTTPDeps, authGuardMw)
	convhttp.Mount(api, convDeps, authGuardMw)
	autoDeps := wiringAutomation(ctx, log, db, bus, workersReg, cfg, redisCli)
	autoDeps.MemberCheck = idMemberCheck(db)

	webhookDeps := wiringWebhooks(log, db, bus, workersReg, idMemberCheck(db), roleOf(db), idDeps.Az, cfg)
	webhookhttp.Mount(api, webhookDeps)

	{
		notifRepo := notifpgrepo.New(db)
		autoDeps.Autopilots.SetFailureNotifier(func(ctx context.Context, ws, creator, name, stat string) {
			err := notifRepo.WriteNotice(ctx, ws, creator, "autopilot_paused",
				"automation rule auto-paused by failure-rate fuse", "rule "+name+" paused: "+stat+". Please re-enable it manually after investigation.",
				"autopilot_fuse:"+ws+":"+name)
			if err != nil {
				log.Warn("autopilot fuse notice failed", "err", err)
			}
		})
	}
	autohttp.Mount(api, autoDeps, authGuardMw)

	orchEng.Checkiner = checkinAd{wake: autoDeps.Wakeups}

	wiringChannels(log, db, cfg, bus, workersReg, idMemberCheck(db), metricsReg)

	{
		chRepo := chpgrepo.New(db)
		chhttp.Mount(api, chhttp.Deps{
			Titles: chapp.NewTitleCallbackService(chRepo), Repo: chRepo,
		})
	}

	batchW := wiringBatch(log, bus, batchSvc, autoDeps.Wakeups)
	workersReg.AddFunc("batch-worker", batchW.Run)

	contentDeps, intentW, err := wiringContent(log, db, cfg, idMemberCheck(db))
	if err != nil {
		log.Error("content wiring", "err", err)
		os.Exit(1)
	}
	workersReg.Add(intentW)
	skillRepo := skillRepoOf(db)
	orchEng.Skills = skillSourceOf(skillRepo)
	skillDeps := wiringSkills(db, cfg, idMemberCheck(db))
	skillhttp.Mount(api, skillDeps, authGuardMw)
	contenthttp.MountAPI(api, contentDeps, authGuardMw)
	root.Handle("/api/", api)
	autohttp.MountPublic(root, autoDeps)
	contenthttp.MountUploads(root, contentDeps)
	mountWS(root, wsHub, authMw)
	identityhttp.Mount(root, idDeps.IdentityMountDeps(proxies))
	idhttp.MountExtensions(root, authGuardMw, idDeps.Ext)
	root.HandleFunc("GET /config", webx.ConfigHandler(webx.ConfigDeps{Extra: func() map[string]any {
		return map[string]any{"product": "orkome"}
	}}))

	// F21 fixed in ploykit v0.4.1 (statusRecorder passes Hijack through) —
	// AccessLog is safe for the /ws upgrade again.
	// FP-01/09/13 三缝同修：①csrf.MaxAge 对齐会话 TTL（v0.4.1 框架无此旋钮，
	// 见 csrf_session.go）；②PATPrefix "ork_"（回落 tk_ 会拦死所有 PAT 写请求）；
	// ③/webhooks/ 豁免（automation 公开入站自带 HMAC 防护，不该被 CSRF 拦）。
	handler := webx.Recover(log)(webx.RequestIDMiddleware(webx.ClientMetadata(webx.AccessLog(log, proxies...)(
		webx.SecurityHeaders(webx.CORS(webx.ParseOrigins(cfg.CORSAllowedOrigins), true)(
			csrfChain(&webx.CSRFConfig{
				Key:                   webx.DeriveCSRFKey([]byte(cfg.JWTSecret)),
				TrustLocalhostAnyPort: true,
				ExemptPrefixes:        []string{wsPathPrefix, webhookInboundPrefix, uiTokenConsumePrefix},
				PATPrefix:             "ork_", // external MCP/client-kit contract
			}, cfg.IsProd(), idDeps.Auth.SessionTTL)(authMw(root))))))))
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	workersReg.AddFunc("dispatcher", func(ctx context.Context) error {
		return dispatcher.RunWithMetrics(ctx, metricsReg)
	})
	workersReg.AddFunc("liveness-sweeper", sweeper.Run)
	workersReg.Start(ctx)

	errCh := make(chan error, 1)
	go func() {
		log.Info("http listening", "addr", srv.Addr, "env", cfg.AppEnv)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
	}

	log.Info("shutting down")

	shCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		log.Error("http shutdown", "err", err)
	}
	workersReg.Drain(15 * time.Second)
	if redisCli != nil {
		_ = redisCli.Close()
	}
	log.Info("bye")
}

func fmt_stderr(msg string) { println(msg) }

func decodeOutboxPayload(event string, raw []byte) any {
	switch event {
	case events.TaskQueued, events.TaskDispatch, events.TaskRunning, events.TaskAwaiting,
		events.TaskResumed, events.TaskCompleted, events.TaskFailed,
		events.TaskCancelled, events.TaskMessage:
		var p orchapp.TaskPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case events.CommentCreated, events.CommentUpdated, events.CommentDeleted:
		var p events.CommentPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	}
	return nil
}

func missingMigrations(ctx context.Context, db *pg.DB) []string {
	var missing []string
	for _, m := range []*pgmigrate.Migrator{
		pgmigrate.New(db.Pool(), pkgmigrations.FS, "."),
		pgmigrate.New(db.Pool(), migrations.FS, "."),
	} {
		rows, err := m.Status(ctx)
		if err != nil {
			return []string{"status-error"}
		}
		for _, row := range rows {
			if !row.Applied {
				missing = append(missing, row.Version)
			}
		}
	}
	return missing
}

func joinVersions(vs []string) string {
	out := ""
	for i, v := range vs {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

func resetIdleWorkItem(ctx context.Context, wiSvc *workapp.WorkItemService, orepo orchapp.Repo, ws, wiID string) {
	wi, err := wiSvc.Get(ctx, ws, wiID)
	if err != nil || wi == nil || wi.StatusKey != "in_progress" {
		return
	}
	active, err := orepo.ListActiveTasks(ctx, wiID)
	if err != nil || len(active) > 0 {
		return
	}
	todo := "todo"
	_, _ = wiSvc.Update(ctx, ws, wiID, workapp.WorkItemPatch{StatusKey: &todo}, 0, "")
}

func idMemberCheck(db *pg.DB) func(ctx context.Context, workspaceID, userID string) (bool, error) {
	repo := idpgrepo.New(db)
	return func(ctx context.Context, wsID, userID string) (bool, error) {
		_, ok, err := repo.GetMember(ctx, wsID, userID)
		return ok, err
	}
}

// idWorkItemCheck adapts the work domain's existing workspace-scoped work item
// read onto identity's WorkItemLocator (ui-token mint-time work_item_id
// binding, FP-15 P1-2). It only calls the existing GetWorkItem API — identity
// and work stay decoupled (the function shape crosses at the composition root).
func idWorkItemCheck(db *pg.DB) func(ctx context.Context, workspaceID, workItemID string) (bool, error) {
	repo := workpgrepo.Repo{DB: db}
	return func(ctx context.Context, wsID, wiID string) (bool, error) {
		_, ok, err := repo.GetWorkItem(ctx, wsID, wiID)
		return ok, err
	}
}
