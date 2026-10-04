package app

import (
	"sync"

	"orkome/internal/channels/wire"
)

type SenderRegistry struct {
	mu   sync.RWMutex
	byID map[string]wire.Channel
}

func NewSenderRegistry() *SenderRegistry { return &SenderRegistry{byID: map[string]wire.Channel{}} }

func (r *SenderRegistry) Register(installationID string, ch wire.Channel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[installationID] = ch
}

func (r *SenderRegistry) Unregister(installationID string, ch wire.Channel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.byID[installationID]; ok && cur == ch {
		delete(r.byID, installationID)
	}
}

func (r *SenderRegistry) Get(installationID string) (wire.Channel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.byID[installationID]
	return ch, ok
}

func (r *SenderRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}
