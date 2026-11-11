// Package cache keeps the answers to the GERs, so the validator doesn't query L1, nor sign, twice for the same GER.
package cache

import (
	"container/list"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// Entry is what is known about a GER: its signature, or why it can't be signed
type Entry struct {
	// Signature is the signature of the GER. If it is nil, Err says why there isn't one
	Signature []byte
	// Err is why the GER can't be signed (unknown, not canonical, not final yet)
	Err error
}

type item struct {
	ger       common.Hash
	entry     Entry
	expiresAt time.Time // zero: never expires
}

// Cache keeps the signatures (until they are evicted, they never expire) and the negative answers (for a short
// time) of the GERs. When it is full the oldest entries are removed. It is safe for concurrent use.
type Cache struct {
	mu         sync.Mutex
	maxEntries int
	order      *list.List // front is the newest
	items      map[common.Hash]*list.Element
	now        func() time.Time
}

// New creates a cache that keeps, at most, maxEntries GERs
func New(maxEntries int) *Cache {
	return &Cache{
		maxEntries: maxEntries,
		order:      list.New(),
		items:      make(map[common.Hash]*list.Element),
		now:        time.Now,
	}
}

// Get returns what is known about the GER. An expired negative answer is not returned.
func (c *Cache) Get(ger common.Hash) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.items[ger]
	if !ok {
		return Entry{}, false
	}
	it, _ := element.Value.(*item)
	if !it.expiresAt.IsZero() && !c.now().Before(it.expiresAt) {
		c.remove(element)
		return Entry{}, false
	}
	entry := it.entry
	if entry.Signature != nil {
		// A copy: the caller can't modify what the cache keeps
		entry.Signature = append([]byte(nil), entry.Signature...)
	}
	return entry, true
}

// PutSignature keeps the signature of the GER. It replaces a negative answer.
func (c *Cache) PutSignature(ger common.Hash, signature []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.put(&item{ger: ger, entry: Entry{Signature: append([]byte(nil), signature...)}})
}

// PutError keeps for ttl the reason a GER can't be signed. It does nothing if ttl is not positive
// or the GER already has a signature: a signature is never replaced by a negative answer.
func (c *Cache) PutError(ger common.Hash, err error, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.items[ger]; ok {
		if it, _ := element.Value.(*item); it.entry.Signature != nil {
			return
		}
	}
	c.put(&item{ger: ger, entry: Entry{Err: err}, expiresAt: c.now().Add(ttl)})
}

// Len returns the number of GERs in the cache, including the expired negative answers not removed yet
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// put inserts the item as the newest one, replacing the previous one of the GER, and evicts the oldest
// entries if the cache is full. The caller holds the lock.
func (c *Cache) put(newItem *item) {
	if element, ok := c.items[newItem.ger]; ok {
		c.remove(element)
	}
	c.items[newItem.ger] = c.order.PushFront(newItem)
	for c.order.Len() > c.maxEntries {
		c.remove(c.order.Back())
	}
}

func (c *Cache) remove(element *list.Element) {
	it, _ := element.Value.(*item)
	delete(c.items, it.ger)
	c.order.Remove(element)
}
