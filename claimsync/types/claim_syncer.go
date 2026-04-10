package types

import (
	"context"
	"math/big"

	aggkittypes "github.com/agglayer/aggkit/types"
)

type ClaimSyncer interface {
	OriginNetwork() uint32
	// GetLastProcessedBlock is deprecated in favour GetProcessedBlockRange
	GetLastProcessedBlock(ctx context.Context) (uint64, bool, error)
	// GetStatus(ctx context.Context) (Status, error)
	// SetNextRequiredBlock sets the next required block number. It is used by aggsender that
	// set the next required block to the next one from the previous settled certificate
	// If the syncer have no block yet is going to use this as starting point
	// If the syncer have any block check that the `blockNumber`is higher than the first synced block
	SetNextRequiredBlock(ctx context.Context, blockNumber uint64) error

	GetClaims(ctx context.Context, fromBlock, toBlock uint64) ([]Claim, error)
	GetClaimsByGlobalIndex(ctx context.Context, globalIndex *big.Int) ([]Claim, error)
	GetLatestBlockNumByGlobalIndexFromRPC(
		ctx context.Context, globalIndex *big.Int, toBlock *aggkittypes.BlockNumberFinality,
	) (uint64, bool, error)
}
