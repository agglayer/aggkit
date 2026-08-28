package domain

import (
	"context"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// ScannedBridge pairs a raw bridge event with the network whose bridge service actually
// returned it — i.e. the network the bridge-creating tx was sent to. This is deliberately NOT
// the same thing as Bridge.OriginNetwork, which is the origin network of the bridged ASSET: the
// two coincide for a first-time bridge of a native asset, but differ when re-bridging an asset
// that itself originated on a third network (e.g. an asset native to L1, already bridged to L2
// A, now bridged again from L2 A to L2 B — that bridge is reported by L2 A's own bridge service,
// yet its OriginNetwork still reads L1). Anything keyed by "which network created this deposit"
// — isClaimed()'s sourceBridgeNetwork, the GlobalIndex encoding, the tracker's TrackingID — must
// use NetworkID here, never Bridge.OriginNetwork (see bridgeservice/utils.go's NewBridgeResponse,
// which threads the requested network — not Bridge.OriginNetwork — into GlobalIndexForBridge).
type ScannedBridge struct {
	Bridge    *bridgeservicetypes.BridgeResponse
	NetworkID uint32
}

// ActivityEntry is one bridge found for a from_address, as of the last time it was (re)checked
// (see ActivityQuerier). Bridge and Claim are stored exactly as the bridge service returned
// them — this feature is a cache over that data, not a reinterpretation of it (see
// bridgeservice/types.BridgeResponse/ClaimResponse)
type ActivityEntry struct {
	// Bridge is the raw bridge event, as returned by the origin network's bridge service
	Bridge *bridgeservicetypes.BridgeResponse
	// BridgeNetworkID is the network whose bridge service reported Bridge (see ScannedBridge) —
	// NOT necessarily Bridge.OriginNetwork
	BridgeNetworkID uint32
	// ClaimStatus is the tri-state result of the destination bridge contract's isClaimed()
	// call the last time it was checked: Unclaimed, Claimed, or Error if the check itself
	// failed (e.g. no bridge contract address configured for the destination network) — a
	// consumer must not read Error as "not claimed"
	ClaimStatus types.ClaimStatus
	// Claim is the raw claim record, as returned by the destination network's bridge
	// service, once ClaimStatus is Claimed and the indexer has recorded it; nil until then
	Claim *bridgeservicetypes.ClaimResponse
	// Tracking is the bridge tracker's current snapshot of this bridge, only populated while
	// it is still unclaimed and the caller asked for it (includeTracking); nil otherwise
	Tracking *TrackingData
	// Errors holds the message of whatever check failed the last time this entry was
	// refreshed, keyed by which check it was — currently only "claim", set when ClaimStatus is
	// Error (the isClaimed() check itself failed). nil while nothing has failed
	Errors map[string]string
	// CreatedAt is when this bridge was first cached (its first successful refresh); it never
	// changes after that
	CreatedAt time.Time
	// UpdatedAt is when this entry's claim/tracking state was last (re)computed — the last time
	// refresh ran for it, whether or not anything about it actually changed. Frozen once the
	// entry settles (see ActivityCache's settled), since a settled entry is never refreshed again
	UpdatedAt time.Time
}

// ActivityBridgeScanner is the driven port to the raw bridge-service data behind the
// GET /activity/from/{from_address} endpoint: it scans every bridge service the tracker knows
// about for bridges sent by fromAddress
type ActivityBridgeScanner interface {
	// BridgesFrom returns every bridge whose sender is fromAddress and whose GlobalIndex (as a
	// decimal string) is not already in known, across every configured bridge service. known is
	// the caller's full set of already-cached global indexes for fromAddress (any network — a
	// GlobalIndex is unique across the whole system); implementations may use it to stop
	// scanning a network as soon as an already-known bridge is reached, since each network's
	// own bridge service reports bridges newest-first and is append-only, so anything after the
	// first known bridge is guaranteed already known too (see sources.ActivitySource)
	BridgesFrom(
		ctx context.Context, fromAddress common.Address, known map[string]struct{},
	) ([]*ScannedBridge, error)
}

// ActivityClaimChecker is the driven port to a bridge's claim state on its destination
// network: IsClaimed is the on-chain source of truth, ClaimInfo is the raw claim record the
// destination network's bridge service indexed for it once claimed
type ActivityClaimChecker interface {
	// IsClaimed calls the destination bridge contract's isClaimed() for bridge
	IsClaimed(ctx context.Context, bridge *ScannedBridge) (bool, error)
	// ClaimInfo returns the raw claim record for bridge from its destination network's
	// bridge service, or nil if the indexer has not recorded it yet
	ClaimInfo(ctx context.Context, bridge *ScannedBridge) (*bridgeservicetypes.ClaimResponse, error)
}

// ActivityQuerier is the driven port the GET /activity/from/{from_address} HTTP command
// depends on
type ActivityQuerier interface {
	// GetActivity returns the bridges sent by fromAddress across every configured bridge
	// service, enriched with their claim state and filtered per filter (see
	// types.ActivityFilter); includeTracking additionally feeds every still-unclaimed bridge in
	// the result to the bridge tracker (see ActivityEntry.Tracking)
	GetActivity(
		ctx context.Context, fromAddress common.Address, includeTracking bool, filter types.ActivityFilter,
	) ([]*ActivityEntry, error)
}
