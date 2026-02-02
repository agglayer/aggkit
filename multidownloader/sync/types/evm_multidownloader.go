package types

import (
	"context"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

type MultidownloaderInterface interface {
	// CheckValidBlock checks if the given blockNumber and blockHash are still valid
	// returns: isValid bool, reorgChainID uint64, err error
	CheckValidBlock(ctx context.Context, blockNumber uint64,
		blockHash common.Hash) (bool, uint64, error)
	// GetReorgedDataByChainID retrieves the reorged data by chain ID
	GetReorgedDataByChainID(ctx context.Context, reorgedChainID uint64) (*mdrtypes.ReorgData, error)
	// IsAvailable checks if the logs for the given query are available
	IsAvailable(query mdrtypes.LogQuery) bool
	// IsPartiallyAvailable checks if the logs for the given query are partially available
	IsPartiallyAvailable(query mdrtypes.LogQuery) (bool, *mdrtypes.LogQuery)
	// GetEthLogs retrieves the logs for the given query
	LogQuery(ctx context.Context, query mdrtypes.LogQuery) (mdrtypes.LogQueryResponse, error)
	// Finality is which block to consider final (typically finalizedBlock)
	Finality() aggkittypes.BlockNumberFinality
	// HeaderByNumber gets the block header for the given block number finality
	HeaderByNumber(ctx context.Context,
		number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error)
	StorageHeaderByNumber(ctx context.Context,
		number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, mdrtypes.FinalizedType, error)
	// ChainID returns the chain ID of the EVM chain
	ChainID(ctx context.Context) (uint64, error)
}
