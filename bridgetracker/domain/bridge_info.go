package domain

import (
	"math/big"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
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
	// BlockHash is the hash of BlockNumber at the moment the BridgeEvent was captured. A
	// persisted store (see bridgetracker.sqliteRegistry) can compare it against the origin
	// chain's current hash at BlockNumber to tell whether this Info was resolved on a block
	// since reorged out, and must be discarded and re-resolved
	BlockHash common.Hash
	// LogIndex is the position of the BridgeEvent log within BlockNumber
	LogIndex uint32
	// OriginNetwork is the network where the bridged asset originates from
	OriginNetwork uint32
	// OriginAddress is the address of the asset on the origin network
	OriginAddress common.Address
	// DestinationAddress is the address that receives the asset on the destination network
	DestinationAddress common.Address
	// Amount is the amount of the asset being bridged
	Amount *big.Int
	// BlockTimestamp is the timestamp of the block, on the origin network, where the BridgeEvent was emitted
	BlockTimestamp uint64
	// BlockFinalized is whether BlockNumber is final on the origin network (it can no longer be
	// removed by a reorg). Unlike every other field here it is not an immutable fact resolved
	// once: it starts false and only the tracker flips it to true later (agglayer/aggkit#1970) —
	// until that follow-up lands it is always false. Served as BridgeStatus.block_finalized
	BlockFinalized bool
}

// BridgeType derives the direction of the bridge from its origin and destination networks
func (b *BridgeInfo) BridgeType() types.BridgeType {
	return BridgeTypeFor(b.NetworkID, b.DestinationNetwork)
}
