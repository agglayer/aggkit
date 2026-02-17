package types

import (
	"fmt"
	"math/big"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// LogQuery defines a query for logs
type LogQuery struct {
	Addrs      []common.Address
	BlockRange aggkitcommon.BlockRange
	// If BlockHash is set BlockRange contains the corresponding blockNumber
	BlockHash *common.Hash
}

// NewLogQuery creates a new LogQuery
func NewLogQuery(fromBlock uint64, toBlock uint64, addrs []common.Address) LogQuery {
	return LogQuery{
		Addrs:      addrs,
		BlockRange: aggkitcommon.NewBlockRange(fromBlock, toBlock),
	}
}

func NewLogQueryBlockHash(blockNumber uint64, blockHash common.Hash, addrs []common.Address) LogQuery {
	blockRange := aggkitcommon.BlockRangeZero
	if blockNumber != 0 {
		blockRange = aggkitcommon.NewBlockRange(blockNumber, blockNumber)
	}
	return LogQuery{
		Addrs:      addrs,
		BlockRange: blockRange,
		BlockHash:  &blockHash,
	}
}

// NewLogQueryFromEthereumFilter creates a new LogQuery from an Ethereum FilterQuery
func NewLogQueryFromEthereumFilter(query ethereum.FilterQuery) LogQuery {
	if query.BlockHash != nil {
		blockNumber := uint64(0)
		if query.FromBlock != nil {
			blockNumber = query.FromBlock.Uint64()
		}
		return NewLogQueryBlockHash(blockNumber, *query.BlockHash, query.Addresses)
	}
	if query.ToBlock == nil {
		panic("NewLogQueryFromEthereumFilter: unsupported nil ToBlock")
	}
	return NewLogQuery(query.FromBlock.Uint64(), query.ToBlock.Uint64(), query.Addresses)
}

// String returns a string representation of the LogQuery
func (l *LogQuery) String() string {
	if l == nil {
		return "LogQuery: <nil>"
	}
	if l.IsBlockHashQuery() {
		bn := " (?)"
		if !l.BlockRange.IsEmpty() {
			bn = fmt.Sprintf(" (%d)", l.BlockRange.FromBlock)
		}
		return fmt.Sprintf("LogQuery: addrs=%v, blockHash=%s%s", l.Addrs, l.BlockHash.String(), bn)
	}
	return fmt.Sprintf("LogQuery: addrs=%v, blockRange=%s", l.Addrs, l.BlockRange.String())
}
func (l *LogQuery) IsBlockHashQuery() bool {
	return l != nil && l.BlockHash != nil
}
func (l *LogQuery) IsBlockRangeQuery() bool {
	return l != nil && l.BlockHash == nil
}

// ToRPCFilterQuery converts the LogQuery to an Ethereum FilterQuery
func (l *LogQuery) ToRPCFilterQuery() ethereum.FilterQuery {
	if l.BlockHash != nil {
		return ethereum.FilterQuery{
			Addresses: l.Addrs,
			BlockHash: l.BlockHash,
		}
	}
	return ethereum.FilterQuery{
		Addresses: l.Addrs,
		FromBlock: new(big.Int).SetUint64(l.BlockRange.FromBlock),
		ToBlock:   new(big.Int).SetUint64(l.BlockRange.ToBlock),
	}
}
func (l *LogQuery) IsEmpty() bool {
	return l == nil || len(l.Addrs) == 0 && l.BlockRange.IsEmpty() &&
		l.BlockHash == nil
}

func (l *LogQuery) IsValid() bool {
	if l == nil {
		return true
	}
	if l.BlockHash != nil {
		return true
	}
	// We use value {0,0} to represent empty range in DB, so it's forbidden
	// to use the BlockRange(0,0) for multidownloader
	if !l.BlockRange.IsEmpty() && l.BlockRange.FromBlock == 0 && l.BlockRange.ToBlock == 0 {
		return false
	}
	return true
}
