package bridgetracker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

// compile-time check: ActivityCache fulfils ActivityRegistry
var _ ActivityRegistry = (*ActivityCache)(nil)

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
	// waiters holds one channel per RegisterAndAwait call currently blocked on this address's
	// next refresh; closed and cleared by RefreshAddress once it completes
	waiters map[chan struct{}]struct{}
	// refreshed is set once RefreshAddress has completed at least once for this address
	// (successful or not) — RegisterAndAwait's ready return value, so a caller with nothing
	// meaningful to show yet can be told apart from one whose address genuinely has no activity
	refreshed bool
}

// ActivityCache implements domain.ActivityRegistry: for a given from_address it scans every
// configured bridge service (via ActivityBridgeScanner) for bridges it has not already cached,
// and keeps a running per-address cache of the resulting bridges, so:
//   - a bridge already known is never re-scanned from the bridge service again (see
//     ActivityBridgeScanner.BridgesFrom's known parameter);
//   - once a bridge is confirmed claimed, isClaimed() is never asked again for it, even if its
//     claim record has not been fetched yet;
//   - once a bridge's claim record has been fetched, it is never asked for again;
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
	scanner    ActivityBridgeScanner
	claims     ActivityClaimChecker
	supervised SupervisedStore
	logger     aggkitcommon.Logger

	idleTimeout time.Duration
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
// through claims, and (when asked) tracker registration through supervised. idleTimeout is how
// long an address survives with no access before being forgotten by PruneIdle; <= 0 falls back
// to DefaultIdleTimeout
func NewActivityCache(
	scanner ActivityBridgeScanner, claims ActivityClaimChecker, supervised SupervisedStore,
	logger aggkitcommon.Logger, idleTimeout time.Duration,
) *ActivityCache {
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout.Duration
	}
	return &ActivityCache{
		scanner:     scanner,
		claims:      claims,
		supervised:  supervised,
		logger:      logger,
		idleTimeout: idleTimeout,
		now:         time.Now,
		byAddr:      make(map[common.Address]*activityAddrCache),
		trigger:     make(chan common.Address, triggerBufferSize),
	}
}

// RegisterAndAwait implements domain.ActivitySupervisedStore. On an already-registered address
// it behaves like a plain touch: no trigger, no wait, ready reports its current refreshed
// state. On a newly registered address it wakes ActivityEngine (see signalTrigger/Triggers) and
// waits up to timeout for that first refresh to complete, reporting whether it actually did
// (ready) or timeout elapsed first with nothing to show yet
func (a *ActivityCache) RegisterAndAwait(fromAddress common.Address, timeout time.Duration) (bool, error) {
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

// RefreshAddress implements domain.ActivitySupervisedStore: it rechecks every bridge already
// cached for fromAddress that is not yet settled (see settled), scans for bridges not seen
// before (see ActivityBridgeScanner.BridgesFrom), forgets whatever the scan reports as
// invalidated, records the scan's warnings, and finally wakes every RegisterAndAwait call
// currently blocked on fromAddress. This is what used to run inline inside GetActivity; it is
// now only ever called by ActivityEngine. A missing cache (address not currently registered) is
// a silent no-op — the regular tick already only iterates GetActiveAddresses
func (a *ActivityCache) RefreshAddress(ctx context.Context, fromAddress common.Address) error {
	a.mu.Lock()
	cache, ok := a.byAddr[fromAddress]
	if !ok {
		a.mu.Unlock()
		return nil
	}
	includeTracking := cache.wantsTracking
	known := make(map[string]domain.KnownBridge, len(cache.entries))
	cached := make([]*domain.ActivityEntry, 0, len(cache.entries))
	for key, entry := range cache.entries {
		known[key] = domain.KnownBridge{
			TxHash: entry.Bridge.TxHash, BlockNum: entry.Bridge.BlockNum,
			Source: entry.Source, NetworkID: entry.BridgeNetworkID,
		}
		cached = append(cached, entry)
	}
	a.mu.Unlock()

	for _, entry := range cached {
		// Source is carried forward from the cached entry, not rediscovered — this is a claim/
		// tracking recheck of data already cached, not a new scan result, so it must not reset an
		// entry's Source back to its zero value
		scanned := &domain.ScannedBridge{Bridge: entry.Bridge, NetworkID: entry.BridgeNetworkID, Source: entry.Source}
		a.upsert(ctx, cache, scanned, includeTracking)
	}

	newItems, invalidated, warnings, err := a.scanner.BridgesFrom(ctx, fromAddress, known)
	if err != nil {
		a.finishRefresh(cache)
		return fmt.Errorf("scanning bridges from %s: %w", fromAddress, err)
	}
	if len(invalidated) > 0 {
		// forget these before upserting newItems (not after): a GlobalIndex the scanner reports
		// as both found and invalidated in the very same call (it should never, but this way a
		// bug in that regard fails toward losing a stale entry rather than a fresh one) must end
		// up cached, not forgotten
		a.mu.Lock()
		for _, key := range invalidated {
			delete(cache.entries, key)
		}
		a.mu.Unlock()
	}
	for _, item := range newItems {
		a.upsert(ctx, cache, item, includeTracking)
	}

	a.mu.Lock()
	cache.warnings = warnings
	a.mu.Unlock()

	a.finishRefresh(cache)
	return nil
}

// finishRefresh marks cache as having completed at least one refresh (see activityAddrCache.
// refreshed, RegisterAndAwait's ready return value) and wakes every RegisterAndAwait call
// currently blocked on it, clearing the waiter set; safe to call even when nobody is waiting
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
		if matchesFilter(entry, filter) {
			out = append(out, entry)
		}
	}
	return out, cache.warnings, nil
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

// upsert (re)computes item's entry via refresh and stores it, unless it is already cached and
// settled — in which case it is left untouched. Safe to call with an item the caller cannot be
// sure is genuinely new (e.g. a defensive re-check, or a pagination-boundary duplicate): settled
// entries are never redundantly refreshed regardless of where item came from
func (a *ActivityCache) upsert(
	ctx context.Context, addrCache *activityAddrCache, item *domain.ScannedBridge, includeTracking bool,
) {
	key := string(item.Bridge.GlobalIndex)

	a.mu.Lock()
	existing := addrCache.entries[key]
	a.mu.Unlock()

	if existing != nil && settled(existing) {
		return
	}

	entry := a.refresh(ctx, item, existing, includeTracking)

	a.mu.Lock()
	addrCache.entries[key] = entry
	a.mu.Unlock()
}

// matchesFilter reports whether entry belongs in a GetActivity result under filter:
// ActivityFilterAll always matches; the other filters match exactly one TrackerClaimStatus each
// — see ActivityFilter's doc for what each one means. TrackerClaimStatus (rather than the
// coarser ClaimStatus) is what distinguishes ActivityFilterPending from
// ActivityFilterReadyToClaim; it mirrors ClaimStatus directly for the claimed/error cases (see
// domain.ActivityEntry.TrackerClaimStatus)
func matchesFilter(entry *domain.ActivityEntry, filter types.ActivityFilter) bool {
	switch filter {
	case types.ActivityFilterClaimed:
		return entry.TrackerClaimStatus == types.TrackerClaimStatusClaimed
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

// settled reports whether entry is done being rechecked: claimed, with the claim record
// already fetched. Anything else (unclaimed, the isClaimed() check itself having failed, or
// claimed but the destination bridge service has not indexed the claim yet) is re-verified on
// every background refresh
func settled(entry *domain.ActivityEntry) bool {
	return entry.ClaimStatus == types.ClaimStatusClaimed && entry.Claim != nil
}

// refresh (re)computes the claim/tracking state of a single bridge item, stamping
// ActivityEntry.CreatedAt (carried forward from existing, or now if this is the first time) and
// UpdatedAt (always now — every call to refresh counts as an update, whether or not anything
// about the entry actually changed). existing is the previously cached entry for this same
// bridge, or nil if it has never been seen before:
//   - if existing is already confirmed claimed, isClaimed() is not asked again — that result
//     never reverts — and refresh goes straight to the claim-record step;
//   - otherwise the on-chain isClaimed() call runs as usual (unclaimed and error states must
//     keep being re-verified, since only a confirmed claim is permanent).
//
// Once claimed, the destination bridge service's claim record is always fetched: unlike before
// RefreshAddress ran independently of any particular request's filter, there is no longer a
// per-call filter to skip it for — a background refresh must fetch it eventually regardless of
// which filter happens to be requested next (see settled) — or, only if includeTracking, the
// tracker's current snapshot is attached for the still-unclaimed tx. A failure at any step is
// logged and left for the next call to retry; it never fails the whole RefreshAddress call,
// since one bad network should not hide every other bridge found
func (a *ActivityCache) refresh(
	ctx context.Context, item *domain.ScannedBridge, existing *domain.ActivityEntry, includeTracking bool,
) *domain.ActivityEntry {
	entry := &domain.ActivityEntry{Bridge: item.Bridge, BridgeNetworkID: item.NetworkID, Source: item.Source}
	if existing != nil {
		entry.CreatedAt = existing.CreatedAt
	} else {
		entry.CreatedAt = a.now()
	}
	entry.UpdatedAt = a.now()

	if existing != nil && existing.ClaimStatus == types.ClaimStatusClaimed {
		entry.ClaimStatus = types.ClaimStatusClaimed
	} else {
		claimed, err := a.claims.IsClaimed(ctx, item)
		if err != nil {
			a.logger.Warnf("activity: checking claim state of bridge tx=%s (network=%d, deposit=%d): %v",
				item.Bridge.TxHash, item.NetworkID, item.Bridge.DepositCount, err)
			entry.ClaimStatus = types.ClaimStatusError
			entry.TrackerClaimStatus = types.TrackerClaimStatusError
			entry.Errors = map[string]string{"claim": err.Error()}
			return entry
		}
		if claimed {
			entry.ClaimStatus = types.ClaimStatusClaimed
		} else {
			entry.ClaimStatus = types.ClaimStatusUnclaimed
		}
	}

	if entry.ClaimStatus == types.ClaimStatusClaimed {
		entry.TrackerClaimStatus = types.TrackerClaimStatusClaimed
		claim, err := a.claims.ClaimInfo(ctx, item)
		if err != nil {
			a.logger.Warnf("activity: fetching claim record of bridge tx=%s: %v", item.Bridge.TxHash, err)
		}
		entry.Claim = claim
		return entry
	}

	// Unclaimed: conservatively "pending" until proven otherwise, either by the tracker's own
	// snapshot (includeTracking) or the direct readiness check below
	entry.TrackerClaimStatus = types.TrackerClaimStatusPending

	if includeTracking {
		id := domain.TrackingID{NetworkID: item.NetworkID, TxHash: common.HexToHash(string(item.Bridge.TxHash))}
		tracking, err := a.supervised.Get(id, true)
		if err != nil {
			a.logger.Warnf("activity: registering bridge tx=%s with the tracker: %v", item.Bridge.TxHash, err)
		} else {
			entry.Tracking = tracking
			entry.TrackerClaimStatus = tracking.ClaimStatus()
			return entry
		}
	}

	// includeTracking was not requested, or registering with the tracker failed: fall back to
	// asking the bridge-service endpoints directly whether the bridge is already ready to claim
	// (see ActivityClaimChecker.IsReadyToClaim), without the cost of registering it with the
	// tracker
	ready, err := a.claims.IsReadyToClaim(ctx, item)
	if err != nil {
		a.logger.Warnf("activity: checking claim readiness of bridge tx=%s (network=%d, deposit=%d): %v",
			item.Bridge.TxHash, item.NetworkID, item.Bridge.DepositCount, err)
		if entry.Errors == nil {
			entry.Errors = make(map[string]string)
		}
		entry.Errors["readiness"] = err.Error()
	} else if ready {
		entry.TrackerClaimStatus = types.TrackerClaimStatusReadyToClaim
	}
	return entry
}
