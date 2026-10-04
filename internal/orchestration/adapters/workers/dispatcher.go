package workers

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"orkome/internal/contract/events"
	orchapp "orkome/internal/orchestration/app"
)

type Dispatcher struct {
	onSettle  func(task orchapp.Task, status string)
	Repo      orchapp.Repo
	Agents    *orchapp.AgentService
	Claim     *orchapp.ClaimService
	Settle    *orchapp.SettlementService
	Engine    orchapp.Engine
	CredFn    func(ctx context.Context, workspaceID, credentialID string) (string, error)
	Log       *slog.Logger
	Emit      Emitter
	PollEvery time.Duration
	Semaphore int
	Timeout   time.Duration

	InstanceID string

	HeartbeatEvery time.Duration

	MCPResolve func(ctx context.Context, workspaceID, agentID string) ([]orchapp.ResolvedMCPServer, error)
	MCPPatch   orchapp.TaskContextPatcher

	metrics interface {
		Incr(name string)
		Observe(name string, v float64)
	}
}

func (d *Dispatcher) snapshotMCP(ctx context.Context, task orchapp.Task, agent orchapp.Agent) {
	if d.MCPResolve == nil || d.MCPPatch == nil {
		return
	}
	list, err := d.MCPResolve(ctx, task.WorkspaceID, agent.ID)
	if err != nil {
		d.Log.Warn("mcp snapshot resolve", "task", task.ID, "err", err)
		return
	}
	entries := make([]orchapp.MCPSnapshotEntry, 0, len(list))
	for _, s := range list {
		entries = append(entries, orchapp.MCPSnapshotEntry{
			Name: s.Name, Transport: s.Transport, URL: s.URL, Status: "resolved",
		})
	}
	if patch := orchapp.MCPContextSnapshot(entries); patch != nil {
		if err := d.MCPPatch.PatchTaskContext(ctx, task.ID, patch); err != nil {
			d.Log.Warn("mcp snapshot patch", "task", task.ID, "err", err)
		}
	}
}

type Emitter interface {
	Emit(ctx context.Context, e events.Envelope)
}

func (d *Dispatcher) RunWithMetrics(ctx context.Context, m interface {
	Incr(name string)
	Observe(name string, v float64)
}) error {
	d.metrics = m
	d.onSettle = func(task orchapp.Task, status string) {
		m.Incr("task_" + status)
		if task.FailureReason != "" {
			m.Incr("task_failed_reason_" + task.FailureReason)
		}
	}
	return d.Run(ctx)
}

func (d *Dispatcher) Run(ctx context.Context) error {
	if d.PollEvery <= 0 {
		d.PollEvery = 500 * time.Millisecond
	}
	if d.Semaphore <= 0 {
		d.Semaphore = 4
	}
	if d.Timeout <= 0 {
		d.Timeout = 30 * time.Minute
	}
	if d.HeartbeatEvery <= 0 {
		d.HeartbeatEvery = 15 * time.Second
	}

	if n, err := d.Repo.RecoverDispatched(ctx); err == nil && n > 0 {
		d.Log.Info("startup recovery: dispatched → queued", "n", n)
	}
	if n, err := d.Repo.FailRunningAtStartup(ctx); err == nil && n > 0 {
		d.Log.Info("startup recovery: running → failed(runtime_recovery)", "n", n)
	}

	if d.InstanceID != "" {
		if err := d.Repo.UpsertRuntime(ctx, d.InstanceID, d.Engine.Name(), []string{"chat", "work_item"}); err != nil {
			d.Log.Error("runtime register", "instance", d.InstanceID, "err", err)
		}
		hb := time.NewTicker(d.HeartbeatEvery)
		defer hb.Stop()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-hb.C:
					if err := d.Repo.HeartbeatRuntime(ctx, d.InstanceID); err != nil {
						d.Log.Error("runtime heartbeat", "instance", d.InstanceID, "err", err)
					}
				}
			}
		}()
	}

	sem := make(chan struct{}, d.Semaphore)
	var wg sync.WaitGroup
	ticker := time.NewTicker(d.PollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-ticker.C:
			agents, err := d.Repo.ListAllAgents(ctx)
			if err != nil {
				d.Log.Error("dispatcher list agents", "err", err)
				continue
			}
			for _, agent := range agents {
				if agent.Archived {
					continue
				}

				if agent.RuntimeKind == "external" {
					continue
				}
				task, ok, err := d.Claim.ClaimNext(ctx, agent)
				if err != nil || !ok {
					continue
				}

				if d.metrics != nil && !task.CreatedAt.IsZero() {
					d.metrics.Observe("task_claim_wait_seconds", time.Since(task.CreatedAt).Seconds())
				}
				wg.Add(1)
				sem <- struct{}{}
				go func(agent orchapp.Agent, task orchapp.Task) {
					defer wg.Done()
					defer func() { <-sem }()
					d.runOne(ctx, agent, task)
				}(agent, task)
			}
		}
	}
}

func (d *Dispatcher) runOne(ctx context.Context, agent orchapp.Agent, task orchapp.Task) {
	d.Emit.Emit(ctx, events.Envelope{Event: events.TaskDispatch, WorkspaceID: task.WorkspaceID, OccurredAt: time.Now().UTC(), Payload: taskPayload(task)})

	started, err := d.Repo.StartTask(ctx, task.ID, d.InstanceID, d.Engine.Name())
	if err != nil {
		d.Log.Error("start task", "id", task.ID, "err", err)
		return
	}
	d.Emit.Emit(ctx, events.Envelope{Event: events.TaskRunning, WorkspaceID: task.WorkspaceID, OccurredAt: time.Now().UTC(), Payload: taskPayload(started)})

	d.snapshotMCP(ctx, started, agent)

	execCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.Timeout)
	defer cancel()
	secret := d.credential(execCtx, agent)
	out, execErr := d.Engine.Execute(execCtx, started, agent, secret)
	if execErr != nil {
		d.Log.Warn("engine execute failed", "id", task.ID, "err", execErr)
	}
	if err := d.Settle.Settle(ctx, started, agent, out, execErr); err != nil {
		d.Log.Error("settle", "id", task.ID, "err", err)
	}
	if d.onSettle != nil {
		st := "completed"
		if execErr != nil {
			st = "failed"
		}
		d.onSettle(started, st)
	}
}

func (d *Dispatcher) credential(ctx context.Context, agent orchapp.Agent) string {
	if d.CredFn == nil {
		return ""
	}
	id, _ := agent.RuntimeConfig["credential_id"].(string)
	if id == "" {
		return ""
	}
	s, err := d.CredFn(ctx, agent.WorkspaceID, id)
	if err != nil {
		d.Log.Error("credential resolve", "err", err)
		return ""
	}
	return s
}

func taskPayload(t orchapp.Task) orchapp.TaskPayload {
	return orchapp.TaskPayload{ID: t.ID, AgentID: t.AgentID, WorkItemID: t.WorkItemID, Status: t.Status}
}
