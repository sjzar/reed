package ai

import (
	"sync"
	"time"
)

const (
	shortPenaltyTTL = 60 * time.Second // 429, 5xx, timeout/network
	longPenaltyTTL  = 1 * time.Hour    // 401, 403
)

// penaltyBox tracks temporarily unhealthy provider:model pairs.
// It is safe for concurrent use.
type penaltyBox struct {
	mu      sync.RWMutex
	entries map[string]time.Time // "providerID:modelID" → expiry
	now     func() time.Time     // injectable clock for testing
}

func newPenaltyBox() *penaltyBox {
	return &penaltyBox{
		entries: make(map[string]time.Time),
		now:     time.Now,
	}
}

func penaltyKey(providerID, modelID string) string {
	return providerID + ":" + modelID
}

// isPenalized returns true if the given provider:model pair is currently penalized.
func (p *penaltyBox) isPenalized(providerID, modelID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	expiry, ok := p.entries[penaltyKey(providerID, modelID)]
	if !ok {
		return false
	}
	return p.now().Before(expiry)
}

// penalize marks a provider:model pair as unhealthy for the given duration.
func (p *penaltyBox) penalize(providerID, modelID string, ttl time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries[penaltyKey(providerID, modelID)] = p.now().Add(ttl)
}
