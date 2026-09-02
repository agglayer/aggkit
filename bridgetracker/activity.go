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

// compile-time check: ActivityCache fulfils ActivityQuerier
var _ ActivityQuerier = (*ActivityCache)(nil)

// activityAddrCache is the per-from_address state ActivityCache keeps: every bridge found for
// it so far, and when it was last requested (see ActivityCache.addrCache, which stamps this on
// every GetActivity call and is what the idle sweep evicts by — the same lastAccess/PruneIdle
// idea memoryRegistry uses for tracking, see registry.go's bridgeEntry)
type activityAddrCache struct {
	entries map[string]*domain.ActivityEntry // key: bridge.GlobalIndex.String()
	// lastAccess is when this address was last requested; addresses idle past idleTimeout are
	// forgotten (see ActivityCache.addrCache)
	lastAccess time.Time
}

// ActivityCache implements domain.ActivityQuerier: for a given from_address it scans every
// configured bridge service (via ActivityBridgeScanner) for bridges it has not already cached,
// and keeps a running per-address cache of the resulting bridges, so:
//   - a bridge already known is never re-scanned from the bridge service again (see
//     ActivityBridgeScanner.BridgesFrom's known parameter);
//   - once a bridge is confirmed claimed, isClaimed() is never asked again for it, even if its
//     claim record has not been fetched yet;
//   - once a bridge's claim record has been fetched, it is never asked for again;
//   - an address nobody has asked about in idleTimeout is forgotten entirely, freeing everything
//     cached for it (mirrors SupervisedStore.PruneIdle's idea, without a dedicated ticker: see
//     addrCache).
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
}

// NewActivityCache returns an ActivityCache resolving bridges through scanner, claim state
// through claims, and (when asked) tracker registration through supervised. idleTimeout is how
// long an address survives with no GetActivity call for it before being forgotten; <= 0 falls
// back to DefaultIdleTimeout
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
	}
}

// GetActivity implements domain.ActivityQuerier: it rechecks every bridge already cached for
// fromAddress that is not yet settled (see settled — their raw bridge data is already cached, so
// this needs no bridge-service call), then scans for bridges not seen before (see
// ActivityBridgeScanner.BridgesFrom), and returns everything cached for fromAddress that matches
// filter. The returned []domain.ActivityWarning is whatever the scan reported for networks it
// could not reach this call (see ActivityBridgeScanner.BridgesFrom) — it never fails the call by
// itself, since the result is still valid for every other network.
func (a *ActivityCache) GetActivity(
	ctx context.Context, fromAddress common.Address, includeTracking bool, filter types.ActivityFilter,
) ([]*domain.ActivityEntry, []domain.ActivityWarning, error) {
	addrCache := a.addrCache(fromAddress)

	a.mu.Lock()
	known := make(map[string]struct{}, len(addrCache.entries))
	cached := make([]*domain.ActivityEntry, 0, len(addrCache.entries))
	for key, entry := range addrCache.entries {
		known[key] = struct{}{}
		cached = append(cached, entry)
	}
	a.mu.Unlock()

	for _, entry := range cached {
		scanned := &domain.ScannedBridge{Bridge: entry.Bridge, NetworkID: entry.BridgeNetworkID}
		a.upsert(ctx, addrCache, scanned, includeTracking, filter)
	}

	newItems, warnings, err := a.scanner.BridgesFrom(ctx, fromAddress, known)
	if err != nil {
		return nil, nil, fmt.Errorf("scanning bridges from %s: %w", fromAddress, err)
	}
	for _, item := range newItems {
		a.upsert(ctx, addrCache, item, includeTracking, filter)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*domain.ActivityEntry, 0, len(addrCache.entries))
	for _, entry := range addrCache.entries {
		if matchesFilter(entry, filter) {
			out = append(out, entry)
		}
	}
	return out, warnings, nil
}

// upsert (re)computes item's entry via refresh and stores it, unless it is already cached and
// settled — in which case it is left untouched. Safe to call with an item the caller cannot be
// sure is genuinely new (e.g. a defensive re-check, or a pagination-boundary duplicate): settled
// entries are never redundantly refreshed regardless of where item came from
func (a *ActivityCache) upsert(
	ctx context.Context, addrCache *activityAddrCache, item *domain.ScannedBridge,
	includeTracking bool, filter types.ActivityFilter,
) {
	key := item.Bridge.GlobalIndex.String()

	a.mu.Lock()
	existing := addrCache.entries[key]
	a.mu.Unlock()

	if existing != nil && settled(existing) {
		return
	}

	entry := a.refresh(ctx, item, existing, includeTracking, filter)

	a.mu.Lock()
	addrCache.entries[key] = entry
	a.mu.Unlock()
}

// matchesFilter reports whether entry belongs in a GetActivity result under filter:
// ActivityFilterAll always matches; the other filters match exactly one ClaimStatus each — see
// ActivityFilter's doc for what each one means
func matchesFilter(entry *domain.ActivityEntry, filter types.ActivityFilter) bool {
	switch filter {
	case types.ActivityFilterClaimed:
		return entry.ClaimStatus == types.ClaimStatusClaimed
	case types.ActivityFilterPending:
		return entry.ClaimStatus == types.ClaimStatusUnclaimed
	case types.ActivityFilterError:
		return entry.ClaimStatus == types.ClaimStatusError
	case types.ActivityFilterAll:
		return true
	default:
		return true
	}
}

// skipsClaimInfo reports whether filter excludes a claimed bridge from its result, making the
// destination bridge service's claim record unnecessary to fetch right now (see refresh)
func skipsClaimInfo(filter types.ActivityFilter) bool {
	return filter == types.ActivityFilterPending || filter == types.ActivityFilterError
}

// addrCache returns (creating if necessary) the per-address cache for fromAddress, stamping its
// lastAccess with now. Before that, it sweeps every address whose lastAccess is older than
// idleTimeout out of byAddr — the idle-eviction sweep. There is no dedicated ticker/goroutine for
// this (unlike SupervisedStore.PruneIdle, which the tracking engine drives on its own poll
// ticker, see engine.go's tick): ActivityCache has no background loop of its own to piggyback
// on, and sweeping on every real request is cheap for the expected number of distinct addresses.
func (a *ActivityCache) addrCache(fromAddress common.Address) *activityAddrCache {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	cutoff := now.Add(-a.idleTimeout)
	for addr, cache := range a.byAddr {
		// fromAddress itself is deliberately not exempted: if it was already idle-expired as of
		// its last access, its stale state is forgotten and it starts fresh below, exactly as if
		// this were its first request
		if cache.lastAccess.Before(cutoff) {
			delete(a.byAddr, addr)
		}
	}

	addrCache, ok := a.byAddr[fromAddress]
	if !ok {
		addrCache = &activityAddrCache{entries: make(map[string]*domain.ActivityEntry)}
		a.byAddr[fromAddress] = addrCache
	}
	addrCache.lastAccess = now
	return addrCache
}

// settled reports whether entry is done being rechecked: claimed, with the claim record
// already fetched. Anything else (unclaimed, the isClaimed() check itself having failed, or
// claimed but the destination bridge service has not indexed the claim yet) is re-verified on
// every GetActivity call
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
// Once claimed, the destination bridge service's claim record is fetched — skipped when filter
// excludes claimed bridges anyway (see skipsClaimInfo); the entry then simply stays unsettled
// and is fetched normally the next time a filter that needs it is used (see settled) — or, only
// if includeTracking, the tracker's current snapshot is attached for the still-unclaimed tx. A
// failure at any step is logged and left for the next call to retry; it never fails the whole
// GetActivity call, since one bad network should not hide every other bridge found
func (a *ActivityCache) refresh(
	ctx context.Context, item *domain.ScannedBridge, existing *domain.ActivityEntry,
	includeTracking bool, filter types.ActivityFilter,
) *domain.ActivityEntry {
	entry := &domain.ActivityEntry{Bridge: item.Bridge, BridgeNetworkID: item.NetworkID}
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
		if skipsClaimInfo(filter) {
			return entry
		}
		claim, err := a.claims.ClaimInfo(ctx, item)
		if err != nil {
			a.logger.Warnf("activity: fetching claim record of bridge tx=%s: %v", item.Bridge.TxHash, err)
		}
		entry.Claim = claim
		return entry
	}

	if includeTracking {
		id := domain.TrackingID{NetworkID: item.NetworkID, TxHash: common.HexToHash(string(item.Bridge.TxHash))}
		tracking, err := a.supervised.Get(id, true)
		if err != nil {
			a.logger.Warnf("activity: registering bridge tx=%s with the tracker: %v", item.Bridge.TxHash, err)
		} else {
			entry.Tracking = tracking
		}
	}
	return entry
}
