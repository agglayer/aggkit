package domain

import (
	"github.com/agglayer/aggkit/bridgetracker/types"
)

// BridgeInfo holds the immutable facts of a bridge, resolved once from its creation tx
type BridgeInfo struct {
	// NetworkID is the network the creating tx was sent to (origin network)
	NetworkID uint32
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
	return BridgeTypeFor(b.NetworkID, b.DestinationNetwork)
}
