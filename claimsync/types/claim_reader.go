package types

import (
	"context"
	"math/big"

	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/ethereum/go-ethereum/common"
)

// ClaimsReader provides read-only access
type ClaimsReader interface {
	GetLastProcessedBlock(ctx context.Context, tx dbtypes.Querier) (uint64, bool, error)
	GetBoundaryBlockForClaimType(ctx context.Context, tx dbtypes.Querier, claimType ClaimType) (uint64, error)
	GetClaims(ctx context.Context, tx dbtypes.Querier, fromBlock, toBlock uint64) ([]Claim, error)
	GetClaimsByGlobalIndex(ctx context.Context, tx dbtypes.Querier, globalIndex *big.Int) ([]Claim, error)
	GetClaimsByGER(ctx context.Context, tx dbtypes.Querier, globalExitRoot common.Hash) ([]*Claim, error)
	GetClaimsPaged(
		ctx context.Context, pageNumber, pageSize uint32,
		networkIDs []uint32, globalIndex *big.Int,
	) ([]*Claim, int, error)
	GetSetClaimsPaged(
		ctx context.Context, pageNumber, pageSize uint32,
		globalIndex *big.Int,
	) ([]*SetClaim, int, error)
	GetUnsetClaimsPaged(
		ctx context.Context, pageNumber, pageSize uint32,
		globalIndex *big.Int,
	) ([]*UnsetClaim, int, error)
}
