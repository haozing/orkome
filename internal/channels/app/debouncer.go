package app

import (
	"sync"
	"time"

	"orkome/internal/channels/domain"
)

type stoppableTimer interface{ Stop() bool }

type Debouncer struct {
	window    time.Duration
	afterFunc func(d time.Duration, fn func()) stoppableTimer

	mu       sync.Mutex
	pending  map[domain.DebounceKey]*debounceEntry
	seq      uint64
	stopped  bool
	inflight sync.WaitGroup
}

type debounceEntry struct {
	timer stoppableTimer
	flush func()
	gen   uint64
}

func NewDebouncer(window time.Duration) *Debouncer {
	if window <= 0 {
		window = domain.DebounceWindow
	}
	return &Debouncer{
		window:    window,
		afterFunc: func(d time.Duration, fn func()) stoppableTimer { return time.AfterFunc(d, fn) },
		pending:   map[domain.DebounceKey]*debounceEntry{},
	}
}

func (b *Debouncer) Schedule(key domain.DebounceKey, flush func()) {
	b.schedule(key, flush, true)
}

func (b *Debouncer) ScheduleIfAbsent(key domain.DebounceKey, flush func()) {
	b.schedule(key, flush, false)
}

func (b *Debouncer) schedule(key domain.DebounceKey, flush func(), replace bool) {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		flush()
		return
	}
	b.seq++
	gen := b.seq
	fire := func() { b.onFire(key, gen) }
	if e, ok := b.pending[key]; ok {
		if !replace {
			b.mu.Unlock()
			return
		}
		e.timer.Stop()
		e.flush = flush
		e.gen = gen
		e.timer = b.afterFunc(b.window, fire)
		b.mu.Unlock()
		return
	}
	b.pending[key] = &debounceEntry{
		flush: flush,
		gen:   gen,
		timer: b.afterFunc(b.window, fire),
	}
	b.mu.Unlock()
}

func (b *Debouncer) onFire(key domain.DebounceKey, gen uint64) {
	b.mu.Lock()
	e, ok := b.pending[key]
	if !ok || b.stopped || e.gen != gen {
		b.mu.Unlock()
		return
	}
	delete(b.pending, key)
	flush := e.flush
	b.inflight.Add(1)
	b.mu.Unlock()

	defer b.inflight.Done()
	flush()
}

func (b *Debouncer) FlushAll() {
	b.mu.Lock()
	b.stopped = true
	entries := make([]*debounceEntry, 0, len(b.pending))
	for _, e := range b.pending {
		e.timer.Stop()
		entries = append(entries, e)
	}
	b.pending = map[domain.DebounceKey]*debounceEntry{}
	b.mu.Unlock()

	for _, e := range entries {
		e.flush()
	}
	b.inflight.Wait()
}

func (b *Debouncer) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}
