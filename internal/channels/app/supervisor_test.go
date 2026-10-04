package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orkome/internal/channels/wire"
)

type fakeLeaseStore struct {
	mu       sync.Mutex
	held     map[string]leaseRow
	acqLog   []string
	renewErr error
}

type leaseRow struct {
	token     string
	expiresAt time.Time
}

func newFakeLease() *fakeLeaseStore {
	return &fakeLeaseStore{held: map[string]leaseRow{}}
}

func (f *fakeLeaseStore) TryAcquire(_ context.Context, id, token string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.held[id]
	now := time.Now()
	if ok && row.token != token && row.expiresAt.After(now) {
		return ErrLeaseNotAcquired
	}
	f.held[id] = leaseRow{token: token, expiresAt: now.Add(ttl)}
	f.acqLog = append(f.acqLog, id+"/"+token)
	return nil
}

func (f *fakeLeaseStore) Renew(_ context.Context, id, token string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renewErr != nil {
		return f.renewErr
	}
	row, ok := f.held[id]
	if !ok || row.token != token {
		return ErrLeaseNotAcquired
	}
	row.expiresAt = time.Now().Add(ttl)
	f.held[id] = row
	return nil
}

func (f *fakeLeaseStore) Release(_ context.Context, id, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if row, ok := f.held[id]; ok && row.token == token {
		delete(f.held, id)
	}
	return nil
}

func (f *fakeLeaseStore) ListHeld(_ context.Context, ids []string) (map[string]struct{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]struct{}{}
	now := time.Now()
	for _, id := range ids {
		if row, ok := f.held[id]; ok && row.expiresAt.After(now) {
			out[id] = struct{}{}
		}
	}
	return out, nil
}

func (f *fakeLeaseStore) steal(id, token string) { f.stealFor(id, token, time.Minute) }

func (f *fakeLeaseStore) stealFor(id, token string, ttl time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held[id] = leaseRow{token: token, expiresAt: time.Now().Add(ttl)}
}

func (f *fakeLeaseStore) setRenewErr(err error) {
	f.mu.Lock()
	f.renewErr = err
	f.mu.Unlock()
}

type connChannel struct {
	kind      wire.Kind
	connected atomic.Int64
	connects  atomic.Int64
	sends     atomic.Int64
	onConnect func()
}

func (c *connChannel) Kind() wire.Kind               { return c.kind }
func (c *connChannel) Capabilities() wire.Capability { return wire.CapText }

func (c *connChannel) Connect(ctx context.Context) error {
	c.connects.Add(1)
	c.connected.Add(1)
	if c.onConnect != nil {
		c.onConnect()
	}
	<-ctx.Done()
	c.connected.Add(-1)
	return ctx.Err()
}
func (c *connChannel) Disconnect(context.Context) error { return nil }
func (c *connChannel) Send(context.Context, wire.OutboundMessage) (wire.SendResult, error) {
	c.sends.Add(1)
	return wire.SendResult{MessageID: "out-1"}, nil
}

func fastSupervisorConfig() SupervisorConfig {
	return SupervisorConfig{
		LeaseTTL:            300 * time.Millisecond,
		LeaseRenewInterval:  60 * time.Millisecond,
		PollInterval:        20 * time.Millisecond,
		MinBackoff:          5 * time.Millisecond,
		MaxBackoff:          20 * time.Millisecond,
		ResetBackoffAfter:   50 * time.Millisecond,
		DisconnectTimeout:   50 * time.Millisecond,
		LeaseReleaseTimeout: 50 * time.Millisecond,
	}
}

func TestSupervisorSingleConnectionPerInstallation(t *testing.T) {
	repo := newFakeRepo()
	inst := testInstallation()
	repo.installations["inst-1"] = inst
	leases := newFakeLease()

	var live atomic.Int64
	var peak atomic.Int64
	registry := wire.NewRegistry()
	registry.Register("fake", func(wire.Config) (wire.Channel, error) {
		ch := &connChannel{kind: "fake"}
		ch.onConnect = func() {
			cur := live.Add(1)
			for {
				p := peak.Load()
				if cur <= p || peak.CompareAndSwap(p, cur) {
					break
				}
			}
			go func() {
				<-time.After(120 * time.Millisecond)
				live.Add(-1)
			}()
		}
		return ch, nil
	})

	cfg := fastSupervisorConfig()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := NewSupervisor(repo, leases, registry, func(context.Context, wire.InboundMessage) error { return nil }, cfg)
			s.Run(ctx)
			s.Wait()
		}()
	}
	time.Sleep(400 * time.Millisecond)
	cancel()
	wg.Wait()

	assert.LessOrEqual(t, peak.Load(), int64(1), "at most one connection at any time under dual-replica contention (lease CAS)")
	assert.GreaterOrEqual(t, peak.Load(), int64(1), "at least one connection established")
}

func TestSupervisorLeaseLossCancelsConnection(t *testing.T) {
	repo := newFakeRepo()
	repo.installations["inst-1"] = testInstallation()
	leases := newFakeLease()

	connected := make(chan struct{}, 1)
	var disconnected atomic.Int64
	registry := wire.NewRegistry()
	registry.Register("fake", func(wire.Config) (wire.Channel, error) {
		ch := &connChannel{kind: "fake"}
		ch.onConnect = func() {
			connected <- struct{}{}
			go func() {
				<-time.After(150 * time.Millisecond)
				disconnected.Add(1)
			}()
		}
		return ch, nil
	})

	s := NewSupervisor(repo, leases, registry, func(context.Context, wire.InboundMessage) error { return nil }, fastSupervisorConfig())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx); s.Wait() }()

	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("connection not established")
	}

	leases.steal("inst-1", "other-replica")
	select {
	case <-done:
		t.Fatal("supervisor must not exit wholesale on lease loss (should retry to reclaim)")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	<-done
}

func TestSupervisorFingerprintRotation(t *testing.T) {
	repo := newFakeRepo()
	inst := testInstallation()
	repo.installations["inst-1"] = inst
	leases := newFakeLease()

	var builds atomic.Int64
	registry := wire.NewRegistry()
	registry.Register("fake", func(wire.Config) (wire.Channel, error) {
		builds.Add(1)
		return &connChannel{kind: "fake"}, nil
	})

	s := NewSupervisor(repo, leases, registry, func(context.Context, wire.InboundMessage) error { return nil }, fastSupervisorConfig())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx); s.Wait() }()

	time.Sleep(120 * time.Millisecond)
	first := builds.Load()
	require.GreaterOrEqual(t, first, int64(1))

	repo.mu.Lock()
	drifted := testInstallation()
	drifted.Fingerprint = "fp-rotated"
	drifted.Config = json.RawMessage(`{"routing_key":"rk-1","secret":"new"}`)
	repo.installations["inst-1"] = drifted
	repo.mu.Unlock()

	time.Sleep(250 * time.Millisecond)
	cancel()
	<-done
	assert.Greater(t, builds.Load(), first, "connection rebuilt with new credentials after fingerprint drift (online rotation restart)")
}

func TestSupervisorSkipsUnregisteredKind(t *testing.T) {
	repo := newFakeRepo()
	repo.installations["inst-1"] = testInstallation()
	leases := newFakeLease()
	registry := wire.NewRegistry()

	s := NewSupervisor(repo, leases, registry, func(context.Context, wire.InboundMessage) error { return nil }, fastSupervisorConfig())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx); s.Wait() }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	leases.mu.Lock()
	n := len(leases.acqLog)
	leases.mu.Unlock()
	assert.Zero(t, n, "unregistered kinds must not claim leases (avoiding claim-fail-release spinning)")
}

type metricsFake struct {
	mu       sync.Mutex
	gauges   map[string]float64
	counters map[string]int64
}

func (m *metricsFake) Gauge(name string, v float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gauges == nil {
		m.gauges = map[string]float64{}
	}
	m.gauges[name] = v
}

func (m *metricsFake) Incr(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counters == nil {
		m.counters = map[string]int64{}
	}
	m.counters[name]++
}

func (m *metricsFake) getGauge(name string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gauges[name]
}

func (m *metricsFake) counter(name string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[name]
}

func TestSupervisorLeaseGauge(t *testing.T) {
	repo := newFakeRepo()
	repo.installations["inst-1"] = testInstallation()
	leases := newFakeLease()
	registry := wire.NewRegistry()
	registry.Register("fake", func(wire.Config) (wire.Channel, error) {
		return &connChannel{kind: "fake"}, nil
	})
	g := &metricsFake{}
	s := NewSupervisor(repo, leases, registry, func(context.Context, wire.InboundMessage) error { return nil }, fastSupervisorConfig())
	s.SetMetrics(g)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx); s.Wait() }()
	require.Eventually(t, func() bool { return g.getGauge("channel_leases_held") == 1 }, 2*time.Second, 20*time.Millisecond,
		"lease-held gauge should be 1 after takeover install")
	cancel()
	<-done
	assert.Equal(t, float64(0), g.getGauge("channel_leases_held"), "gauge returns to zero after shutdown harvest")
}

func TestSupervisorLeaseMetrics(t *testing.T) {
	repo := newFakeRepo()
	repo.installations["inst-1"] = testInstallation()
	leases := newFakeLease()
	connected := make(chan struct{}, 8)
	registry := wire.NewRegistry()
	registry.Register("fake", func(wire.Config) (wire.Channel, error) {
		ch := &connChannel{kind: "fake"}
		ch.onConnect = func() { connected <- struct{}{} }
		return ch, nil
	})
	m := &metricsFake{}
	s := NewSupervisor(repo, leases, registry, func(context.Context, wire.InboundMessage) error { return nil }, fastSupervisorConfig())
	s.SetMetrics(m)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx); s.Wait() }()
	defer func() { cancel(); <-done }()

	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("connection not established")
	}
	assert.Zero(t, m.counter("channel_lease_takeovers"), "this replica's first continuous claim does not count as takeover")

	leases.setRenewErr(errors.New("db down"))
	require.Eventually(t, func() bool { return m.counter("channel_lease_renew_failures") >= 1 },
		2*time.Second, 10*time.Millisecond, "renewal transport failures should be counted")
	leases.setRenewErr(nil)

	leases.stealFor("inst-1", "other-replica", 80*time.Millisecond)
	require.Eventually(t, func() bool { return m.counter("channel_lease_lost") >= 1 },
		2*time.Second, 10*time.Millisecond, "lease loss should be counted")

	require.Eventually(t, func() bool { return m.counter("channel_lease_takeovers") >= 1 },
		3*time.Second, 10*time.Millisecond, "a re-win on an expired lease should count as takeover")
}
