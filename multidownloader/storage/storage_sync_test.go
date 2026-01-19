package storage

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func TestStorage_GetSyncedBlockRangePerContract(t *testing.T) {
	storage := newStorageForTest(t, nil)
	data, err := storage.GetSyncedBlockRangePerContract(nil)
	require.NoError(t, err)
	require.Equal(t, "SetSyncSegment: ", data.String())
}

func TestStorage_UpsertSyncerConfigs(t *testing.T) {
	storage := newStorageForTest(t, nil)
	configs := []mdrtypes.ContractConfig{
		{
			Address:   exampleAddr1,
			FromBlock: 1000,
			ToBlock:   aggkittypes.FinalizedBlock,
		},
		{
			Address:   exampleAddr2,
			FromBlock: 2000,
			ToBlock:   aggkittypes.LatestBlock,
		},
	}
	err := storage.UpsertSyncerConfigs(nil, configs)
	require.NoError(t, err)

	// Upsert again with different start block
	configsUpdated := []mdrtypes.ContractConfig{
		{
			Address:   exampleAddr1,
			FromBlock: 1300,
			ToBlock:   aggkittypes.FinalizedBlock,
		},
		{
			Address:   exampleAddr2,
			FromBlock: 1600,
			ToBlock:   aggkittypes.FinalizedBlock,
		},
	}
	err = storage.UpsertSyncerConfigs(nil, configsUpdated)
	require.NoError(t, err)

	syncSegments, err := storage.GetSyncedBlockRangePerContract(nil)
	require.NoError(t, err)
	require.Equal(t, 2, len(syncSegments.GetAddressesForBlockRange(
		aggkitcommon.NewBlockRange(0, 10000),
	)))
	seg1, exists := syncSegments.GetByContract(exampleAddr1)
	require.True(t, exists)
	require.Equal(t, aggkittypes.FinalizedBlock, seg1.TargetToBlock)
	require.Equal(t, aggkitcommon.BlockRangeZero, seg1.BlockRange)

	seg2, exists := syncSegments.GetByContract(exampleAddr2)
	require.True(t, exists)
	require.Equal(t, aggkittypes.FinalizedBlock, seg2.TargetToBlock)
}

func TestStorage_UpdateSyncedStatus(t *testing.T) {
	storage := newStorageForTest(t, nil)
	segments := []mdrtypes.SyncSegment{
		mdrtypes.NewSyncSegment(
			exampleAddr1,
			aggkitcommon.NewBlockRange(1000, 2000),
			aggkittypes.FinalizedBlock,
			true,
		),
		mdrtypes.NewSyncSegment(
			exampleAddr2,
			aggkitcommon.NewBlockRange(1500, 2500),
			aggkittypes.LatestBlock,
			false,
		),
	}
	err := storage.UpsertSyncerConfigs(nil, []mdrtypes.ContractConfig{
		{
			Address:   exampleAddr1,
			FromBlock: 1000,
			ToBlock:   aggkittypes.FinalizedBlock,
		},
		{
			Address:   exampleAddr2,
			FromBlock: 1500,
			ToBlock:   aggkittypes.LatestBlock,
		},
	})
	require.NoError(t, err)
	err = storage.UpdateSyncedStatus(nil, segments)
	require.NoError(t, err)

	syncedSegments, err := storage.GetSyncedBlockRangePerContract(nil)
	require.NoError(t, err)
	require.Equal(t, 2, len(syncedSegments.GetAddressesForBlockRange(
		aggkitcommon.NewBlockRange(0, 3000),
	)))
	seg1, exists := syncedSegments.GetByContract(exampleAddr1)
	require.True(t, exists)
	require.Equal(t, aggkitcommon.NewBlockRange(1000, 2000), seg1.BlockRange)
	require.Equal(t, aggkittypes.FinalizedBlock, seg1.TargetToBlock)

	seg2, exists := syncedSegments.GetByContract(exampleAddr2)
	require.True(t, exists)
	require.Equal(t, aggkitcommon.NewBlockRange(1500, 2500), seg2.BlockRange)
	require.Equal(t, aggkittypes.LatestBlock, seg2.TargetToBlock)
}
