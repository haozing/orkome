package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"orkome/internal/contract/apierr"

	"github.com/haozing/ploykit/authz"
	identityhttp "github.com/haozing/ploykit/identity/adapters/http"
	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	fwapp "github.com/haozing/ploykit/identity/app"
	pkgmigrations "github.com/haozing/ploykit/migrations"
	"github.com/haozing/ploykit/platform/egressx"
	"github.com/haozing/ploykit/platform/logx"
	"github.com/haozing/ploykit/platform/pg"
	pgmigrate "github.com/haozing/ploykit/platform/pgmigrate"
	"github.com/haozing/ploykit/platform/storagex"
	"github.com/haozing/ploykit/platform/webx"
	workspacehttp "github.com/haozing/ploykit/workspace/adapters/http"
	workspacepg "github.com/haozing/ploykit/workspace/adapters/pgrepo"
	wsapp "github.com/haozing/ploykit/workspace/app"
	autohttp "orkome/internal/automation/adapters/http"
	autoppgrepo "orkome/internal/automation/adapters/pgrepo"
	autopworkers "orkome/internal/automation/adapters/workers"
	autopapp "orkome/internal/automation/app"
	chhttp "orkome/internal/channels/adapters/http"
	chpgrepo "orkome/internal/channels/adapters/pgrepo"
	chapp "orkome/internal/channels/app"
	contenthttp "orkome/internal/content/adapters/http"
	contentpgrepo "orkome/internal/content/adapters/pgrepo"
	contentapp "orkome/internal/content/app"
	"orkome/internal/contract/events"
	"orkome/internal/contract/modelmeta"
	convhttp "orkome/internal/conversation/adapters/http"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	convapp "orkome/internal/conversation/app"
	idhttp "orkome/internal/identity/adapters/http"
	idpgrepo "orkome/internal/identity/adapters/pgrepo"
	notifhttp "orkome/internal/notification/adapters/http"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	notifapp "orkome/internal/notification/app"
	"orkome/internal/orchestration/adapters/engine"
	orchhttp "orkome/internal/orchestration/adapters/http"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	"orkome/internal/orchestration/adapters/workers"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/outboxx"
	skillhttp "orkome/internal/skills/adapters/http"
	skillpgrepo "orkome/internal/skills/adapters/pgrepo"
	skillapp "orkome/internal/skills/app"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
	"orkome/migrations"
)

type scriptModel struct{}

func (s *scriptModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	hist := ""
	for _, m := range input {
		hist += m.Content + "\n"
		for _, tc := range m.ToolCalls {
			hist += "[tool:" + tc.Function.Name + "]"
		}
	}
	switch {
	case strings.Contains(hist, "[tool:post_comment]"):
		return textMsg("tool executed, final conclusion."), nil
	case strings.Contains(hist, "[tool:request_approval]"):
		return textMsg("done after approval."), nil
	default:
		return textMsg("goldens conclusion: all is well."), nil
	}
}

func (s *scriptModel) Stream(ctx context.Context, in []*schema.Message, o ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m, err := s.Generate(ctx, in, o...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{m}), nil
}

func textMsg(c string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: c}
}

type stack struct {
	t        *testing.T
	pool     *pgxpool.Pool
	handler  http.Handler
	rootMux  *http.ServeMux
	rootRec  *webx.Mux
	wsMux    *webx.Mux
	authMw   func(http.Handler) http.Handler
	orepo    *orchpgrepo.Repo
	settle   *orchapp.SettlementService
	eng      *engine.EinoEngine
	wRepo    *workpgrepo.Repo
	comments *workapp.CommentService
	auto     *autopapp.AutopilotService
	jar      map[string][]*http.Cookie
	csrf     map[string]string
	wsID     string

	// bus 事件总线（D4 金线：golden_thread_test.go 的 webhook 扇出 handler
	// 需要 On(PhaseProject)——newStack 的 bus 原为局部变量，回填进 stack。
	// 既有测试零行为改动，不读它）。
	bus *eventbus.Bus
}

func newStack(t *testing.T) *stack {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(context.Background(), base)
	require.NoError(t, err)
	_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS orkome_e2e")
	_, _ = admin.Exec(context.Background(), "CREATE DATABASE orkome_e2e")
	admin.Close(context.Background())
	dsn := strings.Replace(base, "/orkome", "/orkome_e2e", 1)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	fw := pgmigrate.New(pool, pkgmigrations.FS, ".")
	_, err = fw.Up(ctx, 0)
	require.NoError(t, err)
	m := pgmigrate.New(pool, migrations.FS, ".")
	_, err = m.Up(ctx, 0)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `TRUNCATE channel_chat_session_binding, channel_inbound_message_dedup,
channel_installation, attachment, agent_skill, skill_file, skill, notification_preference, event_outbox,
notification, agent_task_checkpoint, agent_model_credential, task_usage, task_supplement, agent_task_queue,
agent_runtime, squad_member, squad, agent_mcp_server, workspace_mcp_server, agent_invocation_target, agent,
activity_log, comment_reaction, comment, work_item_source_context, work_item_reaction, work_item_child_event,
work_item_dependency, work_item_subscriber, work_item_view_preference, work_item_view, work_item_to_label,
label, work_item_property_def, work_item, project_resource, project, work_item_status, work_item_batch_item,
work_item_batch, autopilot_run, autopilot_trigger, autopilot, work_item_wakeup_receipt, work_item_wakeup,
automation_webhook_delivery, pinned_item, personal_access_token, workspace_share_link, workspace_invitation,
channel_task_delivery, channel_chat_context_generation, channel_outbound_card_message, channel_reply_delivery,
channel_inbound_audit, channel_binding_token, channel_media_pending_object, channel_user_binding,
channel_outbound_message, task_usage_hourly_state, task_message, task_usage_hourly, client_usage_daily,
autopilot_subscriber, autopilot_rule_version, autopilot_quota_period, autopilot_collaborator,
autopilot_quota_reservation, search_index_change, search_index_prune_mark, skill_to_label, agent_to_label,
chat_draft_restore, chat_pinned_agent, chat_message, chat_cancel_pending, chat_session,
source_context_object_intent, system_wakeup_setting, agent_builder_draft, quick_action,
member, workspace, agent_credential, webhook_subscription, webhook_delivery, ui_token, pack_ui_entry, pack_tool_schema, "user" RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	db := pg.WrapPool(pool)
	log := logx.New(logx.Options{Level: "error"})
	bus := eventbus.New(log)

	modelmeta.Register("fake", func(context.Context, modelmeta.RuntimeConfig, string) (model.BaseChatModel, error) {
		return &scriptModel{}, nil
	})

	idRepo := idpgrepo.New(db)
	secret := []byte("e2e-secret-32-bytes-aaaaaaaaaaaa")
	auth := &webx.AuthConfig{CookieName: "orkome_auth", SessionTTL: 720 * time.Hour}
	mail := &capMail{codes: map[string]string{}}
	fwRepo := identitypg.New(pool, identitypg.Config{SessionTTL: 720 * time.Hour})
	sessions := fwapp.NewSessionService(fwRepo, mail, fwapp.SessionConfig{
		AllowSignup: true, DevCode: "654321", SecretPepper: string(secret),
	}, 720*time.Hour, 0, time.Now)
	tokens := fwapp.NewTokenService(fwRepo, nil, time.Now)
	idDeps := idhttp.Deps{Repo: idRepo, Tokens: tokens}

	wRepo := &workpgrepo.Repo{DB: db}
	workDeps := workhttpDeps(db, bus, wRepo, idRepo)
	// D4 金线：事件 outbox（与生产 wiring_work.go SetOutbox(rec, db.Within)
	// 同构）——Envelope.EventID 只在 outbox 分支填充，未接时 work_item.updated
	// 事件无 EventID，webhook 扇出（Emit→delivery）按 EventID 空值丢弃。
	rec := outboxx.NewRecorder(db)
	workDeps.WorkItems.SetOutbox(rec, db.Within)
	workDeps.Comments.SetOutbox(rec, db.Within)

	orepo := orchpgrepo.New(db)
	convRepo := convpgrepo.New(db)
	eng := &engine.EinoEngine{
		Checkpoints: &engine.CheckpointStore{DB: db},
		WorkBench:   wbAdapter{wRepo: wRepo},
		// D4 金线：task_message 落库 sink（与生产 wiring_orchestration.go
		// TaskMessages: repo 同构）——此前 stack 未接，engine 的 tool_use/
		// tool_result/text 消息不落 task_message 表，金线的三卡形状断言
		// （B2 数据面）读不到。
		TaskMessages: orepo,
		TaskStatus: func(ctx context.Context, taskID string) (string, error) {
			var st string
			err := db.Tx(ctx).QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id=$1`, taskID).Scan(&st)
			return st, err
		},
		SupplementClaimer: func(ctx context.Context, taskID string) (string, bool, func() error) {
			id, content, ok, err := orepo.ClaimNextSupplement(ctx, taskID)
			if err != nil || !ok {
				return "", false, nil
			}
			return content, true, func() error { return orepo.AckSupplementDelivered(ctx, id) }
		},
	}
	notifRepo := notifpgrepo.New(db)
	settle := orchapp.NewSettlementService(orepo, bus, notifRepo, cwAdapter{wRepo: wRepo}, chatW{repo: convRepo})
	settle.SetApprovalDeps(apvNotif{repo: notifRepo}, engWrap{e: eng},
		func(context.Context, string, string) (string, error) { return "", nil })
	workapp.SetTriggerEngine(trigAdapter{trig: orchapp.NewTriggerService(orepo, bus)})
	workapp.SetAssigneeReader(wRepo)
	orchapp.SetWorkItemReader(wiReader{wRepo: wRepo})

	workapp.SetWorkItemTaskCanceller(orepo.CancelActiveTasksByWorkItem)

	enqSvc := orchapp.NewEnqueueService(orepo, bus)
	wiSvc := workapp.NewWorkItemService(wRepo, bus, time.Now)
	bus.On(eventbus.PhaseCascade, func(ctx context.Context, e events.Envelope) {
		switch e.Event {
		case events.WorkItemUpdated:
			p, ok := e.Payload.(events.WorkItemPayload)
			if !ok || e.ActorUserID == "" || p.AssigneeType != "agent" || p.AssigneeID == "" {
				return
			}
			_, _, _ = enqSvc.EnqueueAssignment(ctx, e.WorkspaceID, p.ID, p.AssigneeID, e.ActorUserID)
		case events.TaskCompleted:
			p, ok := e.Payload.(orchapp.TaskPayload)
			if !ok || p.WorkItemID == "" {
				return
			}
			resetIdleWorkItem(ctx, wiSvc, orepo, e.WorkspaceID, p.WorkItemID)
		}
	})

	notifDeps := notifhttp.Deps{Notification: notifapp.NewNotificationService(notifRepo), Prefs: notifapp.NewPreferenceService(notifRepo)}
	notify := notifapp.NewNotifyService(notifRepo, subsReader{wRepo: wRepo})
	bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		switch e.Event {
		case events.CommentCreated:
			if p, ok := e.Payload.(events.CommentPayload); ok {
				_ = notify.Handle(ctx, notifapp.EventIn{EventID: e.EventID, WorkspaceID: e.WorkspaceID,
					Type: e.Event, WorkItemID: p.WorkItemID, Title: "new comment", Body: p.Content,
					MentionedUserIDs: notifapp.ParseMentionedUsers(p.Content)})
			}
		}
	})

	arepo := autoppgrepo.New(db)
	auto := autopapp.NewAutopilotService(arepo, creatorAdapter{wRepo: wRepo}, enqAdapter{repo: orepo})

	api := http.NewServeMux()
	authMw := webx.Authenticate(auth, fwRepo, fwRepo)
	wsSvc := wsapp.NewWorkspaceService(workspacepg.New(pool), authz.New(authz.DefaultCatalog(), authz.DefaultRoles()),
		mail, nil, wsapp.WorkspaceConfig{}, time.Now)
	workhttpMount(api, workDeps, authMw)
	memberCheck := func(ctx context.Context, ws, uid string) (bool, error) {
		_, ok, err := idRepo.GetMember(ctx, ws, uid)
		return ok, err
	}

	notifDeps.MemberCheck = memberCheck
	notifhttp.Mount(api, notifDeps, authMw)
	orchhttp.Mount(api, orchhttp.Deps{
		Settle:  settle,
		Agents:  orchapp.NewAgentService(orepo, bus),
		Repo:    orepo,
		Trigger: orchapp.NewTriggerService(orepo, bus),
		Builder: orchapp.NewAgentBuilderService(orepo),
		Batches: orchapp.NewBatchService(&orchpgrepo.BatchRepo{DB: db}, nil),
		Squads:  orchapp.NewSquadService(orepo, nil),
		AgentLabels: &orchhttp.AgentLabelEndpoints{
			List: func(ctx context.Context, ws, agentID string) ([]orchhttp.AgentLabelView, error) {
				return labelViews(wRepo.ListAgentLabels(ctx, ws, agentID))
			},
			Attach: func(ctx context.Context, ws, agentID, labelID string) ([]orchhttp.AgentLabelView, error) {
				if err := wRepo.AttachAgentLabel(ctx, ws, agentID, labelID); err != nil {
					return nil, err
				}
				return labelViews(wRepo.ListAgentLabels(ctx, ws, agentID))
			},
			Detach: func(ctx context.Context, ws, agentID, labelID string) ([]orchhttp.AgentLabelView, error) {
				if err := wRepo.DetachAgentLabel(ctx, ws, agentID, labelID); err != nil {
					return nil, err
				}
				return labelViews(wRepo.ListAgentLabels(ctx, ws, agentID))
			},
		},
		MCP: func() *orchapp.MCPServerService {
			m := orchapp.NewMCPServerService(orepo)
			if g, err := egressx.NewGuard([]string{"127.0.0.1/32"}); err == nil {
				m.Egress = g
			}
			return m
		}(),
		MemberCheck:  memberCheck,
		TaskMessages: orepo.ListTaskMessages,
		RetrySourceContext: func(ctx context.Context, ws, taskID, userID string) (any, error) {
			var digest string
			err := db.Tx(ctx).QueryRow(ctx,
				`SELECT coalesce(context->>'source_context_digest','') FROM agent_task_queue WHERE workspace_id=$1 AND id=$2`,
				ws, taskID).Scan(&digest)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && digest == "") {
				return nil, apierr.New(apierr.NotFound, "task carries no source context")
			}
			if err != nil {
				return nil, err
			}
			return workDeps.SourceCtx.CreateFromDigest(ctx, ws, digest, userID, "")
		},
	}, authMw)

	convQA := convapp.NewQuickActionService(convRepo)
	convQA.SetEnqueueWorkItemTask(func(ctx context.Context, ws, wi, agentID, userID, prompt string) (string, error) {
		t2, _, err := enqSvc.EnqueueQuickAction(ctx, ws, wi, agentID, userID, prompt)
		if err != nil {
			return "", err
		}
		return t2.ID, nil
	})
	convQA.SetResolveTargetAgent(func(ctx context.Context, ws, wi string) (string, error) {
		item, _, err := wRepo.GetWorkItem(ctx, ws, wi)
		if err != nil {
			return "", err
		}
		if item.AssigneeType != "agent" {
			return "", nil
		}
		return item.AssigneeID, nil
	})
	convChat := convapp.NewChatService(convRepo, chatEnqA{repo: orepo})
	convhttp.Mount(api, convhttp.Deps{
		Chat: convChat, QuickActions: convQA,
		Pins:        convapp.NewPinService(convRepo),
		Canceller:   chatCancel{settle: settle},
		TaskStatus:  settle.ChatTaskStatus,
		MemberCheck: memberCheck,
	}, authMw)
	wu := autopapp.NewWakeupService(arepo, enqAdapter{repo: orepo}, factsAdapter{wRepo: wRepo})
	wu.SetMemberNotifier(func(ctx context.Context, ws, uid, title, body, dedup string) error {
		return notifRepo.WriteNotice(ctx, ws, uid, "wakeup_member", title, body, dedup)
	})

	bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		switch e.Event {
		case events.WorkItemCreated, events.WorkItemUpdated, events.WorkItemDeleted:
			if p, ok := e.Payload.(events.WorkItemPayload); ok {
				_ = wu.NotifyChildrenChanged(ctx, e.WorkspaceID, p.ID)
			}
		}
	})
	autohttp.Mount(api, autohttp.Deps{
		Wakeups:     wu,
		Autopilots:  auto,
		Webhooks:    autopapp.NewWebhookService(arepo, auto),
		MemberCheck: memberCheck,

		WakeupTasks: func(ctx context.Context, ws, wakeupID string) ([]autopapp.WakeupTaskRef, error) {
			rows, err := db.Tx(ctx).Query(ctx, `
SELECT id::text, status, created_at FROM agent_task_queue
WHERE workspace_id=$1 AND context->>'wakeup_id'=$2
ORDER BY created_at DESC LIMIT 50`, ws, wakeupID)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := []autopapp.WakeupTaskRef{}
			for rows.Next() {
				var r autopapp.WakeupTaskRef
				if err := rows.Scan(&r.ID, &r.Status, &r.CreatedAt); err != nil {
					return nil, err
				}
				out = append(out, r)
			}
			return out, rows.Err()
		},
	}, authMw)

	skillRepo := skillpgrepo.New(db)
	skillhttp.Mount(api, skillhttp.Deps{
		Skills:      skillapp.NewSkillService(skillRepo),
		Import:      skillapp.NewImportServiceWithOptions(skillRepo, skillapp.ImportOptions{}),
		MemberCheck: memberCheck,
		SkillLabels: skillhttp.SkillLabelEndpoints{
			List: func(ctx context.Context, ws, skillID string) ([]skillhttp.SkillLabelView, error) {
				return skillLabelViewsOf(wRepo.ListSkillLabels(ctx, ws, skillID))
			},
			Attach: func(ctx context.Context, ws, skillID, labelID string) ([]skillhttp.SkillLabelView, error) {
				if err := wRepo.AttachSkillLabel(ctx, ws, skillID, labelID); err != nil {
					return nil, err
				}
				return skillLabelViewsOf(wRepo.ListSkillLabels(ctx, ws, skillID))
			},
			Detach: func(ctx context.Context, ws, skillID, labelID string) ([]skillhttp.SkillLabelView, error) {
				if err := wRepo.DetachSkillLabel(ctx, ws, skillID, labelID); err != nil {
					return nil, err
				}
				return skillLabelViewsOf(wRepo.ListSkillLabels(ctx, ws, skillID))
			},
		},
	}, authMw)

	{
		chRepo := chpgrepo.New(db)
		chhttp.Mount(api, chhttp.Deps{
			Titles: chapp.NewTitleCallbackService(chRepo), Repo: chRepo,
		})
	}

	var contentDeps contenthttp.Deps
	{
		dir := "./e2e-uploads"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("e2e uploads dir: %v", err)
		}
		local, err := storagex.NewLocal(dir)
		if err != nil {
			t.Fatalf("e2e local store: %v", err)
		}
		cRepo := contentpgrepo.New(db)
		att := contentapp.NewAttachmentService(cRepo, local, secret, nil)
		att.SetMemberGate(memberCheck)
		att.SetHostChecker(func(ctx context.Context, ws, kind, hid string) (bool, error) {
			switch kind {
			case "work_item":
				_, ok, err := wRepo.GetWorkItem(ctx, ws, hid)
				return ok, err
			case "comment":
				c, ok, err := wRepo.GetComment(ctx, hid)
				if err != nil || !ok {
					return ok, err
				}
				return c.WorkspaceID == ws, nil
			case "chat_session":
				_, ok, err := convRepo.GetSession(ctx, ws, hid)
				return ok, err
			case "chat_message":
				return convRepo.GetMessage(ctx, hid)
			case "task":
				_, ok, err := orepo.GetTask(ctx, ws, hid)
				return ok, err
			}
			return true, nil
		})
		contentDeps = contenthttp.Deps{
			Attachments: att,
			Avatars:     contentapp.NewAvatarService(local, secret, nil),
			Local:       local,
			MemberCheck: memberCheck,
			RoleOf: func(ctx context.Context, ws, uid string) (string, error) {
				m, ok, err := idRepo.GetMember(ctx, ws, uid)
				if err != nil || !ok {
					return "", err
				}
				return m.Role, nil
			},
		}
		contenthttp.MountAPI(api, contentDeps, authMw)
	}
	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	root.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			http.Error(w, "db unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	root.Handle("/api/", api)
	wsDeps := workspacehttp.Deps{Svc: wsSvc, Members: workspacepg.New(pool),
		Authz: authz.New(authz.DefaultCatalog(), authz.DefaultRoles())}
	workspacehttp.Mount(api, wsDeps)
	wsMux := webx.NewMux()
	workspacehttp.Mount(wsMux, wsDeps) // recording twin for route enumeration (e2e walk)
	rootRec := webx.NewMux()
	identityhttp.Mount(root, identityhttp.Deps{
		AuthCfg: *auth, Sessions: fwRepo, PATs: fwRepo, SessionsSvc: sessions, Tokens: tokens,
	})
	identityhttp.Mount(rootRec, identityhttp.Deps{ // recording twin for the walk
		AuthCfg: *auth, Sessions: fwRepo, PATs: fwRepo, SessionsSvc: sessions, Tokens: tokens,
	})
	root.HandleFunc("GET /config", webx.ConfigHandler(webx.ConfigDeps{}))
	rootRec.HandleFunc("GET /config", func(w http.ResponseWriter, _ *http.Request) {})
	idhttp.MountExtensions(rootRec, func(next http.Handler) http.Handler { return next }, idDeps)
	contenthttp.MountUploads(root, contentDeps)
	idhttp.MountExtensions(root, authMw, idDeps)
	autohttp.MountPublic(root, autohttp.Deps{Webhooks: autopapp.NewWebhookService(arepo, auto)})

	s := &stack{t: t, pool: pool, rootRec: rootRec, wsMux: wsMux, handler: webx.ClientMetadata(webx.CSRFConditional(&webx.CSRFConfig{Key: webx.DeriveCSRFKey(secret), TrustLocalhostAnyPort: true}, false)(authMw(root))), rootMux: root, authMw: authMw, orepo: orepo, settle: settle, eng: eng, auto: auto,
		wRepo: wRepo, comments: workapp.NewCommentService(wRepo, bus, time.Now),
		jar: map[string][]*http.Cookie{}, csrf: map[string]string{}, bus: bus}

	dctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := &workers.Dispatcher{Repo: orepo, Claim: orchapp.NewClaimService(orepo), Settle: settle,
		Engine: engWrap{e: eng}, Log: log, Emit: bus,
		PollEvery: 20 * time.Millisecond, Semaphore: 4, Timeout: 10 * time.Second}
	go func() { _ = d.Run(dctx) }()
	_ = autopworkers.AutopilotCron
	return s
}

func (s *stack) do(client, method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "192.0.2.9:5555"
	for _, c := range s.jar[client] {
		req.AddCookie(c)
	}
	if s.csrf[client] != "" {
		req.Header.Set("X-CSRF-Token", s.csrf[client])
	}
	if s.wsID != "" {
		req.Header.Set("X-Workspace-Id", s.wsID)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {

		if c.Name == "orkome_auth" || c.Name == "ploykit_csrf" {
			s.jar[client] = append(s.jar[client], c)
		}
	}
	return rec
}

func (s *stack) login(client, email string) {
	rec := s.do(client, http.MethodGet, "/config", nil, nil)
	require.Equal(s.t, 200, rec.Code)
	var cfg struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cfg)
	s.csrf[client] = cfg.CSRFToken
	rec = s.do(client, http.MethodPost, "/auth/verify-code",
		map[string]string{"email": email, "code": "654321"}, nil)
	require.Equal(s.t, 200, rec.Code, rec.Body.String())
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.CSRFToken != "" { // v0.4.1: verify-code no longer rotates the token
		s.csrf[client] = out.CSRFToken
	}
}

func (s *stack) createWS(t *testing.T, client, slug string) string {
	rec := s.do(client, http.MethodPost, "/api/workspaces",
		map[string]string{"name": "E2E-" + slug, "slug": slug}, nil)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var ws struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ws)
	require.NotEmpty(t, ws.ID)
	return ws.ID
}

func (s *stack) createWorkItem(t *testing.T, client, title string) string {
	rec := s.do(client, http.MethodPost, "/api/work-items", map[string]any{"title": title, "status": "todo"}, nil)
	require.Equal(t, 201, rec.Code, rec.Body.String())
	var wi struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &wi)
	require.NotEmpty(t, wi.ID)
	return wi.ID
}

func (s *stack) createAgent(t *testing.T, ws, name string) string {
	rec := s.do("admin", http.MethodPost, "/api/agents-none", nil, nil)
	_ = rec
	var id string
	err := s.pool.QueryRow(context.Background(), `
INSERT INTO agent (workspace_id, name, description, runtime_config)
VALUES ($1,$2,'goldens worker',$3) RETURNING id::text`, ws, name,
		map[string]any{"provider": "fake", "model": "fake-1"}).Scan(&id)
	require.NoError(t, err)
	return id
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("wait timeout: %s", what)
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

type chatEnqA struct{ repo orchapp.Repo }

func (a chatEnqA) EnqueueChat(ctx context.Context, ws, session, agent, user, content string) (string, error) {
	t, err := a.repo.InsertChatTask(ctx, orchapp.Task{
		WorkspaceID: ws, AgentID: agent,
		Context:          map[string]any{"chat_session_id": session, "message": content},
		OriginatorUserID: user, TriggerEvidenceKind: "chat",
	})
	if err != nil {
		return "", err
	}
	return t.ID, nil
}
