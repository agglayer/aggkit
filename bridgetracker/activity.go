package bridgetracker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/finality"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

// compile-time check: ActivityCache fulfils ActivityRegistry
var _ ActivityRegistry = (*ActivityCache)(nil)

// compile-time check: ActivityCache serves the per-address revision
var _ domain.ActivityRevisionReader = (*ActivityCache)(nil)

// DefaultMaxActivityAddresses bounds how many distinct from_addresses ActivityCache accepts
// before refusing new ones (see ActivityCache.register), mirroring DefaultMaxTrackedBridges'
// DoS-protection reasoning for the tracker's own registry
const DefaultMaxActivityAddresses = 100_000

// activityAddrCache is the per-from_address state ActivityCache keeps: every bridge found for
// it so far, when it was last accessed, whether tracking enrichment has been requested for it,
// the warnings from its last background refresh, and whoever is currently blocked in
// RegisterAndAwait waiting for that refresh to complete.
type activityAddrCache struct {
	entries map[string]*domain.ActivityEntry // key: string(bridge.GlobalIndex)
	// lastAccess is when this address was last requested (RegisterAndAwait/GetActivity);
	// addresses idle past idleTimeout are forgotten (see ActivityCache.PruneIdle, the same
	// lastAccess/PruneIdle idea memoryRegistry uses for tracking, see registry.go's bridgeEntry)
	lastAccess time.Time
	// wantsTracking is set once any caller has asked for includeTracking; from then on every
	// background refresh enriches still-unclaimed bridges with their tracker snapshot (see
	// RefreshAddress) — the background cannot know a future request's own flag
	wantsTracking bool
	// warnings is whatever ActivityBridgeScanner.BridgesFrom reported on the last refresh;
	// overwritten every call rather than accumulated, so a recovered network's warning clears
	// promptly
	warnings []domain.ActivityWarning
	// revision accumulates the hash of every entry in entries (see domain.ActivityRevision,
	// domain.HashActivityEntry, domain.ActivityRevisionReader): every write to entries goes
	// through storeEntry/removeEntry, which keep it in step (Remove(old hash).Add(new hash)). Its
	// zero value is the empty activity
	revision domain.ActivityRevision
	// waiters holds one channel per RegisterAndAwait call currently blocked on this address's
	// next refresh; closed and cleared by RefreshAddress once it completes
	waiters map[chan struct{}]struct{}
	// refreshed is set once RefreshAddress has completed at least once for this address
	// (successful or not) — RegisterAndAwait's ready return value, so a caller with nothing
	// meaningful to show yet can be told apart from one whose address genuinely has no activity
	refreshed bool
}

// ActivityCache implements domain.ActivityRegistry: for a given from_address it scans every
// configured bridge service (via ActivityBridgeScanner) and keeps a running per-address cache of
// the resulting bridges, so:
//   - a bridge already known and final is never re-read from the bridge service again, while
//     the non-final ones are re-read on every refresh and corrected or removed when a reorg
//     moved or dropped them (see ActivityBridgeScanner.BridgesFrom's known parameter,
//     PlanActivityRefresh);
//   - isClaimed() and the claim record are re-checked on every refresh until the entry is
//     settled — claimed, claim record fetched, bridge and claim blocks final (see
//     ActivityEntrySettled) — and never again after that;
//   - an address nobody has asked about in idleTimeout is forgotten entirely, freeing everything
//     cached for it (see PruneIdle, mirroring SupervisedStore.PruneIdle, driven by
//     ActivityEngine's poll tick instead of a sweep-on-every-request).
//
// None of the scanning/rechecking above happens inline inside GetActivity: it happens in
// RefreshAddress, called only by ActivityEngine's poll tick or its trigger fast-path (see
// RegisterAndAwait). GetActivity itself is a cache-only read.
//
// Safe for concurrent use.
type ActivityCache struct {
	scanner   ActivityBridgeScanner
	refresher *ActivityEntryRefresher
	logger    aggkitcommon.Logger

	// now is the clock lastAccess is stamped with and the idle sweep compares against,
	// injectable for tests (mirrors memoryRegistry.now, registry.go)
	now func() time.Time

	mu     sync.Mutex
	byAddr map[common.Address]*activityAddrCache
	// trigger carries the addresses of freshly registered entries out to ActivityEngine (see
	// Triggers), which refreshes them immediately instead of waiting for its next poll tick
	trigger chan common.Address
}

// NewActivityCache returns an ActivityCache resolving bridges through scanner, claim state
// through claims, (when asked) tracker registration through supervised, and whether a bridge or
// claim served by a bridge service is final through resolver (nil: nothing is ever final, so no
// entry ever settles). Idle eviction is entirely driven by whoever wires an ActivityEngine over
// the returned cache (see ActivityEngineConfig.IdleTimeout, PruneIdle) — this constructor has no
// idleTimeout of its own to fall back to
func NewActivityCache(
	scanner ActivityBridgeScanner, claims ActivityClaimChecker, supervised SupervisedStore,
	resolver finality.Resolver, logger aggkitcommon.Logger,
) *ActivityCache {
	return &ActivityCache{
		scanner:   scanner,
		refresher: NewActivityEntryRefresher(claims, supervised, resolver, logger),
		logger:    logger,
		now:       time.Now,
		byAddr:    make(map[common.Address]*activityAddrCache),
		trigger:   make(chan common.Address, triggerBufferSize),
	}
}

// RegisterAndAwait implements domain.ActivitySupervisedStore. On an already-registered address
// it behaves like a plain touch: no trigger, no wait, ready reports its current refreshed
// state. On a newly registered address it wakes ActivityEngine (see signalTrigger/Triggers) and
// waits up to timeout for that first refresh to complete, reporting whether it actually did
// (ready) or timeout elapsed first with nothing to show yet. includeTracking, when true, sets
// the sticky wantsTracking flag immediately — not just on a later GetActivity call — so even the
// very first triggered refresh (the one a caller with timeout > 0 blocks on) already enriches
// tracking, instead of the flag only taking effect on the refresh after that
func (a *ActivityCache) RegisterAndAwait(
	fromAddress common.Address, includeTracking bool, timeout time.Duration,
) (bool, error) {
	a.mu.Lock()
	cache, existed := a.byAddr[fromAddress]
	if !existed {
		if len(a.byAddr) >= DefaultMaxActivityAddresses {
			a.mu.Unlock()
			return false, domain.ErrActivityRegistryFull
		}
		cache = &activityAddrCache{
			entries: make(map[string]*domain.ActivityEntry),
			waiters: make(map[chan struct{}]struct{}),
		}
		a.byAddr[fromAddress] = cache
	}
	cache.lastAccess = a.now()
	if includeTracking {
		cache.wantsTracking = true
	}

	if existed || timeout <= 0 {
		ready := cache.refreshed
		a.mu.Unlock()
		if !existed {
			a.signalTrigger(fromAddress)
		}
		return ready, nil
	}

	ch := make(chan struct{})
	cache.waiters[ch] = struct{}{}
	a.mu.Unlock()

	a.signalTrigger(fromAddress)

	defer func() {
		a.mu.Lock()
		delete(cache.waiters, ch)
		a.mu.Unlock()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	}

	a.mu.Lock()
	ready := cache.refreshed
	a.mu.Unlock()
	return ready, nil
}

// GetActiveAddresses implements domain.ActivitySupervisedStore: every currently supervised
// from_address, for ActivityEngine's poll tick to iterate
func (a *ActivityCache) GetActiveAddresses() ([]common.Address, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	addrs := make([]common.Address, 0, len(a.byAddr))
	for addr := range a.byAddr {
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

// PruneIdle implements domain.ActivitySupervisedStore: it forgets every address with no
// in-flight RegisterAndAwait call that was last accessed before olderThan, returning how many
// were forgotten. Mirrors memoryRegistry.PruneIdle
func (a *ActivityCache) PruneIdle(olderThan time.Time) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	pruned := 0
	for addr, cache := range a.byAddr {
		if len(cache.waiters) == 0 && cache.lastAccess.Before(olderThan) {
			delete(a.byAddr, addr)
			pruned++
		}
	}
	return pruned, nil
}

// signalTrigger notifies ActivityEngine that fromAddress was just registered. It never blocks:
// a full buffer just means this address waits for the next regular poll tick like before
func (a *ActivityCache) signalTrigger(fromAddress common.Address) {
	select {
	case a.trigger <- fromAddress:
	default:
	}
}

// Triggers implements domain.ActivityTriggerable
func (a *ActivityCache) Triggers() <-chan common.Address {
	return a.trigger
}

// RefreshAddress implements domain.ActivitySupervisedStore: it scans fromAddress's bridges
// (see ActivityBridgeScanner.BridgesFrom: new bridges plus a fresh read of every non-final one),
// merges the result into the cache (see PlanActivityRefresh: corrections overwrite, invalidated
// non-final entries are forgotten, every entry not settled yet is rechecked through
// ActivityEntryRefresher), keeps the address's revision in step with every change, records the
// scan's warnings, and finally wakes every RegisterAndAwait call currently blocked on
// fromAddress. A failed scan still rechecks the cached entries (keeping them all) before the
// error is returned. Only ever called by ActivityEngine. A missing cache (address not currently
// registered) is a silent no-op — the regular tick already only iterates GetActiveAddresses
func (a *ActivityCache) RefreshAddress(ctx context.Context, fromAddress common.Address) error {
	a.mu.Lock()
	cache, ok := a.byAddr[fromAddress]
	if !ok {
		a.mu.Unlock()
		return nil
	}
	includeTracking := cache.wantsTracking
	known := make(map[string]domain.KnownBridge, len(cache.entries))
	cached := make(map[string]*domain.ActivityEntry, len(cache.entries))
	for key, entry := range cache.entries {
		known[key] = KnownBridgeOf(entry)
		cached[key] = entry
	}
	a.mu.Unlock()

	found, invalidated, warnings, scanErr := a.scanner.BridgesFrom(ctx, fromAddress, known)
	if scanErr != nil {
		// nothing the failed scan returned can be trusted: keep every entry, just recheck them
		found, invalidated = nil, nil
	}
	plan := PlanActivityRefresh(cached, found, invalidated)

	a.mu.Lock()
	for _, key := range plan.Removed {
		cache.removeEntry(key)
	}
	a.mu.Unlock()

	for _, work := range plan.Items {
		entry := a.refresher.Refresh(ctx, work.Item, work.Existing, includeTracking, a.now())
		a.mu.Lock()
		cache.storeEntry(string(work.Item.Bridge.GlobalIndex), entry)
		a.mu.Unlock()
	}

	if scanErr != nil {
		a.finishRefresh(cache)
		return fmt.Errorf("scanning bridges from %s: %w", fromAddress, scanErr)
	}

	a.mu.Lock()
	cache.warnings = warnings
	a.mu.Unlock()

	a.finishRefresh(cache)
	return nil
}

// storeEntry stores entry under key, keeping revision in step: the hash of the entry it replaces
// (if any) is removed and entry's is added. Callers must hold ActivityCache.mu
func (c *activityAddrCache) storeEntry(key string, entry *domain.ActivityEntry) {
	if old, ok := c.entries[key]; ok {
		c.revision = c.revision.Remove(domain.HashActivityEntry(old))
	}
	c.revision = c.revision.Add(domain.HashActivityEntry(entry))
	c.entries[key] = entry
}

// removeEntry forgets the entry under key, if any, taking its hash out of revision. Callers must
// hold ActivityCache.mu
func (c *activityAddrCache) removeEntry(key string) {
	old, ok := c.entries[key]
	if !ok {
		return
	}
	c.revision = c.revision.Remove(domain.HashActivityEntry(old))
	delete(c.entries, key)
}

// finishRefresh marks cache as having completed at least one refresh (see activityAddrCache.
// refreshed, RegisterAndAwait's ready return value) and wakes every RegisterAndAwait call
// currently blocked on it, clearing the waiter set; safe to call even when nobody is waiting.
// cache is whatever *activityAddrCache RefreshAddress captured at the start of its run — a
// flush_cache + re-register racing with a slow refresh replaces a.byAddr's entry with a brand
// new struct, detaching this one, but that is harmless: cache.refreshed/cache.waiters live on
// the struct itself, not the map slot, so this still only ever affects the registration it
// actually belongs to; the new registration's own waiters were added to the new struct, and get
// woken by its own, separate RefreshAddress call instead
func (a *ActivityCache) finishRefresh(cache *activityAddrCache) {
	a.mu.Lock()
	defer a.mu.Unlock()

	cache.refreshed = true
	for ch := range cache.waiters {
		close(ch)
	}
	cache.waiters = make(map[chan struct{}]struct{})
}

// GetActivity implements domain.ActivityQuerier: a cache-only read of whatever the last
// background refresh (see RefreshAddress) computed for fromAddress, filtered per filter.
// includeTracking additionally marks fromAddress as wanting tracking enrichment for future
// refreshes (see activityAddrCache.wantsTracking) — it does not itself fetch anything. An
// address with no cache yet (never registered, or registered but not refreshed even once)
// returns an empty result, not an error
func (a *ActivityCache) GetActivity(
	_ context.Context, fromAddress common.Address, includeTracking bool, filter types.ActivityFilter,
) ([]*domain.ActivityEntry, []domain.ActivityWarning, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	cache, ok := a.byAddr[fromAddress]
	if !ok {
		return nil, nil, nil
	}
	cache.lastAccess = a.now()
	if includeTracking {
		cache.wantsTracking = true
	}

	out := make([]*domain.ActivityEntry, 0, len(cache.entries))
	for _, entry := range cache.entries {
		if MatchesActivityFilter(entry, filter) {
			out = append(out, entry)
		}
	}
	return out, cache.warnings, nil
}

// GetActivityRevision implements domain.ActivityRevisionReader: the address's current revision
// (activityAddrCache.revision), domain.EmptyActivityRevision for an address with nothing cached.
// It does not count as an access (unlike GetActivity, it does not touch lastAccess): the HTTP
// command has already counted one through RegisterAndAwait, and may not call GetActivity at all
// (a 304 Not Modified)
func (a *ActivityCache) GetActivityRevision(_ context.Context, fromAddress common.Address) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	cache, ok := a.byAddr[fromAddress]
	if !ok {
		return domain.EmptyActivityRevision, nil
	}
	return cache.revision.String(), nil
}

// FlushActivity implements domain.ActivityQuerier: it discards fromAddress's whole per-address
// cache, if any — every cached bridge (settled or not) is forgotten, so the next background
// refresh for fromAddress rescans and rechecks everything from scratch, exactly as if it had
// never been requested before. Safe to call for an address with nothing cached (no-op)
func (a *ActivityCache) FlushActivity(fromAddress common.Address) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.byAddr, fromAddress)
}

// MatchesActivityFilter reports whether entry belongs in a GetActivity result under filter:
// ActivityFilterAll always matches, ActivityFilterClaimed matches both "claimed" and
// "claimed_finalized" (see types.TrackerClaimStatus.IsClaimed), and the other filters match
// exactly one TrackerClaimStatus each — see ActivityFilter's doc for what each one means. Shared
// by both activity stores (ActivityCache and the SQLite one). TrackerClaimStatus (rather than the
// coarser ClaimStatus) is what distinguishes ActivityFilterPending from
// ActivityFilterReadyToClaim; it mirrors ClaimStatus directly for the claimed/error cases (see
// domain.ActivityEntry.TrackerClaimStatus)
func MatchesActivityFilter(entry *domain.ActivityEntry, filter types.ActivityFilter) bool {
	switch filter {
	case types.ActivityFilterClaimed:
		return entry.TrackerClaimStatus.IsClaimed()
	case types.ActivityFilterPending:
		return entry.TrackerClaimStatus == types.TrackerClaimStatusPending
	case types.ActivityFilterReadyToClaim:
		return entry.TrackerClaimStatus == types.TrackerClaimStatusReadyToClaim
	case types.ActivityFilterError:
		return entry.TrackerClaimStatus == types.TrackerClaimStatusError
	case types.ActivityFilterAll:
		return true
	default:
		return true
	}
}
