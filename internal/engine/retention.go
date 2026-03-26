package engine

import "sort"

// evictTerminal removes terminal runs that exceed retention limits.
// Must be called under e.mu.Lock().
func (e *Engine) evictTerminal() {
	maxRuns := e.retention.maxRuns()
	ttl := e.retention.ttl()
	now := e.now()

	// Evict by TTL
	for id, rv := range e.terminalRuns {
		if rv.FinishedAt != nil && now.Sub(*rv.FinishedAt) > ttl {
			delete(e.terminalRuns, id)
		}
	}

	// Evict by count (keep newest)
	if len(e.terminalRuns) <= maxRuns {
		return
	}

	type entry struct {
		id         string
		finishedAt int64
	}
	entries := make([]entry, 0, len(e.terminalRuns))
	for id, rv := range e.terminalRuns {
		var ts int64
		if rv.FinishedAt != nil {
			ts = rv.FinishedAt.UnixNano()
		}
		entries = append(entries, entry{id: id, finishedAt: ts})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].finishedAt < entries[j].finishedAt
	})

	toRemove := len(entries) - maxRuns
	for i := 0; i < toRemove; i++ {
		delete(e.terminalRuns, entries[i].id)
	}
}
