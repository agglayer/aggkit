package bridgetracker

import (
	"context"
	"slices"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/finality"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

// ActivityEntryRefresher (re)computes the claim/tracking/finality state of one activity entry.
// It is the single implementation of that logic shared by both activity stores — the in-memory
// ActivityCache and the SQLite-backed one (bridgetracker/db) — so both always produce the same
// entry, and so the same revision (see domain.HashActivityEntry), from the same inputs.
//
// Safe for concurrent use as long as its driven ports are.
type ActivityEntryRefresher struct {
	claims     ActivityClaimChecker
	supervised SupervisedStore
	// finality decides whether a bridge / claim served by a bridge service is final. nil means
	// nothing is ever final (entries are refreshed forever and never settle)
	finality finality.Resolver
	logger   aggkitcommon.Logger
}

// NewActivityEntryRefresher returns an ActivityEntryRefresher resolving claim state through
// claims, tracker snapshots (includeTracking) through supervised and finality through resolver
// (nil: nothing is ever final)
func NewActivityEntryRefresher(
	claims ActivityClaimChecker, supervised SupervisedStore, resolver finality.Resolver, logger aggkitcommon.Logger,
) *ActivityEntryRefresher {
	return &ActivityEntryRefresher{claims: claims, supervised: supervised, finality: resolver, logger: logger}
}

// ActivityEntrySettled reports whether entry is done being rechecked: claimed, with the claim
// record fetched, and both the bridge's block (origin network) and the claim's block
// (destination network) final. Anything else — unclaimed, the isClaimed() check itself having
// failed, claimed but not indexed yet, or claimed but either block not final yet — is
// re-verified on every background refresh and can still be overwritten, since a reorg can still
// remove the bridge or the claim
func ActivityEntrySettled(entry *domain.ActivityEntry) bool {
	return entry.ClaimStatus == types.ClaimStatusClaimed && entry.Claim != nil &&
		entry.BridgeFinal && entry.ClaimFinal
}

// KnownBridgeOf is the domain.KnownBridge a store hands to ActivityBridgeScanner.BridgesFrom for
// entry, under entry.Key(): where its bridge was last cached, its block position and contents (see
// domain.SameBridge) and whether that location is already final
func KnownBridgeOf(entry *domain.ActivityEntry) domain.KnownBridge {
	known := domain.KnownBridge{Source: entry.Source, NetworkID: entry.BridgeNetworkID, Final: entry.BridgeFinal}
	if b := entry.Bridge; b != nil {
		known.TxHash = b.TxHash
		known.BlockNum = b.BlockNum
		known.BlockTimestamp = b.BlockTimestamp
		known.DepositCount = b.DepositCount
		known.BlockPos = b.BlockPos
		known.GlobalIndex = b.GlobalIndex
		known.BridgeHash = b.BridgeHash
		known.Amount = b.Amount
		known.DestinationNetwork = b.DestinationNetwork
		known.DestinationAddress = b.DestinationAddress
	}
	return known
}

// ActivityRefreshItem is one entry ActivityRefreshPlan says to (re)compute: Item is the bridge to
// refresh (fresh scan data when the scan returned it, the cached bridge otherwise), Existing the
// entry currently cached for it (nil for a new bridge)
type ActivityRefreshItem struct {
	Item     *domain.ScannedBridge
	Existing *domain.ActivityEntry
}

// ActivityRefreshPlan is what one refresh of an address does to its cached entries, computed by
// PlanActivityRefresh: Removed are the keys to forget, Items the entries to (re)compute through
// ActivityEntryRefresher.Refresh and store (each under Item.Key())
type ActivityRefreshPlan struct {
	Removed []domain.BridgeKey
	Items   []ActivityRefreshItem
}

// PlanActivityRefresh merges an address's cached entries (keyed by domain.BridgeKey) with the
// result of one ActivityBridgeScanner.BridgesFrom call (found, invalidated; both nil when the scan
// failed, so every cached entry is kept as it is and only rechecked):
//   - a cached entry the scan returned again is refreshed with the scan's fresh data (a
//     correction when its location or contents changed: same key, so never a duplicate) — unless
//     it is settled and still the very same bridge event (see domain.SameBridge), which is left
//     untouched;
//   - a cached entry the scan invalidated, and did not return again, is removed — unless it is
//     settled (a final entry cannot be reorged out, so it is never removed);
//   - any other cached entry that is not settled is rechecked with its cached data;
//   - a bridge the scan returned that is not cached yet is added.
//
// Cached entries come first, in key order, then new bridges in the order the scan returned them
func PlanActivityRefresh(
	cached map[domain.BridgeKey]*domain.ActivityEntry, found []*domain.ScannedBridge, invalidated []domain.BridgeKey,
) ActivityRefreshPlan {
	foundByKey := make(map[domain.BridgeKey]*domain.ScannedBridge, len(found))
	newKeys := make([]domain.BridgeKey, 0, len(found))
	for _, item := range found {
		key := item.Key()
		if _, dup := foundByKey[key]; !dup {
			newKeys = append(newKeys, key)
		}
		foundByKey[key] = item // the last one reported wins
	}
	removed := make(map[domain.BridgeKey]struct{}, len(invalidated))
	for _, key := range invalidated {
		removed[key] = struct{}{}
	}

	var plan ActivityRefreshPlan
	keys := make([]domain.BridgeKey, 0, len(cached))
	for key := range cached {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, domain.BridgeKey.Compare)
	for _, key := range keys {
		existing := cached[key]
		settled := ActivityEntrySettled(existing)
		if fresh, ok := foundByKey[key]; ok {
			if settled && domain.SameBridge(existing.Bridge, fresh.Bridge) {
				continue
			}
			plan.Items = append(plan.Items, ActivityRefreshItem{Item: fresh, Existing: existing})
			continue
		}
		if settled {
			continue
		}
		if _, ok := removed[key]; ok {
			plan.Removed = append(plan.Removed, key)
			continue
		}
		plan.Items = append(plan.Items, ActivityRefreshItem{
			Item: &domain.ScannedBridge{
				Bridge: existing.Bridge, NetworkID: existing.BridgeNetworkID, Source: existing.Source,
			},
			Existing: existing,
		})
	}
	for _, key := range newKeys {
		if _, ok := cached[key]; ok {
			continue
		}
		plan.Items = append(plan.Items, ActivityRefreshItem{Item: foundByKey[key]})
	}
	return plan
}

// BlockTimeOrNow converts ts, a block's unix-second timestamp, to a UTC time.Time, or returns
// now when ts is zero — the sentinel BridgeResponse.BlockTimestamp effectively uses for "not
// resolved yet" (a real on-chain block at exactly the unix epoch does not happen in practice)
func BlockTimeOrNow(ts uint64, now time.Time) time.Time {
	if ts == 0 {
		return now
	}
	return time.Unix(int64(ts), 0).UTC()
}

// Refresh (re)computes the state of item, stamped at now. existing is the entry currently cached
// for the same domain.BridgeKey, or nil if it has never been seen before. Nothing of existing (its
// creation time, finality or claim state) is carried over unless item is the very same bridge
// event (see domain.SameBridge): a different location or different contents under the same key —
// a reorg, a reused deposit count or a contradictory source — are rechecked from scratch, so a
// replacement never inherits the claim of the bridge it replaces.
//
//   - CreatedAt is carried forward from existing, or item.Bridge's own block timestamp (falling
//     back to now while the bridge service has not populated it, see BlockTimeOrNow) for a new
//     bridge or one that is no longer the same event (another block/tx, position or contents).
//     UpdatedAt is always now: it reports the last time the network was checked, not the last
//     time the entry changed (agglayer/aggkit#1840).
//   - BridgeFinal: false for a bridge only found via RPC; otherwise kept once seen final for the
//     same bridge event, else resolved through the finality resolver with the block_finalized the
//     bridge service served for it.
//   - Claim: isClaimed() is called on every refresh until the claim is final — the claim can be
//     reorged out, sending the entry back to "readyToClaim" or "pending". Once claimed, the
//     destination's claim record is fetched again (it may have been re-included elsewhere) and
//     its finality resolved; a final claim of an unchanged bridge is reused without any call.
//   - Unclaimed: "pending" vs "readyToClaim" comes from the tracker snapshot (includeTracking)
//     or IsReadyToClaim, re-checked on every refresh (readiness stays tentative while unclaimed).
//
// A transient failure never discards known state: a claimed entry whose isClaimed() / claim
// record fetch fails keeps its last known claim, not newly final (only a fetched record is proven
// final), and a resolver error just means "not final yet".
// Failures are logged and retried on the next refresh; they never fail the caller's refresh
func (r *ActivityEntryRefresher) Refresh(
	ctx context.Context, item *domain.ScannedBridge, existing *domain.ActivityEntry, includeTracking bool,
	now time.Time,
) *domain.ActivityEntry {
	sameBridge := existing != nil && domain.SameBridge(existing.Bridge, item.Bridge)
	entry := &domain.ActivityEntry{
		Bridge: item.Bridge, BridgeNetworkID: item.NetworkID, Source: item.Source, UpdatedAt: now,
	}
	if sameBridge {
		entry.CreatedAt = existing.CreatedAt
	} else {
		entry.CreatedAt = BlockTimeOrNow(item.Bridge.BlockTimestamp, now)
	}
	entry.BridgeFinal = r.bridgeFinal(ctx, item, existing, sameBridge)

	// A final claim of an unchanged bridge cannot change any more: no isClaimed() nor claim
	// record call is needed for it (only the bridge's own finality may still be pending)
	if sameBridge && existing.ClaimStatus == types.ClaimStatusClaimed && existing.Claim != nil &&
		existing.ClaimFinal {
		entry.ClaimStatus = types.ClaimStatusClaimed
		entry.Claim = existing.Claim
		entry.ClaimFinal = true
		return finishActivityEntry(entry)
	}
	existingClaimed := sameBridge && existing.ClaimStatus == types.ClaimStatusClaimed

	claimed, err := r.claims.IsClaimed(ctx, item)
	if err != nil {
		r.logger.Warnf("activity: checking claim state of bridge tx=%s (network=%d, deposit=%d): %v",
			item.Bridge.TxHash, item.NetworkID, item.Bridge.DepositCount, err)
		if existingClaimed {
			// keep the last known claim: a failed check is not evidence the claim is gone
			entry.ClaimStatus = types.ClaimStatusClaimed
			keepCachedClaim(entry, existing)
			return finishActivityEntry(entry)
		}
		entry.ClaimStatus = types.ClaimStatusError
		entry.TrackerClaimStatus = types.TrackerClaimStatusError
		entry.Errors = map[string]string{"claim": err.Error()}
		return entry
	}

	if claimed {
		entry.ClaimStatus = types.ClaimStatusClaimed
		claim, err := r.claims.ClaimInfo(ctx, item)
		if err != nil {
			r.logger.Warnf("activity: fetching claim record of bridge tx=%s: %v", item.Bridge.TxHash, err)
			if existingClaimed {
				// keep the last known record until it can be fetched again
				keepCachedClaim(entry, existing)
				return finishActivityEntry(entry)
			}
		}
		entry.Claim = claim
		entry.ClaimFinal = r.claimFinal(ctx, item, existing, claim)
		return finishActivityEntry(entry)
	}

	entry.ClaimStatus = types.ClaimStatusUnclaimed
	// Unclaimed: conservatively "pending" until proven otherwise, either by the tracker's own
	// snapshot (includeTracking) or the direct readiness check below
	entry.TrackerClaimStatus = types.TrackerClaimStatusPending

	if includeTracking {
		tracking, err := r.supervised.Get(domain.BridgeTrackingID(item.NetworkID, item.Bridge), true)
		if err != nil {
			r.logger.Warnf("activity: registering bridge tx=%s with the tracker: %v", item.Bridge.TxHash, err)
		} else {
			entry.Tracking = tracking
			entry.TrackerClaimStatus = domain.UnclaimedTrackerClaimStatus(tracking)
			return finishActivityEntry(entry)
		}
	}

	// includeTracking was not requested, or registering with the tracker failed: fall back to
	// asking the bridge-service endpoints directly whether the bridge is already ready to claim
	// (see ActivityClaimChecker.IsReadyToClaim), without the cost of registering it with the
	// tracker
	ready, err := r.claims.IsReadyToClaim(ctx, item)
	if err != nil {
		r.logger.Warnf("activity: checking claim readiness of bridge tx=%s (network=%d, deposit=%d): %v",
			item.Bridge.TxHash, item.NetworkID, item.Bridge.DepositCount, err)
		entry.Errors = map[string]string{"readiness": err.Error()}
	} else if ready {
		entry.TrackerClaimStatus = types.TrackerClaimStatusReadyToClaim
	}
	return finishActivityEntry(entry)
}

// keepCachedClaim sets entry's claim to existing's, the last known one, when it could not be
// refreshed (isClaimed() or the claim record fetch failed), with existing's finality as is: a
// record not fetched again is never newly proven final. It may be an orphaned record (re-included
// at the same height in another block, with another timestamp) that the receipt fallback of the
// finality resolver would prove final by hash and height alone, after which the final-claim
// shortcut would never fetch the corrected record again
func keepCachedClaim(entry, existing *domain.ActivityEntry) {
	entry.Claim = existing.Claim
	entry.ClaimFinal = existing.ClaimFinal
}

// finishActivityEntry derives Finalized and, for a claimed entry, TrackerClaimStatus from the
// entry's claim state and finality flags (see domain.ActivityEntry.Finalized,
// ActivityEntrySettled). An unclaimed entry keeps the TrackerClaimStatus its caller set
func finishActivityEntry(entry *domain.ActivityEntry) *domain.ActivityEntry {
	switch entry.ClaimStatus {
	case types.ClaimStatusClaimed:
		entry.Finalized = ActivityEntrySettled(entry)
		if entry.Finalized {
			entry.TrackerClaimStatus = types.TrackerClaimStatusClaimedFinalized
		} else {
			entry.TrackerClaimStatus = types.TrackerClaimStatusClaimed
		}
	case types.ClaimStatusUnclaimed:
		entry.Finalized = entry.BridgeFinal
	default:
		entry.Finalized = false
	}
	return entry
}

// bridgeFinal resolves whether item's bridge block is final on its origin network (see
// domain.ActivityEntry.BridgeFinal)
func (r *ActivityEntryRefresher) bridgeFinal(
	ctx context.Context, item *domain.ScannedBridge, existing *domain.ActivityEntry, sameBridge bool,
) bool {
	if item.Source == domain.ActivitySourceRPC {
		return false // not served by the bridge service: never final until it reports it
	}
	if sameBridge && existing.BridgeFinal {
		return true // monotonic
	}
	return r.isFinal(ctx, "bridge", item.NetworkID, item.Bridge.TxHash, item.Bridge.BlockNum,
		item.Bridge.BlockFinalized)
}

// claimFinal resolves whether claim's block is final on item's destination network (see
// domain.ActivityEntry.ClaimFinal); false without a claim record
func (r *ActivityEntryRefresher) claimFinal(
	ctx context.Context, item *domain.ScannedBridge, existing *domain.ActivityEntry,
	claim *bridgeservicetypes.ClaimResponse,
) bool {
	if claim == nil {
		return false
	}
	if existing != nil && existing.ClaimFinal && domain.SameClaimLocation(existing.Claim, claim) {
		return true // monotonic
	}
	return r.isFinal(ctx, "claim", item.Bridge.DestinationNetwork, claim.TxHash, claim.BlockNum,
		claim.BlockFinalized)
}

// isFinal asks the finality resolver; an error is logged and read as "not final yet"
func (r *ActivityEntryRefresher) isFinal(
	ctx context.Context, what string, networkID uint32, txHash bridgeservicetypes.Hash, blockNum uint64,
	reported *bool,
) bool {
	if r.finality == nil {
		return false
	}
	final, err := r.finality.IsFinal(ctx, networkID, common.HexToHash(string(txHash)), blockNum, reported)
	if err != nil {
		r.logger.Warnf("activity: resolving finality of %s tx=%s (network=%d, block=%d), "+
			"kept as not final: %v", what, txHash, networkID, blockNum, err)
		return false
	}
	return final
}
