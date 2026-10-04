package main

import (
	"context"
	"fmt"
	"log/slog"

	chpgrepo "orkome/internal/channels/adapters/pgrepo"
	chworkers "orkome/internal/channels/adapters/workers"
	chapp "orkome/internal/channels/app"
	chengine "orkome/internal/channels/engine"
	"orkome/internal/channels/wire"
	contentdomain "orkome/internal/content/domain"
	"orkome/internal/contract/events"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	convapp "orkome/internal/conversation/app"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	"orkome/internal/platform/metrics"
	"orkome/internal/platform/pg"
	"orkome/internal/platform/storagex"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
)

func wiringChannels(log *slog.Logger, db *pg.DB, cfg *Config, bus *eventbus.Bus, reg *Workers,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
	mreg *metrics.Registry,
) *chengine.Runtime {
	repo := chpgrepo.New(db)
	convRepo := convpgrepo.New(db)
	orepo := orchpgrepo.New(db)
	enq := orchapp.NewEnqueueService(orepo, bus)
	chat := convapp.NewChatService(convRepo, chatEnqAd{enq: enq})
	wSvc := workapp.NewWorkItemService(&workpgrepo.Repo{DB: db}, bus, nil)
	leases := chpgrepo.NewSQLLeaseStore(repo)
	registry := wire.NewRegistry()

	router := chapp.NewRouter(repo,
		convoRouterAd{chat: chat, repo: convRepo},
		invokePolicyAd{db: db},
		chatRunAd{enq: enq, db: db},
		workItemAd{svc: wSvc},
		membershipAd{check: memberCheck},
		chapp.RouterConfig{Logger: log},
	)
	supervisor := chapp.NewSupervisor(repo, leases, registry, inboundAd{r: router}.Handle, chapp.SupervisorConfig{
		Logger: log,
	})
	if mreg != nil {
		supervisor.SetMetrics(mreg)
	}
	senders := chapp.NewSenderRegistry()

	store, _, err := buildStore(cfg)
	if err != nil {
		log.Error("channels wiring: store unavailable (media reconciliation degraded: objects not deleted)", "err", err)
		store = nil
	}
	reconciler := chworkers.NewReconciler(repo, attachmentRefAd{db: db}, deleterAd{store: store})

	reconciler.SetMessageRewriter(mediaRewriterAd{db: db})

	rt := chengine.New(chengine.Config{
		Supervisor: supervisor, Router: router, Reconciler: reconciler,
		Senders: senders, Logger: log,
	})
	reg.AddFunc("channels-runtime", rt.Run)

	outbound := chapp.NewOutboundService(repo, senders, textGuardAd{}, taskOutcomeAd{db: db})
	bus.On(eventbus.PhaseBroadcast, func(ctx context.Context, e events.Envelope) {
		if e.Event != events.TaskCompleted && e.Event != events.TaskFailed {
			return
		}
		if p, ok := e.Payload.(orchapp.TaskPayload); ok {
			_, _ = outbound.DeliverTaskReply(ctx, p.ID)
		}
	})
	return rt
}

type convoRouterAd struct {
	chat *convapp.ChatService
	repo *convpgrepo.Repo
}

func (a convoRouterAd) EnsureChannelSession(ctx context.Context, p chapp.EnsureSessionParams) (string, error) {
	title := p.Title
	if title == "" {
		title = "Channel session"
	}
	s, err := a.chat.CreateSession(ctx, p.WorkspaceID, p.AgentID, p.CreatorUserID, title)
	if err != nil {
		return "", err
	}
	return s.ID, nil
}

func (a convoRouterAd) AppendChannelMessage(ctx context.Context, p chapp.AppendParams, markInTx func(ctx context.Context) error) (chapp.AppendResult, error) {
	seq, err := a.repo.NextSeq(ctx, p.SessionID)
	if err != nil {
		return chapp.AppendResult{}, err
	}
	m, err := a.repo.InsertMessage(ctx, convapp.Message{
		SessionID: p.SessionID, Role: "user", Kind: p.Kind, Content: p.Content, Seq: seq,
	})
	if err != nil {
		return chapp.AppendResult{}, err
	}
	if markInTx != nil {
		_ = markInTx(ctx)
	}
	return chapp.AppendResult{MessageID: m.ID}, nil
}

type invokePolicyAd struct{ db *pg.DB }

func (a invokePolicyAd) MemberMayInvokeAgent(ctx context.Context, agentID, userID string) (bool, error) {
	var visibility string
	err := a.db.Tx(ctx).QueryRow(ctx,
		`SELECT visibility FROM agent WHERE id=$1::uuid`, agentID).Scan(&visibility)
	if err != nil {
		return false, nil
	}
	if visibility == "public" {
		return true, nil
	}
	var n int
	err = a.db.Tx(ctx).QueryRow(ctx, `
SELECT count(*) FROM agent_invocation_target
WHERE agent_id=$1::uuid AND ((target_type='workspace') OR (target_type='member' AND target_id=$2))`,
		agentID, userID).Scan(&n)
	return err == nil && n > 0, nil
}

type chatRunAd struct {
	enq *orchapp.EnqueueService
	db  *pg.DB
}

func (a chatRunAd) EnqueueChannelChatRun(ctx context.Context, p chapp.EnqueueChatRunParams) (string, error) {
	var last string
	_ = a.db.Tx(ctx).QueryRow(ctx, `
SELECT content FROM chat_message
WHERE session_id=$1::uuid AND role='user' ORDER BY seq DESC LIMIT 1`, p.SessionID).Scan(&last)
	const maxRunes = 4000
	if len([]rune(last)) > maxRunes {
		last = string([]rune(last)[:maxRunes])
	}
	taskID, err := a.enq.EnqueueChat(ctx, p.WorkspaceID, p.SessionID, p.AgentID, p.UserID, last)
	if err != nil {
		return "", err
	}

	_, _ = a.db.Tx(ctx).Exec(ctx, `
UPDATE agent_task_queue SET context = coalesce(context,'{}'::jsonb) || jsonb_build_object(
  'channel', jsonb_build_object('context_revision',$2::bigint,'media_pending',$3))
WHERE id=$1::uuid`, taskID, p.ContextRevision, p.MediaPending)
	return taskID, nil
}

type workItemAd struct{ svc *workapp.WorkItemService }

func (a workItemAd) CreateFromChannel(ctx context.Context, p chapp.CreateWorkItemParams) (string, error) {
	wi, err := a.svc.Create(ctx, workapp.NewWorkItem{
		WorkspaceID: p.WorkspaceID, Title: p.Title, Description: p.Description,
		StatusKey: "todo", AssigneeType: "agent", AssigneeID: p.AgentID,
		CreatorType: "user", CreatorID: p.CreatorUserID, OriginType: "channel",
	}, p.CreatorUserID)
	if err != nil {
		return "", err
	}
	return wi.ID, nil
}

type textGuardAd struct{}

func (textGuardAd) BreakLinks(s string) string { return contentdomain.BreakLinkAdjacency(s) }

type taskOutcomeAd struct{ db *pg.DB }

func (a taskOutcomeAd) TaskOutput(ctx context.Context, taskID string) (string, bool, error) {
	var out string
	err := a.db.Tx(ctx).QueryRow(ctx,
		`SELECT coalesce(result,'') FROM agent_task_queue WHERE id=$1::uuid`, taskID).Scan(&out)
	if err != nil {
		return "", false, nil
	}
	return out, true, nil
}

type attachmentRefAd struct{ db *pg.DB }

func (a attachmentRefAd) ExistsByStorageKey(ctx context.Context, workspaceID, storageKey string) (bool, error) {
	var n int
	err := a.db.Tx(ctx).QueryRow(ctx,
		`SELECT count(*) FROM attachment WHERE workspace_id=$1 AND storage_key=$2`,
		workspaceID, storageKey).Scan(&n)
	return n > 0, err
}

type deleterAd struct{ store storagex.Store }

func (a deleterAd) Delete(ctx context.Context, key string) error {
	if a.store == nil {
		return nil
	}
	return a.store.Delete(ctx, key)
}

type membershipAd struct {
	check func(ctx context.Context, workspaceID, userID string) (bool, error)
}

func (a membershipAd) IsWorkspaceMember(ctx context.Context, ws, userID string) (bool, error) {
	return a.check(ctx, ws, userID)
}

type inboundAd struct{ r *chapp.Router }

func (a inboundAd) Handle(ctx context.Context, msg wire.InboundMessage) error {
	_, err := a.r.Handle(ctx, msg)
	return err
}

type chatEnqAd struct{ enq *orchapp.EnqueueService }

func (a chatEnqAd) EnqueueChat(ctx context.Context, ws, sessionID, agentID, userID, content string) (string, error) {
	return a.enq.EnqueueChat(ctx, ws, sessionID, agentID, userID, content)
}

type mediaRewriterAd struct{ db *pg.DB }

func (a mediaRewriterAd) RewriteMediaReady(ctx context.Context, chatMessageID string, refs []chapp.MediaMaterializedRef) error {
	for _, ref := range refs {
		md := markdownOfMedia(ref)
		if md == "" {
			continue
		}

		_, err := a.db.Tx(ctx).Exec(ctx, `
UPDATE chat_message SET content = content || E'
' || $3, updated_at = now()
WHERE id=$1::uuid AND NOT (content LIKE '%' || $2 || '%')`,
			chatMessageID, ref.StorageKey, md)
		if err != nil {
			return err
		}
	}
	return nil
}

func markdownOfMedia(ref chapp.MediaMaterializedRef) string {
	if ref.StorageURL == "" {
		return ""
	}
	if ref.Kind == "image" {
		return fmt.Sprintf("![%s](%s)\n> source: %s", ref.Filename, ref.StorageURL, ref.StorageKey)
	}
	return fmt.Sprintf("[%s](%s)\n> source: %s", ref.Filename, ref.StorageURL, ref.StorageKey)
}
