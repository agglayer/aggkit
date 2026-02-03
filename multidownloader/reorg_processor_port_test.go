package multidownloader

import (
	"context"
	"fmt"
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	dbmocks "github.com/agglayer/aggkit/db/mocks"
	"github.com/agglayer/aggkit/etherman"
	mdtypes "github.com/agglayer/aggkit/multidownloader/types"
	mdmocks "github.com/agglayer/aggkit/multidownloader/types/mocks"
	aggkittypes "github.com/agglayer/aggkit/types"
	typesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestReorgPort_NewTx(t *testing.T) {
	t.Run("successfully creates new transaction", func(t *testing.T) {
		mockStorage := mdmocks.NewStorager(t)
		mockTx := dbmocks.NewTxer(t)

		reorgPort := &ReorgPort{
			storage: mockStorage,
		}

		ctx := context.Background()
		mockStorage.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()

		result, err := reorgPort.NewTx(ctx)

		require.NoError(t, err)
		require.Equal(t, mockTx, result)
	})

	t.Run("returns error when NewTx fails", func(t *testing.T) {
		mockStorage := mdmocks.NewStorager(t)

		reorgPort := &ReorgPort{
			storage: mockStorage,
		}

		ctx := context.Background()
		expectedErr := fmt.Errorf("database connection error")
		mockStorage.EXPECT().NewTx(ctx).Return(nil, expectedErr).Once()

		result, err := reorgPort.NewTx(ctx)

		require.Error(t, err)
		require.Equal(t, expectedErr, err)
		require.Nil(t, result)
	})
}

func TestReorgPort_GetBlockStorageAndRPC(t *testing.T) {
	t.Run("successfully gets block from both storage and RPC", func(t *testing.T) {
		mockStorage := mdmocks.NewStorager(t)
		mockEthClient := typesmocks.NewBaseEthereumClienter(t)
		mockTx := dbmocks.NewQuerier(t)

		reorgPort := &ReorgPort{
			storage:   mockStorage,
			ethClient: mockEthClient,
		}

		ctx := context.Background()
		blockNumber := uint64(100)

		storageHeader := &aggkittypes.BlockHeader{
			Number: blockNumber,
			Hash:   common.HexToHash("0x1234"),
		}
		rpcHeader := &aggkittypes.BlockHeader{
			Number: blockNumber,
			Hash:   common.HexToHash("0x1234"),
		}

		mockStorage.EXPECT().GetBlockHeaderByNumber(mockTx, blockNumber).
			Return(storageHeader, mdtypes.Finalized, nil).Once()
		mockEthClient.EXPECT().CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber)).
			Return(rpcHeader, nil).Once()

		result, err := reorgPort.GetBlockStorageAndRPC(ctx, mockTx, blockNumber)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, blockNumber, result.BlockNumber)
		require.Equal(t, storageHeader, result.StorageHeader)
		require.Equal(t, rpcHeader, result.RpcHeader)
		require.Equal(t, mdtypes.Finalized, result.IsFinalized)
	})

	t.Run("returns error when storage GetBlockHeaderByNumber fails", func(t *testing.T) {
		mockStorage := mdmocks.NewStorager(t)
		mockEthClient := typesmocks.NewBaseEthereumClienter(t)
		mockTx := dbmocks.NewQuerier(t)

		reorgPort := &ReorgPort{
			storage:   mockStorage,
			ethClient: mockEthClient,
		}

		ctx := context.Background()
		blockNumber := uint64(100)
		expectedErr := fmt.Errorf("storage error")

		mockStorage.EXPECT().GetBlockHeaderByNumber(mockTx, blockNumber).
			Return(nil, mdtypes.NotFinalized, expectedErr).Once()

		result, err := reorgPort.GetBlockStorageAndRPC(ctx, mockTx, blockNumber)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error getting block in storage")
		require.Nil(t, result)
	})

	t.Run("returns error when RPC CustomHeaderByNumber fails with non-NotFound error", func(t *testing.T) {
		mockStorage := mdmocks.NewStorager(t)
		mockEthClient := typesmocks.NewBaseEthereumClienter(t)
		mockTx := dbmocks.NewQuerier(t)

		reorgPort := &ReorgPort{
			storage:   mockStorage,
			ethClient: mockEthClient,
		}

		ctx := context.Background()
		blockNumber := uint64(100)

		storageHeader := &aggkittypes.BlockHeader{
			Number: blockNumber,
			Hash:   common.HexToHash("0x1234"),
		}
		expectedErr := fmt.Errorf("RPC connection error")

		mockStorage.EXPECT().GetBlockHeaderByNumber(mockTx, blockNumber).
			Return(storageHeader, mdtypes.Finalized, nil).Once()
		mockEthClient.EXPECT().CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber)).
			Return(nil, expectedErr).Once()

		result, err := reorgPort.GetBlockStorageAndRPC(ctx, mockTx, blockNumber)

		require.Error(t, err)
		require.Contains(t, err.Error(), "error getting block in RPC")
		require.Nil(t, result)
	})

	t.Run("handles NotFound error from RPC gracefully", func(t *testing.T) {
		mockStorage := mdmocks.NewStorager(t)
		mockEthClient := typesmocks.NewBaseEthereumClienter(t)
		mockTx := dbmocks.NewQuerier(t)

		reorgPort := &ReorgPort{
			storage:   mockStorage,
			ethClient: mockEthClient,
		}

		ctx := context.Background()
		blockNumber := uint64(100)

		storageHeader := &aggkittypes.BlockHeader{
			Number: blockNumber,
			Hash:   common.HexToHash("0x1234"),
		}

		mockStorage.EXPECT().GetBlockHeaderByNumber(mockTx, blockNumber).
			Return(storageHeader, mdtypes.Finalized, nil).Once()
		mockEthClient.EXPECT().CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber)).
			Return(nil, etherman.ErrNotFound).Once()

		result, err := reorgPort.GetBlockStorageAndRPC(ctx, mockTx, blockNumber)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, blockNumber, result.BlockNumber)
		require.Equal(t, storageHeader, result.StorageHeader)
		require.Nil(t, result.RpcHeader)
		require.Equal(t, mdtypes.Finalized, result.IsFinalized)
	})
}

func TestReorgPort_GetLastBlockNumberInStorage(t *testing.T) {
	t.Run("successfully gets highest block number", func(t *testing.T) {
		mockStorage := mdmocks.NewStorager(t)
		mockTx := dbmocks.NewQuerier(t)

		reorgPort := &ReorgPort{
			storage: mockStorage,
		}

		expectedBlockNumber := uint64(12345)
		mockStorage.EXPECT().GetHighestBlockNumber(mock.Anything).
			Return(expectedBlockNumber, nil).Once()

		result, err := reorgPort.GetLastBlockNumberInStorage(mockTx)

		require.NoError(t, err)
		require.Equal(t, expectedBlockNumber, result)
	})

	t.Run("returns error when GetHighestBlockNumber fails", func(t *testing.T) {
		mockStorage := mdmocks.NewStorager(t)
		mockTx := dbmocks.NewQuerier(t)

		reorgPort := &ReorgPort{
			storage: mockStorage,
		}

		expectedErr := fmt.Errorf("database query error")
		mockStorage.EXPECT().GetHighestBlockNumber(mock.Anything).
			Return(uint64(0), expectedErr).Once()

		result, err := reorgPort.GetLastBlockNumberInStorage(mockTx)

		require.Error(t, err)
		require.Contains(t, err.Error(), "GetLastBlockNumberInStorage")
		require.Contains(t, err.Error(), "error getting highest block from storage")
		require.Equal(t, uint64(0), result)
	})
}

func TestReorgPort_MoveReorgedBlocks(t *testing.T) {
	t.Run("successfully moves reorged blocks", func(t *testing.T) {
		mockStorage := mdmocks.NewStorager(t)
		mockTx := dbmocks.NewQuerier(t)

		reorgPort := &ReorgPort{
			storage: mockStorage,
		}

		reorgData := mdtypes.ReorgData{
			ChainID:            1,
			BlockRangeAffected: aggkitcommon.NewBlockRange(100, 200),
		}
		expectedAffectedRows := uint64(101)

		mockStorage.EXPECT().InsertReorgAndMoveReorgedBlocksAndLogs(mockTx, reorgData).
			Return(expectedAffectedRows, nil).Once()

		result, err := reorgPort.MoveReorgedBlocks(mockTx, reorgData)

		require.NoError(t, err)
		require.Equal(t, expectedAffectedRows, result)
	})

	t.Run("returns error when InsertReorgAndMoveReorgedBlocksAndLogs fails", func(t *testing.T) {
		mockStorage := mdmocks.NewStorager(t)
		mockTx := dbmocks.NewQuerier(t)

		reorgPort := &ReorgPort{
			storage: mockStorage,
		}

		reorgData := mdtypes.ReorgData{
			ChainID:            1,
			BlockRangeAffected: aggkitcommon.NewBlockRange(100, 200),
		}
		expectedErr := fmt.Errorf("transaction failed")

		mockStorage.EXPECT().InsertReorgAndMoveReorgedBlocksAndLogs(mockTx, reorgData).
			Return(uint64(0), expectedErr).Once()

		result, err := reorgPort.MoveReorgedBlocks(mockTx, reorgData)

		require.Error(t, err)
		require.Equal(t, expectedErr, err)
		require.Equal(t, uint64(0), result)
	})
}

func TestReorgPort_GetBlockNumberInRPC(t *testing.T) {
	t.Run("successfully gets block number from RPC with latest finality", func(t *testing.T) {
		mockEthClient := typesmocks.NewBaseEthereumClienter(t)

		reorgPort := &ReorgPort{
			ethClient: mockEthClient,
		}

		ctx := context.Background()
		blockFinality := aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest}
		expectedBlockNumber := uint64(500)

		rpcHeader := &aggkittypes.BlockHeader{
			Number: expectedBlockNumber,
			Hash:   common.HexToHash("0xabcd"),
		}

		mockEthClient.EXPECT().CustomHeaderByNumber(ctx, &blockFinality).
			Return(rpcHeader, nil).Once()

		result, err := reorgPort.GetBlockNumberInRPC(ctx, blockFinality)

		require.NoError(t, err)
		require.Equal(t, expectedBlockNumber, result)
	})

	t.Run("successfully gets block number from RPC with finalized finality", func(t *testing.T) {
		mockEthClient := typesmocks.NewBaseEthereumClienter(t)

		reorgPort := &ReorgPort{
			ethClient: mockEthClient,
		}

		ctx := context.Background()
		blockFinality := aggkittypes.BlockNumberFinality{Block: aggkittypes.Finalized}
		expectedBlockNumber := uint64(450)

		rpcHeader := &aggkittypes.BlockHeader{
			Number: expectedBlockNumber,
			Hash:   common.HexToHash("0xdef0"),
		}

		mockEthClient.EXPECT().CustomHeaderByNumber(ctx, &blockFinality).
			Return(rpcHeader, nil).Once()

		result, err := reorgPort.GetBlockNumberInRPC(ctx, blockFinality)

		require.NoError(t, err)
		require.Equal(t, expectedBlockNumber, result)
	})

	t.Run("returns error when CustomHeaderByNumber fails", func(t *testing.T) {
		mockEthClient := typesmocks.NewBaseEthereumClienter(t)

		reorgPort := &ReorgPort{
			ethClient: mockEthClient,
		}

		ctx := context.Background()
		blockFinality := aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest}
		expectedErr := fmt.Errorf("RPC connection timeout")

		mockEthClient.EXPECT().CustomHeaderByNumber(ctx, &blockFinality).
			Return(nil, expectedErr).Once()

		result, err := reorgPort.GetBlockNumberInRPC(ctx, blockFinality)

		require.Error(t, err)
		require.Contains(t, err.Error(), "GetBlockNumberInRPC")
		require.Contains(t, err.Error(), "error getting block number")
		require.Equal(t, uint64(0), result)
	})
}
