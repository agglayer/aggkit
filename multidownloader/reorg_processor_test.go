package multidownloader

import (
	"context"
	"fmt"
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	commonmocks "github.com/agglayer/aggkit/common/mocks"
	dbmocks "github.com/agglayer/aggkit/db/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/types"
	mdtypes "github.com/agglayer/aggkit/multidownloader/types"
	mdmocks "github.com/agglayer/aggkit/multidownloader/types/mocks"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestReorgProcessor_CheckBlocks(t *testing.T) {
	t.Run("returns error when blocks is nil", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		processor := &ReorgProcessor{log: mockLogger}

		match, err := processor.checkBlocks(nil)

		require.Error(t, err)
		require.Contains(t, err.Error(), "blocks is nil")
		require.False(t, match)
	})

	t.Run("returns false when storage header is nil", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		processor := &ReorgProcessor{log: mockLogger}
		mockLogger.EXPECT().Warnf(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

		blocks := &mdtypes.CompareBlockHeaders{
			BlockNumber:   100,
			StorageHeader: nil,
			RpcHeader:     &aggkittypes.BlockHeader{Number: 100},
		}

		match, err := processor.checkBlocks(blocks)

		require.NoError(t, err)
		require.False(t, match)
	})

	t.Run("returns false when RPC header is nil", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		processor := &ReorgProcessor{log: mockLogger}
		mockLogger.EXPECT().Warnf(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

		blocks := &mdtypes.CompareBlockHeaders{
			BlockNumber:   100,
			StorageHeader: &aggkittypes.BlockHeader{Number: 100},
			RpcHeader:     nil,
		}

		match, err := processor.checkBlocks(blocks)

		require.NoError(t, err)
		require.False(t, match)
	})

	t.Run("returns error when block numbers do not match", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		processor := &ReorgProcessor{log: mockLogger}

		blocks := &mdtypes.CompareBlockHeaders{
			BlockNumber: 100,
			StorageHeader: &aggkittypes.BlockHeader{
				Number: 100,
				Hash:   common.HexToHash("0x1234"),
			},
			RpcHeader: &aggkittypes.BlockHeader{
				Number: 101,
				Hash:   common.HexToHash("0x1234"),
			},
		}

		match, err := processor.checkBlocks(blocks)

		require.Error(t, err)
		require.Contains(t, err.Error(), "block numbers do not match")
		require.False(t, match)
	})

	t.Run("returns false when hashes do not match (not finalized)", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		processor := &ReorgProcessor{log: mockLogger}

		blocks := &mdtypes.CompareBlockHeaders{
			BlockNumber: 100,
			StorageHeader: &aggkittypes.BlockHeader{
				Number: 100,
				Hash:   common.HexToHash("0x1234"),
			},
			RpcHeader: &aggkittypes.BlockHeader{
				Number: 100,
				Hash:   common.HexToHash("0x5678"),
			},
			IsFinalized: mdtypes.NotFinalized,
		}

		match, err := processor.checkBlocks(blocks)

		require.NoError(t, err)
		require.False(t, match)
	})

	t.Run("returns false when hashes do not match (finalized, logs warning)", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		processor := &ReorgProcessor{log: mockLogger}
		mockLogger.EXPECT().Warnf(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Once()

		blocks := &mdtypes.CompareBlockHeaders{
			BlockNumber: 100,
			StorageHeader: &aggkittypes.BlockHeader{
				Number: 100,
				Hash:   common.HexToHash("0x1234"),
			},
			RpcHeader: &aggkittypes.BlockHeader{
				Number: 100,
				Hash:   common.HexToHash("0x5678"),
			},
			IsFinalized: mdtypes.Finalized,
		}

		match, err := processor.checkBlocks(blocks)

		require.NoError(t, err)
		require.False(t, match)
	})

	t.Run("returns true when blocks match", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		processor := &ReorgProcessor{log: mockLogger}

		hash := common.HexToHash("0x1234")
		blocks := &mdtypes.CompareBlockHeaders{
			BlockNumber: 100,
			StorageHeader: &aggkittypes.BlockHeader{
				Number: 100,
				Hash:   hash,
			},
			RpcHeader: &aggkittypes.BlockHeader{
				Number: 100,
				Hash:   hash,
			},
			IsFinalized: mdtypes.Finalized,
		}

		match, err := processor.checkBlocks(blocks)

		require.NoError(t, err)
		require.True(t, match)
	})
}

func TestReorgProcessor_FindFirstUnaffectedBlock(t *testing.T) {
	mockLogger := commonmocks.NewLogger(t)

	t.Run("returns error when genesis block is reached", func(t *testing.T) {
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewQuerier(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}

		ctx := context.Background()
		hash1 := common.HexToHash("0x1234")
		hash2 := common.HexToHash("0x5678")

		// Block 1 - mismatch, then loop decrements to 0 and checks genesis before calling again
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(1)).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: 1,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: 1,
					Hash:   hash1,
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: 1,
					Hash:   hash2,
				},
			}, nil).Once()

		result, err := processor.findFirstUnaffectedBlock(ctx, mockTx, 1)

		require.Error(t, err)
		require.Contains(t, err.Error(), "genesis block reached")
		require.Equal(t, uint64(0), result)
		mockPort.AssertExpectations(t)
	})

	t.Run("returns error when GetBlockStorageAndRPC fails", func(t *testing.T) {
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewQuerier(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}

		ctx := context.Background()
		expectedErr := fmt.Errorf("RPC connection error")

		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(100)).
			Return(nil, expectedErr).Once()

		result, err := processor.findFirstUnaffectedBlock(ctx, mockTx, 100)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error getting block storage and RPC")
		require.Equal(t, uint64(0), result)
		mockPort.AssertExpectations(t)
	})

	t.Run("finds first unaffected block after checking multiple blocks", func(t *testing.T) {
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewQuerier(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}

		ctx := context.Background()
		matchingHash := common.HexToHash("0xabcd")
		differentHash1 := common.HexToHash("0x1234")
		differentHash2 := common.HexToHash("0x5678")

		// Block 102 - mismatch
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(102)).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: 102,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: 102,
					Hash:   differentHash1,
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: 102,
					Hash:   differentHash2,
				},
			}, nil).Once()

		// Block 101 - mismatch
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(101)).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: 101,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: 101,
					Hash:   differentHash1,
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: 101,
					Hash:   differentHash2,
				},
			}, nil).Once()

		// Block 100 - match (first unaffected)
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(100)).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: 100,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
			}, nil).Once()

		result, err := processor.findFirstUnaffectedBlock(ctx, mockTx, 102)

		require.NoError(t, err)
		require.Equal(t, uint64(100), result)
		mockPort.AssertExpectations(t)
	})
}

func TestReorgProcessor_ProcessReorg(t *testing.T) {
	mockLogger := commonmocks.NewLogger(t)

	t.Run("returns error when NewTx fails", func(t *testing.T) {
		mockPort := mdmocks.NewReorgPorter(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}

		ctx := context.Background()
		expectedErr := fmt.Errorf("transaction creation error")
		reorgErr := mdtypes.NewDetectedReorgError(
			100,
			mdtypes.ReorgDetectionReason_BlockHashMismatch,
			common.HexToHash("0x1234"),
			common.HexToHash("0x5678"),
			"test reorg",
		)

		mockPort.EXPECT().NewTx(ctx).Return(nil, expectedErr).Once()

		err := processor.ProcessReorg(ctx, *reorgErr)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error starting new tx")
		mockPort.AssertExpectations(t)
	})

	t.Run("returns error and rolls back when findFirstUnaffectedBlock fails", func(t *testing.T) {
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewTxer(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}

		ctx := context.Background()
		expectedErr := fmt.Errorf("block search error")
		reorgErr := mdtypes.NewDetectedReorgError(
			100,
			mdtypes.ReorgDetectionReason_BlockHashMismatch,
			common.HexToHash("0x1234"),
			common.HexToHash("0x5678"),
			"test reorg",
		)

		mockLogger.EXPECT().Debugf(mock.Anything).Once()
		mockPort.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(99)).
			Return(nil, expectedErr).Once()
		mockTx.EXPECT().Rollback().Return(nil).Once()

		err := processor.ProcessReorg(ctx, *reorgErr)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error finding first unaffected block")
		mockPort.AssertExpectations(t)
	})

	t.Run("successfully processes reorg and commits transaction", func(t *testing.T) {
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewTxer(t)

		nowValue := uint64(1234567890)
		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}
		mockPort.EXPECT().TimeNowUnix().Return(nowValue).Maybe()
		ctx := context.Background()
		matchingHash := common.HexToHash("0xabcd")
		offendingBlockNumber := uint64(105)
		firstUnaffectedBlock := uint64(100)
		lastBlockInStorage := uint64(110)
		latestBlockInRPC := uint64(115)
		finalizedBlockInRPC := uint64(100)
		chainID := uint64(1)
		reorgErr := mdtypes.NewDetectedReorgError(
			offendingBlockNumber,
			mdtypes.ReorgDetectionReason_BlockHashMismatch,
			common.HexToHash("0x1234"),
			common.HexToHash("0x5678"),
			"test reorg",
		)

		mockLogger.EXPECT().Infof(mock.Anything, mock.Anything, mock.Anything).Once()
		mockLogger.EXPECT().Warnf(mock.Anything, mock.Anything).Once()
		mockPort.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()

		// findFirstUnaffectedBlock: Block 104 matches (first unaffected)
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, offendingBlockNumber-1).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: firstUnaffectedBlock,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: firstUnaffectedBlock,
					Hash:   matchingHash,
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: firstUnaffectedBlock,
					Hash:   matchingHash,
				},
			}, nil).Once()

		mockPort.EXPECT().GetLastBlockNumberInStorage(mockTx).Return(lastBlockInStorage, nil).Once()
		mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.LatestBlock).Return(latestBlockInRPC, nil).Once()
		mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.FinalizedBlock).Return(finalizedBlockInRPC, nil).Once()
		mockPort.EXPECT().MoveReorgedBlocks(mockTx, mock.Anything).Return(chainID, nil).Once()

		mockTx.EXPECT().Commit().Return(nil).Once()

		err := processor.ProcessReorg(ctx, *reorgErr)

		require.NoError(t, err)
		mockPort.AssertExpectations(t)
	})

	t.Run("returns error and rolls back when GetLastBlockNumberInStorage fails", func(t *testing.T) {
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewTxer(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}

		ctx := context.Background()
		matchingHash := common.HexToHash("0xabcd")
		expectedErr := fmt.Errorf("storage query error")
		reorgErr := mdtypes.NewDetectedReorgError(
			100,
			mdtypes.ReorgDetectionReason_BlockHashMismatch,
			common.HexToHash("0x1234"),
			common.HexToHash("0x5678"),
			"test reorg",
		)

		mockLogger.EXPECT().Debugf(mock.Anything).Once()
		mockPort.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(99)).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: 100,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
			}, nil).Once()
		mockPort.EXPECT().GetLastBlockNumberInStorage(mockTx).Return(uint64(0), expectedErr).Once()
		mockTx.EXPECT().Rollback().Return(nil).Once()

		err := processor.ProcessReorg(ctx, *reorgErr)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error getting last block number in storage")
		mockPort.AssertExpectations(t)
	})

	t.Run("returns error and rolls back when MoveReorgedBlocks fails", func(t *testing.T) {
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewTxer(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}
		mockPort.EXPECT().TimeNowUnix().Return(1234567890).Maybe()
		ctx := context.Background()
		matchingHash := common.HexToHash("0xabcd")
		expectedErr := fmt.Errorf("move blocks error")
		reorgErr := mdtypes.NewDetectedReorgError(
			100,
			mdtypes.ReorgDetectionReason_BlockHashMismatch,
			common.HexToHash("0x1234"),
			common.HexToHash("0x5678"),
			"test reorg",
		)

		mockLogger.EXPECT().Infof(mock.Anything, mock.Anything, mock.Anything).Once()
		mockLogger.EXPECT().Debugf(mock.Anything).Once()
		mockPort.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(99)).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: 100,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
			}, nil).Once()
		mockPort.EXPECT().GetLastBlockNumberInStorage(mockTx).Return(uint64(110), nil).Once()
		mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.LatestBlock).Return(uint64(115), nil).Once()
		mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.FinalizedBlock).Return(uint64(100), nil).Once()
		mockPort.EXPECT().MoveReorgedBlocks(mockTx, mock.Anything).Return(uint64(0), expectedErr).Once()
		mockTx.EXPECT().Rollback().Return(nil).Once()

		err := processor.ProcessReorg(ctx, *reorgErr)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error moving reorged blocks")
		mockPort.AssertExpectations(t)
	})

	t.Run("returns error and rolls back when GetBlockNumberInRPC for latest fails", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewTxer(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}

		ctx := context.Background()
		matchingHash := common.HexToHash("0xabcd")
		expectedErr := fmt.Errorf("RPC error for latest")
		reorgErr := mdtypes.NewDetectedReorgError(
			100,
			mdtypes.ReorgDetectionReason_BlockHashMismatch,
			common.HexToHash("0x1234"),
			common.HexToHash("0x5678"),
			"test reorg",
		)

		mockLogger.EXPECT().Debugf(mock.Anything).Once()
		mockPort.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(99)).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: 100,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
			}, nil).Once()
		mockPort.EXPECT().GetLastBlockNumberInStorage(mockTx).Return(uint64(110), nil).Once()
		mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.LatestBlock).Return(uint64(0), expectedErr).Once()
		mockTx.EXPECT().Rollback().Return(nil).Once()

		err := processor.ProcessReorg(ctx, *reorgErr)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error getting latest block number in RPC")
		mockPort.AssertExpectations(t)
	})

	t.Run("returns error and rolls back when GetBlockNumberInRPC for finalized fails", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewTxer(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}

		ctx := context.Background()
		matchingHash := common.HexToHash("0xabcd")
		expectedErr := fmt.Errorf("RPC error for finalized")
		reorgErr := mdtypes.NewDetectedReorgError(
			100,
			mdtypes.ReorgDetectionReason_BlockHashMismatch,
			common.HexToHash("0x1234"),
			common.HexToHash("0x5678"),
			"test reorg",
		)

		mockLogger.EXPECT().Debugf(mock.Anything).Once()
		mockPort.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(99)).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: 100,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
			}, nil).Once()
		mockPort.EXPECT().GetLastBlockNumberInStorage(mockTx).Return(uint64(110), nil).Once()
		mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.LatestBlock).Return(uint64(115), nil).Once()
		mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.FinalizedBlock).Return(uint64(0), expectedErr).Once()
		mockTx.EXPECT().Rollback().Return(nil).Once()

		err := processor.ProcessReorg(ctx, *reorgErr)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error getting finalized block number in RPC")
		mockPort.AssertExpectations(t)
	})

	t.Run("returns error and rolls back when Commit fails", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewTxer(t)

		nowValue := uint64(1234567890)
		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}
		mockPort.EXPECT().TimeNowUnix().Return(nowValue).Maybe()
		ctx := context.Background()
		matchingHash := common.HexToHash("0xabcd")
		expectedErr := fmt.Errorf("commit failed")
		chainID := uint64(1)
		reorgErr := mdtypes.NewDetectedReorgError(
			100,
			mdtypes.ReorgDetectionReason_BlockHashMismatch,
			common.HexToHash("0x1234"),
			common.HexToHash("0x5678"),
			"test reorg",
		)

		mockLogger.EXPECT().Infof(mock.Anything, mock.Anything, mock.Anything).Once()
		mockPort.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(99)).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: 100,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   matchingHash,
				},
			}, nil).Once()
		mockPort.EXPECT().GetLastBlockNumberInStorage(mockTx).Return(uint64(110), nil).Once()
		mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.LatestBlock).Return(uint64(115), nil).Once()
		mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.FinalizedBlock).Return(uint64(100), nil).Once()
		mockPort.EXPECT().MoveReorgedBlocks(mockTx, mock.Anything).Return(chainID, nil).Once()
		mockTx.EXPECT().Commit().Return(expectedErr).Once()

		err := processor.ProcessReorg(ctx, *reorgErr)

		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot commit tx")
		mockPort.AssertExpectations(t)
	})

	t.Run("returns error when checkBlocks fails in findFirstUnaffectedBlock", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewQuerier(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}

		ctx := context.Background()

		// Return blocks with mismatched block numbers which will cause checkBlocks to error
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(100)).
			Return(&mdtypes.CompareBlockHeaders{
				BlockNumber: 100,
				StorageHeader: &aggkittypes.BlockHeader{
					Number: 100,
					Hash:   common.HexToHash("0x1234"),
				},
				RpcHeader: &aggkittypes.BlockHeader{
					Number: 101, // Different block number will cause error
					Hash:   common.HexToHash("0x1234"),
				},
			}, nil).Once()

		result, err := processor.findFirstUnaffectedBlock(ctx, mockTx, 100)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error checking blocks")
		require.Equal(t, uint64(0), result)
		mockPort.AssertExpectations(t)
	})

	t.Run("logs error when rollback fails", func(t *testing.T) {
		mockLogger := commonmocks.NewLogger(t)
		mockPort := mdmocks.NewReorgPorter(t)
		mockTx := dbmocks.NewTxer(t)

		processor := &ReorgProcessor{
			log:  mockLogger,
			port: mockPort,
		}

		ctx := context.Background()
		rollbackErr := fmt.Errorf("rollback failed")
		originalErr := fmt.Errorf("original error")
		reorgErr := mdtypes.NewDetectedReorgError(
			100,
			mdtypes.ReorgDetectionReason_BlockHashMismatch,
			common.HexToHash("0x1234"),
			common.HexToHash("0x5678"),
			"test reorg",
		)

		mockLogger.EXPECT().Debugf(mock.Anything).Once()
		mockLogger.EXPECT().Errorf(mock.Anything, mock.Anything).Once()
		mockPort.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(99)).
			Return(nil, originalErr).Once()
		mockTx.EXPECT().Rollback().Return(rollbackErr).Once()

		err := processor.ProcessReorg(ctx, *reorgErr)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error finding first unaffected block")
		mockPort.AssertExpectations(t)
	})
}

func TestReorgProcessor_ForcedReorgInDeveloperMode(t *testing.T) {
	testCases := []struct {
		name                      string
		developerMode             bool
		expectedReorgStartBlock   uint64
		expectedReorgDescription  string
	}{
		{
			name:                     "with developerMode enabled - reorgs from detected block",
			developerMode:            true,
			expectedReorgStartBlock:  100,
			expectedReorgDescription: "Reorgs from detected block (overriding first unaffected block)",
		},
		{
			name:                     "with developerMode disabled - reorgs from first unaffected block",
			developerMode:            false,
			expectedReorgStartBlock:  99,
			expectedReorgDescription: "Reorgs from first unaffected block + 1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testForcedReorg(t, tc.developerMode, tc.expectedReorgStartBlock)
		})
	}
}

func testForcedReorg(t *testing.T, developerMode bool, expectedReorgStartBlock uint64) {
	t.Helper()

	logger := log.WithFields("module", "test")
	mockPort := mdmocks.NewReorgPorter(t)
	mockTx := dbmocks.NewTxer(t)

	processor := &ReorgProcessor{
		log:           logger,
		port:          mockPort,
		developerMode: developerMode,
	}

	ctx := context.Background()
	detectedReorgBlock := uint64(100)
	reorgErr := mdtypes.NewDetectedReorgError(
		detectedReorgBlock,
		mdtypes.ReorgDetectionReason_Forced,
		common.Hash{},
		common.Hash{},
		"test reorg",
	)
	nowTimestamp := uint64(1234567890)
	lastBlockInStorage := uint64(110)
	latestBlockInRPC := uint64(115)
	finalizedBlockInRPC := uint64(100)

	// Setup mock expectations
	mockPort.EXPECT().TimeNowUnix().Return(nowTimestamp).Maybe()
	mockPort.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()

	// Mock block 99 - mismatch
	mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(99)).
		Return(&types.CompareBlockHeaders{
			BlockNumber: 99,
			StorageHeader: &aggkittypes.BlockHeader{
				Number: 99,
				Hash:   common.HexToHash("0x1234"),
			},
			RpcHeader: &aggkittypes.BlockHeader{
				Number: 99,
				Hash:   common.HexToHash("0x5678"),
			},
		}, nil).Once()

	// Mock block 98 - match (first unaffected block)
	mockPort.EXPECT().GetBlockStorageAndRPC(ctx, mockTx, uint64(98)).
		Return(&types.CompareBlockHeaders{
			BlockNumber: 98,
			StorageHeader: &aggkittypes.BlockHeader{
				Number: 98,
				Hash:   common.HexToHash("0x1234"),
			},
			RpcHeader: &aggkittypes.BlockHeader{
				Number: 98,
				Hash:   common.HexToHash("0x1234"),
			},
		}, nil).Once()

	mockPort.EXPECT().GetLastBlockNumberInStorage(mockTx).Return(lastBlockInStorage, nil).Once()
	mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.LatestBlock).Return(latestBlockInRPC, nil).Once()
	mockPort.EXPECT().GetBlockNumberInRPC(ctx, aggkittypes.FinalizedBlock).Return(finalizedBlockInRPC, nil).Once()

	expectedReorgData := mdtypes.ReorgData{
		BlockRangeAffected:        aggkitcommon.NewBlockRange(expectedReorgStartBlock, lastBlockInStorage),
		DetectedAtBlock:           detectedReorgBlock,
		DetectedTimestamp:         nowTimestamp,
		NetworkLatestBlock:        latestBlockInRPC,
		NetworkFinalizedBlock:     finalizedBlockInRPC,
		NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
		Description:               reorgErr.Error(),
	}
	mockPort.EXPECT().MoveReorgedBlocks(mockTx, expectedReorgData).Return(uint64(1), nil).Once()
	mockTx.EXPECT().Commit().Return(nil).Once()

	err := processor.ProcessReorg(ctx, *reorgErr)

	require.NoError(t, err)
	mockPort.AssertExpectations(t)
	mockTx.AssertExpectations(t)
}
