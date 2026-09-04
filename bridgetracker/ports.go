package bridgetracker

import (
	"context"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// TrackingID identifies a supervised bridge: the network the creating tx was sent to plus its hash
type TrackingID = domain.TrackingID

// SupervisedStore is the driven port to the supervised-bridges state. The HTTP handlers use
// its read side (Register) and the tracking engine its write side (SetStatus / SetError).
//
// Implementations must be safe for concurrent use.
type SupervisedStore = domain.SupervisedStore

// StatusNotifier is the driven port push consumers (the WebSocket handler) use to follow a
// supervised bridge.
//
// Implementations must deliver every SetStatus / SetError of the same bridge as a
// TrackingData snapshot to all its active subscriptions: both ports are two views of one
// subsystem and are always implemented together (see SupervisedRegistry).
type StatusNotifier = domain.StatusNotifier

// SupervisedRegistry is the full supervised-bridges subsystem: state plus change
// notification. The in-memory adapter (NewMemoryRegistry) implements it for a single
// instance; a shared-store adapter can replace it so several tracker instances behind a
// proxy answer for any registered tx (see the statefulness note in the API doc)
type SupervisedRegistry = domain.SupervisedRegistry

// ErrBridgeTxNotFound is returned by BridgeEventSource.FindBridge when the transaction does
// not exist on the network yet. domain.ResolveBridgeTx keeps retrying for up to EngineConfig.
// UnresolvedTimeout (the tx may simply not be mined yet) before marking the bridge as
// terminally failed
var ErrBridgeTxNotFound = domain.ErrBridgeTxNotFound

// ErrBridgeTxNotABridge is returned by BridgeEventSource.FindBridge when the transaction
// exists but is definitely not a bridge transaction (reverted, or mined without emitting a
// BridgeEvent log): unlike ErrBridgeTxNotFound, retrying cannot change this, so it is wrapped
// as domain.Permanent — the tracker marks the bridge as terminally failed immediately, with no
// retries
var ErrBridgeTxNotABridge = domain.ErrBridgeTxNotABridge

// ErrSourceUnavailable is returned by BridgeEventSource.FindBridge when the bridge's origin
// network has no source configured to resolve it (e.g. no JSON-RPC client for a
// statically-configured network): like ErrBridgeTxNotABridge, this is a permanent condition
// that retrying cannot change, so it is wrapped as domain.Permanent too
var ErrSourceUnavailable = domain.ErrSourceUnavailable

// BridgeInfo holds the immutable facts of a bridge, resolved once from its creation tx
type BridgeInfo = domain.BridgeInfo

// BridgeStepPath is the domain-internal representation of one step of the expected path of a
// bridge; see api.BridgeStepPath for the wire shape published to clients
type BridgeStepPath = domain.BridgeStepPath

// BridgeEventSource is the driven port resolving the BridgeEvent behind a supervised tx on
// its origin network (RPC receipt or bridge service, resolved per network via the finder).
// domain.ResolveBridgeTx calls it directly — see its doc
type BridgeEventSource = domain.BridgeEventSource

// CertificateSource is the driven port to the agglayer: which certificate includes a bridge
// and in which state it is
type CertificateSource interface {
	// CertificateFor returns the certificate that includes the bridge, or nil if the bridge
	// is not part of any certificate yet
	CertificateFor(ctx context.Context, bridge *BridgeInfo) (*types.CertificateInclusionData, error)
}

// GERSource is the driven port to the Global Exit Root state on both sides of a bridge
type GERSource interface {
	// OriginGER returns the GER update on the origin network that covers the bridge, or nil
	// if the bridge is not covered by any GER update yet. Only queried for L1-originated
	// bridges
	OriginGER(ctx context.Context, bridge *BridgeInfo) (*types.GERData, error)

	// InjectedGER returns the GER injected on the destination network that covers the
	// bridge, or nil if no covering GER has been injected yet. Only meaningful when the
	// destination is an L2 (injection does not apply to Mainnet)
	InjectedGER(ctx context.Context, bridge *BridgeInfo) (*types.GERData, error)

	// L1InfoTreeIndexForGER resolves the L1 info tree leaf index ger (produced by the bridge's
	// certificate settlement, see types.L1SettledGERResult) landed at, or nil if the L1 info
	// tree has not caught up with it yet. Only queried by StepWaitL1SettledGER, and only when
	// the settlement tx did not emit UpdateL1InfoTreeV2 (which already carries the index)
	L1InfoTreeIndexForGER(ctx context.Context, bridge *BridgeInfo, ger common.Hash) (*uint32, error)

	// InjectedGERAtIndex returns the GER injected at leafIndex on the bridge's destination
	// network, or nil if it has not been injected yet. Only queried for L2-originated bridges
	// arriving at an L2, right after StepWaitL1SettledGER
	InjectedGERAtIndex(ctx context.Context, bridge *BridgeInfo, leafIndex uint32) (*types.GERData, error)

	// L1InfoTreeIndexForBridge returns the L1 info tree leaf index covering bridge's origin
	// deposit — GET /bridge/v1/l1-info-tree-index, queried against the bridge-service instance
	// that will build the claim proof for it (the origin network's own instance) — or nil if
	// that instance's own L1 info tree sync has not caught up to this deposit yet. Only queried
	// by StepWaitingL1InfoLeafAvailable
	L1InfoTreeIndexForBridge(ctx context.Context, bridge *BridgeInfo) (*uint32, error)
}

// LERSource is the driven port to the Local Exit Root state on an L2-originated bridge's
// origin network
type LERSource interface {
	// OriginLER returns the LER update on the origin L2 network that covers the bridge, or
	// nil if the bridge is not covered yet
	OriginLER(ctx context.Context, bridge *BridgeInfo) (*types.LERUpdateResult, error)
}

// ClaimChecker is the driven port to whether a bridge has been claimed on its destination
// network, per isClaimed() on the destination bridge contract — the on-chain source of truth
// StepWaitingClaim resolves against
type ClaimChecker interface {
	// IsClaimed reports whether bridge has already been claimed on its destination network
	IsClaimed(ctx context.Context, bridge *BridgeInfo) (bool, error)
}

// ClaimSource is the driven port to the claim record of a bridge on its destination network
type ClaimSource interface {
	// ClaimFor returns the claim transaction of the bridge on the destination network, or
	// nil if the destination network's bridge service has not indexed it yet
	ClaimFor(ctx context.Context, bridge *BridgeInfo) (*types.ClaimResult, error)
}

// ActivityEntry is one bridge found for a from_address by the GET /activity/from/{from_address}
// endpoint, enriched with its claim/tracking state; see domain.ActivityEntry
type ActivityEntry = domain.ActivityEntry

// ActivityBridgeScanner is the driven port to the raw bridge-service data behind the activity
// endpoint: every bridge sent by a given address, across every configured bridge service
type ActivityBridgeScanner = domain.ActivityBridgeScanner

// ActivityClaimChecker is the driven port to a bridge's claim state on its destination network
type ActivityClaimChecker = domain.ActivityClaimChecker

// ActivityQuerier is the driven port the activity endpoint depends on
type ActivityQuerier = domain.ActivityQuerier

// BridgeAddressResolver is the driven port the bridge-address endpoint depends on: it resolves
// the bridge contract address for one network, or enumerates every network currently known
type BridgeAddressResolver = domain.BridgeAddressResolver

// SettlementSource is the driven port to the L1 evidence a certificate's settlement produces:
// the RollupManager/GlobalExitRoot events (VerifyBatchesTrustedAggregator, UpdateL1InfoTree[V2])
// emitted by the settlement tx itself
type SettlementSource interface {
	// SettlementGERUpdate returns the evidence read off settlementTxHash's L1 receipt once it
	// reaches the configured L1 finality, or nil if it is not there yet
	SettlementGERUpdate(
		ctx context.Context, bridge *BridgeInfo, settlementTxHash common.Hash,
	) (*types.L1SettledGERResult, error)
}
