package app

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	mathrand "math/rand/v2"
	"strconv"
	"sync"
	"time"

	"orkome/internal/channels/wire"
)

type Supervisor struct {
	installations Repo
	leases        LeaseStore
	registry      *wire.Registry
	handler       wire.InboundHandler
	senders       *SenderRegistry
	cfg           SupervisorConfig

	nodeID string

	mu            sync.Mutex
	supervisors   map[string]supervisorEntry
	supervisorGen uint64
	wg            sync.WaitGroup
	stopped       bool
	stopChan      chan struct{}

	foreignHeld map[string]struct{}

	metrics LeaseMetrics
}

type supervisorEntry struct {
	cancel      context.CancelFunc
	done        <-chan struct{}
	fingerprint string
	gen         uint64
}

type SupervisorConfig struct {
	LeaseTTL            time.Duration
	LeaseRenewInterval  time.Duration
	PollInterval        time.Duration
	MinBackoff          time.Duration
	MaxBackoff          time.Duration
	ResetBackoffAfter   time.Duration
	DisconnectTimeout   time.Duration
	LeaseReleaseTimeout time.Duration
	RotationWaitTimeout time.Duration
	Now                 func() time.Time
	Logger              *slog.Logger
}

const (
	DefaultLeaseTTL          = 180 * time.Second
	DefaultPollInterval      = 30 * time.Second
	defaultMinBackoff        = 2 * time.Second
	defaultMaxBackoff        = 60 * time.Second
	defaultResetBackoffAfter = 60 * time.Second
)

const (
	metricLeasesHeld       = "channel_leases_held"
	metricLeaseTakeovers   = "channel_lease_takeovers"
	metricLeaseRenewFailed = "channel_lease_renew_failures"
	metricLeaseLost        = "channel_lease_lost"
)

func (c SupervisorConfig) withDefaults() SupervisorConfig {
	if c.LeaseTTL == 0 {
		c.LeaseTTL = DefaultLeaseTTL
	}
	if c.LeaseRenewInterval == 0 {
		c.LeaseRenewInterval = min(60*time.Second, c.LeaseTTL/3)
	}
	if c.PollInterval == 0 {
		c.PollInterval = min(DefaultPollInterval, c.LeaseRenewInterval/2)
	}
	if c.MinBackoff == 0 {
		c.MinBackoff = defaultMinBackoff
	}
	if c.MaxBackoff == 0 {
		c.MaxBackoff = defaultMaxBackoff
	}
	if c.ResetBackoffAfter == 0 {
		c.ResetBackoffAfter = defaultResetBackoffAfter
	}
	if c.DisconnectTimeout == 0 {
		c.DisconnectTimeout = 5 * time.Second
	}
	if c.LeaseReleaseTimeout == 0 {
		c.LeaseReleaseTimeout = 5 * time.Second
	}
	if c.RotationWaitTimeout == 0 {
		c.RotationWaitTimeout = c.DisconnectTimeout + c.LeaseReleaseTimeout
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

func (s *Supervisor) SetSenderRegistry(r *SenderRegistry) { s.senders = r }

func (s *Supervisor) SetMetrics(m LeaseMetrics) { s.metrics = m }

func (s *Supervisor) incr(name string) {
	if s.metrics != nil {
		s.metrics.Incr(name)
	}
}

func (s *Supervisor) markForeignHeld(id string) {
	s.mu.Lock()
	s.foreignHeld[id] = struct{}{}
	s.mu.Unlock()
}

func (s *Supervisor) countTakeover(id string) {
	s.mu.Lock()
	_, marked := s.foreignHeld[id]
	delete(s.foreignHeld, id)
	s.mu.Unlock()
	if marked {
		s.incr(metricLeaseTakeovers)
	}
}

func (s *Supervisor) observeLeases() {
	if s.metrics == nil {
		return
	}
	s.mu.Lock()
	n := len(s.supervisors)
	s.mu.Unlock()
	s.metrics.Gauge(metricLeasesHeld, float64(n))
}

func NewSupervisor(installations Repo, leases LeaseStore, registry *wire.Registry, handler wire.InboundHandler, cfg SupervisorConfig) *Supervisor {
	cfg = cfg.withDefaults()
	return &Supervisor{
		installations: installations,
		leases:        leases,
		registry:      registry,
		handler:       handler,
		cfg:           cfg,
		nodeID:        newNodeID(),
		supervisors:   map[string]supervisorEntry{},
		foreignHeld:   map[string]struct{}{},
		stopChan:      make(chan struct{}),
	}
}

func (s *Supervisor) NodeID() string { return s.nodeID }

func (s *Supervisor) Run(ctx context.Context) {
	defer close(s.stopChan)
	s.sweep(ctx)
	t := time.NewTicker(s.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.cancelAll()
			return
		case <-t.C:
			s.sweep(ctx)
		}
	}
}

func (s *Supervisor) Wait() {
	<-s.stopChan
	s.wg.Wait()
}

func (s *Supervisor) sweep(ctx context.Context) {
	defer s.observeLeases()
	rows, err := s.installations.ListActiveInstallations(ctx)
	if err != nil {
		s.cfg.Logger.Warn("channels supervisor: list installations failed", "error", err)
		return
	}
	active := make(map[string]struct{}, len(rows))
	var candidates []Installation
	var rotationWaits []<-chan struct{}
	for _, row := range rows {

		if _, ok := s.registry.Lookup(row.Kind); !ok {
			continue
		}
		active[row.ID] = struct{}{}
		done, rotated := s.cancelOnRotation(row)
		if rotated {
			rotationWaits = append(rotationWaits, done)
		}
		if !s.isSupervised(row.ID) {
			candidates = append(candidates, row)
		}
	}
	s.mu.Lock()
	for id, entry := range s.supervisors {
		if _, still := active[id]; !still {
			entry.cancel()
			delete(s.supervisors, id)
		}
	}
	s.mu.Unlock()
	if len(candidates) == 0 {
		return
	}
	s.waitForRotations(ctx, rotationWaits)

	ids := make([]string, len(candidates))
	for i, c := range candidates {
		ids[i] = c.ID
	}
	held, err := s.leases.ListHeld(ctx, ids)
	if err != nil {
		s.cfg.Logger.Warn("channels supervisor: list held leases failed", "error", err)
		return
	}
	for _, row := range candidates {
		if _, occupied := held[row.ID]; occupied {
			s.markForeignHeld(row.ID)
			continue
		}
		s.startSupervisor(ctx, row)
	}
}

func (s *Supervisor) isSupervised(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.supervisors[id]
	return ok
}

func (s *Supervisor) cancelOnRotation(row Installation) (<-chan struct{}, bool) {
	s.mu.Lock()
	entry, ok := s.supervisors[row.ID]
	if !ok || entry.fingerprint == row.Fingerprint {
		s.mu.Unlock()
		return nil, false
	}
	s.cfg.Logger.Info("channels supervisor: credential fingerprint drifted; rotating",
		"installation_id", row.ID, "kind", string(row.Kind))
	entry.cancel()
	delete(s.supervisors, row.ID)
	s.mu.Unlock()
	return entry.done, true
}

func (s *Supervisor) waitForRotations(ctx context.Context, waits []<-chan struct{}) {
	if len(waits) == 0 {
		return
	}
	deadline := time.Now().Add(s.cfg.RotationWaitTimeout)
	for _, done := range waits {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		t := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-done:
			t.Stop()
		case <-t.C:
			return
		}
	}
}

func (s *Supervisor) startSupervisor(parent context.Context, inst Installation) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	if _, exists := s.supervisors[inst.ID]; exists {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.supervisorGen++
	gen := s.supervisorGen
	s.supervisors[inst.ID] = supervisorEntry{
		cancel:      cancel,
		done:        done,
		fingerprint: inst.Fingerprint,
		gen:         gen,
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer cancel()
		s.supervise(ctx, inst, gen, done)
	}()
}

func leaseToken(nodeID string, gen uint64) string {
	return nodeID + "-g" + strconv.FormatUint(gen, 10)
}

func (s *Supervisor) supervise(ctx context.Context, inst Installation, gen uint64, done chan<- struct{}) {
	defer close(done)
	defer func() {

		s.mu.Lock()
		if entry, ok := s.supervisors[inst.ID]; ok && entry.gen == gen {
			delete(s.supervisors, inst.ID)
		}
		s.mu.Unlock()
	}()

	tok := leaseToken(s.nodeID, gen)
	log := s.cfg.Logger.With("installation_id", inst.ID, "kind", string(inst.Kind), "node_id", s.nodeID)
	backoff := s.cfg.MinBackoff

	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.leases.TryAcquire(ctx, inst.ID, tok, s.cfg.LeaseTTL); err != nil {
			if errors.Is(err, ErrLeaseNotAcquired) {
				s.markForeignHeld(inst.ID)
				return
			}
			log.Warn("channels supervisor: acquire lease error", "error", err)
			return
		}
		s.countTakeover(inst.ID)

		ch, err := s.registry.Build(wire.Config{
			Kind:    inst.Kind,
			ID:      inst.ID,
			Raw:     inst.Config,
			Handler: s.handler,
		})
		if err != nil {
			log.Error("channels supervisor: build channel failed", "error", err)
			s.releaseLease(inst.ID, tok)
			if sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, s.cfg.MaxBackoff)
			continue
		}

		runCtx, runCancel := context.WithCancel(ctx)
		renewDone := make(chan struct{})
		go func() {
			defer close(renewDone)

			s.renewUntil(runCtx, runCancel, inst.ID, tok)
		}()

		if s.senders != nil {
			s.senders.Register(inst.ID, ch)
		}
		startedAt := s.cfg.Now()
		runErr := ch.Connect(runCtx)
		runCancel()
		<-renewDone
		if s.senders != nil {
			s.senders.Unregister(inst.ID, ch)
		}
		s.disconnect(ch)
		s.releaseLease(inst.ID, tok)

		if ctx.Err() != nil {
			return
		}
		if uptime := s.cfg.Now().Sub(startedAt); uptime >= s.cfg.ResetBackoffAfter {
			backoff = s.cfg.MinBackoff
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			log.Warn("channels supervisor: connection exited with error", "error", runErr)
		}
		if sleepCtx(ctx, jitterDuration(backoff)) {
			return
		}
		backoff = nextBackoff(backoff, s.cfg.MaxBackoff)
	}
}

func (s *Supervisor) renewUntil(ctx context.Context, cancelRun context.CancelFunc, instID, tok string) {
	t := time.NewTicker(s.cfg.LeaseRenewInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.leases.Renew(ctx, instID, tok, s.cfg.LeaseTTL); err != nil {
				if errors.Is(err, ErrLeaseNotAcquired) {
					s.incr(metricLeaseLost)
					s.markForeignHeld(instID)
					s.cfg.Logger.Warn("channels supervisor: lease lost; cancelling connection context",
						"installation_id", instID)
					cancelRun()
					return
				}

				s.incr(metricLeaseRenewFailed)
				s.cfg.Logger.Warn("channels supervisor: renew error", "installation_id", instID, "error", err)
			}
		}
	}
}

func (s *Supervisor) releaseLease(instID, tok string) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.LeaseReleaseTimeout)
	defer cancel()
	if err := s.leases.Release(ctx, instID, tok); err != nil {
		s.cfg.Logger.Warn("channels supervisor: release lease failed", "installation_id", instID, "error", err)
	}
}

func (s *Supervisor) disconnect(ch wire.Channel) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.DisconnectTimeout)
	defer cancel()
	_ = ch.Disconnect(ctx)
}

func (s *Supervisor) cancelAll() {
	s.mu.Lock()
	s.stopped = true
	for id, entry := range s.supervisors {
		entry.cancel()
		delete(s.supervisors, id)
	}
	s.mu.Unlock()
	s.observeLeases()
}

func newNodeID() string {
	buf := make([]byte, 16)
	if _, err := cryptorand.Read(buf); err != nil {
		return "nodeid-fallback-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(buf)
}

func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

func jitterDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	delta := d / 2
	return d - delta + time.Duration(mathrand.Int64N(int64(2*delta)+1))
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() != nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}
