package sessioncapture

import (
	"sync"
	"time"
)

// admissionGateKey cannot collide with an inbox file name (names never hold NUL).
const admissionGateKey = "\x00admission"

type gateEntry struct {
	attempts int
	until    time.Time
}

// quotaGate remembers work that was rejected with ErrQuota so an exhausted,
// unchanged enrichment capacity is not re-probed (policy callback, Lstat,
// COUNT scans) on every idle tick. Each key backs off exponentially from 1s to
// the 5-minute retry ceiling. The gate is in-process only: a restart starts
// open, and capacityChanged reopens it at once. Nothing is ever dropped here;
// the durable inbox file and the jobs.enrichment_gap marker stay authoritative.
type quotaGate struct {
	mu      sync.Mutex
	entries map[string]gateEntry
}

func (g *quotaGate) blocked(key string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return now.Before(g.entries[key].until)
}

func (g *quotaGate) fail(key string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.entries == nil {
		g.entries = map[string]gateEntry{}
	}
	e := g.entries[key]
	e.attempts++
	e.until = now.Add(retryDelay(e.attempts, func(d time.Duration) time.Duration { return d }))
	g.entries[key] = e
}

func (g *quotaGate) clear(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.entries, key)
}

// keepOnly forgets inbox entries whose files are gone, bounding the map.
func (g *quotaGate) keepOnly(names map[string]bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k := range g.entries {
		if k != admissionGateKey && !names[k] {
			delete(g.entries, k)
		}
	}
}

func (g *quotaGate) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.entries = nil
}

// capacityChanged must be called by any code that frees enrichment capacity
// (retention, when it exists) so blocked work is retried immediately.
func (q *Queue) capacityChanged() { q.quota.reset() }
