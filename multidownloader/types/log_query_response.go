package types

import (
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

type Log struct {
	// Consensus fields:
	// address of the contract that generated the event
	Address common.Address `json:"address" gencodec:"required"`
	// list of topics provided by the contract.
	Topics []common.Hash `json:"topics" gencodec:"required"`
	// supplied by the contract, usually ABI-encoded
	Data []byte `json:"data" gencodec:"required"`

	// Derived fields. These fields are filled in by the node
	// but not secured by consensus.
	// block in which the transaction was included
	BlockNumber uint64 `json:"blockNumber" rlp:"-"`
	// hash of the transaction
	TxHash common.Hash `json:"transactionHash" gencodec:"required" rlp:"-"`
	// index of the transaction in the block
	TxIndex uint `json:"transactionIndex" rlp:"-"`
	// timestamp of the block in which the transaction was included
	BlockTimestamp uint64 `json:"blockTimestamp" rlp:"-"`
	// index of the log in the block
	Index uint `json:"logIndex" rlp:"-"`

	// The Removed field is true if this log was reverted due to a chain reorganisation.
	// You must pay attention to this field if you receive logs through a filter query.
	Removed bool `json:"removed" rlp:"-"`
}

type BlockWithLogs struct {
	Header  aggkittypes.BlockHeader
	IsFinal bool
	Logs    []Log
}

type LogQueryResponse struct {
	Blocks []BlockWithLogs
	// ResponseRange indicates the block range covered by the response, even if blocks are empty
	ResponseRange aggkitcommon.BlockRange
	// UnsafeRange indicates the block range that are in unsafe zone (not finalized)
	UnsafeRange aggkitcommon.BlockRange
}

func (lqr *LogQueryResponse) CountLogs() int {
	if lqr == nil {
		return 0
	}
	count := 0
	for _, block := range lqr.Blocks {
		count += len(block.Logs)
	}
	return count
}
