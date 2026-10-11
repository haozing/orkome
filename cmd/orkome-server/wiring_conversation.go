package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/haozing/ploykit/platform/ids"
	"github.com/haozing/ploykit/platform/pg"
	"orkome/internal/contract/events"
	convhttp "orkome/internal/conversation/adapters/http"
	convpgrepo "orkome/internal/conversation/adapters/pgrepo"
	convapp "orkome/internal/conversation/app"
	"orkome/internal/orchestration/adapters/engine"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
)

func wiringConversation(log *slog.Logger, db *pg.DB, bus *eventbus.Bus,
	eng *engine.EinoEngine, enq *orchapp.EnqueueService, settle *orchapp.SettlementService,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
) convhttp.Deps {
	convRepo := convpgrepo.New(db)
	orchRepo := orchpgrepo.New(db)
	quick := convapp.NewQuickActionService(convRepo)

	gen := convapp.NewQuickActionGenerator(convRepo)
	gen.Engine = qaEngine{e: eng}
	gen.TaskLookup = func(ctx context.Context, taskID string) (string, string, string, bool) {
		var wsID, agentID, creator string
		err := db.Tx(ctx).QueryRow(ctx, `
SELECT workspace_id::text, agent_id::text, coalesce(originator_user_id::text,'') FROM agent_task_queue
WHERE id=$1`, taskID).Scan(&wsID, &agentID, &creator)
		return wsID, agentID, creator, err == nil
	}
	gen.AgentLookup = func(ctx context.Context, wsID, agentID string) (convapp.AgentLite, bool) {
		a, ok, err := orchRepo.GetAgent(ctx, wsID, agentID)
		if err != nil || !ok {
			return convapp.AgentLite{}, false
		}
		return convapp.AgentLite{WorkspaceID: a.WorkspaceID, ID: a.ID,
			Description: a.Description, RuntimeConfig: a.RuntimeConfig}, true
	}
	gen.AuxUsage = func(ctx context.Context, wsID string, u convapp.UsageLite) error {
		return orchRepo.InsertAuxUsage(ctx, wsID, orchapp.UsageReport{
			Provider: u.Provider, Model: u.Model, AgentLabel: "aux",
			PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens,
		})
	}
	gen.Emit = func(ctx context.Context, wsID, sessionID string, created []convapp.QuickAction) {
		bus.Emit(ctx, events.Envelope{
			EventID: "qa-" + ids.NewV4().String(), Event: "chat.quick_actions",
			WorkspaceID: wsID, OccurredAt: time.Now().UTC(),
			Payload: map[string]any{"session_id": sessionID, "quick_actions": created},
		})
	}
	gen.Log = log.Warn
	bus.On(eventbus.PhaseBroadcast, func(ctx context.Context, e events.Envelope) {
		if e.Event != events.ChatDone {
			return
		}
		p, ok := e.Payload.(orchapp.ChatDoneFrame)
		if !ok {
			return
		}
		gen.OnChatDone(ctx, p.SessionID, p.TaskID, p.Content)
	})

	chat := convapp.NewChatService(convRepo, enq)

	chat.SetEnqueueChatTx(enq.EnqueueChatInTx)

	chat.SetAgentStarters(func(ctx context.Context, ws, agentID string) ([]string, error) {
		a, ok, err := orchRepo.GetAgent(ctx, ws, agentID)
		if err != nil || !ok {
			return nil, err
		}
		labels := make([]string, 0, len(a.ConversationStarters))
		for _, st := range a.ConversationStarters {
			labels = append(labels, st.Label)
		}
		return labels, nil
	})

	wRepo := &workpgrepo.Repo{DB: db}
	quick.SetEnqueueWorkItemTask(func(ctx context.Context, ws, wi, agentID, userID, prompt string) (string, error) {
		t, _, err := enq.EnqueueQuickAction(ctx, ws, wi, agentID, userID, prompt)
		if err != nil {
			return "", err
		}
		return t.ID, nil
	})
	quick.SetResolveTargetAgent(func(ctx context.Context, ws, wi string) (string, error) {
		item, _, err := wRepo.GetWorkItem(ctx, ws, wi)
		if err != nil {
			return "", err
		}
		if item.AssigneeType != "agent" {
			return "", nil
		}
		return item.AssigneeID, nil
	})

	bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		if e.Event != events.TaskCancelled {
			return
		}
		tp, ok := e.Payload.(orchapp.TaskPayload)
		if !ok || tp.ID == "" {
			return
		}
		t, ok, err := orchRepo.GetTask(ctx, e.WorkspaceID, tp.ID)
		if err != nil || !ok {
			return
		}
		sid, _ := t.Context["chat_session_id"].(string)
		if sid == "" {
			return
		}
		initiator := t.OriginatorUserID
		if _, err := chat.HandleChatTaskTerminal(ctx, e.WorkspaceID, sid, initiator, tp.ID, "cancelled"); err != nil {
			log.Warn("chat cancel verdict failed", "task", tp.ID, "err", err)
		}
	})

	chat.SetSessionEventSink(func(ctx context.Context, event, workspaceID, sessionID string) {
		bus.Emit(ctx, events.Envelope{
			Event: event, WorkspaceID: workspaceID,
			Payload: map[string]any{"session_id": sessionID, "workspace_id": workspaceID},
		})
	})

	ops := chatTaskOps{repo: orchRepo, db: db}
	chat.SetChatTaskOps(ops)
	chat.SetPendingChatReader(ops)
	chat.SetTaskPrioritizer(ops)

	return convhttp.Deps{
		Chat:         chat,
		QuickActions: quick,
		Pins:         convapp.NewPinService(convRepo),
		Canceller:    chatCanceller{settle: settle},
		TaskStatus:   settle.ChatTaskStatus,
		MemberCheck:  memberCheck,
	}
}

type chatTaskOps struct {
	repo *orchpgrepo.Repo
	db   *pg.DB
}

func (o chatTaskOps) LatestChatTask(ctx context.Context, ws, sessionID string) (string, string, bool, error) {
	t, ok, err := o.repo.GetTaskByChatSession(ctx, ws, sessionID)
	return t.Status, t.ID, ok, err
}

func (o chatTaskOps) CancelQueued(ctx context.Context, ws, taskID string) error {
	_, err := o.repo.CancelRun(ctx, ws, taskID)
	return err
}

func (o chatTaskOps) SupplementRunning(ctx context.Context, ws, taskID, content, userID string) (bool, error) {
	var id string
	err := o.db.Tx(ctx).QueryRow(ctx, `
INSERT INTO task_supplement (task_id, status, content, author_user_id)
SELECT id, 'pending', $3, nullif($4,'')::uuid FROM agent_task_queue
WHERE workspace_id=$1 AND id=$2::uuid AND status='running'
RETURNING id`, ws, taskID, content, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (o chatTaskOps) ListPendingChatTasks(ctx context.Context, ws, userID string) ([]convapp.PendingChatTask, error) {
	rows, err := o.db.Tx(ctx).Query(ctx, `
SELECT t.id::text, t.chat_session_id::text, t.status, t.created_at
FROM agent_task_queue t JOIN chat_session s ON s.id = t.chat_session_id
WHERE t.workspace_id=$1 AND s.workspace_id=$1 AND s.creator_id=$2
  AND t.status IN ('queued','dispatched','running')
ORDER BY t.created_at DESC LIMIT 200`, ws, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []convapp.PendingChatTask
	for rows.Next() {
		var p convapp.PendingChatTask
		if err := rows.Scan(&p.TaskID, &p.SessionID, &p.Status, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (o chatTaskOps) HasPendingChatTask(ctx context.Context, ws, userID string) (bool, error) {
	var exists bool
	err := o.db.Tx(ctx).QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM agent_task_queue t JOIN chat_session s ON s.id = t.chat_session_id
WHERE t.workspace_id=$1 AND s.workspace_id=$1 AND s.creator_id=$2
  AND t.status IN ('queued','dispatched','running'))`, ws, userID).Scan(&exists)
	return exists, err
}

func (o chatTaskOps) PrioritizeChatTask(ctx context.Context, ws, sessionID, taskID string) (int, bool, error) {
	var priority int
	err := o.db.Tx(ctx).QueryRow(ctx, `
UPDATE agent_task_queue t SET priority = (SELECT coalesce(max(priority),0)+1 FROM agent_task_queue WHERE workspace_id=$1)
WHERE t.workspace_id=$1 AND t.id=$2::uuid AND t.chat_session_id=$3::uuid
  AND t.status IN ('queued','dispatched')
RETURNING t.priority`, ws, taskID, sessionID).Scan(&priority)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return priority, true, nil
}

type qaEngine struct{ e *engine.EinoEngine }

func (q qaEngine) Generate(ctx context.Context, a convapp.AgentLite, secret, system, input string) (string, convapp.UsageLite, error) {
	out, u, err := q.e.Generate(ctx, orchapp.Agent{
		WorkspaceID: a.WorkspaceID, ID: a.ID,
		Description: a.Description, RuntimeConfig: a.RuntimeConfig,
	}, secret, system, input)
	if err != nil {
		return "", convapp.UsageLite{}, err
	}
	return out, convapp.UsageLite{Provider: u.Provider, Model: u.Model,
		PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens}, nil
}

type chatCanceller struct{ settle *orchapp.SettlementService }

func (c chatCanceller) CancelChatTask(ctx context.Context, ws, sessionID string) (string, string, bool, error) {
	return c.settle.CancelChatTask(ctx, ws, sessionID)
}
