package bridgeservicefinder

import (
	"sync"
)

// cacheEntry is a single networkID -> bridge service URL cache record. It tracks not only the URL
// but the Source that produced it (so config entries stay immune to on-chain updates and the
// metadata-over-sequencer precedence can be enforced on live updates) and the result of the most
// recent /health probe (used by the health-gating rule).
type cacheEntry struct {
	url     string
	source  Source
	healthy bool
}

// cache is a concurrency-safe map of networkID -> cacheEntry. GetURL reads under a read lock while
// live updates (added in a later step) take a write lock. All exported access goes through the
// methods below so the mutex is never leaked.
type cache struct {
	mu      sync.RWMutex
	entries map[uint32]cacheEntry
}

// newCache returns an empty, ready-to-use cache.
func newCache() *cache {
	return &cache{
		entries: make(map[uint32]cacheEntry),
	}
}

// get returns the cache entry for networkID and whether it exists. It takes a read lock so it is
// safe to call concurrently with set.
func (c *cache) get(networkID uint32) (cacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	e, ok := c.entries[networkID]

	return e, ok
}

// set stores (or replaces) the cache entry for networkID under a write lock.
func (c *cache) set(networkID uint32, entry cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[networkID] = entry
}
