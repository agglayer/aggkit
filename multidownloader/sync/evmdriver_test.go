package multidownloader

import (
	"errors"
	"fmt"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	compatibilityMocks "github.com/agglayer/aggkit/db/compatibility/mocks"
	"github.com/agglayer/aggkit/log"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	"github.com/agglayer/aggkit/multidownloader/sync/types/mocks"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type evmDriverTestData struct {
	driver                   *EVMDriver
	mockProcessor            *mocks.ProcessorInterface
	mockDownloader           *mocks.DownloaderInterface
	mockCompatibilityChecker *compatibilityMocks.CompatibilityChecker
	syncerConfig             aggkittypes.SyncerConfig
	logger                   aggkitcommon.Logger
	rh                       *sync.RetryHandler
}

func newEVMDriverTestData(t *testing.T, compatibilityCheckExpectations bool) *evmDriverTestData {
	t.Helper()
	mockProcessor := mocks.NewProcessorInterface(t)
	mockDownloader := mocks.NewDownloaderInterface(t)
	mockCompatibilityChecker := compatibilityMocks.NewCompatibilityChecker(t)
	syncerConfig := aggkittypes.SyncerConfig{}
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      time.Millisecond * 10,
		MaxRetryAttemptsAfterError: 0,
	}
	if compatibilityCheckExpectations {
		mockCompatibilityChecker.EXPECT().Check(mock.Anything, mock.Anything).Return(nil).Maybe()
	}
	driver := NewEVMDriver(
		logger,
		mockProcessor,
		mockDownloader,
		syncerConfig,
		100,
		rh,
		mockCompatibilityChecker,
	)
	require.NotNil(t, driver)
	return &evmDriverTestData{
		driver:                   driver,
		mockProcessor:            mockProcessor,
		mockDownloader:           mockDownloader,
		mockCompatibilityChecker: mockCompatibilityChecker,
		syncerConfig:             syncerConfig,
		logger:                   logger,
		rh:                       rh,
	}
}

func TestNewEVMDriver_SyncStep(t *testing.T) {
	t.Run("fail compatibility check", func(t *testing.T) {
		testData := newEVMDriverTestData(t, false)
		expectedErr := errors.New("compatibility check failed")
		testData.mockCompatibilityChecker.EXPECT().Check(mock.Anything, mock.Anything).Return(expectedErr).Once()
		ctx := t.Context()
		err := testData.driver.syncStep(ctx)
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("compatibility check it's only executed 1 time", func(t *testing.T) {
		testData := newEVMDriverTestData(t, false)
		expectedErr := errors.New("compatibility check failed")
		testData.mockCompatibilityChecker.EXPECT().Check(mock.Anything, mock.Anything).Return(expectedErr).Once()
		ctx := t.Context()
		err := testData.driver.syncStep(ctx)
		require.ErrorIs(t, err, expectedErr)
		// This round the compatibility check is called because the previous one failed
		testData.mockCompatibilityChecker.EXPECT().Check(mock.Anything, mock.Anything).Return(nil).Once()
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
		err = testData.driver.syncStep(ctx)
		require.NoError(t, err)
		// This round the compatibility check should not be executed again
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
		err = testData.driver.syncStep(ctx)
		require.NoError(t, err)
	})

	t.Run("DownloadNextBlocks returns ErrLogsNotAvailable", func(t *testing.T) {
		testData := newEVMDriverTestData(t, true)
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, mock.Anything,
			mock.Anything, mock.Anything).Return(nil, ErrLogsNotAvailable).Once()
		err := testData.driver.syncStep(t.Context())
		require.NoError(t, err)
	})

	t.Run("DownloadNextBlocks returns ReorgedError", func(t *testing.T) {
		testData := newEVMDriverTestData(t, true)
		expectedErr := mdrtypes.NewReorgedError(aggkitcommon.NewBlockRange(10, 20), 20, "test")
		testData.mockProcessor.EXPECT().GetLastProcessedBlockHeader(mock.Anything).Return(nil, nil).Once()
		testData.mockDownloader.EXPECT().DownloadNextBlocks(mock.Anything, mock.Anything,
			mock.Anything, mock.Anything).Return(nil, expectedErr).Once()
		testData.mockProcessor.EXPECT().Reorg(mock.Anything, uint64(10)).Return(nil).Once()
		err := testData.driver.syncStep(t.Context())
		require.NoError(t, err)
	})
}

func TestNewEVMDriver_ProcessBlocks(t *testing.T) {
	t.Run("xxx", func(t *testing.T) {
		testData := newEVMDriverTestData(t, true)
		ctx := t.Context()
		testData.driver.rh.MaxRetryAttemptsAfterError = 2
		data := &mdrsynctypes.DownloadResult{
			Data: []*sync.EVMBlock{
				{ // sync.EVMBlock
					EVMBlockHeader: sync.EVMBlockHeader{
						Num: 10,
					},
				},
			},
			CompletionPercentage: 50,
		}
		errProcessBlock := fmt.Errorf("error processing blocks")
		testData.mockProcessor.EXPECT().
			ProcessBlocks(mock.Anything, data).Return(errProcessBlock).Once()
		testData.mockProcessor.EXPECT().
			ProcessBlocks(mock.Anything, data).Return(nil).Once()
		err := testData.driver.processBlocks(ctx, data)
		require.NoError(t, err)
		require.Equal(t, data.CompletionPercentage, *testData.driver.GetCompletionPercentage())
	})
}
