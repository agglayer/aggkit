package types

import (
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type ReorgData struct {
	// ChainID is the id of the roerged chain stored on DB (incremental ID)
	ChainID uint64
	// BlockRangeAffected is the range of blocks affected by the reorg (from,to inclusive)
	BlockRangeAffected aggkitcommon.BlockRange
	// DetectedAtBlock is the block number where the reorg was detected
	DetectedAtBlock           uint64
	DetectedTimestamp         uint64
	NetworkLatestBlock        uint64
	NetworkFinalizedBlock     uint64
	NetworkFinalizedBlockName aggkittypes.BlockNumberFinality
	Description               string
}

func (r *ReorgData) String() string {
	return fmt.Sprintf("ReorgData{ChainID: %d, BlockRangeAffected: %s, DetectedAtBlock: %d, DetectedTimestamp: %d, "+
		"NetworkLatestBlock: %d, NetworkFinalizedBlock: %d (%s), Description: %s}",
		r.ChainID,
		r.BlockRangeAffected.String(),
		r.DetectedAtBlock,
		r.DetectedTimestamp,
		r.NetworkLatestBlock,
		r.NetworkFinalizedBlock,
		r.NetworkFinalizedBlockName.String(),
		r.Description)
}
