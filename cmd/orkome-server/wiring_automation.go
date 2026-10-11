package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/haozing/ploykit/platform/pg"
	autobhttp "orkome/internal/automation/adapters/http"
	autoppgrepo "orkome/internal/automation/adapters/pgrepo"
	autopworkers "orkome/internal/automation/adapters/workers"
	autopapp "orkome/internal/automation/app"
	autodomain "orkome/internal/automation/domain"
	"orkome/internal/contract/events"
	notifpgrepo "orkome/internal/notification/adapters/pgrepo"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
)

func wiringAutomation(ctx context.Context, log *slog.Logger, db *pg.DB, bus *eventbus.Bus, reg *Workers,
	cfg *Config, redisCli redis.UniversalClient,
) autobhttp.Deps {
	arepo := autoppgrepo.New(db)
	orepo := orchpgrepo.New(db)
	wRepo := &workpgrepo.Repo{DB: db}

	enq := wakeupEnqueuer{repo: orepo}
	wu := autopapp.NewWakeupService(arepo, enq, factsReader{db: db, wRepo: wRepo})
	wu.SetUow(db.Within)

	{
		notifRepo := notifpgrepo.New(db)
		wu.SetMemberNotifier(func(ctx context.Context, ws, uid, title, body, dedup string) error {
			return notifRepo.WriteNotice(ctx, ws, uid, "wakeup_member", title, body, dedup)
		})
	}
	auto := autopapp.NewAutopilotService(arepo, creatorAdapter{wRepo: wRepo}, enq)
	auto.SetUow(db.Within)
	wh := autopapp.NewWebhookService(arepo, auto)

	bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		switch e.Event {
		case events.TaskCompleted:
			auto.BackfillFromTask(ctx, payloadTaskID(e), "completed")
		case events.TaskFailed, events.TaskCancelled:
			auto.BackfillFromTask(ctx, payloadTaskID(e), "failed")
		}
	})

	bus.On(eventbus.PhaseCascade, func(ctx context.Context, e events.Envelope) {
		wu.RecordEvent(ctx, e)
	})

	bus.On(eventbus.PhaseProject, func(ctx context.Context, e events.Envelope) {
		switch e.Event {
		case events.WorkItemCreated, events.WorkItemUpdated, events.WorkItemDeleted:
			if p, ok := e.Payload.(events.WorkItemPayload); ok {
				_ = wu.NotifyChildrenChanged(ctx, e.WorkspaceID, p.ID)
			}
		}
	})

	for _, w := range []*autopworkers.TickWorker{
		autopworkers.WakeupTick(wu, log, time.Second),
		autopworkers.AutopilotCron(auto, log, time.Second),
		autopworkers.WebhookDelivery(wh, log, time.Second),
	} {
		ww := w
		reg.AddFunc(ww.Name, ww.Run)
	}

	return autobhttp.Deps{Wakeups: wu, Autopilots: auto, Webhooks: wh,

		PublicRL: autobhttp.NewWebhookRateLimiter(redisCli, autobhttp.DefaultWebhookRateConfig(),
			autobhttp.ParseTrustedProxies(cfg.TrustedProxies)),

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
	}
}

func payloadTaskID(e events.Envelope) string {
	if p, ok := e.Payload.(orchapp.TaskPayload); ok {
		return p.ID
	}
	return ""
}

type wakeupEnqueuer struct{ repo orchapp.Repo }

func (u wakeupEnqueuer) EnqueueForWakeup(ctx context.Context, ws, wi, agentID, userID, evidence string) (string, bool, error) {
	return u.EnqueueForWakeupWithID(ctx, ws, wi, agentID, userID, "", evidence)
}

func (u wakeupEnqueuer) EnqueueForWakeupWithID(ctx context.Context, ws, wi, agentID, userID, wakeupID, evidence string) (string, bool, error) {
	return u.EnqueueForWakeupWithChain(ctx, ws, wi, agentID, userID, wakeupID, nil, evidence)
}

func (u wakeupEnqueuer) EnqueueForWakeupWithChain(ctx context.Context, ws, wi, agentID, userID, wakeupID string, chain []string, evidence string) (string, bool, error) {
	if wi != "" {
		merged, err := u.repo.MergeIntoPending(ctx, ws, wi, agentID, "")
		if err != nil {
			return "", false, err
		}
		if merged {
			return "", true, nil
		}
	}
	ctxMap := map[string]any{"trigger": "wakeup", "wakeup_note": evidence}
	if wakeupID != "" {
		ctxMap["wakeup_id"] = wakeupID
	}
	if len(chain) > 0 {
		ctxMap["wakeup_chain"] = chain
	}
	t, err := u.repo.InsertTask(ctx, orchapp.Task{
		WorkspaceID: ws, AgentID: agentID, WorkItemID: wi, Status: "queued", MaxAttempts: 2,
		Context:          ctxMap,
		OriginatorUserID: orZeroUUID(userID), AccountableUserID: orZeroUUID(userID),
		OriginatorSource: "trigger_owner", TriggerEvidenceKind: "rule",
	})
	if err != nil {
		return "", false, err
	}
	return t.ID, false, nil
}

type factsReader struct {
	db    *pg.DB
	wRepo *workpgrepo.Repo
}

func (f factsReader) ParentOf(ctx context.Context, ws, wi string) (string, error) {
	var parent string
	err := f.db.Tx(ctx).QueryRow(ctx, `
SELECT coalesce(parent_work_item_id::text,'') FROM work_item
WHERE workspace_id=$1 AND id=$2::uuid`, ws, wi).Scan(&parent)
	if err != nil {
		return "", err
	}
	return parent, nil
}

func (f factsReader) WorkItemFacts(ctx context.Context, ws, wi string) (string, int, error) {
	w, _, err := f.wRepo.GetWorkItem(ctx, ws, wi)
	return w.StatusKey, w.Priority, err
}

func (f factsReader) ChildStageFacts(ctx context.Context, ws, wi string) ([]autodomain.ChildStageFact, error) {
	terminal := map[string]bool{"done": true, "cancelled": true}
	if defs, err := f.wRepo.ListStatuses(ctx, ws); err == nil {
		for _, d := range defs {
			if d.Category == "done" || d.Category == "closed" {
				terminal[d.Key] = true
			}
		}
	}
	rows, err := f.db.Tx(ctx).Query(ctx, `
SELECT coalesce(stage,''), status_key FROM work_item
WHERE workspace_id=$1 AND parent_work_item_id=$2::uuid`, ws, wi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []autodomain.ChildStageFact{}
	for rows.Next() {
		var fact autodomain.ChildStageFact
		var key string
		if err := rows.Scan(&fact.Stage, &key); err != nil {
			return nil, err
		}
		fact.Terminal = terminal[key]
		out = append(out, fact)
	}
	return out, rows.Err()
}

func (f factsReader) WakeupMemberTarget(ctx context.Context, ws, wi string) (string, error) {
	var assigneeType, assigneeID string
	err := f.db.Tx(ctx).QueryRow(ctx, `
SELECT assignee_type, coalesce(assignee_id::text,'') FROM work_item
WHERE workspace_id=$1 AND id=$2::uuid`, ws, wi).Scan(&assigneeType, &assigneeID)
	if err != nil {
		return "", err
	}
	if assigneeType == "user" {
		return assigneeID, nil
	}
	return "", nil
}

func (f factsReader) WakeupTargetAgent(ctx context.Context, ws, wi string) (string, error) {
	var assigneeType, assigneeID string
	err := f.db.Tx(ctx).QueryRow(ctx, `
SELECT assignee_type, coalesce(assignee_id::text,'') FROM work_item
WHERE workspace_id=$1 AND id=$2::uuid`, ws, wi).Scan(&assigneeType, &assigneeID)
	if err != nil {
		return "", err
	}
	switch assigneeType {
	case "agent":
		return assigneeID, nil
	case "squad":
		var leader string
		err := f.db.Tx(ctx).QueryRow(ctx, `
SELECT coalesce(leader_agent_id::text,'') FROM squad
WHERE workspace_id=$1 AND id=$2::uuid`, ws, assigneeID).Scan(&leader)
		return leader, err
	}
	return "", nil
}

type creatorAdapter struct{ wRepo *workpgrepo.Repo }

func (c creatorAdapter) CreateForAutopilot(ctx context.Context, ws, title, desc, agentID, creator, origin string) (string, error) {
	return c.CreateForAutopilotWithSubscribers(ctx, ws, title, desc, agentID, creator, origin, nil)
}

func (c creatorAdapter) CreateForAutopilotWithSubscribers(ctx context.Context, ws, title, desc, agentID, creator, origin string, subscribers []autopapp.SubscriberRef) (string, error) {
	in := workapp.NewWorkItem{
		WorkspaceID: ws, Title: title, Description: desc, StatusKey: "todo",
		AssigneeType: "agent", AssigneeID: agentID, CreatorType: "user", CreatorID: creator,
		OriginType: origin,
	}
	if len(subscribers) > 0 {
		ids := make([]string, 0, len(subscribers))
		for _, s := range subscribers {
			ids = append(ids, s.UserID)
		}
		in.InitialSubscribers = workapp.AutopilotTemplateSubscribers(ids)
	}
	wi, err := c.wRepo.CreateWorkItem(ctx, in)
	if err != nil {
		return "", err
	}
	return wi.ID, nil
}

func orZeroUUID(s string) string {
	if s == "" {
		return "00000000-0000-0000-0000-000000000000"
	}
	return s
}

type checkinAd struct{ wake *autopapp.WakeupService }

func (a checkinAd) CheckInWakeup(ctx context.Context, ws, workItemID, wakeupID, taskID, note string) error {
	return a.wake.CheckIn(ctx, ws, workItemID, wakeupID, taskID, note)
}
