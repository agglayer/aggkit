package types

import (
	"context"
	"math/big"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/db/compatibility"
	dbtypes "github.com/agglayer/aggkit/db/types"
	aggsync "github.com/agglayer/aggkit/sync"
)

// Storage defines the interface for claim storage operations.
// Each method accepts an optional tx dbtypes.Querier; pass nil to use the default DB connection.
type ClaimStorager interface {
	// InsertBlock records a block so claims can reference it via foreign key
	InsertBlock(tx dbtypes.Querier, blockNum uint64, blockHash string) error
	// InsertClaim persists a single claim record
	InsertClaim(tx dbtypes.Querier, claim bridgesync.Claim) error
	// InsertUnsetClaim persists an unset claim record
	InsertUnsetClaim(tx dbtypes.Querier, u bridgesync.UnsetClaim) error
	// InsertSetClaim persists a set claim record
	InsertSetClaim(tx dbtypes.Querier, s bridgesync.SetClaim) error
	// GetClaims returns claims in [fromBlock, toBlock] using compaction logic
	GetClaims(tx dbtypes.Querier, fromBlock, toBlock uint64) ([]bridgesync.Claim, error)
	// GetClaimsByGlobalIndex returns claims for the given global index using compaction logic
	GetClaimsByGlobalIndex(tx dbtypes.Querier, globalIndex *big.Int) ([]bridgesync.Claim, error)
	// GetLastProcessedBlock returns the highest block number stored
	GetLastProcessedBlock(tx dbtypes.Querier) (uint64, error)
	// GetBoundaryBlockForClaimType returns the max block_num for claims of the given type
	GetBoundaryBlockForClaimType(tx dbtypes.Querier, claimType bridgesync.ClaimType) (uint64, error)
	// DeleteBlocksFrom deletes all blocks with num >= firstBlock (cascade-deletes claims etc.)
	DeleteBlocksFrom(tx dbtypes.Querier, firstBlock uint64) (int64, error)
	// NewTx begins a new database transaction.
	NewTx(ctx context.Context) (dbtypes.Txer, error)
	compatibility.CompatibilityDataStorager[aggsync.RuntimeData]
}
