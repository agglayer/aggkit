package bridgesync

import (
	"fmt"
	"math/big"

	aggkitabi "github.com/agglayer/aggkit/abi"
	"github.com/ethereum/go-ethereum/common"
)

// LeafData represents the data structure of a leaf in the local exit tree
// used in ForwardLET events.
type LeafData struct {
	LeafType           uint8          `abi:"leafType"`
	OriginNetwork      uint32         `abi:"originNetwork"`
	OriginAddress      common.Address `abi:"originAddress"`
	DestinationNetwork uint32         `abi:"destinationNetwork"`
	DestinationAddress common.Address `abi:"destinationAddress"`
	Amount             *big.Int       `abi:"amount"`
	Metadata           []byte         `abi:"metadata"`
}

// String returns a string representation of the LeafData
func (l LeafData) String() string {
	return fmt.Sprintf("LeafData{LeafType: %d, OriginNetwork: %d, OriginAddress: %s, "+
		"DestinationNetwork: %d, DestinationAddress: %s, Amount: %s, Metadata: %x}",
		l.LeafType,
		l.OriginNetwork,
		l.OriginAddress.Hex(),
		l.DestinationNetwork,
		l.DestinationAddress.Hex(),
		l.Amount.String(),
		l.Metadata,
	)
}

// ToBridge converts the LeafData to a Bridge structure
func (l LeafData) ToBridge(
	blockNum, blockPos, blockTimestamp uint64,
	depositCount uint32,
	txnHash common.Hash,
	txnSender common.Address,
	fromAddr *common.Address) Bridge {
	return Bridge{
		BlockNum:           blockNum,
		BlockPos:           blockPos,
		BlockTimestamp:     blockTimestamp,
		DepositCount:       depositCount,
		TxHash:             txnHash,
		FromAddress:        fromAddr,
		TxnSender:          txnSender,
		LeafType:           l.LeafType,
		OriginNetwork:      l.OriginNetwork,
		OriginAddress:      l.OriginAddress,
		DestinationNetwork: l.DestinationNetwork,
		DestinationAddress: l.DestinationAddress,
		Amount:             l.Amount,
		Metadata:           l.Metadata,
		Source:             BridgeSourceForwardLET, // this leaf comes from ForwardLET event
	}
}

// decodeForwardLETLeaves decodes the newLeaves bytes from a ForwardLET event
func decodeForwardLETLeaves(newLeavesBytes []byte) ([]LeafData, error) {
	return aggkitabi.DecodeABIEncodedStructArray[LeafData](newLeavesBytes)
}
