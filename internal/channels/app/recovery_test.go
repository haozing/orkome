package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/channels/domain"
)

func TestDebounceCrashRecovery(t *testing.T) {
	repo := boundRepo()
	runs := &fakeRuns{}
	router := NewRouter(repo, &fakeConvo{}, &fakeInvoke{}, runs, &fakeIssues{},
		&fakeMembers{}, RouterConfig{DebounceWindow: 80 * time.Millisecond})

	_, err := router.Handle(context.Background(), p2pMsg("last one before the crash"))
	require.NoError(t, err)
	assert.Empty(t, runs.snapshot(), "no trigger inside the window")
	require.Equal(t, 1, router.debouncer.PendingCount(), "already armed in-process")
	require.Equal(t, 1, repo.armedCount(), "arming already persisted first (DB in the guard set)")

	router.debouncer.mu.Lock()
	router.debouncer.pending = map[domain.DebounceKey]*debounceEntry{}
	router.debouncer.mu.Unlock()
	require.Zero(t, router.debouncer.PendingCount(), "in-process map cleared (crash)")

	router2 := NewRouter(repo, &fakeConvo{}, &fakeInvoke{}, runs, &fakeIssues{},
		&fakeMembers{}, RouterConfig{DebounceWindow: 80 * time.Millisecond})
	defer router2.Drain()

	router2.RecoverArmedDebounces(context.Background())
	assert.Equal(t, 1, router2.debouncer.PendingCount(), "recovery scan re-arms exactly one key")
	assert.Equal(t, 1, repo.armedCount(), "re-arming does not touch marks (cleared only on trigger success)")

	require.Eventually(t, func() bool { return len(runs.snapshot()) == 1 },
		2*time.Second, 10*time.Millisecond, "the recovery window fires exactly one run after the silent period")

	call := runs.snapshot()[0]
	assert.Equal(t, "sess-chat-1-p2p", call.SessionID)
	assert.Equal(t, int64(1), call.ContextRevision)
	assert.Equal(t, "user-1", call.UserID, "run initiator restored losslessly from the arming snapshot")

	assert.Eventually(t, func() bool { return repo.armedCount() == 0 },
		2*time.Second, 10*time.Millisecond, "arming marks cleared token-fenced after trigger")
}

func TestDebounceRecoverySkipsFired(t *testing.T) {
	repo := boundRepo()
	runs := &fakeRuns{}
	router := NewRouter(repo, &fakeConvo{}, &fakeInvoke{}, runs, &fakeIssues{},
		&fakeMembers{}, RouterConfig{DebounceWindow: 60 * time.Millisecond})
	defer router.Drain()

	_, err := router.Handle(context.Background(), p2pMsg("normal trigger"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(runs.snapshot()) == 1 },
		2*time.Second, 10*time.Millisecond, "exactly one trigger after the silent window")
	require.Eventually(t, func() bool { return repo.armedCount() == 0 },
		2*time.Second, 10*time.Millisecond, "arming marks cleared after a normal trigger")

	router2 := NewRouter(repo, &fakeConvo{}, &fakeInvoke{}, runs, &fakeIssues{},
		&fakeMembers{}, RouterConfig{DebounceWindow: 60 * time.Millisecond})
	defer router2.Drain()

	router2.RecoverArmedDebounces(context.Background())
	assert.Zero(t, router2.debouncer.PendingCount(), "triggered rows not re-armed")

	time.Sleep(150 * time.Millisecond)
	assert.Len(t, runs.snapshot(), 1, "no second run after restart")
}

func TestDebounceRecoveryDoesNotResetLiveWindow(t *testing.T) {
	repo := boundRepo()
	runs := &fakeRuns{}
	router := NewRouter(repo, &fakeConvo{}, &fakeInvoke{}, runs, &fakeIssues{},
		&fakeMembers{}, RouterConfig{DebounceWindow: 250 * time.Millisecond})

	_, err := router.Handle(context.Background(), p2pMsg("pre-crash messages"))
	require.NoError(t, err)
	require.Equal(t, 1, repo.armedCount())

	router.debouncer.mu.Lock()
	router.debouncer.pending = map[domain.DebounceKey]*debounceEntry{}
	router.debouncer.mu.Unlock()
	router.RecoverArmedDebounces(context.Background())
	require.Equal(t, 1, router.debouncer.PendingCount(), "re-arming is one shot")

	_, err = router.Handle(context.Background(), p2pMsg("new message after restart"))
	require.NoError(t, err)
	require.Equal(t, 1, router.debouncer.PendingCount(), "same key still one inside the guard window")

	defer router.Drain()

	time.Sleep(500 * time.Millisecond)
	assert.Len(t, runs.snapshot(), 1, "recovery window + new message merge into exactly one run")
	assert.Eventually(t, func() bool { return repo.armedCount() == 0 },
		2*time.Second, 10*time.Millisecond, "marks cleared after trigger")
}
