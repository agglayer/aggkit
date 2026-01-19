package multidownloader

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestStateInitial(t *testing.T) {
	addr1 := common.HexToAddress("0x10")
	addr2 := common.HexToAddress("0x20")
	storageData := mdtypes.NewSetSyncSegment()
	storageData.Add(mdtypes.NewSyncSegment(addr1,
		aggkitcommon.BlockRangeZero, aggkittypes.FinalizedBlock,
		false))
	storageData.Add(mdtypes.NewSyncSegment(addr2,
		aggkitcommon.BlockRangeZero, aggkittypes.LatestBlock,
		false))
	configData := mdtypes.NewSetSyncSegment()
	segment1 := mdtypes.NewSyncSegment(addr1,
		aggkitcommon.NewBlockRange(0, 1000), aggkittypes.FinalizedBlock,
		false)
	segment2 := mdtypes.NewSyncSegment(addr2,
		aggkitcommon.NewBlockRange(0, 2000), aggkittypes.LatestBlock,
		false)
	configData.Add(segment1)
	configData.Add(segment2)

	state, err := NewStateFromStorageSyncedBlocks(storageData, configData)
	require.NoError(t, err)
	require.NotNil(t, state)
	logQuery := mdtypes.NewLogQuery(
		1, 456, []common.Address{addr1})

	err = state.OnNewSyncedLogQuery(&logQuery)
	require.NoError(t, err)
	pendingSegments := state.SyncedSegmentsByContract([]common.Address{addr1})
	require.Equal(t, 1, len(pendingSegments))
	require.Equal(t, addr1, pendingSegments[0].ContractAddr)
	require.Equal(t, aggkitcommon.NewBlockRange(0, 456), pendingSegments[0].BlockRange)
	require.Equal(t, aggkittypes.FinalizedBlock, pendingSegments[0].TargetToBlock)
}
