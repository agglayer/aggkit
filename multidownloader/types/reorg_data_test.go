package types

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func TestXxx(t *testing.T) {
	reorgData := &ReorgData{
		ChainID:                   1,
		BlockRangeAffected:        aggkitcommon.NewBlockRange(100, 200),
		DetectedAtBlock:           250,
		DetectedTimestamp:         1620000000,
		NetworkLatestBlock:        300,
		NetworkFinalizedBlock:     240,
		NetworkFinalizedBlockName: aggkittypes.LatestBlock,
	}
	require.Equal(t, "ReorgData{ChainID: 1, BlockRangeAffected: From: 100, To: 200 (101), "+
		"DetectedAtBlock: 250, DetectedTimestamp: 1620000000, NetworkLatestBlock: 300, NetworkFinalizedBlock: 240 (LatestBlock)}",
		reorgData.String())
}
