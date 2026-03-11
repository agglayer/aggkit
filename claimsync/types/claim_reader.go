package types

import (
	"math/big"

	"github.com/agglayer/aggkit/bridgesync"
	dbtypes "github.com/agglayer/aggkit/db/types"
)

// ClaimsReader provides read-only access
type ClaimsReader interface {
	GetLastProcessedBlock(tx dbtypes.Querier) (uint64, error)
	GetBoundaryBlockForClaimType(tx dbtypes.Querier, claimType bridgesync.ClaimType) (uint64, error)
	GetClaims(tx dbtypes.Querier, fromBlock, toBlock uint64) ([]bridgesync.Claim, error)
	GetClaimsByGlobalIndex(tx dbtypes.Querier, globalIndex *big.Int) ([]bridgesync.Claim, error)
}
