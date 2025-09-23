package l2gersync

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/l1infotreesync"
	l2gersyncmocks "github.com/agglayer/aggkit/l2gersync/mocks"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDownloaderSovereign_Download(t *testing.T) {
	t.Parallel()

	fromBlock := uint64(100)
	syncBlockChunkSize := uint64(10)
	latestBlock := uint64(120)
	l2GERAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
	mockL1InfoTreeSync := &l2gersyncmocks.L1InfoTreeQuerier{}
	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      time.Millisecond,
	}

	testGER := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	testHashChainValue := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	testL1InfoTreeIndex := uint32(42)
	testBlockHeader := &ethtypes.Header{
		Number:      big.NewInt(int64(fromBlock)),
		ParentHash:  common.HexToHash("0xabc123"),
		Root:        common.HexToHash("0xdef456"),
		TxHash:      common.HexToHash("0x789abc"),
		ReceiptHash: common.HexToHash("0x101112"),
		Time:        uint64(time.Now().Unix()),
		GasLimit:    8000000,
		GasUsed:     21000,
	}
	testBlockHash := testBlockHeader.Hash()
	testLogs := []ethtypes.Log{
		{
			Address:     l2GERAddr,
			Topics:      []common.Hash{insertGEREventSignature, testGER, testHashChainValue},
			Data:        []byte{},
			BlockNumber: fromBlock,
			TxHash:      common.HexToHash("0x111"),
			TxIndex:     0,
			BlockHash:   testBlockHash,
			Index:       0,
		},
	}

	mockL2Client.EXPECT().ChainID(mock.Anything).Return(big.NewInt(1), nil).Maybe()
	mockL2Client.EXPECT().HeaderByNumber(mock.Anything, (*big.Int)(nil)).Return(&ethtypes.Header{
		Number: big.NewInt(int64(latestBlock)),
	}, nil).Maybe()
	mockL2Client.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(int64(fromBlock))).Return(testBlockHeader, nil).Maybe()
	// Mock L1 client expectations for IsUpToDate check
	mockL1Client.EXPECT().BlockByNumber(mock.Anything, mock.Anything).Return(ethtypes.NewBlock(
		&ethtypes.Header{Number: big.NewInt(int64(latestBlock))},
		nil, nil, nil,
	), nil).Maybe()

	// Note: IsUpToDate is called via type assertion, so we don't need to mock it here
	mockL1InfoTreeSync.EXPECT().GetInfoByGlobalExitRoot(testGER).Return(&l1infotreesync.L1InfoTreeLeaf{
		L1InfoTreeIndex:   testL1InfoTreeIndex,
		GlobalExitRoot:    testGER,
		Timestamp:         uint64(time.Now().Unix()),
		PreviousBlockHash: common.Hash{},
		BlockNumber:       fromBlock,
		BlockPosition:     0,
		MainnetExitRoot:   common.Hash{},
		RollupExitRoot:    common.Hash{},
		Hash:              common.Hash{},
	}, nil).Maybe()
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(testLogs, nil).Maybe()

	downloader, err := newDownloaderSovereign(
		mockL2Client,
		l2GERAddr,
		mockL1InfoTreeSync,
		mockL1Client,
		rh,
		aggkittypes.LatestBlock,
		time.Millisecond*10, // waitForNewBlocksPeriod
		syncBlockChunkSize,
	)
	require.NoError(t, err)
	downloadedCh := make(chan sync.EVMBlock, 10)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	downloader.Download(ctx, fromBlock, downloadedCh)

	// Collect blocks sent through the channel
	for block := range downloadedCh {
		require.Equal(t, fromBlock, block.Num, "Block number should match")

		// Verify event content
		require.Len(t, block.Events, 1, "Should have exactly one event")
		event, ok := block.Events[0].(*Event)
		require.True(t, ok, "Event should be of type *Event")
		require.NotNil(t, event.GERInfo, "Event should have GERInfo")
		require.Equal(t, testGER, event.GERInfo.GlobalExitRoot, "GER should match test data")
		require.Equal(t, testL1InfoTreeIndex, event.GERInfo.L1InfoTreeIndex, "L1InfoTreeIndex should match")
		require.Equal(t, GEREventTypeInsert, event.EventType, "Should be insert event type")
		t.Logf("✅ Successfully verified block with processed GER event!")
	}

	mockL2Client.AssertExpectations(t)
	mockL1InfoTreeSync.AssertExpectations(t)
}

// mockL1InfoTreeSyncWithIsUpToDate extends the generated mock to include IsUpToDate method
type mockL1InfoTreeSyncWithIsUpToDate struct {
	*l2gersyncmocks.L1InfoTreeQuerier
	isUpToDateResult bool
	isUpToDateError  error
}

func (m *mockL1InfoTreeSyncWithIsUpToDate) IsUpToDate(ctx context.Context, l1Client aggkittypes.BaseEthereumClienter) (bool, error) {
	return m.isUpToDateResult, m.isUpToDateError
}

func TestIsL1InfoTreeSyncUpToDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		hasIsUpToDate    bool
		isUpToDateResult bool
		isUpToDateError  error
		expectedResult   bool
		expectedError    bool
	}{
		{
			name:             "L1InfoTreeSync with IsUpToDate method returns true",
			hasIsUpToDate:    true,
			isUpToDateResult: true,
			isUpToDateError:  nil,
			expectedResult:   true,
			expectedError:    false,
		},
		{
			name:             "L1InfoTreeSync with IsUpToDate method returns false",
			hasIsUpToDate:    true,
			isUpToDateResult: false,
			isUpToDateError:  nil,
			expectedResult:   false,
			expectedError:    false,
		},
		{
			name:             "L1InfoTreeSync without IsUpToDate method",
			hasIsUpToDate:    false,
			isUpToDateResult: false,
			isUpToDateError:  nil,
			expectedResult:   false,
			expectedError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
			mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)

			var mockL1InfoTreeSync interface{}
			if tt.hasIsUpToDate {
				mockL1InfoTreeSync = &mockL1InfoTreeSyncWithIsUpToDate{
					L1InfoTreeQuerier: l2gersyncmocks.NewL1InfoTreeQuerier(t),
					isUpToDateResult:  tt.isUpToDateResult,
					isUpToDateError:   tt.isUpToDateError,
				}
			} else {
				mockL1InfoTreeSync = l2gersyncmocks.NewL1InfoTreeQuerier(t)
			}

			rh := &sync.RetryHandler{
				MaxRetryAttemptsAfterError: 5,
				RetryAfterErrorPeriod:      time.Millisecond,
			}

			downloader, err := newDownloaderSovereign(
				mockL2Client,
				common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				mockL1InfoTreeSync.(L1InfoTreeQuerier),
				mockL1Client,
				rh,
				aggkittypes.LatestBlock,
				time.Millisecond*10,
				10,
			)
			require.NoError(t, err)

			ctx := context.Background()
			result, err := downloader.isL1InfoTreeSyncUpToDate(ctx)

			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedResult, result)
			}
		})
	}
}
