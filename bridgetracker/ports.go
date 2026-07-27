package bridgetracker

import (
	"context"
	"errors"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// BridgeKey identifies a supervised bridge: the network the creating tx was sent to plus its hash
type BridgeKey = types.BridgeKey

// SupervisedStore is the driven port to the supervised-bridges state. The HTTP handlers use
// its read side (Register) and the tracking engine its write side (SetStatus / SetError).
//
// Implementations must be safe for concurrent use.
type SupervisedStore = types.SupervisedStore

// StatusNotifier is the driven port push consumers (the WebSocket handler) use to follow a
// supervised bridge.
//
// Implementations must deliver every SetStatus / SetError of the same bridge as a
// BridgeUpdate to all its active subscriptions: both ports are two views of one subsystem
// and are always implemented together (see SupervisedRegistry).
type StatusNotifier = types.StatusNotifier

// SupervisedRegistry is the full supervised-bridges subsystem: state plus change
// notification. The in-memory adapter (NewMemoryRegistry) implements it for a single
// instance; a shared-store adapter can replace it so several tracker instances behind a
// proxy answer for any registered tx (see the statefulness note in the API doc)
type SupervisedRegistry = types.SupervisedRegistry

// ErrBridgeTxNotFound is returned by BridgeEventSource.FindBridge when the transaction does
// not exist on the network or emitted no BridgeEvent. The engine turns it into the terminal
// 404 after EngineConfig.NotFoundAfter consecutive polls (the tx may not be mined yet)
var ErrBridgeTxNotFound = errors.New("bridge tx not found")

// BridgeInfo holds the immutable facts of a bridge, resolved once from its creation tx
type BridgeInfo struct {
	// Key is the supervised-bridge key (origin network + creating tx hash)
	Key BridgeKey
	// LeafType is the kind of leaf the bridge created (asset or message)
	LeafType types.BridgeLeafType
	// DestinationNetwork is the network the bridge exits to (0 -> Mainnet)
	DestinationNetwork uint32
	// DepositCount is the index of the bridge leaf in the origin exit tree
	DepositCount uint32
	// BlockNumber is the block, on the origin network, where the BridgeEvent was emitted
	BlockNumber uint64
	// LogIndex is the position of the BridgeEvent log within BlockNumber
	LogIndex uint32
}

// BridgeType derives the direction of the bridge from its origin and destination networks
func (b *BridgeInfo) BridgeType() types.BridgeType {
	return domain.BridgeTypeFor(b.Key.NetworkID, b.DestinationNetwork)
}

// BridgeEventSource is the driven port resolving the BridgeEvent behind a supervised tx on
// its origin network (RPC receipt or bridge service, resolved per network via the finder)
type BridgeEventSource interface {
	// FindBridge returns the facts of the bridge created by the tx, or ErrBridgeTxNotFound
	// if the tx does not exist / emitted no BridgeEvent. Other errors are transient
	FindBridge(ctx context.Context, networkID uint32, txHash common.Hash) (*BridgeInfo, error)
}

// CertificateSource is the driven port to the agglayer: which certificate includes a bridge
// and in which state it is
type CertificateSource interface {
	// CertificateFor returns the certificate that includes the bridge, or nil if the bridge
	// is not part of any certificate yet
	CertificateFor(ctx context.Context, bridge *BridgeInfo) (*types.CertificateData, error)
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
}

// LERSource is the driven port to the Local Exit Root state on an L2-originated bridge's
// origin network
type LERSource interface {
	// OriginLER returns the LER update on the origin L2 network that covers the bridge, or
	// nil if the bridge is not covered yet
	OriginLER(ctx context.Context, bridge *BridgeInfo) (*types.LERUpdateResult, error)
}

// ClaimSource is the driven port to the claim state of a bridge on its destination network
type ClaimSource interface {
	// ClaimFor returns the claim transaction of the bridge on the destination network, or
	// nil if it has not been claimed yet
	ClaimFor(ctx context.Context, bridge *BridgeInfo) (*types.ClaimResult, error)
}
