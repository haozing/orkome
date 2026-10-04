package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"orkome/internal/automation/domain"
)

func TestConsumeOnce_BackoffRhythmAndExhaustion(t *testing.T) {
	repo := newFakeRepo()
	ap, trig := seedAutopilotWithTrigger(t, repo, 10)
	ap.Enabled = false
	repo.autopilots[ap.ID] = ap
	repo.deliveries = append(repo.deliveries, fakeDelivery{id: "dlv-b", triggerID: trig.ID, status: "queued"})

	cur := testNow
	wh := NewWebhookService(repo, newTestAutopilotService(repo, &fakeEnqueuer{}))
	wh.now = func() time.Time { return cur }
	repo.nowFn = func() time.Time { return cur }

	rhythm := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	for i, step := range rhythm {
		ok, err := wh.ConsumeOnce(context.Background())
		require.NoError(t, err)
		require.True(t, ok, "attempt %d should be claimable", i+1)
		d := repo.deliveries[0]
		require.Equal(t, "queued", d.status, "backoff returns to queued, not terminal failed")
		require.Equal(t, i+1, d.attempts, "claiming increments attempts")
		require.NotNil(t, d.nextAttempt, "backoff must persist next_attempt_at")
		require.True(t, d.nextAttempt.Equal(cur.Add(step)), "backoff %d = %v", i+1, step)

		ok, err = wh.ConsumeOnce(context.Background())
		require.NoError(t, err)
		require.False(t, ok, "rows whose backoff is not due must not be claimed")

		cur = cur.Add(step)
	}

	ok, err := wh.ConsumeOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	d := repo.deliveries[0]
	require.Equal(t, "failed", d.status, "  5-attempt exhaustion must land in failed dead letter")
	require.Equal(t, 5, d.attempts)
	require.Nil(t, d.nextAttempt, "dead letter has no next attempt")
	require.NotEmpty(t, d.err)

	ok, err = wh.ConsumeOnce(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
}

func TestConsumeOnce_TriggerGone_DeadLetterImmediately(t *testing.T) {
	repo := newFakeRepo()
	repo.deliveries = append(repo.deliveries, fakeDelivery{id: "dlv-g", triggerID: "trig-gone", status: "queued"})
	wh := NewWebhookService(repo, newTestAutopilotService(repo, &fakeEnqueuer{}))
	wh.now = func() time.Time { return testNow }

	ok, err := wh.ConsumeOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	d := repo.deliveries[0]
	require.Equal(t, "failed", d.status)
	require.Equal(t, 1, d.attempts)
	require.Nil(t, d.nextAttempt)
	require.Equal(t, "trigger gone", d.err)
}

func TestConsumeOnce_ReplayResetsBackoffCycle(t *testing.T) {
	repo := newFakeRepo()
	_, trig := seedAutopilotWithTrigger(t, repo, 10)
	repo.deliveries = append(repo.deliveries,
		fakeDelivery{id: "dlv-r", triggerID: trig.ID, status: "failed", err: "boom", attempts: 5})
	wh := NewWebhookService(repo, newTestAutopilotService(repo, &fakeEnqueuer{}))
	wh.now = func() time.Time { return testNow }

	out, err := wh.ReplayDelivery(context.Background(), "ws-1", "dlv-r")
	require.NoError(t, err)
	require.Equal(t, "queued", out.Status)
	require.Equal(t, 0, out.Attempts, "reset attempts=0 (fresh delivery cycle)")
	require.Nil(t, out.NextAttemptAt, "backoff-due point cleared")

	ok, err := wh.ConsumeOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "delivered", repo.deliveries[0].status)
	require.Equal(t, 1, repo.deliveries[0].attempts)
}

func TestConsumeOnce_QuotaSkippedHasNoBackoff(t *testing.T) {
	repo := newFakeRepo()
	ap, trig := seedAutopilotWithTrigger(t, repo, 1)

	svc := newTestAutopilotService(repo, &fakeEnqueuer{})
	svc.SetUow(rollbackUow(repo))
	_, err := svc.StartRun(context.Background(), ap.ID, "", "manual")
	require.NoError(t, err)

	repo.deliveries = append(repo.deliveries, fakeDelivery{id: "dlv-q", triggerID: trig.ID, status: "queued"})
	wh := NewWebhookService(repo, svc)
	wh.now = func() time.Time { return testNow }

	ok, err := wh.ConsumeOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	d := repo.deliveries[0]
	require.Equal(t, "skipped", d.status, "quota rejected = skipped terminal state")
	require.Nil(t, d.nextAttempt, "skipped does not enter the backoff face")

	ok, _ = wh.ConsumeOnce(context.Background())
	require.False(t, ok)
}

func seedFuseRuns(t *testing.T, repo *fakeRepo, ap Autopilot, failed, completed int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < failed; i++ {
		_, err := repo.InsertRun(ctx, Run{AutopilotID: ap.ID, Source: "schedule",
			Status: domain.RunFailed, PlannedAt: testNow})
		require.NoError(t, err)
	}
	for i := 0; i < completed; i++ {
		_, err := repo.InsertRun(ctx, Run{AutopilotID: ap.ID, Source: "schedule",
			Status: domain.RunCompleted, PlannedAt: testNow})
		require.NoError(t, err)
	}
}

func newFuseAutopilot(t *testing.T, repo *fakeRepo, name string) Autopilot {
	t.Helper()
	ap, err := repo.CreateAutopilot(context.Background(), Autopilot{
		WorkspaceID: "ws-1", Name: name, ExecMode: "task_only", DailyLimit: 100,
		Enabled: true, CreatedBy: "u-9"})
	require.NoError(t, err)
	return ap
}

func TestMonitorAutopilotFailures_BoundariesAndIdempotent(t *testing.T) {
	repo := newFakeRepo()
	apA := newFuseAutopilot(t, repo, "ap-A")
	seedFuseRuns(t, repo, apA, 49, 0)

	_, err := repo.InsertRun(context.Background(), Run{AutopilotID: apA.ID, Source: "schedule",
		Status: domain.RunFailed, PlannedAt: testNow.Add(-8 * 24 * time.Hour)})
	require.NoError(t, err)

	apB := newFuseAutopilot(t, repo, "ap-B")
	seedFuseRuns(t, repo, apB, 44, 6)

	apC := newFuseAutopilot(t, repo, "ap-C")
	seedFuseRuns(t, repo, apC, 45, 5)

	svc := newTestAutopilotService(repo, &fakeEnqueuer{})
	type notice struct{ ws, creator, name, stat string }
	var got []notice
	svc.SetFailureNotifier(func(_ context.Context, ws, creator, name, stat string) {
		got = append(got, notice{ws, creator, name, stat})
	})

	paused, err := svc.MonitorAutopilotFailures(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, paused, "only ap-C hit")

	require.True(t, repo.autopilots[apA.ID].Enabled, "  49 runs no trigger")
	require.Empty(t, repo.autopilots[apA.ID].PausedReason)
	require.True(t, repo.autopilots[apB.ID].Enabled, "  50 runs but 88% no trigger")
	require.Empty(t, repo.autopilots[apB.ID].PausedReason)
	c := repo.autopilots[apC.ID]
	require.False(t, c.Enabled, "a hit must pause with enabled=false")
	require.Equal(t, domain.AutopilotPausedFailureRate, c.PausedReason)

	require.Len(t, got, 1)
	require.Equal(t, "ws-1", got[0].ws)
	require.Equal(t, "u-9", got[0].creator)
	require.Equal(t, "ap-C", got[0].name)
	require.Contains(t, got[0].stat, "50")
	require.Contains(t, got[0].stat, "45")
	require.Contains(t, got[0].stat, "90%")

	paused, err = svc.MonitorAutopilotFailures(context.Background())
	require.NoError(t, err)
	require.Zero(t, paused)
	require.Len(t, got, 1)
	require.False(t, repo.autopilots[apC.ID].Enabled)
}

func TestMonitorAutopilotFailures_NotifierNilSkips(t *testing.T) {
	repo := newFakeRepo()
	ap := newFuseAutopilot(t, repo, "ap-N")
	seedFuseRuns(t, repo, ap, 50, 0)

	svc := newTestAutopilotService(repo, &fakeEnqueuer{})
	require.Nil(t, svc.notifier)
	paused, err := svc.MonitorAutopilotFailures(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, paused, "nil notification does not affect the pause itself")
	require.False(t, repo.autopilots[ap.ID].Enabled)
}

func TestCronTickOnce_MonitorThrottle(t *testing.T) {
	repo := newFakeRepo()
	cur := testNow
	svc := newTestAutopilotService(repo, &fakeEnqueuer{})
	svc.now = func() time.Time { return cur }

	_, err := svc.CronTickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, repo.pauseFuseCalls, "first tick triggers monitoring")

	_, err = svc.CronTickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, repo.pauseFuseCalls, "same window (within 1h) tick does not re-monitor")

	cur = cur.Add(90 * time.Minute)
	_, err = svc.CronTickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, repo.pauseFuseCalls, "cross-window tick resumes monitoring")

	_, err = svc.MonitorAutopilotFailures(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, repo.pauseFuseCalls)
}

func chainKey(ws, wi, agent string) string { return ws + "|" + wi + "|" + agent }

func TestFireOne_SelfLoopPausesRule(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetUow(rollbackUow(repo))

	w := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(wk *Wakeup) { wk.EventTypes = []string{"comment.created"} })
	rev := w.Revision
	repo.taskChains = map[string][]string{chainKey("ws-1", "wi-1", "ag-1"): {w.ID}}

	require.False(t, svc.fireOne(context.Background(), w, testNow, fireOpts{}), "loops must skip dispatch")
	require.Zero(t, enq.calls, "loops must not enqueue")

	stored := repo.wakeups[w.ID]
	require.Equal(t, domain.PausedReasonLoop, stored.PausedReason, "paused_reason=loop")
	require.True(t, stored.Enabled, "loop pause keeps enabled (distinct from terminal expired)")
	require.Equal(t, rev+1, stored.Revision, "pause advances revision")
	require.Zero(t, stored.FireCount, "no trigger counted without dispatch")

	paused, err := svc.ListPaused(context.Background(), "ws-1")
	require.NoError(t, err)
	require.Len(t, paused, 1)
	require.Equal(t, domain.PausedReasonLoop, paused[0].PausedReason)

	require.False(t, svc.fireOne(context.Background(), stored, testNow, fireOpts{}))
	require.Equal(t, rev+1, repo.wakeups[w.ID].Revision, "paused rules get no new writes")
}

func TestFireOne_LoopReadErrorFailOpen(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetUow(rollbackUow(repo))

	w := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-1", "ag-1",
		func(wk *Wakeup) { nv := testNow.Add(-time.Minute); wk.NextFireAt = &nv })
	repo.chainReadErr = fmt.Errorf("task store down")

	require.True(t, svc.fireOne(context.Background(), w, testNow, fireOpts{}), "read-chain failures must not block dispatch")
	require.Equal(t, 1, enq.calls)
	require.Empty(t, repo.wakeups[w.ID].PausedReason)
}

func TestFireOne_ChainPropagationAndCap(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetUow(rollbackUow(repo))

	w := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-1", "ag-1",
		func(wk *Wakeup) { nv := testNow.Add(-time.Minute); wk.NextFireAt = &nv })
	repo.taskChains = map[string][]string{chainKey("ws-1", "wi-1", "ag-1"): {"other-1", "other-2"}}
	require.True(t, svc.fireOne(context.Background(), w, testNow, fireOpts{}))
	require.Len(t, enq.chains, 1)
	require.Equal(t, []string{"other-1", "other-2", w.ID}, enq.chains[0], "chain propagation = parent chain ∪ current id")
	require.Empty(t, repo.wakeups[w.ID].PausedReason, "non-loops must not be paused")

	w2 := seedWakeup(t, svc, repo, domain.WakeupTime, "ws-1", "wi-2", "ag-1",
		func(wk *Wakeup) { nv := testNow.Add(-time.Minute); wk.NextFireAt = &nv })
	long := make([]string, 32)
	for i := range long {
		long[i] = fmt.Sprintf("c%d", i)
	}
	repo.taskChains[chainKey("ws-1", "wi-2", "ag-1")] = long
	require.True(t, svc.fireOne(context.Background(), w2, testNow, fireOpts{}))
	dispatched := enq.chains[len(enq.chains)-1]
	require.Len(t, dispatched, domain.WakeupChainMax, "chain capacity capped at 32")
	require.Equal(t, "c1", dispatched[0], "over capacity drops the oldest entry")
	require.Equal(t, w2.ID, dispatched[len(dispatched)-1], "current id at the chain tail")
	require.NotContains(t, dispatched, "c0", "oldest entry falls out of window")
}

func TestFireOne_CrossRuleLoopPauses(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, fakeFacts{})
	svc.SetUow(rollbackUow(repo))

	w1 := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(wk *Wakeup) { wk.EventTypes = []string{"comment.created"} })
	w2 := seedWakeup(t, svc, repo, domain.WakeupEvent, "ws-1", "wi-1", "ag-1",
		func(wk *Wakeup) { wk.EventTypes = []string{"comment.created"} })

	repo.taskChains = map[string][]string{chainKey("ws-1", "wi-1", "ag-1"): {w1.ID}}

	require.True(t, svc.fireOne(context.Background(), w2, testNow, fireOpts{}), "w2 not in the chain: dispatched as usual")
	require.Empty(t, repo.wakeups[w2.ID].PausedReason)

	repo.taskChains[chainKey("ws-1", "wi-1", "ag-1")] = enq.chains[0]
	require.False(t, svc.fireOne(context.Background(), w1, testNow, fireOpts{}), "w1 detects the bypass loop")
	require.Equal(t, domain.PausedReasonLoop, repo.wakeups[w1.ID].PausedReason)
	require.Equal(t, 1, enq.calls)
}

type memberFacts struct {
	fakeFacts
	memberID string
}

func (f memberFacts) WakeupMemberTarget(_ context.Context, _, _ string) (string, error) {
	return f.memberID, nil
}

func TestFireOne_MemberAssigneeNotifiesInsteadOfDispatch(t *testing.T) {
	repo := newFakeRepo()
	enq := &fakeEnqueuer{}
	svc := newTestWakeupService(repo, enq, memberFacts{fakeFacts: fakeFacts{targetAgent: ""}, memberID: "u-9"})
	svc.SetUow(rollbackUow(repo))

	w := Wakeup{ID: repo.seq("syswk"), WorkspaceID: "ws-1", WorkItemID: "wi-1",
		Kind: domain.WakeupSystem, SystemRule: domain.SystemRuleChildrenDone,
		Instruction: "all sub-items done", Enabled: true, Revision: 1}
	repo.wakeups[w.ID] = w
	rev := w.Revision

	var gotTitle, gotDedup string
	svc.SetMemberNotifier(func(_ context.Context, _, uid, title, _, dedup string) error {
		require.Equal(t, "u-9", uid)
		gotTitle, gotDedup = title, dedup
		return nil
	})
	require.True(t, svc.fireOne(context.Background(), w, testNow, fireOpts{}), "member receipt = digested")
	require.Zero(t, enq.calls, "member branch must not enqueue")
	require.Equal(t, "work item wakeup notice", gotTitle)
	require.Equal(t, "wakeup_member:"+w.ID+":"+itoa(rev), gotDedup)
	require.Equal(t, rev+1, repo.wakeups[w.ID].Revision, "notification branch advances revision")

	repo2 := newFakeRepo()
	svc2 := newTestWakeupService(repo2, enq, memberFacts{fakeFacts: fakeFacts{targetAgent: ""}, memberID: "u-9"})
	svc2.SetUow(rollbackUow(repo2))
	w2 := Wakeup{ID: repo2.seq("syswk"), WorkspaceID: "ws-1", WorkItemID: "wi-1",
		Kind: domain.WakeupSystem, SystemRule: domain.SystemRuleChildrenDone,
		Enabled: true, Revision: 1}
	repo2.wakeups[w2.ID] = w2
	require.False(t, svc2.fireOne(context.Background(), w2, testNow, fireOpts{}), "nil port = old semantics")
}

func TestCronTickOnce_MultiReplicaClaimDedup(t *testing.T) {
	duetime := testNow.Add(-time.Minute)
	mkRepo := func() *fakeRepo {
		r := newFakeRepo()
		r.autopilots["ap-1"] = Autopilot{ID: "ap-1", WorkspaceID: "ws-1",
			Name: "a", ExecMode: "create_work_item", Enabled: true}
		r.triggers["trig-1"] = Trigger{ID: "trig-1", AutopilotID: "ap-1",
			Kind: domain.TriggerSchedule, CronExpression: "0 0 * * *", Timezone: "UTC",
			NextRunAt: &duetime, Enabled: true}
		return r
	}

	ra := mkRepo()
	svcA := NewAutopilotService(ra, &fakeCreator{}, &fakeEnqueuer{})
	svcA.now = func() time.Time { return testNow }
	n1, err := svcA.CronTickOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n1, "the first replica creates 1 run")
	require.Len(t, ra.runs, 1)
	advanced := ra.triggers["trig-1"].NextRunAt
	require.False(t, advanced.Equal(duetime), "claiming advances next_run_at")

	rb := mkRepo()
	tb := rb.triggers["trig-1"]
	tb.NextRunAt = advanced
	rb.triggers["trig-1"] = tb
	stale := []Trigger{{ID: "trig-1", AutopilotID: "ap-1", Kind: domain.TriggerSchedule,
		CronExpression: "0 0 * * *", Timezone: "UTC", NextRunAt: &duetime, Enabled: true}}
	rb.dueOverride = func(time.Time) ([]Trigger, error) { return stale, nil }
	svcB := NewAutopilotService(rb, &fakeCreator{}, &fakeEnqueuer{})
	svcB.now = func() time.Time { return testNow }
	n2, err := svcB.CronTickOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, n2, "replica claims on stale snapshots must lose; no duplicate runs")
	require.Empty(t, rb.runs)
}

type parentFacts struct {
	fakeFacts
	parentOf map[string]string
}

func (f parentFacts) ParentOf(_ context.Context, _, wi string) (string, error) {
	return f.parentOf[wi], nil
}

func TestNotifyChildrenChanged_ResolvesParentRules(t *testing.T) {
	repo := newFakeRepo()

	w := Wakeup{ID: repo.seq("syswk"), WorkspaceID: "ws-1", WorkItemID: "parent-1",
		Kind: domain.WakeupSystem, SystemRule: domain.SystemRuleChildrenDone,
		Enabled: true, Revision: 1, Condition: map[string]any{"type": "children_done"}}
	repo.wakeups[w.ID] = w
	svc := newTestWakeupService(repo, &fakeEnqueuer{}, parentFacts{
		fakeFacts: fakeFacts{targetAgent: "ag-parent", children: []domain.ChildStageFact{{Stage: "", Terminal: true}}},
		parentOf:  map[string]string{"child-1": "parent-1"},
	})
	require.NoError(t, svc.NotifyChildrenChanged(context.Background(), "ws-1", "child-1"))

	require.Equal(t, int64(2), repo.wakeups[w.ID].Revision, "parent rules must be evaluated and triggered")
}
