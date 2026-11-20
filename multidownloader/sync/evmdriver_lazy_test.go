package multidownloader

import (
	"context"
	"errors"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSyncStep_LazyModeWaitsForTheStartingPoint(t *testing.T) {
	testData := newEVMDriverTestData(t, true)
	testData.driver.waitForFirstBlock.Store(true)

	// No processed block: nothing is downloaded
	testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
	require.NoError(t, testData.driver.syncStep(t.Context()))

	// Once there is a processed block it downloads from it
	header := &aggkittypes.BlockHeader{Number: 100}
	testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(header, nil).Once()
	testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, header, mock.Anything, mock.Anything).
		Return(nil, nil).Once()
	require.NoError(t, testData.driver.syncStep(t.Context()))
}

func TestSyncStep_StaleLogsAreNotAnError(t *testing.T) {
	testData := newEVMDriverTestData(t, true)
	testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
	testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, aggkittypes.ErrTransactionInclusionMismatch).Once()

	require.NoError(t, testData.driver.syncStep(t.Context()))
}

func TestSync_LazyModeStopsOnContextDone(t *testing.T) {
	testData := newEVMDriverTestData(t, true)
	testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Maybe()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		testData.driver.Sync(ctx, nil)
	}()
	require.Eventually(t, testData.driver.waitForFirstBlock.Load, time.Second, time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		require.Fail(t, "Sync didn't stop")
	}
}

func TestSyncNextBlock(t *testing.T) {
	result := func(blockNum uint64) *mdrsynctypes.DownloadResult {
		return &mdrsynctypes.DownloadResult{
			Data: sync.EVMBlocks{{EVMBlockHeader: sync.EVMBlockHeader{Num: blockNum}}},
		}
	}

	t.Run("already bootstrapped", func(t *testing.T) {
		testData := newEVMDriverTestData(t, false)
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).
			Return(&aggkittypes.BlockHeader{Number: 5}, nil).Once()

		require.ErrorIs(t, testData.driver.SyncNextBlock(t.Context(), 100), sync.ErrAlreadyBootstrapped)
	})

	t.Run("error getting the last processed block", func(t *testing.T) {
		testData := newEVMDriverTestData(t, false)
		expectedErr := errors.New("db error")
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, expectedErr).Once()

		require.ErrorIs(t, testData.driver.SyncNextBlock(t.Context(), 100), expectedErr)
	})

	t.Run("downloads and processes only the block, waiting until it is available", func(t *testing.T) {
		testData := newEVMDriverTestData(t, false)
		testData.driver.syncerConfig.FromBlock = 50
		// The downloader returns the blocks after the given header, that is not checked for reorgs
		previousHeader := &aggkittypes.BlockHeader{Number: 99, Hash: aggkitcommon.ZeroHash}
		blocks := result(100)
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, previousHeader, uint64(1),
			testData.driver.syncerConfig).Return(nil, ErrLogsNotAvailable).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, previousHeader, uint64(1),
			testData.driver.syncerConfig).Return(blocks, nil).Once()
		testData.mockProcessor.EXPECT().ProcessBlocks(mock.Anything, blocks).Return(nil).Once()

		require.NoError(t, testData.driver.SyncNextBlock(t.Context(), 100))
	})

	t.Run("block 0 has no previous block", func(t *testing.T) {
		testData := newEVMDriverTestData(t, false)
		testData.driver.syncerConfig.FromBlock = 50
		blocks := result(0)
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, (*aggkittypes.BlockHeader)(nil), uint64(1),
			aggkittypes.SyncerConfig{FromBlock: 0}).Return(blocks, nil).Once()
		testData.mockProcessor.EXPECT().ProcessBlocks(mock.Anything, blocks).Return(nil).Once()

		require.NoError(t, testData.driver.SyncNextBlock(t.Context(), 0))
	})

	t.Run("download error", func(t *testing.T) {
		testData := newEVMDriverTestData(t, false)
		expectedErr := errors.New("download error")
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, expectedErr).Once()

		require.ErrorIs(t, testData.driver.SyncNextBlock(t.Context(), 100), expectedErr)
	})

	t.Run("unexpected download result", func(t *testing.T) {
		testData := newEVMDriverTestData(t, false)
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(result(101), nil).Once()

		require.ErrorContains(t, testData.driver.SyncNextBlock(t.Context(), 100), "unexpected download result")
	})

	t.Run("process error", func(t *testing.T) {
		testData := newEVMDriverTestData(t, false)
		expectedErr := errors.New("process error")
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(result(100), nil).Once()
		testData.mockProcessor.EXPECT().ProcessBlocks(mock.Anything, mock.Anything).Return(expectedErr).Once()

		require.ErrorIs(t, testData.driver.SyncNextBlock(t.Context(), 100), expectedErr)
	})

	t.Run("context done while the block is not available", func(t *testing.T) {
		testData := newEVMDriverTestData(t, false)
		ctx, cancel := context.WithCancel(t.Context())
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, *aggkittypes.BlockHeader, uint64,
				aggkittypes.SyncerConfig) (*mdrsynctypes.DownloadResult, error) {
				cancel()
				return nil, ErrLogsNotAvailable
			}).Once()

		require.ErrorIs(t, testData.driver.SyncNextBlock(ctx, 100), context.Canceled)
	})
}
