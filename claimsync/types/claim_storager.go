package types

import (
	"context"
	"math/big"

	"github.com/agglayer/aggkit/db/compatibility"
	dbtypes "github.com/agglayer/aggkit/db/types"
	aggsync "github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
)

// Storage defines the interface for claim storage operations.
// Each method accepts an optional tx dbtypes.Querier; pass nil to use the default DB connection.
type ClaimStorager interface {
	// InsertBlock records a block so claims can reference it via foreign key
	InsertBlock(ctx context.Context, tx dbtypes.Querier, blockNum uint64, blockHash common.Hash) error
	// InsertClaim persists a single claim record
	InsertClaim(ctx context.Context, tx dbtypes.Querier, claim Claim) error
	// InsertUnsetClaim persists an unset claim record
	InsertUnsetClaim(ctx context.Context, tx dbtypes.Querier, u UnsetClaim) error
	// InsertSetClaim persists a set claim record
	InsertSetClaim(ctx context.Context, tx dbtypes.Querier, s SetClaim) error
	// GetClaims returns claims in [fromBlock, toBlock] using compaction logic
	GetClaims(ctx context.Context, tx dbtypes.Querier, fromBlock, toBlock uint64) ([]Claim, error)
	// GetClaimsByGlobalIndex returns claims for the given global index using compaction logic
	GetClaimsByGlobalIndex(ctx context.Context, tx dbtypes.Querier, globalIndex *big.Int) ([]Claim, error)
	// GetFirstProcessedBlock returns the lowest block number stored if any.
	// Returns (0, false, nil) if there are no blocks.
	GetFirstProcessedBlock(ctx context.Context, tx dbtypes.Querier) (uint64, bool, error)
	// GetLastProcessedBlock returns the highest block number stored if any
	// it returns:
	// - the highest block number stored, or 0 if there are no blocks
	// - a boolean indicating whether a block was found (false if there are no blocks)
	// - error if the operation failed, or nil if successful
	GetLastProcessedBlock(ctx context.Context, tx dbtypes.Querier) (uint64, bool, error)
	// GetBoundaryBlockForClaimType returns the max block_num for claims of the given type
	GetBoundaryBlockForClaimType(ctx context.Context, tx dbtypes.Querier, claimType ClaimType) (uint64, error)
	// DeleteBlocksFrom deletes all blocks with num >= firstBlock (cascade-deletes claims etc.)
	DeleteBlocksFrom(ctx context.Context, tx dbtypes.Querier, firstBlock uint64) (int64, error)
	// GetClaimsByGER returns all DetailedClaimEvent claims with the given global exit root
	GetClaimsByGER(ctx context.Context, tx dbtypes.Querier, globalExitRoot common.Hash) ([]*Claim, error)
	// GetClaimsPaged returns claims for the given page parameters and filters,
	// it returns:
	// - the list of claims for the requested page
	// - the total count of claims matching the filters (ignoring pagination)
	// - error if the operation failed, or nil if successful
	GetClaimsPaged(
		ctx context.Context, pageNumber, pageSize uint32, networkIDs []uint32, globalIndex *big.Int,
	) ([]*Claim, int, error)
	// GetSetClaimsPaged returns set claims for the given page parameters and filters,
	// it returns:
	// - the list of set claims for the requested page
	// - the total count of set claims matching the filters (ignoring pagination)
	// - error if the operation failed, or nil if successful
	GetSetClaimsPaged(
		ctx context.Context, pageNumber, pageSize uint32,
		globalIndex *big.Int,
	) ([]*SetClaim, int, error)
	// GetUnsetClaimsPaged returns unset claims for the given page parameters and filters,
	// it returns:
	// - the list of unset claims for the requested page
	// - the total count of unset claims matching the filters (ignoring pagination)
	// - error if the operation failed, or nil if successful
	GetUnsetClaimsPaged(
		ctx context.Context, pageNumber, pageSize uint32,
		globalIndex *big.Int,
	) ([]*UnsetClaim, int, error)

	// NewTx begins a new database transaction.
	NewTx(ctx context.Context) (dbtypes.Txer, error)
	compatibility.CompatibilityDataStorager[aggsync.RuntimeData]
}
