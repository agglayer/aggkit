package domain

import (
	"context"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// ActivitySourceKind identifies which system supplied a ScannedBridge/ActivityEntry's current
// data: the network's own bridge service (ActivitySourceBridgeService — the source of record) or
// this package's RPC-based fallback (ActivitySourceRPC, see sources.activityRPCScanner,
// agglayer/aggkit#1837), used only while the bridge service has not indexed it yet (or is
// unreachable). Whenever both report the very same bridge in one scan, the bridge service always
// wins (see ActivitySource.BridgesFrom's merge — a bridge the bridge service reports is never
// left labeled ActivitySourceRPC just because the RPC fallback also happened to find it).
type ActivitySourceKind string

const (
	// ActivitySourceBridgeService: the bridge came from the network's own bridge service
	// GET /bridge/v1/bridges — the default, and the source of record whenever it is available.
	ActivitySourceBridgeService ActivitySourceKind = "bridge"
	// ActivitySourceRPC: the bridge service had not indexed this bridge (or could not be
	// reached), and it was only discovered by scanning the network directly via RPC.
	ActivitySourceRPC ActivitySourceKind = "rpc"
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
	// Source is which system supplied Bridge — the bridge service or the RPC fallback (see
	// ActivitySourceKind); ActivityQuerier.GetActivity carries it into ActivityEntry.Source
	Source ActivitySourceKind
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
	// Source is which system supplied Bridge as of the last time this entry was (re)scanned —
	// the bridge service or the RPC fallback (see ActivitySourceKind, ScannedBridge.Source)
	Source ActivitySourceKind
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
	// TrackerClaimStatus mirrors the bridge tracker's own simplified claim-readiness summary
	// (see TrackingData.ClaimStatus) so the activity endpoint reports the same vocabulary —
	// "pending"/"readyToClaim"/"claimed"/"error" — instead of ClaimStatus's plain tri-state.
	// Derived from ClaimStatus: TrackerClaimStatusClaimed/Error mirror ClaimStatus directly;
	// while ClaimStatus is Unclaimed, it is copied straight from Tracking.ClaimStatus() when
	// Tracking is populated, or otherwise resolved directly against the bridge-service
	// endpoints (see ActivityClaimChecker.IsReadyToClaim) without registering the bridge with
	// the tracker
	TrackerClaimStatus types.TrackerClaimStatus
	// Errors holds the message of whatever check failed the last time this entry was
	// refreshed, keyed by which check it was — "claim" when ClaimStatus is Error (the
	// isClaimed() check itself failed), "readiness" when IsReadyToClaim itself failed (Claimed
	// then conservatively stays "pending"). nil while nothing has failed
	Errors map[string]string
	// CreatedAt is when this bridge was first cached (its first successful refresh); it never
	// changes after that
	CreatedAt time.Time
	// UpdatedAt is when this entry's claim/tracking state was last (re)computed — the last time
	// refresh ran for it, whether or not anything about it actually changed. Frozen once the
	// entry settles (see ActivityCache's settled), since a settled entry is never refreshed again
	UpdatedAt time.Time
}

// ActivityWarning reports one network's bridge service that could not be scanned/reached while
// serving a request that spans every configured network (see ActivityBridgeScanner.BridgesFrom,
// ActivityQuerier.GetActivity) — the scan still succeeds with whatever every other network
// reported, but the caller must be told this network's activity may be incomplete
type ActivityWarning struct {
	// NetworkID is the network whose bridge service could not be scanned
	NetworkID uint32
	// Message is the error encountered while scanning NetworkID
	Message string
}

// KnownBridge is the on-chain location (TxHash/BlockNum) the caller last cached a bridge's data
// at, keyed by its GlobalIndex in the known map ActivityBridgeScanner.BridgesFrom takes. A
// source whose own data is not otherwise reorg-protected (see sources.activityRPCScanner, which
// reads a recent, possibly-unfinalized block range directly via RPC) can compare this against
// what it currently observes on-chain for the same GlobalIndex: an unchanged location means
// nothing to do, but a different one means the deposit has since been re-included in a different
// block (or, more rarely, the same deposit count reused by a different bridge) — either way the
// caller's cached data is now stale and must be corrected, not silently trusted forever just
// because the GlobalIndex was seen before.
type KnownBridge struct {
	// TxHash is the transaction hash the bridge was last reported at.
	TxHash bridgeservicetypes.Hash
	// BlockNum is the block number the bridge was last reported at.
	BlockNum uint64
	// Source is which system supplied it the last time it was cached (see ActivitySourceKind).
	// A bridge-service scanner uses this to upgrade an entry it once had to report via the RPC
	// fallback: once the bridge service itself reports the same bridge, Source should become
	// ActivitySourceBridgeService and stay that way, even though nothing about the bridge's
	// on-chain location changed (see sources.fetchNewBridgesFrom).
	Source ActivitySourceKind
}

// ActivityBridgeScanner is the driven port to the raw bridge-service data behind the
// GET /activity/from/{from_address} endpoint: it scans every bridge service the tracker knows
// about for bridges sent by fromAddress
type ActivityBridgeScanner interface {
	// BridgesFrom returns every bridge whose sender is fromAddress, across every configured
	// bridge service: every bridge not yet in known (a genuinely new one), plus any bridge that
	// is in known but whose on-chain location no longer matches what known has cached for it
	// (see KnownBridge) — a source scanning only finalized/indexed data (the bridge-service REST
	// scan) never has the latter case, since its data cannot un-happen once indexed; a source
	// reading a recent, possibly-unfinalized block range directly via RPC (see
	// sources.activityRPCScanner) can, whenever a reorg re-included the same deposit at a
	// different block.
	//
	// known is the caller's full set of already-cached bridges for fromAddress, keyed by
	// GlobalIndex as a decimal string (any network — a GlobalIndex is unique across the whole
	// system); implementations may use plain existence in known (ignoring the cached
	// TxHash/BlockNum) to stop scanning a network as soon as an already-known bridge is reached,
	// since each network's own bridge service reports bridges newest-first and is append-only,
	// so anything after the first known bridge is guaranteed already known too (see
	// sources.ActivitySource).
	//
	// A network whose bridge service cannot be scanned does not fail the call: it is skipped and
	// reported back as an ActivityWarning instead, so one misbehaving network never hides every
	// other network's activity.
	BridgesFrom(
		ctx context.Context, fromAddress common.Address, known map[string]KnownBridge,
	) ([]*ScannedBridge, []ActivityWarning, error)
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
	// IsReadyToClaim reports whether bridge's covering L1 info tree leaf has already been
	// injected on its destination network: GET /bridge/v1/l1-info-tree-index resolves the leaf
	// covering bridge's own origin deposit, then GET /bridge/v1/injected-l1-info-leaf checks
	// whether that leaf (or a later one) has reached the destination — the same pair of
	// bridge-service endpoints the tracker's own StepWaitingL1InfoLeafAvailable/
	// StepWaitingGERInjection steps rely on, queried directly instead of through the full
	// tracker. Only meaningful (and only ever called) once IsClaimed has reported bridge
	// unclaimed, to tell ActivityEntry.TrackerClaimStatus's "pending" from "readyToClaim"
	// without registering the bridge with the tracker (see ActivityEntry.Tracking, populated
	// instead when the caller asked for includeTracking)
	IsReadyToClaim(ctx context.Context, bridge *ScannedBridge) (bool, error)
}

// ActivityQuerier is the driven port the GET /activity/from/{from_address} HTTP command
// depends on
type ActivityQuerier interface {
	// GetActivity returns the bridges sent by fromAddress across every configured bridge
	// service, enriched with their claim state and filtered per filter (see
	// types.ActivityFilter); includeTracking additionally feeds every still-unclaimed bridge in
	// the result to the bridge tracker (see ActivityEntry.Tracking). The returned
	// []ActivityWarning lists every network whose bridge service could not be scanned this call
	// (see ActivityBridgeScanner.BridgesFrom) — the result is still whatever every other network
	// reported, just possibly incomplete for the networks listed
	GetActivity(
		ctx context.Context, fromAddress common.Address, includeTracking bool, filter types.ActivityFilter,
	) ([]*ActivityEntry, []ActivityWarning, error)

	// FlushActivity discards whatever is cached for fromAddress, forcing every bridge found for
	// it to be freshly rechecked (claim state re-verified, tracker re-consulted) on the next
	// GetActivity call, instead of reusing anything cached so far. Safe to call for an address
	// with nothing cached (no-op). The activity endpoint's ?flush_cache=true parameter uses it
	// to force a fresh recheck
	FlushActivity(fromAddress common.Address)
}
