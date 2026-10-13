package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	identitypg "github.com/haozing/ploykit/identity/adapters/pgrepo"
	fwapp "github.com/haozing/ploykit/identity/app"
	"github.com/haozing/ploykit/platform/egressx"
	"github.com/haozing/ploykit/platform/pg"
	"github.com/haozing/ploykit/platform/sealx"
	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	convapp "orkome/internal/conversation/app"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	notifapp "orkome/internal/notification/app"
	"orkome/internal/orchestration/adapters/engine"
	orchhttp "orkome/internal/orchestration/adapters/http"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	"orkome/internal/orchestration/adapters/workers"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/outboxx"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
	workdomain "orkome/internal/work/domain"
)

func credentialSealer(log *slog.Logger, cfg *Config) *sealx.Sealer {
	if cfg.CredentialMasterKey != "" {
		s, err := sealx.New([]byte(cfg.CredentialMasterKey))
		if err != nil {
			log.Error("invalid ENGINE_CREDENTIAL_MASTER_KEY", "err", err)
			os.Exit(1)
		}
		return s
	}
	log.Warn("ENGINE_CREDENTIAL_MASTER_KEY not set: non-production fallback—credential key derived from JWT_SECRET (must be set explicitly in production)")
	s, err := sealx.New(sealx.DeriveKey(cfg.JWTSecret, "credential"))
	if err != nil {
		log.Error("failed to construct credential sealer", "err", err)
		os.Exit(1)
	}
	return s
}

func egressGuardOf(cfg *Config) *egressx.Guard {
	g, err := egressx.NewGuard(cfg.EgressExtraAllowCIDRs)
	if err != nil {
		panic(err)
	}
	return g
}

func wiringOrchestration(log *slog.Logger, db *pg.DB, bus *eventbus.Bus, cfg *Config,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
) (*workers.Dispatcher, *workers.LivenessSweeper, orchhttp.Deps, *orchapp.EnqueueService, *engine.EinoEngine, *orchapp.BatchService, map[string]orchapp.BatchProcessor) {
	repo := orchpgrepo.New(db)
	repo.Seal = credentialSealer(log, cfg)
	convRepo := convpgrepo.New(db)
	wRepo := &workpgrepo.Repo{DB: db}
	notification := notifpgrepo.New(db)

	instanceID := fmt.Sprintf("%s-%d", hostname(), os.Getpid())

	wSvc := workapp.NewWorkItemService(wRepo, bus, nil)
	bench := workBenchAdapter{wRepo: wRepo, svc: wSvc}
	mcpSvc := orchapp.NewMCPServerService(repo)
	mcpSvc.Egress = egressGuardOf(cfg)

	eng := &engine.EinoEngine{
		Checkpoints: &engine.CheckpointStore{DB: db},
		WorkBench:   bench,
		InstanceID:  instanceID,

		FailoverCredentials: repo.CredentialChain,

		ChildItems:   bench,
		StatusUpdate: bench,
		SquadEvals:   bench,
		MCPResolver:  mcpResolverAd{svc: mcpSvc},

		TaskMessages: repo,

		// C1：统一 Tool 注册表接线——B1 存档读取（未自描述工具补输出
		// 契约，勿走 HTTP，见 handoff-b1 §3）+ 引擎侧 task.context 快照
		// 回写（tools 全量清单 + mcp_servers 细化态）+ 输出契约违规审计
		// 日志（A1 遗留坑 #7：AuditLog 未显式接线，此处收口——专用
		// audit logger 复用主 logger 的 handler，键名带 audit 语义经
		// 结构化字段区分，不额外开文件句柄）。
		PackToolSchemas:    orchapp.NewPackToolSchemaService(repo),
		TaskContextPatcher: repo,
		AuditLog:           slog.New(log.Handler()).With(slog.String("channel", "audit")),

		WorkItemSession: func(ctx context.Context, wi, agentID string) ([]orchapp.SessionTurn, error) {
			return repo.SessionTranscript(ctx, wi, agentID, 5)
		},

		SquadLoader: func(ctx context.Context, wsID, squadID string) (orchapp.SquadInfo, bool, error) {
			return repo.GetSquad(ctx, wsID, squadID)
		},

		WorkspaceSearch: func(ctx context.Context, wsID, q string) ([]orchapp.SearchHit, error) {
			hits, err := workapp.NewSearchService(wRepo).SearchFederated(ctx, wsID, q)
			if err != nil {
				return nil, err
			}
			out := make([]orchapp.SearchHit, len(hits))
			for i, h := range hits {
				out[i] = orchapp.SearchHit{WorkItemID: h.WorkItemID, Title: h.Title, Snippet: h.Snippet, Source: h.Source}
			}
			return out, nil
		},
		TaskStatus: func(ctx context.Context, taskID string) (string, error) {
			var st string
			err := db.Tx(ctx).QueryRow(ctx,
				`SELECT status FROM agent_task_queue WHERE id=$1`, taskID).Scan(&st)
			return st, err
		},

		SupplementClaimer: func(ctx context.Context, taskID string) (string, bool, func() error) {
			id, content, ok, err := repo.ClaimNextSupplement(ctx, taskID)
			if err != nil || !ok {
				return "", false, nil
			}
			return content, true, func() error { return repo.AckSupplementDelivered(ctx, id) }
		},

		ChatHistory: func(ctx context.Context, sessionID string, limit int) ([]engine.ChatTurn, error) {
			ms, err := convRepo.ListMessagesAsc(ctx, sessionID)
			if err != nil {
				return nil, err
			}
			var out []engine.ChatTurn
			for _, m := range ms {
				if m.Kind == "message" || m.Kind == "no_response" {
					out = append(out, engine.ChatTurn{Role: m.Role, Content: m.Content})
				}
			}
			if len(out) > limit {
				out = out[len(out)-limit:]
			}
			return out, nil
		},

		CredentialFn: func(ctx context.Context, workspaceID, credentialID string) (string, error) {
			_, secret, ok, err := repo.GetCredential(ctx, workspaceID, credentialID)
			if err != nil || !ok {
				return "", err
			}
			return secret, nil
		},
	}

	egressGuard := egressGuardOf(cfg)
	engine.SetEgressGuard(egressGuard)
	// 第三处接线缺口（真模型 E2E 实证）：SetEgressGuard 只设置模型工厂的
	// 包级 guard；MCP 桥接（buildMCPTools）读的是 EinoEngine.Egress 字段，
	// 不赋值则恒为 DefaultGuard——loopback/内网 MCP pack 全部 ssrf_rejected。
	eng.Egress = egressGuard
	// 工具集成页"查看工具"：工作区级 MCP 服务器实时探针（建连+ListTools，
	// 与运行期同参；B1 存档补输出契约同 buildMCPTools）。app 层不依赖
	// engine 包，经函数注入反转。probeToolProbeFn 供两个 MCPServerService
	// 实例（引擎 resolver 用 / HTTP Deps 用）共用——接线只做一次。
	probeFn := func(ctx context.Context, srv orchapp.ResolvedMCPServer, workspaceID string) ([]orchapp.ProbeTool, error) {
		archived := map[string]json.RawMessage{}
		if eng.PackToolSchemas != nil {
			if list, err := eng.PackToolSchemas.ListPackToolSchemas(ctx, workspaceID); err == nil {
				for _, it := range list {
					if it.ToolName == "" || it.OutputSchema == nil {
						continue
					}
					b, err := json.Marshal(it.OutputSchema)
					if err != nil {
						continue
					}
					archived[it.ToolName] = b
				}
			}
		}
		probe, err := engine.ProbeServerTools(ctx, srv, egressGuard, archived)
		if err != nil {
			return nil, err
		}
		out := make([]orchapp.ProbeTool, len(probe))
		for i, p := range probe {
			out[i] = orchapp.ProbeTool{Name: p.Name, Description: p.Description, OutputSchema: p.OutputSchema}
		}
		return out, nil
	}
	mcpSvc.ToolProbe = probeFn
	agents := orchapp.NewAgentService(repo, bus)
	agents.Egress = egressGuard
	enq := orchapp.NewEnqueueService(repo, bus)
	eng.LeaderDispatch = enq
	trig := orchapp.NewTriggerService(repo, bus)
	settle := orchapp.NewSettlementService(repo, bus, notification,
		workCommentWriter{wRepo: wRepo},
		chatWriter{repo: convRepo},
	)
	// 工单产出（"需求→产出"一等公民）：completed 结算后写覆盖 work_item.outcome
	settle.SetOutcomeWriter(workOutcomeWriter{wRepo: wRepo})
	// 派发式小队自动回环：成员任务终态后检查同队全部收割 → leader 插收割任务
	//（兑现 dispatch_to_member/create_child_work_item 对 leader 的"woken
	// automatically"承诺；真模型 E2E 实证该回环此前未实现）。
	settle.SetSquadWake(squadLeaderWake{repo: repo, enqueue: enq, log: log})

	settle.SetApprovalDeps(approvalNotifier{repo: notification}, eng, func(ctx context.Context, ws, credID string) (string, error) {
		_, secret, ok, err := repo.GetCredential(ctx, ws, credID)
		if err != nil || !ok {
			return "", err
		}
		return secret, nil
	})
	// A2：summary 生成上移 service 层。开关 ORKOME_SUMMARY_LLM_ENABLED
	// 默认关——关闭时纯截断（byte-identical 旧行为），打开走 LLM 七段
	// checkpoint（Engine.Generate aux 调用，失败 fail-open 回落）。
	summarySvc := orchapp.NewSummaryService(repo, eng, func(ctx context.Context, ws, credID string) (string, error) {
		_, secret, ok, err := repo.GetCredential(ctx, ws, credID)
		if err != nil || !ok {
			return "", err
		}
		return secret, nil
	}, log)
	summarySvc.Enabled = cfg != nil && cfg.SummaryLLMEnabled
	settle.SetSummarySummarizer(summarySvc)

	settle.SetRuntimeInstance(instanceID)

	notifRepo := notifpgrepo.New(db)
	notify := notifapp.NewNotifyService(notifRepo, subscriberPort{wRepo: wRepo})
	bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		var title, body, wi string
		var direct []string
		switch e.Event {
		case events.TaskCompleted, events.TaskFailed:
			title = "Task " + map[bool]string{true: "completed", false: "failed"}[e.Event == events.TaskCompleted]
			if p, ok := e.Payload.(orchapp.TaskPayload); ok {
				wi = p.WorkItemID
				body = p.Result
			}
		case events.CommentCreated:
			title = "New comment"
			if p, ok := e.Payload.(events.CommentPayload); ok {
				wi = p.WorkItemID
				body = p.Content
				direct = notifapp.ParseMentionedUsers(p.Content)
			}
		default:
			return
		}
		_ = notify.Handle(ctx, notifapp.EventIn{
			EventID: e.EventID, WorkspaceID: e.WorkspaceID, Type: e.Event,
			WorkItemID: wi, Title: title, Body: body, DirectUserIDs: direct,
			// FixF 移交激活项：起源者自通知过滤（FP-10 P2-2 / FP-06 P2-2）。
			// 空串不过滤——不接这行，投影侧过滤器在生产恒不生效。
			ActorUserID: e.ActorUserID,
		})
	})

	workapp.SetTriggerEngine(triggerPort{trig: trig})
	workapp.SetAssigneeReader(wRepo)

	orchapp.SetWorkItemReader(workItemPort{wRepo: wRepo})

	agentLabels := &orchhttp.AgentLabelEndpoints{
		List: func(ctx context.Context, ws, agentID string) ([]orchhttp.AgentLabelView, error) {
			return labelViewsOf(wRepo.ListAgentLabels(ctx, ws, agentID))
		},
		Attach: func(ctx context.Context, ws, agentID, labelID string) ([]orchhttp.AgentLabelView, error) {
			if err := wRepo.AttachAgentLabel(ctx, ws, agentID, labelID); err != nil {
				return nil, err
			}
			return labelViewsOf(wRepo.ListAgentLabels(ctx, ws, agentID))
		},
		Detach: func(ctx context.Context, ws, agentID, labelID string) ([]orchhttp.AgentLabelView, error) {
			if err := wRepo.DetachAgentLabel(ctx, ws, agentID, labelID); err != nil {
				return nil, err
			}
			return labelViewsOf(wRepo.ListAgentLabels(ctx, ws, agentID))
		},
	}

	d := &workers.Dispatcher{
		Repo: repo, Agents: agents, Claim: orchapp.NewClaimService(repo),
		Settle: settle, Engine: eng, Log: log, Emit: bus,
		// 凭证解析（真模型密钥链）：agent.runtime_config.credential_id → 密封库解封。
		// 审计后真模型 E2E 发现此线从未接通（dispatcher 空 Bearer 打 LLM 401）。
		CredFn: func(ctx context.Context, ws, credID string) (string, error) {
			_, secret, ok, err := repo.GetCredential(ctx, ws, credID)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", apierr.New(apierr.NotFound, "credential not found")
			}
			return secret, nil
		},
		PollEvery: 500 * time.Millisecond, Semaphore: 4, Timeout: 30 * time.Minute,
		InstanceID: instanceID, HeartbeatEvery: 15 * time.Second,

		MCPResolve: mcpSvc.ResolveForEngine, MCPPatch: repo,
	}
	sweeper := &workers.LivenessSweeper{
		Repo: repo, Settle: settle, Log: log, Emit: bus,
		Every: 30 * time.Second, StaleSeconds: 150,

		Trigger: trig, JanitorEvery: 5 * time.Minute, JanitorHours: 24,
	}

	recorder := outboxx.NewRecorder(db)
	enq.SetOutbox(recorder, db.Within)
	settle.SetOutbox(recorder, db.Within)

	external := orchapp.NewExternalTaskService(repo, bus)
	external.SetOutbox(recorder, db.Within)
	external.SetSummarySummarizer(summarySvc) // A2：external runtime 结算同口径
	mintRuntimeCredential := func(ctx context.Context, ws, agentID, name string) (string, error) {
		a, ok, err := repo.GetAgent(ctx, ws, agentID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", apierr.New(apierr.NotFound, "agent not found")
		}
		if a.RuntimeKind != "external" {
			return "", apierr.NewValidation("runtime credentials are for external agents")
		}

		fwRepo := identitypg.New(db.Pool(), identitypg.Config{})
		email := "agent-" + agentID + "@runtime.internal"
		u, err := fwRepo.UpsertUserByEmail(ctx, email, time.Now())
		if err != nil {
			return "", err
		}
		if _, err := db.Tx(ctx).Exec(ctx, `
INSERT INTO member (workspace_id, user_id, role)
VALUES ($1,$2,'member') ON CONFLICT DO NOTHING`, ws, u.ID); err != nil {
			return "", err
		}
		if name == "" {
			name = "runtime " + a.Name
		}
		pat, plaintext, err := fwapp.NewTokenService(fwRepo, nil, time.Now).WithPATPrefix("ork_").CreatePAT(ctx,
			&webx.Principal{UserID: u.ID, Source: webx.SourceSession}, name, 0, nil)
		if err != nil {
			return "", err
		}

		_, err = db.Tx(ctx).Exec(ctx, `
INSERT INTO agent_credential (workspace_id, agent_id, pat_id, name)
VALUES ($1,$2,$3,$4)`, ws, agentID, pat.ID, name)
		if err != nil {
			return "", err
		}
		return plaintext, nil
	}

	orchapp.SetEngineTag(eng.Name())

	if cfg != nil && cfg.EvalSampleRate > 0 {
		ev := orchapp.NewEvalService(eng, repo, notification, func(ctx context.Context, ws, credID string) (string, error) {
			_, secret, ok, err := repo.GetCredential(ctx, ws, credID)
			if err != nil || !ok {
				return "", err
			}
			return secret, nil
		}, log.Warn)
		ev.Rate = cfg.EvalSampleRate
		ev.DailyQuota = cfg.EvalDailyQuota
		bus.On(eventbus.PhaseBroadcast, func(ctx context.Context, e events.Envelope) {
			if e.Event != events.TaskCompleted {
				return
			}
			if p, ok := e.Payload.(orchapp.TaskPayload); ok {
				if t, ok2, _ := repo.GetTask(ctx, p.WorkspaceID, p.ID); ok2 && t.Status == "completed" {
					ev.OnTaskCompleted(ctx, t)
				}
			}
		})
	}

	batches := orchapp.NewBatchService(&orchpgrepo.BatchRepo{DB: db}, nil)
	// llm_comment 的 params 自动补全（workspace_id/work_item_id + 工单指派
	// agent）：用户在批量面板只填 payloads，三个上下文 ID 由 server 从工单
	// 解析——指派 agent 缺失时拒绝创建（fail-fast，而非条目全败）。
	batches.ParamsFiller = func(ctx context.Context, b *orchapp.Batch) error {
		if b.Kind != "llm_comment" {
			return nil
		}
		b.Params["workspace_id"] = b.WorkspaceID
		b.Params["work_item_id"] = b.WorkItemID
		if b.Params["agent_id"] == nil || b.Params["agent_id"] == "" {
			wi, ok, err := wRepo.GetWorkItem(ctx, b.WorkspaceID, b.WorkItemID)
			if err != nil {
				return err
			}
			if !ok || wi.AssigneeType != "agent" || wi.AssigneeID == "" {
				return errors.New("llm_comment requires the work item to be assigned to an agent (assignee_id missing)")
			}
			b.Params["agent_id"] = wi.AssigneeID
		}
		return nil
	}

	// 首个真模型 BatchProcessor（kind=llm_comment）：payload.seed → 工单指派
	// agent 生成摘要并 post_comment。Batch 执行面此前为插件留白（Registry 空）。
	batchRegistry := map[string]orchapp.BatchProcessor{
		"llm_comment": &llmCommentBatchProcessor{
			engine: eng, wb: bench,
			resolve: func(ctx context.Context, ws, agentID string) (orchapp.Agent, string, error) {
				a, ok, err := repo.GetAgent(ctx, ws, agentID)
				if err != nil {
					return orchapp.Agent{}, "", err
				}
				if !ok {
					return orchapp.Agent{}, "", fmt.Errorf("agent %s not found", agentID)
				}
				secret := ""
				if id, _ := a.RuntimeConfig["credential_id"].(string); id != "" {
					_, sec, _, serr := repo.GetCredential(ctx, ws, id)
					if serr != nil {
						return orchapp.Agent{}, "", serr
					}
					secret = sec
				}
				return a, secret, nil
			},
			log: log,
		},
	}

	deps := orchhttp.Deps{
		Settle: settle, Agents: agents,
		Repo:        repo,
		Trigger:     trig,
		Batches:     batches,
		AgentLabels: agentLabels,
		Squads:      orchapp.NewSquadService(repo, nil),
		MCP: func() *orchapp.MCPServerService {
			m := orchapp.NewMCPServerService(repo)
			m.Egress = egressGuard
			m.ToolProbe = probeFn
			return m
		}(),
		MemberCheck:           memberCheck,
		Credentials:           wiringCredentials(repo),
		RoleOf:                roleOf(db),
		External:              external,
		PackUIEntries:         orchapp.NewPackUIEntryService(repo),
		PackToolSchemas:       orchapp.NewPackToolSchemaService(repo),
		MintRuntimeCredential: mintRuntimeCredential,
		TaskMessages: func(ctx context.Context, ws, taskID string, q orchapp.TaskMessagesQuery) ([]orchapp.TaskMessage, error) {
			return repo.ListTaskMessages(ctx, ws, taskID, q)
		},
	}
	return d, sweeper, deps, enq, eng, batches, batchRegistry
}

func wiringCredentials(repo *orchpgrepo.Repo) *orchapp.CredentialService {
	return orchapp.NewCredentialService(repo)
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

type workBenchAdapter struct {
	wRepo *workpgrepo.Repo
	svc   *workapp.WorkItemService
}

func (a workBenchAdapter) CreateChildWorkItem(ctx context.Context, in orchapp.ChildItemInput) (orchapp.WorkItemInfo, error) {
	wi, err := a.svc.CreateChildByAgent(ctx, in.WorkspaceID, in.ParentID, in.Title, in.Description,
		in.CreatorAgentID, workdomain.DelegationChain{
			OriginType:           in.Delegation.OriginType,
			ChainRootUserID:      in.Delegation.ChainRootUserID,
			ChainRootDirectHuman: in.Delegation.ChainRootDirectHuman,
		})
	if err != nil {
		return orchapp.WorkItemInfo{}, err
	}
	return orchapp.WorkItemInfo{ID: wi.ID, Title: wi.Title, Desc: wi.Description}, nil
}

func (a workBenchAdapter) UpdateStatusByAgent(ctx context.Context, ws, wi, agentID, statusKey string) error {
	_, err := a.svc.UpdateStatusByAgent(ctx, ws, wi, agentID, statusKey)
	return err
}

func (a workBenchAdapter) RecordSquadEvaluation(ctx context.Context, e orchapp.SquadEvaluation) error {
	return a.svc.RecordSquadEvaluationByAgent(ctx, workapp.SquadEvaluationRecord{
		WorkspaceID: e.WorkspaceID, WorkItemID: e.WorkItemID, LeaderAgentID: e.LeaderAgentID,
		SquadID: e.SquadID, TaskID: e.TaskID, Member: e.Member, Verdict: e.Verdict, Rationale: e.Rationale,
	})
}

func (a workBenchAdapter) PostAgentComment(ctx context.Context, ws, wi, agentID, content string) error {
	_, err := a.wRepo.CreateComment(ctx, workapp.NewComment{
		WorkspaceID: ws, WorkItemID: wi, AuthorType: "agent", AuthorID: agentID, Content: content,
	})
	return err
}

func (a workBenchAdapter) GetWorkItemBrief(ctx context.Context, ws, wi string) (string, string, error) {
	w, _, err := a.wRepo.GetWorkItem(ctx, ws, wi)
	return w.Title, w.Description, err
}

type approvalNotifier struct{ repo *notifpgrepo.Repo }

func (a approvalNotifier) WriteApprovalRequest(ctx context.Context, ws, userID, taskID, payload string) error {
	return a.repo.WriteApprovalRequest(ctx, ws, userID, taskID, payload)
}

type subscriberPort struct{ wRepo *workpgrepo.Repo }

func (p subscriberPort) ListSubscriberUserIDs(ctx context.Context, workItemID string) ([]string, error) {
	subs, err := p.wRepo.ListSubscribers(ctx, workItemID)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(subs))
	for i, s := range subs {
		out[i] = s.UserID
	}
	return out, nil
}

type triggerPort struct{ trig *orchapp.TriggerService }

func (p triggerPort) Preview(ctx context.Context, in workapp.TriggerInput) ([]workapp.PreviewHit, []workapp.PreviewHit, error) {
	out, err := p.trig.Preview(ctx, orchapp.CommentInput{
		WorkspaceID: in.WorkspaceID, WorkItemID: in.WorkItemID,
		AuthorType: in.AuthorType, AuthorID: in.AuthorID, Content: in.Content,
		ParentAuthorType: in.ParentAuthorType, ParentAuthorID: in.ParentAuthorID,
		RootAuthorType: in.RootAuthorType, RootAuthorID: in.RootAuthorID,
		AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
	})
	if err != nil {
		return nil, nil, err
	}
	var tg, sp []workapp.PreviewHit
	for _, h := range out.Triggers {
		tg = append(tg, workapp.PreviewHit{AgentID: h.AgentID, Reason: h.Reason})
	}
	for _, h := range out.Suppressions {
		sp = append(sp, workapp.PreviewHit{AgentID: h.AgentID, Reason: h.Reason})
	}
	return tg, sp, nil
}

func (p triggerPort) OnComment(ctx context.Context, in workapp.TriggerInput, commentID string) (string, string, error) {
	return p.trig.OnComment(ctx, orchapp.CommentInput{
		WorkspaceID: in.WorkspaceID, WorkItemID: in.WorkItemID,
		AuthorType: in.AuthorType, AuthorID: in.AuthorID, Content: in.Content,
		ParentAuthorType: in.ParentAuthorType, ParentAuthorID: in.ParentAuthorID,
		RootAuthorType: in.RootAuthorType, RootAuthorID: in.RootAuthorID,
		AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
	}, commentID)
}

type workItemPort struct{ wRepo *workpgrepo.Repo }

func (p workItemPort) GetWorkItem(ctx context.Context, ws, id string) (orchapp.WorkItemInfo, bool, error) {
	wi, ok, err := p.wRepo.GetWorkItem(ctx, ws, id)
	return orchapp.WorkItemInfo{ID: wi.ID, Title: wi.Title, Desc: wi.Description}, ok, err
}

// squadLeaderWake orchapp.LeaderWakeHook 的装配适配器：成员任务终态后检查
// 同工单同小队是否全部成员已收割，是则给 leader 插入一个收割任务
//（IsLeaderTask=true，instruction 指向 record_evaluation + 最终汇总）。
type squadLeaderWake struct {
	repo    *orchpgrepo.Repo
	enqueue *orchapp.EnqueueService
	log     *slog.Logger
}

func (w squadLeaderWake) WakeLeaderIfSquadDone(ctx context.Context, memberTask orchapp.Task) error {
	squadID, _ := memberTask.Context["squad_id"].(string)
	leaderTaskID, _ := memberTask.Context["leader_task_id"].(string)
	w.log.Info("squad wake: invoked", "trigger", memberTask.Context["trigger"], "squad_id", squadID, "leader_task_id", leaderTaskID, "wi", memberTask.WorkItemID)
	if squadID == "" || leaderTaskID == "" {
		return nil
	}
	leader, ok, err := w.repo.FindLeaderTaskByID(ctx, memberTask.WorkspaceID, leaderTaskID)
	if err != nil {
		w.log.Error("squad wake: find leader failed", "err", err)
		return err
	}
	if !ok {
		w.log.Error("squad wake: leader task not found", "squad", squadID)
		return nil
	}
	// 唯一性：leader 已有收割任务在队（同工单同队 leader 任务 active）则不重复挂
	open, err := w.repo.CountOpenDispatchedMembers(ctx, memberTask.WorkspaceID, memberTask.WorkItemID, leader.ID)
	if err != nil {
		w.log.Error("squad wake: count open failed", "err", err)
		return err
	}
	if open > 0 {
		w.log.Info("squad wake: members still open, skip", "open", open, "leader_task", leader.ID)
		return nil
	}
	// leader 自身 active（如正在跑）也不重复挂——HasActiveTask 语义复用
	if active, err := w.repo.HasActiveTask(ctx, memberTask.WorkspaceID, memberTask.WorkItemID, leader.AgentID); err == nil && active {
		w.log.Info("squad wake: leader has active task, skip", "leader_task", leader.ID)
		return nil
	}
	instruction := "小队成员任务已全部完成。请收尾：1) 用 record_evaluation 对各成员工作记录评估（正式台账，勿用评论代替）；2) 汇总各成员产出，用 post_comment 发中文总结评论（含各成员结果与最终结论）；3) 视需要更新工单状态。"
	// 收割任务直接插 leader 任务（IsLeaderTask=true + SquadID）——
	// EnqueueLeaderDispatch 会阻断 MemberAgentID==LeaderAgentID，不适用。
	_, reason, err := w.enqueue.EnqueueSquadHarvest(ctx, orchapp.SquadHarvestInput{
		WorkspaceID: memberTask.WorkspaceID, WorkItemID: memberTask.WorkItemID,
		SquadID: squadID, LeaderTaskID: leader.ID, LeaderAgentID: leader.AgentID,
		Instruction:    instruction,
		OriginatorUserID: memberTask.OriginatorUserID, AccountableUserID: memberTask.AccountableUserID,
	})
	if err != nil {
		w.log.Error("squad wake: enqueue harvest task failed", "err", err)
		return err
	}
	w.log.Info("squad wake: harvest task enqueued", "leader_agent", leader.AgentID, "reason", reason)
	return nil
}

// workOutcomeWriter orchapp.OutcomeWriter 的 work 域适配器：把结算产出
// 组装成 work.Outcome 落 work_item.outcome（后写覆盖）。
type workOutcomeWriter struct{ wRepo *workpgrepo.Repo }

func (w workOutcomeWriter) WriteOutcome(ctx context.Context, wsID, wiID, taskID, summary string) error {
	return w.wRepo.WriteOutcome(ctx, wsID, wiID, workapp.Outcome{
		Summary: summary, TaskID: taskID, CompletedAt: time.Now(),
	})
}

type workCommentWriter struct{ wRepo *workpgrepo.Repo }

func (w workCommentWriter) WriteAgentComment(ctx context.Context, wsID, wiID, agentID, content string) error {
	_, err := w.wRepo.CreateComment(ctx, workapp.NewComment{
		WorkItemID: wiID, WorkspaceID: wsID, AuthorType: "agent", AuthorID: agentID, Content: content,
	})
	return err
}

func (w workCommentWriter) HasAgentComment(ctx context.Context, wiID string) (bool, error) {
	rows, err := w.wRepo.ListComments(ctx, wiID)
	if err != nil {
		return false, err
	}
	for _, c := range rows {
		if c.AuthorType == "agent" {
			return true, nil
		}
	}
	return false, nil
}

type chatWriter struct{ repo *convpgrepo.Repo }

func (c chatWriter) AppendAssistantMessage(ctx context.Context, sessionID, taskID, content, kind string) error {
	ps := convapp.NewPointersService(c.repo)
	return ps.AppendAssistantMessage(ctx, sessionID, taskID, content, kind)
}

func (c chatWriter) AppendAssistantParts(ctx context.Context, sessionID, taskID, content, kind string, parts []orchapp.ContentPart) error {
	ps := convapp.NewPointersService(c.repo)
	pm := make([]map[string]any, len(parts))
	for i, p := range parts {
		pm[i] = map[string]any{"type": p.Type, "text": p.Text, "url": p.URL}
	}
	return ps.AppendAssistantParts(ctx, sessionID, taskID, content, kind, pm)
}

func (c chatWriter) UpdateResumePointer(ctx context.Context, sessionID, ref string) error {
	return c.repo.UpdateResumePointer(ctx, sessionID, ref)
}

func (c chatWriter) ClearResumePointer(ctx context.Context, sessionID string) error {
	return c.repo.ClearResumePointer(ctx, sessionID)
}

var _ = events.TaskQueued

type mcpResolverAd struct{ svc *orchapp.MCPServerService }

func (a mcpResolverAd) ResolveMCP(ctx context.Context, ws, agentID string) ([]orchapp.ResolvedMCPServer, error) {
	return a.svc.ResolveForEngine(ctx, ws, agentID)
}

func labelViewsOf(ls []workapp.Label, err error) ([]orchhttp.AgentLabelView, error) {
	if err != nil {
		return nil, err
	}
	out := make([]orchhttp.AgentLabelView, 0, len(ls))
	for _, l := range ls {
		out = append(out, orchhttp.AgentLabelView{ID: l.ID, Name: l.Name, Color: l.Color})
	}
	return out, nil
}
