package workers

import (
	"context"
	"log/slog"
	"time"

	"orkome/internal/contract/events"
	"orkome/internal/contract/failuretax"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/orchestration/domain"
)

type LivenessSweeper struct {
	Repo   orchapp.Repo
	Settle *orchapp.SettlementService
	Log    *slog.Logger
	Emit   Emitter

	Every time.Duration

	StaleSeconds int

	Trigger *orchapp.TriggerService

	JanitorEvery time.Duration

	JanitorHours int

	Metrics interface {
		IncrAttrs(name string, attrs map[string]string)
	}
}

func (s *LivenessSweeper) hit(kind string, n int) {
	if s.Metrics == nil || n <= 0 {
		return
	}
	s.Metrics.IncrAttrs("sweep_hits", map[string]string{"kind": kind})
}

func (s *LivenessSweeper) RunOnce(ctx context.Context) (int, error) {
	stale := s.StaleSeconds
	if stale <= 0 {
		stale = 150
	}
	ids, err := s.Repo.FailStaleRunning(ctx, stale)
	if err != nil {
		return 0, err
	}
	s.hit("runtime_recovery", len(ids))
	for _, id := range ids {

		if _, err := s.Repo.FailRun(ctx, id, failuretax.RuntimeRecovery,
			"engine instance heartbeat absent beyond grace window"); err != nil {
			return len(ids), err
		}
		_ = s.Repo.DeleteTaskCheckpoints(ctx, id)
		if s.Emit != nil {
			s.Emit.Emit(ctx, events.Envelope{Event: events.TaskFailed, OccurredAt: time.Now().UTC(),
				Payload: orchapp.TaskPayload{ID: id, Status: "failed"}})
		}
	}
	if len(ids) > 0 {
		s.Log.Info("liveness sweep: running → failed(runtime_recovery)", "n", len(ids))
	}

	if s.Repo != nil {
		if promoted, err := s.Repo.PromoteDeferred(ctx); err != nil {
			s.Log.Error("deferred promotion sweep", "err", err)
		} else if len(promoted) > 0 {
			s.hit("deferred_promotion", len(promoted))
			s.Log.Info("deferred promotion sweep: deferred → queued", "n", len(promoted))
			if s.Emit != nil {
				for _, p := range promoted {
					s.Emit.Emit(ctx, events.Envelope{Event: events.TaskQueued, WorkspaceID: p.WorkspaceID,
						OccurredAt: time.Now().UTC(),
						Payload:    orchapp.TaskPayload{ID: p.ID, WorkspaceID: p.WorkspaceID, AgentID: p.AgentID, WorkItemID: p.WorkItemID, Status: p.Status}})
				}
			}
		}
	}

	if s.Settle != nil {
		if n, err := s.Settle.SweepExpiredAwaiting(ctx); err == nil && n > 0 {
			s.hit("interrupt_timeout", n)
			s.Log.Info("liveness sweep: awaiting → failed(interrupt_timeout)", "n", n)
		}
	}

	if n, err := s.Repo.CountStaleExternalQueued(ctx, 60); err == nil && n > 0 {
		s.hit("external_queued_stale", n)
		s.Log.Warn("external runtime task stranded in queue", "n", n, "older_than_min", 60)
	}

	if n, err := s.Repo.RollupUsageHourly(ctx, domain.ClosedHourBoundary(time.Now().UTC())); err != nil {
		s.Log.Error("usage hourly rollup", "err", err)
	} else if n > 0 {
		s.Log.Info("usage hourly rollup", "buckets", n)
	}
	return len(ids), nil
}

func (s *LivenessSweeper) Run(ctx context.Context) error {
	if s.Every <= 0 {
		s.Every = 30 * time.Second
	}
	if s.JanitorEvery <= 0 {
		s.JanitorEvery = 5 * time.Minute
	}
	ticker := time.NewTicker(s.Every)
	defer ticker.Stop()
	janitor := time.NewTicker(s.JanitorEvery)
	defer janitor.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := s.RunOnce(ctx); err != nil {
				s.Log.Error("liveness sweep", "err", err)
			}
		case <-janitor.C:
			s.RunJanitorOnce(ctx)
		}
	}
}

func (s *LivenessSweeper) RunJanitorOnce(ctx context.Context) {
	if n, err := s.SweepSessionCheckpoints(ctx); err != nil {
		s.Log.Error("checkpoint janitor", "err", err)
	} else if n > 0 {
		s.hit("checkpoint_janitor", n)
		s.Log.Info("checkpoint janitor: stale session:* reclaimed", "n", n)
	}
	if s.Trigger == nil {
		return
	}
	if n, err := s.SweepDelegatedRecovery(ctx); err != nil {
		s.Log.Error("delegated recovery sweep", "err", err)
	} else if n > 0 {
		s.hit("delegated_recovery", n)
		s.Log.Info("delegated recovery sweep: recovery tasks created", "n", n)
	}

	if n, err := s.Trigger.ReconcileUncovered(ctx); err != nil {
		s.Log.Error("uncovered comment reconcile sweep", "err", err)
	} else if n > 0 {
		s.hit("uncovered_comment_reconcile", n)
		s.Log.Info("uncovered comment reconcile sweep: deferred_fallback retriggered", "n", n)
	}
}

func (s *LivenessSweeper) SweepSessionCheckpoints(ctx context.Context) (int, error) {
	hours := s.JanitorHours
	if hours <= 0 {
		hours = 24
	}
	return s.Repo.DeleteStaleSessionCheckpoints(ctx, hours)
}

func (s *LivenessSweeper) SweepDelegatedRecovery(ctx context.Context) (int, error) {
	tasks, err := s.Repo.ListUnsettledDelegatedFailures(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tasks {
		if domain.RetryEligible(t.FailureReason, t.Attempt, t.MaxAttempts) {
			continue
		}
		ok, err := s.Trigger.RecoverDelegated(ctx, t, "delegated failure recovery sweep (internal archive 05 §2.11)")
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}
