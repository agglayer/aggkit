package domain

import (
	"context"
	"errors"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// ErrActivityRegistryFull is returned by ActivitySupervisedStore.RegisterAndAwait when
// registering a new from_address would exceed the store's configured capacity (see
// ErrRegistryFull for the tracker's equivalent and the same DoS-protection reasoning: without
// this bound, an unauthenticated caller could register an unbounded number of distinct
// addresses, each staying supervised — and periodically rescanned by the ActivityEngine — until
// its idle timeout elapses)
var ErrActivityRegistryFull = errors.New("activity registry is full")

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
	// then conservatively stays "pending"). nil while nothing has failed. Every value is
	// already redacted (aggkitcommon.RedactError) since this map reaches API clients via
	// GET /activity/from/{from_address}
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
	// Message is the error encountered while scanning NetworkID, already redacted
	// (aggkitcommon.RedactSensitive) since this value reaches API clients - see
	// ActivitySource.warnf
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
	// NetworkID is the network whose bridge service reported it last time (see
	// ScannedBridge.NetworkID) — lets an implementation scanning one network at a time tell
	// which known entries are its own, e.g. to decide whether one of them has gone missing from
	// its own recent scan window (see ActivityBridgeScanner.BridgesFrom's invalidated return).
	NetworkID uint32
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
	// invalidated lists the GlobalIndex (as a decimal string, same encoding as known's keys) of
	// every entry in known that should be forgotten instead of trusted: known.Source ==
	// ActivitySourceRPC, its BlockNum still falls inside the range this call actually scanned via
	// RPC for its own network (see KnownBridge.NetworkID), yet the bridge no longer shows up
	// there at all — the deposit was reorged out and nothing has re-included it (yet). A caller
	// must remove these from its cache rather than keep serving their last known (now unverified)
	// data; if the bridge is re-included later, at the same or a different block, it is
	// discovered again as new. A bridge cached from the bridge service (Source ==
	// ActivitySourceBridgeService) is never invalidated this way — that data is presumed
	// reorg-safe already (the bridge service's own sync pipeline is responsible for that).
	//
	// A network whose bridge service cannot be scanned does not fail the call: it is skipped and
	// reported back as an ActivityWarning instead, so one misbehaving network never hides every
	// other network's activity.
	BridgesFrom(
		ctx context.Context, fromAddress common.Address, known map[string]KnownBridge,
	) (found []*ScannedBridge, invalidated []string, warnings []ActivityWarning, err error)
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
// depends on for reading. Unlike before ActivitySupervisedStore existed, GetActivity is a
// cache-only read: it never scans a bridge service or consults claims/the tracker itself — that
// work is done in the background by whatever refreshes the address's cache (see
// ActivitySupervisedStore.RefreshAddress, driven by ActivityEngine), the same way
// SupervisedStore.Get never resolves a tracker bridge on the caller's behalf either
type ActivityQuerier interface {
	// GetActivity returns whatever is currently cached for fromAddress, filtered per filter (see
	// types.ActivityFilter). includeTracking additionally marks fromAddress as wanting tracking
	// enrichment (see ActivityEntry.Tracking) for future background refreshes — the very first
	// response after this flag flips may still be missing Tracking for a given bridge until the
	// next refresh populates it, the same "poll again" precedent the tracker itself established
	// for a freshly registered bridge's BridgeStatus. The returned []ActivityWarning is whatever
	// the last background refresh recorded for a network it could not scan — the result is still
	// whatever every other network reported, just possibly incomplete for the networks listed.
	// Returns an empty result, not an error, for an address that is not (yet) supervised — callers
	// reach this port only after ActivitySupervisedStore.RegisterAndAwait
	GetActivity(
		ctx context.Context, fromAddress common.Address, includeTracking bool, filter types.ActivityFilter,
	) ([]*ActivityEntry, []ActivityWarning, error)

	// FlushActivity discards whatever is cached for fromAddress, forcing every bridge found for
	// it to be freshly rechecked (claim state re-verified, tracker re-consulted) on the next
	// background refresh, instead of reusing anything cached so far. Safe to call for an address
	// with nothing cached (no-op). The activity endpoint's ?flush_cache=true parameter uses it
	// to force a fresh recheck
	FlushActivity(fromAddress common.Address)
}

// ActivitySupervisedStore is the driven port to the supervised from_addresses list behind
// GET /activity/from/{from_address} — the engine-facing counterpart of ActivityQuerier,
// mirroring SupervisedStore for tracked tx IDs (see SupervisedStore's doc for the same idea
// applied there).
//
// Implementations must be safe for concurrent use.
type ActivitySupervisedStore interface {
	// RegisterAndAwait behaves like SupervisedStore.GetAndAwait: on an already-registered
	// address, immediate return, no trigger, no wait. On a newly registered address, it
	// additionally wakes the ActivityEngine to refresh it right away (see ActivityTriggerable)
	// instead of leaving it for the next poll tick, and waits up to timeout for that first
	// refresh to finish before returning. timeout <= 0 skips the wait entirely.
	//
	// includeTracking, when true, sets the sticky tracking-enrichment flag (see
	// ActivityQuerier.GetActivity) immediately, before the trigger fires — not only on a later
	// GetActivity call — so even the very first refresh a caller with timeout > 0 blocks on
	// already enriches tracking, instead of requiring one more refresh after that to take effect.
	//
	// ready reports whether fromAddress has completed at least one refresh (successful or not)
	// as of the moment this call returns — an already-registered address reports whatever its
	// current state is (no wait either way), a newly registered one is false unless the wait
	// above resolved before timeout. A caller getting back false has nothing meaningful to show
	// yet and should tell the client to retry later (e.g. HTTP 503 + Retry-After) instead of
	// answering with an empty result indistinguishable from "no activity at all" — see
	// ActivityCommand.Execute. Returns ErrActivityRegistryFull if fromAddress is new and the
	// store is at capacity
	RegisterAndAwait(fromAddress common.Address, includeTracking bool, timeout time.Duration) (ready bool, err error)

	// GetActiveAddresses returns every currently supervised from_address, for
	// ActivityEngine's poll tick to iterate (mirrors SupervisedStore.GetTrackerActives)
	GetActiveAddresses() ([]common.Address, error)

	// RefreshAddress performs the actual scan (ActivityBridgeScanner.BridgesFrom) plus
	// claim/tracking recheck for fromAddress — what GetActivity itself used to do inline — and
	// notifies whoever is blocked in RegisterAndAwait for it. Only ever called by ActivityEngine,
	// never directly by an HTTP handler
	RefreshAddress(ctx context.Context, fromAddress common.Address) error

	// PruneIdle forgets addresses whose cache has not been accessed (GetActivity/
	// RegisterAndAwait) since before olderThan, returning how many were forgotten — mirrors
	// SupervisedStore.PruneIdle, driven by ActivityEngine's own poll tick instead of a
	// sweep-on-every-request
	PruneIdle(olderThan time.Time) (int, error)
}

// ActivityTriggerable is an optional capability of an ActivitySupervisedStore: it exposes the
// from_addresses that were just registered, so ActivityEngine can refresh them immediately
// instead of waiting for its next poll tick (see RegisterAndAwait and ActivityEngine.Start).
// Mirrors Triggerable for tracked tx IDs
type ActivityTriggerable interface {
	// Triggers returns the channel of freshly registered from_addresses ActivityEngine should
	// refresh right away. A send may be dropped if the channel is full — that address is not
	// lost, it is simply left for the next regular poll tick like before
	Triggers() <-chan common.Address
}

// ActivityRegistry is the full activity subsystem: state (ActivitySupervisedStore) plus reads
// (ActivityQuerier) — what the activity HTTP command depends on, mirroring SupervisedRegistry
type ActivityRegistry interface {
	ActivitySupervisedStore
	ActivityQuerier
}
