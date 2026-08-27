package bridgetracker

import (
	"context"
	"fmt"
	"sync"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

// compile-time check: ActivityCache fulfils ActivityQuerier
var _ ActivityQuerier = (*ActivityCache)(nil)

// ActivityCache implements domain.ActivityQuerier: for a given from_address it scans every
// configured bridge service (via ActivityBridgeScanner) and keeps a running per-address cache
// of the resulting bridges, so an already-settled bridge — claimed, with its claim record
// already fetched — is never rechecked again. Every other entry (new, still unclaimed, or
// claimed but not yet indexed by the destination bridge service) is rechecked on every call.
//
// Safe for concurrent use.
type ActivityCache struct {
	scanner    ActivityBridgeScanner
	claims     ActivityClaimChecker
	supervised SupervisedStore
	logger     aggkitcommon.Logger

	mu     sync.Mutex
	byAddr map[common.Address]map[string]*domain.ActivityEntry // key: bridge.GlobalIndex.String()
}

// NewActivityCache returns an ActivityCache resolving bridges through scanner, claim state
// through claims, and (when asked) tracker registration through supervised
func NewActivityCache(
	scanner ActivityBridgeScanner, claims ActivityClaimChecker, supervised SupervisedStore,
	logger aggkitcommon.Logger,
) *ActivityCache {
	return &ActivityCache{
		scanner:    scanner,
		claims:     claims,
		supervised: supervised,
		logger:     logger,
		byAddr:     make(map[common.Address]map[string]*domain.ActivityEntry),
	}
}

// GetActivity implements domain.ActivityQuerier
func (a *ActivityCache) GetActivity(
	ctx context.Context, fromAddress common.Address, includeTracking bool,
) ([]*domain.ActivityEntry, error) {
	items, err := a.scanner.BridgesFrom(ctx, fromAddress)
	if err != nil {
		return nil, fmt.Errorf("scanning bridges from %s: %w", fromAddress, err)
	}

	addrCache := a.addrCache(fromAddress)

	for _, item := range items {
		key := item.GlobalIndex.String()

		a.mu.Lock()
		existing := addrCache[key]
		a.mu.Unlock()

		if existing != nil && settled(existing) {
			continue
		}

		entry := a.refresh(ctx, item, includeTracking)

		a.mu.Lock()
		addrCache[key] = entry
		a.mu.Unlock()
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*domain.ActivityEntry, 0, len(addrCache))
	for _, entry := range addrCache {
		out = append(out, entry)
	}
	return out, nil
}

// addrCache returns (creating if necessary) the per-address cache map for fromAddress
func (a *ActivityCache) addrCache(fromAddress common.Address) map[string]*domain.ActivityEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	addrCache, ok := a.byAddr[fromAddress]
	if !ok {
		addrCache = make(map[string]*domain.ActivityEntry)
		a.byAddr[fromAddress] = addrCache
	}
	return addrCache
}

// settled reports whether entry is done being rechecked: claimed, with the claim record
// already fetched. Anything else (unclaimed, the isClaimed() check itself having failed, or
// claimed but the destination bridge service has not indexed the claim yet) is re-verified on
// every GetActivity call
func settled(entry *domain.ActivityEntry) bool {
	return entry.ClaimStatus == types.ClaimStatusClaimed && entry.Claim != nil
}

// refresh (re)computes the claim/tracking state of a single bridge item: the on-chain
// isClaimed() call, then either the destination bridge service's claim record (once claimed)
// or — only if includeTracking — the tracker's current snapshot for the still-unclaimed tx.
// A failure at any step is logged and left for the next call to retry; it never fails the
// whole GetActivity call, since one bad network should not hide every other bridge found
func (a *ActivityCache) refresh(
	ctx context.Context, item *bridgeservicetypes.BridgeResponse, includeTracking bool,
) *domain.ActivityEntry {
	entry := &domain.ActivityEntry{Bridge: item}

	claimed, err := a.claims.IsClaimed(ctx, item)
	if err != nil {
		a.logger.Warnf("activity: checking claim state of bridge tx=%s (origin network=%d, deposit=%d): %v",
			item.TxHash, item.OriginNetwork, item.DepositCount, err)
		entry.ClaimStatus = types.ClaimStatusError
		return entry
	}

	if claimed {
		entry.ClaimStatus = types.ClaimStatusClaimed
		claim, err := a.claims.ClaimInfo(ctx, item)
		if err != nil {
			a.logger.Warnf("activity: fetching claim record of bridge tx=%s: %v", item.TxHash, err)
		}
		entry.Claim = claim
		return entry
	}
	entry.ClaimStatus = types.ClaimStatusUnclaimed

	if includeTracking {
		id := domain.TrackingID{NetworkID: item.OriginNetwork, TxHash: common.HexToHash(string(item.TxHash))}
		tracking, err := a.supervised.Get(id, true)
		if err != nil {
			a.logger.Warnf("activity: registering bridge tx=%s with the tracker: %v", item.TxHash, err)
		} else {
			entry.Tracking = tracking
		}
	}
	return entry
}
