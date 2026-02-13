package multidownloader

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestStateInitial(t *testing.T) {
	addr1 := common.HexToAddress("0x10")
	addr2 := common.HexToAddress("0x20")
	storageData := mdrtypes.NewSetSyncSegment()
	storageData.Add(mdrtypes.NewSyncSegment(addr1,
		aggkitcommon.BlockRangeZero, aggkittypes.FinalizedBlock,
		false))
	storageData.Add(mdrtypes.NewSyncSegment(addr2,
		aggkitcommon.BlockRangeZero, aggkittypes.LatestBlock,
		false))
	configData := mdrtypes.NewSetSyncSegment()
	segment1 := mdrtypes.NewSyncSegment(addr1,
		aggkitcommon.NewBlockRange(0, 1000), aggkittypes.FinalizedBlock,
		false)
	segment2 := mdrtypes.NewSyncSegment(addr2,
		aggkitcommon.NewBlockRange(0, 2000), aggkittypes.LatestBlock,
		false)
	configData.Add(segment1)
	configData.Add(segment2)

	state, err := NewStateFromStorageSyncedBlocks(storageData, configData)
	require.NoError(t, err)
	require.NotNil(t, state)
	logQuery := mdrtypes.NewLogQuery(
		0, 456, []common.Address{addr1})

	err = state.OnNewSyncedLogQuery(&logQuery)
	require.NoError(t, err)
	pendingSegments := state.SyncedSegmentsByContract([]common.Address{addr1})
	require.Equal(t, 1, len(pendingSegments))
	require.Equal(t, addr1, pendingSegments[0].ContractAddr)
	require.Equal(t, aggkitcommon.NewBlockRange(0, 456), pendingSegments[0].BlockRange)
	require.Equal(t, aggkittypes.FinalizedBlock, pendingSegments[0].TargetToBlock)
}

func TestState_OnNewSyncedLogQuery(t *testing.T) {
	t.Run("nil state", func(t *testing.T) {
		var state *State
		logQuery := mdrtypes.NewLogQuery(1, 10, []common.Address{common.HexToAddress("0x1")})
		err := state.OnNewSyncedLogQuery(&logQuery)
		require.Error(t, err)
		require.Contains(t, err.Error(), "state is nil")
	})

	t.Run("nil logQuery", func(t *testing.T) {
		state := NewEmptyState()
		err := state.OnNewSyncedLogQuery(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "logQuery is nil")
	})

	t.Run("successful sync", func(t *testing.T) {
		addr1 := common.HexToAddress("0x100")

		syncedSet := mdrtypes.NewSetSyncSegment()
		syncedSet.Add(mdrtypes.NewSyncSegment(addr1,
			aggkitcommon.NewBlockRange(1, 100),
			aggkittypes.FinalizedBlock,
			false))

		pendingSet := mdrtypes.NewSetSyncSegment()
		pendingSet.Add(mdrtypes.NewSyncSegment(addr1,
			aggkitcommon.NewBlockRange(101, 200),
			aggkittypes.LatestBlock,
			false))

		state := NewState(&syncedSet, &pendingSet)

		// Get counts before
		syncedBefore := state.SyncedSegmentsByContract([]common.Address{addr1})
		pendingBefore := state.TotalBlocksPendingToSync()

		require.Equal(t, 1, len(syncedBefore))
		require.Equal(t, aggkitcommon.NewBlockRange(1, 100), syncedBefore[0].BlockRange)
		require.Equal(t, uint64(100), pendingBefore)

		// Sync blocks 101-150
		logQuery := mdrtypes.NewLogQuery(101, 150, []common.Address{addr1})
		err := state.OnNewSyncedLogQuery(&logQuery)
		require.NoError(t, err)

		// Verify synced was extended
		syncedAfter := state.SyncedSegmentsByContract([]common.Address{addr1})
		require.Equal(t, 1, len(syncedAfter))
		require.Equal(t, aggkitcommon.NewBlockRange(1, 150), syncedAfter[0].BlockRange)

		// Verify pending was reduced
		pendingAfter := state.TotalBlocksPendingToSync()
		require.Equal(t, uint64(50), pendingAfter) // 151-200 = 50 blocks
	})

	t.Run("transactional behavior - state unchanged on error", func(t *testing.T) {
		addr1 := common.HexToAddress("0x100")

		syncedSet := mdrtypes.NewSetSyncSegment()
		syncedSet.Add(mdrtypes.NewSyncSegment(addr1,
			aggkitcommon.NewBlockRange(1, 100),
			aggkittypes.FinalizedBlock,
			false))

		pendingSet := mdrtypes.NewSetSyncSegment()
		pendingSet.Add(mdrtypes.NewSyncSegment(addr1,
			aggkitcommon.NewBlockRange(101, 1000),
			aggkittypes.LatestBlock,
			false))

		state := NewState(&syncedSet, &pendingSet)

		// Get state before
		syncedBefore := state.SyncedSegmentsByContract([]common.Address{addr1})
		pendingBefore := state.TotalBlocksPendingToSync()
		syncedCountBefore := len(syncedBefore)

		// Try to sync a range in the middle (500-600) which would split the pending segment
		// This should fail with "cannot split segment" error
		logQuery := mdrtypes.NewLogQuery(500, 600, []common.Address{addr1})
		err := state.OnNewSyncedLogQuery(&logQuery)

		// Should fail because it would split the segment into two parts
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot split segment")

		// Verify state is unchanged
		syncedAfter := state.SyncedSegmentsByContract([]common.Address{addr1})
		pendingAfter := state.TotalBlocksPendingToSync()

		require.Equal(t, syncedCountBefore, len(syncedAfter), "synced segments count should be unchanged")
		require.Equal(t, syncedBefore[0].BlockRange, syncedAfter[0].BlockRange, "synced range should be unchanged")
		require.Equal(t, pendingBefore, pendingAfter, "pending blocks should be unchanged")
	})

	t.Run("multiple consecutive syncs", func(t *testing.T) {
		addr1 := common.HexToAddress("0x100")

		syncedSet := mdrtypes.NewSetSyncSegment()
		pendingSet := mdrtypes.NewSetSyncSegment()
		pendingSet.Add(mdrtypes.NewSyncSegment(addr1,
			aggkitcommon.NewBlockRange(1, 1000),
			aggkittypes.LatestBlock,
			false))

		state := NewState(&syncedSet, &pendingSet)

		// Sync in chunks
		chunks := []struct {
			from uint64
			to   uint64
		}{
			{1, 100},
			{101, 200},
			{201, 300},
		}

		for i, chunk := range chunks {
			logQuery := mdrtypes.NewLogQuery(chunk.from, chunk.to, []common.Address{addr1})
			err := state.OnNewSyncedLogQuery(&logQuery)
			require.NoError(t, err, "chunk %d should succeed", i)

			// Verify synced range
			synced := state.SyncedSegmentsByContract([]common.Address{addr1})
			require.Equal(t, 1, len(synced))
			require.Equal(t, uint64(1), synced[0].BlockRange.FromBlock)
			require.Equal(t, chunk.to, synced[0].BlockRange.ToBlock)
		}

		// Verify final state
		synced := state.SyncedSegmentsByContract([]common.Address{addr1})
		require.Equal(t, aggkitcommon.NewBlockRange(1, 300), synced[0].BlockRange)
		require.Equal(t, uint64(700), state.TotalBlocksPendingToSync()) // 301-1000
	})

	t.Run("sync everything until finished", func(t *testing.T) {
		addr1 := common.HexToAddress("0x100")

		// Start with empty synced and full pending
		syncedSet := mdrtypes.NewSetSyncSegment()
		pendingSet := mdrtypes.NewSetSyncSegment()
		pendingSet.Add(mdrtypes.NewSyncSegment(addr1,
			aggkitcommon.NewBlockRange(1, 300),
			aggkittypes.LatestBlock,
			false))

		state := NewState(&syncedSet, &pendingSet)

		// Verify initial state
		require.False(t, state.IsSyncFinished(), "should not be finished initially")
		require.Equal(t, uint64(300), state.TotalBlocksPendingToSync())

		// Sync all blocks in chunks
		chunks := []struct {
			from uint64
			to   uint64
		}{
			{1, 100},
			{101, 200},
			{201, 300},
		}

		for i, chunk := range chunks {
			logQuery := mdrtypes.NewLogQuery(chunk.from, chunk.to, []common.Address{addr1})
			err := state.OnNewSyncedLogQuery(&logQuery)
			require.NoError(t, err, "chunk %d should succeed", i)

			if i < len(chunks)-1 {
				// Not finished yet
				require.False(t, state.IsSyncFinished(), "should not be finished after chunk %d", i)
				require.Greater(t, state.TotalBlocksPendingToSync(), uint64(0),
					"should have pending blocks after chunk %d", i)
			}
		}

		// Verify everything is synced
		require.True(t, state.IsSyncFinished(), "should be finished after syncing all blocks")
		require.Equal(t, uint64(0), state.TotalBlocksPendingToSync(), "should have 0 pending blocks")

		// Verify synced range covers everything
		synced := state.SyncedSegmentsByContract([]common.Address{addr1})
		require.Equal(t, 1, len(synced))
		require.Equal(t, aggkitcommon.NewBlockRange(1, 300), synced[0].BlockRange)

		// Verify total pending block range is nil or empty
		totalPending := state.GetTotalPendingBlockRange()
		if totalPending != nil {
			require.True(t, totalPending.IsEmpty(), "total pending range should be empty")
		}
	})

	t.Run("sync everything with single query", func(t *testing.T) {
		addr1 := common.HexToAddress("0x100")

		// Start with some already synced
		syncedSet := mdrtypes.NewSetSyncSegment()
		syncedSet.Add(mdrtypes.NewSyncSegment(addr1,
			aggkitcommon.NewBlockRange(1, 50),
			aggkittypes.FinalizedBlock,
			false))

		pendingSet := mdrtypes.NewSetSyncSegment()
		pendingSet.Add(mdrtypes.NewSyncSegment(addr1,
			aggkitcommon.NewBlockRange(51, 100),
			aggkittypes.LatestBlock,
			false))

		state := NewState(&syncedSet, &pendingSet)

		// Verify initial state
		require.False(t, state.IsSyncFinished())
		require.Equal(t, uint64(50), state.TotalBlocksPendingToSync())

		// Sync remaining blocks in one go
		logQuery := mdrtypes.NewLogQuery(51, 100, []common.Address{addr1})
		err := state.OnNewSyncedLogQuery(&logQuery)
		require.NoError(t, err)

		// Verify finished
		require.True(t, state.IsSyncFinished(), "should be finished")
		require.Equal(t, uint64(0), state.TotalBlocksPendingToSync(), "should have 0 pending blocks")
		require.Nil(t, state.GetTotalPendingBlockRange(), "total pending range should be nil")
		// Verify complete synced range
		synced := state.SyncedSegmentsByContract([]common.Address{addr1})
		require.Equal(t, 1, len(synced))
		require.Equal(t, aggkitcommon.NewBlockRange(1, 100), synced[0].BlockRange)
	})
}

func TestState_Clone(t *testing.T) {
	t.Run("nil state", func(t *testing.T) {
		var state *State
		cloned := state.Clone()
		require.Nil(t, cloned, "cloning a nil state should return nil")
	})

	t.Run("deep copy verification", func(t *testing.T) {
		// Create original state with synced and pending segments
		addr1 := common.HexToAddress("0x100")

		syncedSet := mdrtypes.NewSetSyncSegment()
		syncedSet.Add(mdrtypes.NewSyncSegment(addr1,
			aggkitcommon.NewBlockRange(1, 100),
			aggkittypes.FinalizedBlock,
			false))

		pendingSet := mdrtypes.NewSetSyncSegment()
		pendingSet.Add(mdrtypes.NewSyncSegment(addr1,
			aggkitcommon.NewBlockRange(101, 200),
			aggkittypes.LatestBlock,
			false))

		original := NewState(&syncedSet, &pendingSet)

		// Clone the state
		cloned := original.Clone()

		// Verify cloned state has same values initially
		require.NotNil(t, cloned, "cloned state should not be nil")

		// Get synced segments before modification
		originalSyncedBefore := original.SyncedSegmentsByContract([]common.Address{addr1})
		clonedSyncedBefore := cloned.SyncedSegmentsByContract([]common.Address{addr1})

		require.Equal(t, len(originalSyncedBefore), len(clonedSyncedBefore))
		require.Equal(t, originalSyncedBefore[0].BlockRange, clonedSyncedBefore[0].BlockRange)

		// Modify the original by syncing more blocks
		logQuery := mdrtypes.NewLogQuery(101, 150, []common.Address{addr1})
		err := original.OnNewSyncedLogQuery(&logQuery)
		require.NoError(t, err)

		// Get synced segments after modification
		originalSyncedAfter := original.SyncedSegmentsByContract([]common.Address{addr1})
		clonedSyncedAfter := cloned.SyncedSegmentsByContract([]common.Address{addr1})

		// Original should have extended synced range (1-150)
		require.Equal(t, 1, len(originalSyncedAfter))
		require.Equal(t, aggkitcommon.NewBlockRange(1, 150), originalSyncedAfter[0].BlockRange,
			"original should have extended range after sync")

		// Cloned should still have the original range (1-100)
		require.Equal(t, 1, len(clonedSyncedAfter))
		require.Equal(t, aggkitcommon.NewBlockRange(1, 100), clonedSyncedAfter[0].BlockRange,
			"cloned state should not be affected by modifications to original")
	})

	t.Run("empty state", func(t *testing.T) {
		original := NewEmptyState()
		cloned := original.Clone()

		require.NotNil(t, cloned, "cloned empty state should not be nil")
		require.True(t, cloned.IsSyncFinished(), "cloned empty state should be finished")
		require.Equal(t, uint64(0), cloned.TotalBlocksPendingToSync(), "cloned empty state should have 0 pending blocks")
	})

	t.Run("complex state with multiple segments", func(t *testing.T) {
		addr1 := common.HexToAddress("0x1")
		addr2 := common.HexToAddress("0x2")
		addr3 := common.HexToAddress("0x3")

		syncedSet := mdrtypes.NewSetSyncSegment()
		syncedSet.Add(mdrtypes.NewSyncSegment(addr1, aggkitcommon.NewBlockRange(0, 100), aggkittypes.FinalizedBlock, false))
		syncedSet.Add(mdrtypes.NewSyncSegment(addr2, aggkitcommon.NewBlockRange(0, 200), aggkittypes.FinalizedBlock, false))

		pendingSet := mdrtypes.NewSetSyncSegment()
		pendingSet.Add(mdrtypes.NewSyncSegment(addr1, aggkitcommon.NewBlockRange(101, 500), aggkittypes.LatestBlock, false))
		pendingSet.Add(mdrtypes.NewSyncSegment(addr2, aggkitcommon.NewBlockRange(201, 600), aggkittypes.LatestBlock, false))
		pendingSet.Add(mdrtypes.NewSyncSegment(addr3, aggkitcommon.NewBlockRange(0, 1000), aggkittypes.LatestBlock, false))

		original := NewState(&syncedSet, &pendingSet)
		cloned := original.Clone()

		// Verify counts before modification
		originalPendingBefore := original.TotalBlocksPendingToSync()
		clonedPendingBefore := cloned.TotalBlocksPendingToSync()
		require.Equal(t, originalPendingBefore, clonedPendingBefore)

		// Modify original - sync blocks at the end of addr3 range to avoid splitting
		logQuery := mdrtypes.NewLogQuery(901, 1000, []common.Address{addr3})
		err := original.OnNewSyncedLogQuery(&logQuery)
		require.NoError(t, err)

		// Verify original changed
		originalPendingAfter := original.TotalBlocksPendingToSync()
		require.Less(t, originalPendingAfter, originalPendingBefore, "original pending should decrease")

		// Verify cloned is independent
		clonedPendingAfter := cloned.TotalBlocksPendingToSync()
		require.Equal(t, clonedPendingBefore, clonedPendingAfter,
			"cloned state should be independent from original after modification")
	})
}

func TestStateInitial_case_startBlock0(t *testing.T) {
	var err error
	configs := mdrtypes.NewSetSyncerConfig()
	cfg := aggkittypes.SyncerConfig{
		SyncerID:          "syncer1",
		ContractAddresses: []common.Address{common.HexToAddress("0x10")},
		FromBlock:         0,
		ToBlock:           aggkittypes.FinalizedBlock,
	}
	configs.Add(cfg)
	mapBlocks := map[aggkittypes.BlockNumberFinality]uint64{
		aggkittypes.FinalizedBlock: 256,
	}
	syncSegments, err := configs.SyncSegments(mapBlocks)
	require.NoError(t, err)
	storageSyncSegments := mdrtypes.NewSetSyncSegment()
	storageSyncSegments.Add(mdrtypes.NewSyncSegment(
		common.HexToAddress("0x10"),
		aggkitcommon.BlockRangeZero,
		aggkittypes.FinalizedBlock,
		true))

	sut, err := NewStateFromStorageSyncedBlocks(
		storageSyncSegments, *syncSegments)
	require.NoError(t, err)
	br := sut.GetTotalPendingBlockRange()
	require.NotNil(t, br)
	require.Equal(t, "From: 0, To: 256 (257)", br.String())
	nextRequest, err := sut.NextQueryToSync(20, 250)
	require.NoError(t, err)
	require.Equal(t, "From: 0, To: 19 (20)", nextRequest.BlockRange.String())
	// after: synced: {0-19}, pending: {20-256}
	err = sut.OnNewSyncedLogQuery(nextRequest)
	require.NoError(t, err)
	br = sut.GetTotalPendingBlockRange()
	require.Equal(t, "From: 20, To: 256 (237)", br.String())
	require.True(t, sut.IsAvailable(*nextRequest))
	// nextRequest = {20-400}
	nextRequest.BlockRange = aggkitcommon.NewBlockRange(10, 400)
	require.False(t, sut.IsAvailable(*nextRequest))
	partial, subRequest := sut.IsPartiallyAvailable(*nextRequest)
	require.True(t, partial)
	require.Equal(t, "From: 10, To: 19 (10)", subRequest.BlockRange.String())
}

func TestStateInitial_case_startBlock1(t *testing.T) {
	var err error
	configs := mdrtypes.NewSetSyncerConfig()
	cfg := aggkittypes.SyncerConfig{
		SyncerID:          "syncer1",
		ContractAddresses: []common.Address{common.HexToAddress("0x10")},
		FromBlock:         1,
		ToBlock:           aggkittypes.FinalizedBlock,
	}
	configs.Add(cfg)
	mapBlocks := map[aggkittypes.BlockNumberFinality]uint64{
		aggkittypes.FinalizedBlock: 256,
	}
	syncSegments, err := configs.SyncSegments(mapBlocks)
	require.NoError(t, err)
	storageSyncSegments := mdrtypes.NewSetSyncSegment()
	storageSyncSegments.Add(mdrtypes.NewSyncSegment(
		common.HexToAddress("0x10"),
		aggkitcommon.BlockRangeZero,
		aggkittypes.FinalizedBlock,
		true))

	sut, err := NewStateFromStorageSyncedBlocks(
		storageSyncSegments, *syncSegments)
	require.NoError(t, err)
	br := sut.GetTotalPendingBlockRange()
	require.NotNil(t, br)
	require.Equal(t, "From: 1, To: 256 (256)", br.String())
}
func TestStateInitial_ExtendPendingRange(t *testing.T) {
	var err error
	configs := mdrtypes.NewSetSyncerConfig()
	cfg := aggkittypes.SyncerConfig{
		SyncerID:          "syncer1",
		ContractAddresses: []common.Address{common.HexToAddress("0x10")},
		FromBlock:         1,
		ToBlock:           aggkittypes.FinalizedBlock,
	}
	configs.Add(cfg)
	mapBlocks := map[aggkittypes.BlockNumberFinality]uint64{
		aggkittypes.FinalizedBlock: 200,
	}

	syncSegments, err := configs.SyncSegments(mapBlocks)
	require.NoError(t, err)

	storageSyncSegments := mdrtypes.NewSetSyncSegment()
	storageSyncSegments.Add(mdrtypes.NewSyncSegment(
		common.HexToAddress("0x10"),
		aggkitcommon.BlockRangeZero,
		aggkittypes.FinalizedBlock,
		true))
	sut, err := NewStateFromStorageSyncedBlocks(
		storageSyncSegments, *syncSegments)
	require.NoError(t, err)
	pendingSync := sut.GetTotalPendingBlockRange()
	require.NotNil(t, pendingSync)

	// Sync first batch 1-200
	err = sut.OnNewSyncedLogQuery(&mdrtypes.LogQuery{
		BlockRange: aggkitcommon.NewBlockRange(1, 200),
		Addrs:      []common.Address{common.HexToAddress("0x10")},
	})
	require.NoError(t, err)
	require.True(t, sut.IsSyncFinished())

	// Now extend the range to block 350
	mapBlocks[aggkittypes.FinalizedBlock] = 350
	err = sut.ExtendPendingRange(mapBlocks, &configs)
	require.NoError(t, err)
	pendingBlockRange := sut.GetTotalPendingBlockRange()
	require.NotNil(t, pendingBlockRange)
	require.Equal(t, "From: 201, To: 350 (150)", pendingBlockRange.String())
}

func TestState_AfterFullySync(t *testing.T) {
	// Setup: Create a state with a segment to sync from block 1 to 100
	addr := common.HexToAddress("0x123124543423")
	configs := mdrtypes.NewSetSyncerConfig()
	cfg := aggkittypes.SyncerConfig{
		SyncerID:          "syncer1",
		ContractAddresses: []common.Address{addr},
		FromBlock:         1,
		ToBlock:           aggkittypes.LatestBlock,
	}
	configs.Add(cfg)

	// Initial target block is 100
	mapBlocks := map[aggkittypes.BlockNumberFinality]uint64{
		aggkittypes.LatestBlock: 100,
	}

	syncSegments, err := configs.SyncSegments(mapBlocks)
	require.NoError(t, err)

	// Start with empty storage (nothing synced yet)
	storageSyncSegments := mdrtypes.NewSetSyncSegment()
	storageSyncSegments.Add(mdrtypes.NewSyncSegment(
		addr,
		aggkitcommon.BlockRangeZero,
		aggkittypes.LatestBlock,
		true))

	state, err := NewStateFromStorageSyncedBlocks(storageSyncSegments, *syncSegments)
	require.NoError(t, err)

	// Verify initial pending state
	require.False(t, state.IsSyncFinished(), "should not be finished initially")
	require.Equal(t, uint64(100), state.TotalBlocksPendingToSync(), "should have 100 blocks pending")

	// Sync all blocks 1-100
	logQuery := mdrtypes.NewLogQuery(1, 100, []common.Address{addr})
	err = state.OnNewSyncedLogQuery(&logQuery)
	require.NoError(t, err)

	// Verify sync is complete
	require.True(t, state.IsSyncFinished(), "should be finished after syncing all blocks")
	require.Equal(t, uint64(0), state.TotalBlocksPendingToSync(), "should have 0 blocks pending")

	// Verify synced range
	syncedSegments := state.SyncedSegmentsByContract([]common.Address{addr})
	require.Equal(t, 1, len(syncedSegments))
	require.Equal(t, aggkitcommon.NewBlockRange(1, 100), syncedSegments[0].BlockRange)

	// Now simulate that the chain has progressed to block 150
	// This is analogous to UpdateTargetBlockToNumber in the SetSyncSegment test
	mapBlocks[aggkittypes.LatestBlock] = 150
	err = state.ExtendPendingRange(mapBlocks, &configs)
	require.NoError(t, err)

	// Verify that new blocks are now pending (101-150)
	require.False(t, state.IsSyncFinished(), "should not be finished after extending range")
	require.Equal(t, uint64(50), state.TotalBlocksPendingToSync(), "should have 50 new blocks pending")

	// Verify pending range
	pendingBlockRange := state.GetTotalPendingBlockRange()
	require.NotNil(t, pendingBlockRange)
	require.Equal(t, "From: 101, To: 150 (50)", pendingBlockRange.String())

	// Verify synced segments remain unchanged
	syncedSegments = state.SyncedSegmentsByContract([]common.Address{addr})
	require.Equal(t, 1, len(syncedSegments))
	require.Equal(t, aggkitcommon.NewBlockRange(1, 100), syncedSegments[0].BlockRange,
		"synced range should remain unchanged at 1-100")
}
