package l2gersync

import (
	"context"
	"fmt"
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
	mockL1InfoTreeSync := l2gersyncmocks.NewL1InfoTreeQuerier(t)
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
	// First call to get latest block header (with nil)
	mockL2Client.EXPECT().HeaderByNumber(mock.Anything, (*big.Int)(nil)).Return(&ethtypes.Header{
		Number: big.NewInt(int64(latestBlock)),
	}, nil).Maybe()
	// Second call to get the offset block header (with latestBlock since offset is 0)
	mockL2Client.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(int64(latestBlock))).Return(&ethtypes.Header{
		Number: big.NewInt(int64(latestBlock)),
	}, nil).Maybe()
	mockL2Client.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(int64(fromBlock))).Return(testBlockHeader, nil).Maybe()

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
		common.HexToAddress("0x0000000000000000000000000000000000000001"), // l1GERAddr
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

func TestIsL1InfoTreeQuerierUpToDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		isUpToDateResult bool
		isUpToDateError  error
		expectedResult   bool
		expectedError    bool
	}{
		{
			name:             "L1InfoTreeSync IsUpToDate returns true",
			isUpToDateResult: true,
			isUpToDateError:  nil,
			expectedResult:   true,
			expectedError:    false,
		},
		{
			name:             "L1InfoTreeSync IsUpToDate returns false",
			isUpToDateResult: false,
			isUpToDateError:  nil,
			expectedResult:   false,
			expectedError:    false,
		},
		{
			name:             "L1InfoTreeSync IsUpToDate returns error",
			isUpToDateResult: false,
			isUpToDateError:  fmt.Errorf("test error"),
			expectedResult:   false,
			expectedError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
			mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
			mockL1InfoTreeSync := l2gersyncmocks.NewL1InfoTreeQuerier(t)

			// Set up the mock expectation for IsUpToDate
			mockL1InfoTreeSync.EXPECT().IsUpToDate(mock.Anything, mock.Anything).Return(tt.isUpToDateResult, tt.isUpToDateError).Maybe()

			rh := &sync.RetryHandler{
				MaxRetryAttemptsAfterError: 5,
				RetryAfterErrorPeriod:      time.Millisecond,
			}

			downloader, err := newDownloaderSovereign(
				mockL2Client,
				common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				mockL1InfoTreeSync,
				mockL1Client,
				common.HexToAddress("0x0000000000000000000000000000000000000001"), // l1GERAddr
				rh,
				aggkittypes.LatestBlock,
				time.Millisecond*10,
				10,
			)
			require.NoError(t, err)

			ctx := context.Background()
			result, err := downloader.l1InfoTreeSync.IsUpToDate(ctx, mockL1Client)

			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

func TestDownloaderSovereign_GetInfoByGlobalExitRootErrorHandlingInAppender(t *testing.T) {
	t.Parallel()

	fromBlock := uint64(100)
	syncBlockChunkSize := uint64(10)
	latestBlock := uint64(120)
	l2GERAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	testGER := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	testHashChainValue := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
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

	tests := []struct {
		name                 string
		getInfoByGERError    error
		isUpToDateResult     bool
		isUpToDateError      error
		l1ContractTimestamp  *big.Int
		l1ContractError      error
		l2ContractTimestamp  *big.Int
		l2ContractError      error
		expectError          bool
		expectedErrorMessage string
	}{
		{
			name:                 "GetInfoByGlobalExitRoot_fails_IsUpToDate_returns_error",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			isUpToDateResult:     false,
			isUpToDateError:      fmt.Errorf("L1InfoTreeSync check failed"),
			expectError:          true,
			expectedErrorMessage: "failed to fetch l1 info tree for global exit root",
		},
		{
			name:                 "GetInfoByGlobalExitRoot_fails_IsUpToDate_returns_false",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			isUpToDateResult:     false,
			isUpToDateError:      nil,
			expectError:          true,
			expectedErrorMessage: "failed to fetch l1 info tree for global exit root",
		},
		{
			name:                 "GetInfoByGlobalExitRoot_fails_IsUpToDate_true_L1Contract_GER_exists",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			isUpToDateResult:     true,
			isUpToDateError:      nil,
			l1ContractTimestamp:  big.NewInt(1234567890), // timestamp > 0 means GER exists
			l1ContractError:      nil,
			expectError:          true,
			expectedErrorMessage: "failed to fetch l1 info tree for global exit root",
		},
		{
			name:                 "GetInfoByGlobalExitRoot_fails_IsUpToDate_true_L1Contract_timestamp_zero_L2Contract_timestamp_zero",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			isUpToDateResult:     true,
			isUpToDateError:      nil,
			l1ContractTimestamp:  big.NewInt(0), // timestamp = 0 means GER not found
			l1ContractError:      nil,
			l2ContractTimestamp:  big.NewInt(0), // timestamp = 0 means GER removed from L2
			l2ContractError:      nil,
			expectError:          false, // Should return nil when GER is removed from L2
			expectedErrorMessage: "",
		},
		{
			name:                 "GetInfoByGlobalExitRoot_fails_IsUpToDate_true_L1Contract_timestamp_zero_L2Contract_timestamp_nonzero",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			isUpToDateResult:     true,
			isUpToDateError:      nil,
			l1ContractTimestamp:  big.NewInt(0), // timestamp = 0 means GER not found
			l1ContractError:      nil,
			l2ContractTimestamp:  big.NewInt(1234567890), // timestamp > 0 means GER exists on L2
			l2ContractError:      nil,
			expectError:          true,
			expectedErrorMessage: "failed to fetch l1 info tree for global exit root",
		},
		{
			name:                 "GetInfoByGlobalExitRoot_fails_IsUpToDate_false_skips_L1Contract_call",
			getInfoByGERError:    fmt.Errorf("GER lookup failed"),
			isUpToDateResult:     false, // Set to false to avoid L1 contract call path
			isUpToDateError:      nil,
			expectError:          true,
			expectedErrorMessage: "failed to fetch l1 info tree for global exit root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
			mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
			mockL1InfoTreeSync := l2gersyncmocks.NewL1InfoTreeQuerier(t)
			rh := &sync.RetryHandler{
				MaxRetryAttemptsAfterError: 5,
				RetryAfterErrorPeriod:      time.Millisecond,
			}

			// Set up mock expectations
			mockL2Client.EXPECT().ChainID(mock.Anything).Return(big.NewInt(1), nil).Maybe()
			// First call to get latest block header (with nil)
			mockL2Client.EXPECT().HeaderByNumber(mock.Anything, (*big.Int)(nil)).Return(&ethtypes.Header{
				Number: big.NewInt(int64(latestBlock)),
			}, nil).Maybe()
			// Second call to get the offset block header (with latestBlock since offset is 0)
			mockL2Client.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(int64(latestBlock))).Return(&ethtypes.Header{
				Number: big.NewInt(int64(latestBlock)),
			}, nil).Maybe()
			mockL2Client.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(int64(fromBlock))).Return(testBlockHeader, nil).Maybe()

			mockL1InfoTreeSync.EXPECT().GetInfoByGlobalExitRoot(testGER).Return(nil, tt.getInfoByGERError).Maybe()
			mockL1InfoTreeSync.EXPECT().IsUpToDate(mock.Anything, mock.Anything).Return(tt.isUpToDateResult, tt.isUpToDateError).Maybe()

			// Mock L1 client contract calls for test cases where isUpToDate is true
			if tt.isUpToDateResult {
				if tt.l1ContractTimestamp != nil || tt.l1ContractError != nil {
					callResult := make([]byte, 32)
					if tt.l1ContractTimestamp != nil {
						tt.l1ContractTimestamp.FillBytes(callResult)
					}
					// Even on error, return a valid byte array so contract binding can decode it
					mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(callResult, tt.l1ContractError).Maybe()
				}

				// Mock L2 client contract calls when L1 returns timestamp = 0
				if tt.l1ContractTimestamp != nil && tt.l1ContractTimestamp.Cmp(big.NewInt(0)) == 0 && tt.l1ContractError == nil {
					if tt.l2ContractTimestamp != nil || tt.l2ContractError != nil {
						var l2CallResult []byte
						if tt.l2ContractTimestamp != nil {
							l2CallResult = make([]byte, 32)
							tt.l2ContractTimestamp.FillBytes(l2CallResult)
						}
						mockL2Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(l2CallResult, tt.l2ContractError).Maybe()
					}
				}
			}

			mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(testLogs, nil).Maybe()

			downloader, err := newDownloaderSovereign(
				mockL2Client,
				l2GERAddr,
				mockL1InfoTreeSync,
				mockL1Client,
				common.HexToAddress("0x0000000000000000000000000000000000000001"), // l1GERAddr
				rh,
				aggkittypes.LatestBlock,
				time.Millisecond*10,
				syncBlockChunkSize,
			)
			require.NoError(t, err)

			// Test the appender function directly to cover the error paths
			appender := downloader.buildAppender(downloader.l2GERManager)
			insertAppender := appender[insertGEREventSignature]

			block := &sync.EVMBlock{
				EVMBlockHeader: sync.EVMBlockHeader{
					Num: fromBlock,
				},
				Events: []any{},
			}

			// This should trigger the error path (or return nil in some cases)
			err = insertAppender(block, testLogs[0])
			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErrorMessage)
			} else {
				require.NoError(t, err, "Expected no error when GER is removed from L2")
			}

			mockL2Client.AssertExpectations(t)
			mockL1Client.AssertExpectations(t)
			mockL1InfoTreeSync.AssertExpectations(t)
		})
	}
}
