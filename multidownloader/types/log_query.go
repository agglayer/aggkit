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
}

// NewLogQuery creates a new LogQuery
func NewLogQuery(fromBlock uint64, toBlock uint64, addrs []common.Address) LogQuery {
	return LogQuery{
		Addrs:      addrs,
		BlockRange: aggkitcommon.NewBlockRange(fromBlock, toBlock),
	}
}

// NewLogQueryFromEthereumFilter creates a new LogQuery from an Ethereum FilterQuery
func NewLogQueryFromEthereumFilter(query ethereum.FilterQuery) LogQuery {
	return LogQuery{
		Addrs:      query.Addresses,
		BlockRange: aggkitcommon.NewBlockRange(query.FromBlock.Uint64(), query.ToBlock.Uint64()),
	}
}

// String returns a string representation of the LogQuery
func (l *LogQuery) String() string {
	if l == nil {
		return "LogQuery: <nil>"
	}
	return fmt.Sprintf("LogQuery: addrs=%v, blockRange=%s", l.Addrs, l.BlockRange.String())
}

// ToRPCFilterQuery converts the LogQuery to an Ethereum FilterQuery
func (l *LogQuery) ToRPCFilterQuery() ethereum.FilterQuery {
	return ethereum.FilterQuery{
		Addresses: l.Addrs,
		FromBlock: new(big.Int).SetUint64(l.BlockRange.FromBlock),
		ToBlock:   new(big.Int).SetUint64(l.BlockRange.ToBlock),
	}
}
