package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"time"

	"orkome/internal/contract/apierr"
	"orkome/internal/contract/events"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	convapp "orkome/internal/conversation/app"
	idpgrepo "orkome/internal/identity/adapters/pgrepo"
	idapp "orkome/internal/identity/app"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	notifapp "orkome/internal/notification/app"
	"orkome/internal/orchestration/adapters/engine"
	orchhttp "orkome/internal/orchestration/adapters/http"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	"orkome/internal/orchestration/adapters/workers"
	orchapp "orkome/internal/orchestration/app"
	"github.com/haozing/ploykit/platform/egressx"
	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/outboxx"
	"github.com/haozing/ploykit/platform/pg"
	"github.com/haozing/ploykit/platform/sealx"
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
	s, err := sealx.New(deriveSealKey(cfg.JWTSecret, "credential"))
	if err != nil {
		log.Error("failed to construct credential sealer", "err", err)
		os.Exit(1)
	}
	return s
}

// deriveSealKey derives a per-domain 32-byte sealing key from a base secret
// (product policy; the framework sealx only takes ready-made keys).
func deriveSealKey(baseSecret, domain string) []byte {
	mac := hmac.New(sha256.New, []byte(baseSecret))
	mac.Write([]byte("orkome:seal:" + domain))
	return mac.Sum(nil)
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
) (*workers.Dispatcher, *workers.LivenessSweeper, orchhttp.Deps, *orchapp.EnqueueService, *engine.EinoEngine, *orchapp.BatchService) {
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
	agents := orchapp.NewAgentService(repo, bus)
	agents.Egress = egressGuard
	enq := orchapp.NewEnqueueService(repo, bus)
	eng.LeaderDispatch = enq
	trig := orchapp.NewTriggerService(repo, bus)
	settle := orchapp.NewSettlementService(repo, bus, notification,
		workCommentWriter{wRepo: wRepo},
		chatWriter{repo: convRepo},
	)

	settle.SetApprovalDeps(approvalNotifier{repo: notification}, eng, func(ctx context.Context, ws, credID string) (string, error) {
		_, secret, ok, err := repo.GetCredential(ctx, ws, credID)
		if err != nil || !ok {
			return "", err
		}
		return secret, nil
	})
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

		idr := idpgrepo.New(db)
		email := "agent-" + agentID + "@runtime.internal"
		u, err := idr.UpsertUserByEmail(ctx, email)
		if err != nil {
			return "", err
		}
		if _, ok, _ := idr.GetMember(ctx, ws, u.ID); !ok {
			if _, err := idr.AddMember(ctx, ws, u.ID, "member"); err != nil {
				return "", err
			}
		}
		if name == "" {
			name = "runtime " + a.Name
		}
		res, err := idapp.NewTokenService(idr, nil, time.Now).Create(ctx, u.ID, name, nil)
		if err != nil {
			return "", err
		}

		_, err = db.Tx(ctx).Exec(ctx, `
INSERT INTO agent_credential (workspace_id, agent_id, pat_id, name)
VALUES ($1,$2,$3,$4)`, ws, agentID, res.PAT.ID, name)
		if err != nil {
			return "", err
		}
		return res.Plaintext, nil
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

	return d, sweeper, orchhttp.Deps{
		Settle: settle, Agents: agents,
		Repo:        repo,
		Trigger:     trig,
		Batches:     batches,
		AgentLabels: agentLabels,
		Squads:      orchapp.NewSquadService(repo, nil),
		MCP: func() *orchapp.MCPServerService {
			m := orchapp.NewMCPServerService(repo)
			m.Egress = egressGuard
			return m
		}(),
		MemberCheck:           memberCheck,
		Credentials:           wiringCredentials(repo),
		RoleOf:                roleOf(db),
		External:              external,
		PackUIEntries:         orchapp.NewPackUIEntryService(repo),
		MintRuntimeCredential: mintRuntimeCredential,
		TaskMessages: func(ctx context.Context, ws, taskID string, q orchapp.TaskMessagesQuery) ([]orchapp.TaskMessage, error) {
			return repo.ListTaskMessages(ctx, ws, taskID, q)
		},
	}, enq, eng, batches
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
