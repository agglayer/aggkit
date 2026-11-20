package domain

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
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

// BridgeKey identifies one bridge within one from_address's activity: the leaf it occupies in the
// local exit tree of the network that emitted it — the network whose bridge service (or RPC
// fallback) reported it (ScannedBridge.NetworkID, ActivityEntry.BridgeNetworkID), never
// Bridge.OriginNetwork — and its deposit count there. Every activity cache, the change detection
// of ActivityBridgeScanner.BridgesFrom and the SQLite store key entries by it.
//
// Bridge.GlobalIndex is NOT an identity, only a data field served as is: it encodes the same pair,
// but ambiguously under the legacy pre-Etrog exception (see bridgesync.GlobalIndexForBridge, which
// returns the bare deposit count for a pre-Etrog L1 bridge destined to network 1 — the very value
// a modern bridge with the same deposit count emitted on network 1 encodes to). Two such bridges
// share a GlobalIndex but never a BridgeKey.
//
// The key stays the same when a reorg re-includes the bridge at another block or transaction, or
// when another bridge takes its deposit count: either is a correction of the same entry, told apart
// from "nothing changed" by SameBridge
type BridgeKey struct {
	// NetworkID is the network whose local exit tree holds the bridge (see ScannedBridge.NetworkID)
	NetworkID uint32
	// DepositCount is the bridge's deposit count (leaf index) in that network's local exit tree
	DepositCount uint32
}

// String is "network:deposit_count", for logs and error messages
func (k BridgeKey) String() string {
	return fmt.Sprintf("%d:%d", k.NetworkID, k.DepositCount)
}

// Compare orders keys by network, then deposit count: -1 if k sorts before o, 0 if equal, +1 after
func (k BridgeKey) Compare(o BridgeKey) int {
	if c := cmp.Compare(k.NetworkID, o.NetworkID); c != 0 {
		return c
	}
	return cmp.Compare(k.DepositCount, o.DepositCount)
}

// Key is b's BridgeKey: the network that reported it and its deposit count there
func (b *ScannedBridge) Key() BridgeKey {
	key := BridgeKey{NetworkID: b.NetworkID}
	if b.Bridge != nil {
		key.DepositCount = b.Bridge.DepositCount
	}
	return key
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
	// "pending"/"readyToClaim"/"claimed"/"claimed_finalized"/"error" — instead of ClaimStatus's
	// plain tri-state. Derived from ClaimStatus: TrackerClaimStatusClaimed/Error mirror
	// ClaimStatus directly, and TrackerClaimStatusClaimedFinalized is that same "claimed" once the
	// entry is settled (claim record fetched, BridgeFinal and ClaimFinal, see Finalized); while
	// ClaimStatus is Unclaimed, it is taken from Tracking.ClaimStatus() when Tracking is populated
	// — except that a tracker "claimed" is never served against an on-chain "unclaimed" (see
	// UnclaimedTrackerClaimStatus) — or otherwise resolved directly against the bridge-service
	// endpoints (see ActivityClaimChecker.IsReadyToClaim) without registering the bridge with the
	// tracker
	TrackerClaimStatus types.TrackerClaimStatus
	// BridgeFinal is whether Bridge's block is final on its origin network (BridgeNetworkID), as
	// resolved from Bridge.BlockFinalized with a receipt fallback (see finality.Resolver). Always
	// false for a bridge only found through the RPC fallback (Source == ActivitySourceRPC): it
	// becomes final once the bridge service reports it. Monotonic while Bridge stays the very same
	// event (see SameBridge). Not part of the wire contract on its own
	BridgeFinal bool
	// ClaimFinal is whether Claim's block is final on the destination network, resolved the same
	// way from Claim.BlockFinalized. Only meaningful while Claim is set
	ClaimFinal bool
	// Finalized is whether this entry can no longer be changed by a reorg: Bridge's block is
	// final (BridgeFinal) and, if the bridge is claimed, its claim record is known and the claim's
	// block is final too (ClaimFinal). A claimed entry that is Finalized is settled: it is served
	// as TrackerClaimStatusClaimedFinalized and never refreshed again. A change of this value
	// alone (false -> true) does not change the address's revision (see ActivityRevisionReader,
	// HashActivityEntry)
	Finalized bool
	// Errors holds the message of whatever check failed the last time this entry was
	// refreshed, keyed by which check it was — "claim" when ClaimStatus is Error (the
	// isClaimed() check itself failed), "readiness" when IsReadyToClaim itself failed (Claimed
	// then conservatively stays "pending"). nil while nothing has failed. Values are the raw
	// error strings, backend URLs included: redaction happens at the API layer, where this map
	// becomes client-facing as GET /activity/from/{from_address}'s "errors" object (see
	// api.ActivityItem.MarshalJSON)
	Errors map[string]string
	// CreatedAt is when this bridge was created: Bridge's own origin deposit block timestamp,
	// via bridgetracker.BlockTimeOrNow (see bridgetracker.ActivityEntryRefresher.Refresh, shared
	// by both activity stores), falling back to the first successful refresh only if that is ever
	// unavailable (BlockTimestamp not yet populated by the bridge service). Set once, and only
	// changed again if a reorg moves the bridge to another block or transaction
	CreatedAt time.Time
	// UpdatedAt is when this entry's claim/tracking state was last (re)computed — the last time
	// refresh ran for it, whether or not anything about it actually changed. Deliberately always
	// now, never a deterministic on-chain fact (agglayer/aggkit#1840): unlike CreatedAt, this is
	// not "the last time this entry's state actually changed", it is "the last time the tracker
	// checked the network for it" — a fact about the tracker's own polling, not the bridge.
	// Frozen once the entry settles (see bridgetracker.ActivityEntrySettled), since a settled
	// entry is never refreshed again
	UpdatedAt time.Time
}

// Key is e's BridgeKey: the network that reported its bridge and its deposit count there
func (e *ActivityEntry) Key() BridgeKey {
	key := BridgeKey{NetworkID: e.BridgeNetworkID}
	if e.Bridge != nil {
		key.DepositCount = e.Bridge.DepositCount
	}
	return key
}

// SameBridge reports whether a and b are the very same bridge event, so data cached for a (its
// claim state, finality, creation time) can be carried over to b: the same on-chain location (see
// SameBridgeLocation), the same position in the block (block_pos, the log index) and the same
// contents (global_index, amount, destination network and address, and bridge_hash). Two
// observations of one BridgeKey that differ in any of these are different events — a reorg moved
// it, another bridge took its deposit count, or a source reported contradictory data — and nothing
// cached for one may be reused for the other without being checked again.
// An empty bridge_hash on either side (a source that does not report it) is read as "unknown", not
// as a mismatch; every other field compares exactly (addresses case-insensitively)
func SameBridge(a, b *bridgeservicetypes.BridgeResponse) bool {
	if a == nil || b == nil {
		return a == b
	}
	return SameBridgeLocation(a, b) && a.BlockPos == b.BlockPos && a.GlobalIndex == b.GlobalIndex &&
		a.Amount == b.Amount && a.DestinationNetwork == b.DestinationNetwork &&
		strings.EqualFold(string(a.DestinationAddress), string(b.DestinationAddress)) &&
		(a.BridgeHash == "" || b.BridgeHash == "" || strings.EqualFold(string(a.BridgeHash), string(b.BridgeHash)))
}

// SameBridgeLocation reports whether a and b are the same bridge at the same on-chain location:
// same tx_hash (compared case-insensitively), block_num and block_timestamp. A zero
// block_timestamp on either side (the bridge service has not populated it yet) matches any value,
// so a timestamp being filled in is not read as the bridge having moved
func SameBridgeLocation(a, b *bridgeservicetypes.BridgeResponse) bool {
	if a == nil || b == nil {
		return a == b
	}
	return sameLocation(a.TxHash, a.BlockNum, a.BlockTimestamp, b)
}

func sameLocation(txHash bridgeservicetypes.Hash, blockNum, blockTimestamp uint64,
	b *bridgeservicetypes.BridgeResponse) bool {
	return strings.EqualFold(string(txHash), string(b.TxHash)) && blockNum == b.BlockNum &&
		(blockTimestamp == 0 || b.BlockTimestamp == 0 || blockTimestamp == b.BlockTimestamp)
}

// SameClaimLocation reports whether a and b are the same claim record at the same on-chain
// location: same tx_hash (compared case-insensitively) and block_num
func SameClaimLocation(a, b *bridgeservicetypes.ClaimResponse) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.EqualFold(string(a.TxHash), string(b.TxHash)) && a.BlockNum == b.BlockNum
}

// UnclaimedTrackerClaimStatus is ActivityEntry.TrackerClaimStatus for a bridge the destination
// bridge contract's isClaimed() just reported as unclaimed, given the tracker's snapshot of it:
// tracking.ClaimStatus(), except that a claimed status (types.TrackerClaimStatusClaimed or
// types.TrackerClaimStatusClaimedFinalized) is downgraded to types.TrackerClaimStatusPending.
// "claimed" must be confirmed on-chain: serving it for a bridge isClaimed() just reported
// unclaimed would hide the claim action from the user until the next refresh, whatever made the
// snapshot say so (e.g. a store read mid-way through a multi-step write)
func UnclaimedTrackerClaimStatus(tracking *TrackingData) types.TrackerClaimStatus {
	status := tracking.ClaimStatus()
	if status.IsClaimed() {
		return types.TrackerClaimStatusPending
	}
	return status
}

// ActivityWarning reports one network's bridge service that could not be scanned/reached while
// serving a request that spans every configured network (see ActivityBridgeScanner.BridgesFrom,
// ActivityQuerier.GetActivity) — the scan still succeeds with whatever every other network
// reported, but the caller must be told this network's activity may be incomplete
type ActivityWarning struct {
	// NetworkID is the network whose bridge service could not be scanned
	NetworkID uint32
	// Message is the error encountered while scanning NetworkID, the raw error string with
	// backend URLs included: redaction happens at the API layer, where this value becomes
	// client-facing as GET /activity/from/{from_address}'s "warnings[].message" (see
	// api.ActivityWarningItem.MarshalJSON)
	Message string
}

// KnownBridge is what the caller last cached for one bridge, keyed by its BridgeKey in the known
// map ActivityBridgeScanner.BridgesFrom takes: its on-chain location (TxHash/BlockNum/
// BlockTimestamp), its position in the block and contents (BlockPos, GlobalIndex, BridgeHash,
// Amount, DestinationNetwork, DestinationAddress), plus whether that location is already final. A
// scanner compares it against what it currently observes for the same BridgeKey (see SameBridge):
// the very same event means nothing to correct, but a different location or different contents
// mean the deposit has since been re-included in a different block, the same deposit count was
// reused by a different bridge, or a source reported contradictory data — either way the caller's
// cached data is now stale and must be corrected, not silently trusted forever just because the
// key was seen before. Only a Final entry can be trusted without being read again.
type KnownBridge struct {
	// TxHash is the transaction hash the bridge was last reported at.
	TxHash bridgeservicetypes.Hash
	// BlockNum is the block number the bridge was last reported at.
	BlockNum uint64
	// BlockTimestamp is the block timestamp the bridge was last reported with (0 if the bridge
	// service had not populated it yet).
	BlockTimestamp uint64
	// DepositCount is the bridge's deposit count on NetworkID: the bridge service lists bridges by
	// deposit_count descending, so it tells a scanner how far down the list it must read to have
	// passed this bridge's position.
	DepositCount uint32
	// Final is whether the caller has seen this location final (see ActivityEntry.BridgeFinal): a
	// final bridge cannot be reorged out any more, so a scan can stop as soon as it reaches one
	// (every older bridge of the same network is final too) and never needs to read it again.
	Final bool
	// Source is which system supplied it the last time it was cached (see ActivitySourceKind).
	// A bridge-service scanner uses this to upgrade an entry it once had to report via the RPC
	// fallback: once the bridge service itself reports the same bridge, Source should become
	// ActivitySourceBridgeService and stay that way, even though nothing about the bridge's
	// on-chain location changed (see sources.scanBridgeService).
	Source ActivitySourceKind
	// NetworkID is the network whose bridge service reported it last time (see
	// ScannedBridge.NetworkID) — lets an implementation scanning one network at a time tell
	// which known entries are its own, e.g. to decide whether one of them has gone missing from
	// its own recent scan window (see ActivityBridgeScanner.BridgesFrom's invalidated return).
	NetworkID uint32
	// BlockPos is the position (log index) in its block the bridge was last reported at.
	BlockPos uint64
	// GlobalIndex, BridgeHash, Amount, DestinationNetwork and DestinationAddress are the contents
	// the bridge was last reported with (see SameBridge).
	GlobalIndex        bridgeservicetypes.BigIntString
	BridgeHash         bridgeservicetypes.Hash
	Amount             bridgeservicetypes.BigIntString
	DestinationNetwork uint32
	DestinationAddress bridgeservicetypes.Address
}

// Key is k's BridgeKey (see BridgeKey): its NetworkID and DepositCount
func (k KnownBridge) Key() BridgeKey {
	return BridgeKey{NetworkID: k.NetworkID, DepositCount: k.DepositCount}
}

// SameLocation reports whether b is still at the location k was cached at (see
// SameBridgeLocation for how block_timestamp is compared)
func (k KnownBridge) SameLocation(b *bridgeservicetypes.BridgeResponse) bool {
	if b == nil {
		return false
	}
	return sameLocation(k.TxHash, k.BlockNum, k.BlockTimestamp, b)
}

// SameBridge reports whether b is the very bridge event k was cached as: same location, block
// position and contents (see SameBridge)
func (k KnownBridge) SameBridge(b *bridgeservicetypes.BridgeResponse) bool {
	if b == nil {
		return false
	}
	cached := bridgeservicetypes.BridgeResponse{
		TxHash: k.TxHash, BlockNum: k.BlockNum, BlockTimestamp: k.BlockTimestamp, BlockPos: k.BlockPos,
		DepositCount: k.DepositCount, GlobalIndex: k.GlobalIndex, BridgeHash: k.BridgeHash, Amount: k.Amount,
		DestinationNetwork: k.DestinationNetwork, DestinationAddress: k.DestinationAddress,
	}
	return SameBridge(&cached, b)
}

// ActivityBridgeScanner is the driven port to the raw bridge-service data behind the
// GET /activity/from/{from_address} endpoint: it scans every bridge service the tracker knows
// about for bridges sent by fromAddress
type ActivityBridgeScanner interface {
	// BridgesFrom returns every bridge whose sender is fromAddress, across every configured
	// bridge service: every bridge not yet in known (a genuinely new one), every bridge in known
	// that is not final yet (KnownBridge.Final false), re-read with the data the bridge service
	// serves now — whether or not it changed, so the caller always works with the latest
	// block_finalized — and any bridge that is no longer the very event known has cached for it
	// (see KnownBridge.SameBridge): a reorg re-included the same deposit at a different block or
	// transaction, a different bridge took its deposit count, or a source now reports different
	// contents for it. The caller overwrites its entry with the returned data (a correction never
	// duplicates an entry: they share the BridgeKey).
	//
	// known is the caller's full set of already-cached bridges for fromAddress, keyed by BridgeKey
	// (the emitting network and deposit count — never the GlobalIndex, which is ambiguous under the
	// legacy pre-Etrog encoding, see BridgeKey). Each network's bridge service lists bridges by
	// deposit_count descending and finality is monotonic, so only the non-final head of that list
	// can still change: an implementation stops reading a network at the first bridge that is known
	// AND final (and past every non-final known bridge of that network), never reading deeper pages
	// again (see sources.ActivitySource).
	//
	// invalidated lists the BridgeKey of every entry in known that should be forgotten instead of
	// trusted: a non-final entry that the bridge service no longer reports although the scan went
	// past its position, or an entry found through the RPC fallback (Source == ActivitySourceRPC)
	// whose BlockNum still falls inside the range this call scanned via RPC for its own network
	// (see KnownBridge.NetworkID) yet no longer shows up there at all. Either way the deposit was
	// reorged out and nothing has re-included it (yet). A caller must remove these from its cache
	// rather than keep serving their last known (now unverified) data; if the bridge is
	// re-included later, at the same or a different block, it is discovered again as new. A
	// final entry is never invalidated.
	//
	// A bridge the bridge service lists always wins over a contradictory RPC observation of the
	// same BridgeKey (the bridge service is the source of record): the RPC one is logged as a
	// conflict and dropped. The RPC fallback only ever fills keys the bridge service does not
	// account for.
	//
	// A network whose bridge service cannot be scanned (unreachable, any transient error, or a
	// 503 while it resolves a reorg) does not fail the call and never invalidates anything of
	// that network: it is skipped and reported back as an ActivityWarning instead, its entries
	// kept as they are until a later call can scan it, so one misbehaving network never hides
	// every other network's activity.
	BridgesFrom(
		ctx context.Context, fromAddress common.Address, known map[BridgeKey]KnownBridge,
	) (found []*ScannedBridge, invalidated []BridgeKey, warnings []ActivityWarning, err error)
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
	// never directly by an HTTP handler, and never twice at once for the same fromAddress (the
	// plan a refresh applies is computed from the entries it read before scanning, see
	// PlanActivityRefresh, so overlapping refreshes of one address could apply a stale plan)
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

// ActivityRevisionReader is an optional capability of an ActivityQuerier (like
// ActivityTriggerable is for an ActivitySupervisedStore): it serves the address's revision, so
// the activity HTTP command can report it as api.ActivityResponse.Revision. A querier that does
// not implement it is served as EmptyActivityRevision.
//
// The revision is an opaque string, a hash of the address's activity (see ActivityRevision and
// HashActivityEntry for exactly what is hashed), not a counter: it changes whenever a bridge is
// added or removed, changes claim_status or is corrected after a reorg, wherever the entry sits
// in the sorted result, and it does NOT change when an entry only goes from not final to final
// (ActivityEntry.Finalized false -> true, claimed -> claimed_finalized). It lets a client that
// has loaded several pages detect a change beyond page 1 with one cheap request. It covers all
// the bridges of the address, independent of the filterBridges/page of the request. Being a pure
// function of the entries it needs no persistence: it is the same after a restart, after the
// address's cache is rebuilt and on every instance holding the same data. Consumers must compare
// it for equality only.
//
// Callers that also read the entries must call GetActivityRevision BEFORE
// ActivityQuerier.GetActivity: a refresh landing between the two calls then yields entries newer
// than the returned revision, so the client merely sees one more change on its next poll,
// instead of a revision newer than the data it holds (which would hide that change for good).
//
// Both implementations (bridgetracker.ActivityCache and the SQLite store) keep it up to date on
// every addition, removal and change of an entry, and give the same value for the same entries
type ActivityRevisionReader interface {
	// GetActivityRevision returns fromAddress's current revision: EmptyActivityRevision, not an
	// error, for an address with nothing cached
	GetActivityRevision(ctx context.Context, fromAddress common.Address) (string, error)
}

// ActivityRegistry is the full activity subsystem: state (ActivitySupervisedStore) plus reads
// (ActivityQuerier) — what the activity HTTP command depends on, mirroring SupervisedRegistry
type ActivityRegistry interface {
	ActivitySupervisedStore
	ActivityQuerier
}
