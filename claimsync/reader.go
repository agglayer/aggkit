package claimsync

import (
	"math/big"

	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	dbtypes "github.com/agglayer/aggkit/db/types"
)

var _ claimsynctypes.ClaimsReader = (*processorReader)(nil)

type processorReader struct {
	storage claimsynctypes.ClaimStorager
	log     aggkitcommon.Logger
}

func NewProcessorReader(logger aggkitcommon.Logger, storage claimsynctypes.ClaimStorager) *processorReader {
	return &processorReader{
		storage: storage,
		log:     logger,
	}
}

// GetLastProcessedBlock returns the highest block number stored.
func (p *processorReader) GetLastProcessedBlock(tx dbtypes.Querier) (uint64, error) {
	return p.storage.GetLastProcessedBlock(tx)
}

// GetBoundaryBlockForClaimType returns the max block_num for claims of the given type.
// Returns db.ErrNotFound if no claims of that type exist.
func (p *processorReader) GetBoundaryBlockForClaimType(tx dbtypes.Querier, claimType bridgesync.ClaimType) (uint64, error) {
	return p.storage.GetBoundaryBlockForClaimType(tx, claimType)
}

// GetClaims returns claims in [fromBlock, toBlock] using compaction logic.
func (p *processorReader) GetClaims(tx dbtypes.Querier, fromBlock, toBlock uint64) ([]bridgesync.Claim, error) {
	return p.storage.GetClaims(tx, fromBlock, toBlock)
}

// GetClaimsByGlobalIndex returns claims for the given global index using compaction logic.
func (p *processorReader) GetClaimsByGlobalIndex(tx dbtypes.Querier, globalIndex *big.Int) ([]bridgesync.Claim, error) {
	return p.storage.GetClaimsByGlobalIndex(tx, globalIndex)
}
