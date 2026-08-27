package domain

import (
	"context"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// ActivityEntry is one bridge found for a from_address, as of the last time it was (re)checked
// (see ActivityQuerier). Bridge and Claim are stored exactly as the bridge service returned
// them — this feature is a cache over that data, not a reinterpretation of it (see
// bridgeservice/types.BridgeResponse/ClaimResponse)
type ActivityEntry struct {
	// Bridge is the raw bridge event, as returned by the origin network's bridge service
	Bridge *bridgeservicetypes.BridgeResponse
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
}

// ActivityBridgeScanner is the driven port to the raw bridge-service data behind the
// GET /activity/from/{from_address} endpoint: it scans every bridge service the tracker knows
// about for bridges sent by fromAddress
type ActivityBridgeScanner interface {
	// BridgesFrom returns every bridge whose sender is fromAddress, across every configured
	// bridge service, exactly as each network's own bridge service reports it
	BridgesFrom(ctx context.Context, fromAddress common.Address) ([]*bridgeservicetypes.BridgeResponse, error)
}

// ActivityClaimChecker is the driven port to a bridge's claim state on its destination
// network: IsClaimed is the on-chain source of truth, ClaimInfo is the raw claim record the
// destination network's bridge service indexed for it once claimed
type ActivityClaimChecker interface {
	// IsClaimed calls the destination bridge contract's isClaimed() for bridge
	IsClaimed(ctx context.Context, bridge *bridgeservicetypes.BridgeResponse) (bool, error)
	// ClaimInfo returns the raw claim record for bridge from its destination network's
	// bridge service, or nil if the indexer has not recorded it yet
	ClaimInfo(ctx context.Context, bridge *bridgeservicetypes.BridgeResponse) (*bridgeservicetypes.ClaimResponse, error)
}

// ActivityQuerier is the driven port the GET /activity/from/{from_address} HTTP command
// depends on
type ActivityQuerier interface {
	// GetActivity returns every bridge sent by fromAddress across every configured bridge
	// service, enriched with its claim state; includeTracking additionally feeds every
	// still-unclaimed bridge to the bridge tracker (see ActivityEntry.Tracking)
	GetActivity(ctx context.Context, fromAddress common.Address, includeTracking bool) ([]*ActivityEntry, error)
}
