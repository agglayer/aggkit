package db

import "sync"

// activityRevisionCache keeps the revision GetActivityRevision last computed for each address, so
// a request does not read every activity_bridge row of the address again while none of them
// changed (the revision is read on every GET, 304s included). Every write to
// an address's rows invalidates its entry (see invalidate); an entry is only stored if no write to
// that address happened while it was being computed, which a per-address generation tells: a
// computation records the generation it started at (begin) and store drops its result if the
// generation moved meanwhile. Generations come from one counter, so an address forgotten and seen
// again never reuses one
type activityRevisionCache struct {
	mu        sync.Mutex
	next      uint64
	gens      map[string]uint64
	revisions map[string]string
}

func newActivityRevisionCache() *activityRevisionCache {
	return &activityRevisionCache{gens: make(map[string]uint64), revisions: make(map[string]string)}
}

// get returns addr's cached revision, if any
func (c *activityRevisionCache) get(addr string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	revision, ok := c.revisions[addr]
	return revision, ok
}

// begin returns the generation a computation of addr's revision starts at
func (c *activityRevisionCache) begin(addr string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	gen, ok := c.gens[addr]
	if !ok {
		c.next++
		gen = c.next
		c.gens[addr] = gen
	}
	return gen
}

// store caches revision for addr, computed from generation gen, unless addr was written since
func (c *activityRevisionCache) store(addr string, gen uint64, revision string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if current, ok := c.gens[addr]; ok && current == gen {
		c.revisions[addr] = revision
	}
}

// invalidate drops addr's cached revision and moves its generation, so a computation that read
// the rows before the write cannot store its result. Callers invalidate both before and after
// writing addr's rows
func (c *activityRevisionCache) invalidate(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	c.gens[addr] = c.next
	delete(c.revisions, addr)
}

// forget drops addr altogether (its rows are gone), and so does reset for every address
func (c *activityRevisionCache) forget(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.gens, addr)
	delete(c.revisions, addr)
}

func (c *activityRevisionCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gens = make(map[string]uint64)
	c.revisions = make(map[string]string)
}
