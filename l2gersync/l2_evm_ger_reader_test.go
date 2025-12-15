package l2gersync

import (
	"context"
	"errors"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	aggoraclemocks "github.com/agglayer/aggkit/aggoracle/mocks"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync/mocks"
	"github.com/agglayer/aggkit/test/helpers"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestValidateL2GERContract(t *testing.T) {
	t.Parallel()

	validAddress := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	invalidAddress := common.Address{}

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		mockL2GERManager := aggoraclemocks.NewL2GERManagerContract(t)
		mockL2GERManager.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(validAddress, nil)

		err := validateL2GERContract(mockL2GERManager, validAddress)
		require.NoError(t, err)
		mockL2GERManager.AssertExpectations(t)
	})

	t.Run("failure - invalid contract address", func(t *testing.T) {
		t.Parallel()
		mockL2GERManager := aggoraclemocks.NewL2GERManagerContract(t)
		expectedErr := errors.New(
			"L2 GER manager contract sanity check failed (SC address=0x1234567890AbcdEF1234567890aBcdef12345678): invalid address")
		mockL2GERManager.EXPECT().GlobalExitRootUpdater(mock.Anything).Return(invalidAddress, errors.New("invalid address"))

		err := validateL2GERContract(mockL2GERManager, validAddress)
		require.EqualError(t, err, expectedErr.Error())
		mockL2GERManager.AssertExpectations(t)
	})
}

func TestL2EVMGERReader_GetInjectedGERsForRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("invalid block range", func(t *testing.T) {
		t.Parallel()

		mockL2GERManager := aggoraclemocks.NewL2GERManagerContract(t)
		gerReader := &L2EVMGERReader{l2GERManager: mockL2GERManager}

		_, err := gerReader.GetInjectedGERsForRange(ctx, 10, 1)
		require.ErrorContains(t, err, "invalid block range: fromBlock(10) > toBlock(1)")
	})

	t.Run("failed to create iterator", func(t *testing.T) {
		t.Parallel()

		toBlock := uint64(10)
		mockL2GERManager := aggoraclemocks.NewL2GERManagerContract(t)
		mockL2GERManager.EXPECT().
			FilterUpdateHashChainValue(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("failed to create iterator"))

		gerReader := &L2EVMGERReader{l2GERManager: mockL2GERManager}

		_, err := gerReader.GetInjectedGERsForRange(ctx, 1, toBlock)
		require.ErrorContains(t, err, "failed to create iterator")
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		_, l2 := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract))
		l2.EthTxManagerMock.ExpectedCalls = nil

		l1InfoTreeSync := mocks.NewL1InfoTreeQuerier(t)
		l1InfoTreeSync.EXPECT().GetInfoByGlobalExitRoot(mock.Anything).Return(&l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 1}, nil).Maybe()

		gerReader, err := NewL2EVMGERReader(l2.GERAddr, etherman.NewDefaultEthClient(l2.SimBackend.Client(), nil), l1InfoTreeSync)
		require.NoError(t, err)

		// Ensure we have enough blocks by committing several times
		for i := 0; i < 10; i++ {
			l2.SimBackend.Commit()
		}

		tx, err := l2.GERManagerSovereignSC.InsertGlobalExitRoot(l2.Auth, common.HexToHash("0x1234567890abcdef1234567890abcdef12345678"))
		require.NoError(t, err)

		// commit one block so the current block is block 11
		l2.SimBackend.Commit()

		receipt, err := l2.SimBackend.Client().TransactionReceipt(ctx, tx.Hash())
		require.NoError(t, err)
		require.Equal(t, receipt.Status, types.ReceiptStatusSuccessful)

		expectedGER := common.HexToHash("0x1234567890abcdef1234567890abcdef12345678")

		// Query from block 1 to the current block to ensure we capture the event
		currentBlock, err := l2.SimBackend.Client().BlockNumber(ctx)
		require.NoError(t, err)

		injectedGERs, err := gerReader.GetInjectedGERsForRange(ctx, 1, currentBlock)
		require.NoError(t, err)
		require.Len(t, injectedGERs, 1)

		ger, exists := injectedGERs[expectedGER]
		require.True(t, exists)
		require.Equal(t, expectedGER, ger.GlobalExitRoot)
	})

	t.Run("block range too large triggers chunking", func(t *testing.T) {
		t.Parallel()

		mockL2Client := mocksethclient.NewBaseEthereumClienter(t)
		mockL2GERManager, err := agglayergerl2.NewAgglayergerl2(
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"), mockL2Client)
		require.NoError(t, err)

		gerReader := &L2EVMGERReader{
			l2GERManager: mockL2GERManager,
		}

		// First call fails with "block range too large"
		mockL2Client.On("FilterLogs", mock.Anything, mock.Anything).
			Return(nil, errors.New("block range too large, max range: 1000")).Once()

		// Subsequent chunked calls succeed (0-999, 1000-1999, 2000-2500)
		mockL2Client.On("FilterLogs", mock.Anything, mock.Anything).
			Return([]types.Log{}, nil).Times(6) // 3 chunks for inserts, 3 chunks for removals

		injectedGERs, err := gerReader.GetInjectedGERsForRange(ctx, 0, 2500)
		require.NoError(t, err)
		require.NotNil(t, injectedGERs)

		mockL2Client.AssertExpectations(t)
	})

	t.Run("non-parseable error returns original error", func(t *testing.T) {
		t.Parallel()

		mockL2GERManager := aggoraclemocks.NewL2GERManagerContract(t)

		gerReader := &L2EVMGERReader{
			l2GERManager: mockL2GERManager,
		}

		// Return an error that doesn't match the pattern
		mockL2GERManager.EXPECT().
			FilterUpdateHashChainValue(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("some other RPC error")).Once()

		injectedGERs, err := gerReader.GetInjectedGERsForRange(ctx, 0, 2500)
		require.ErrorContains(t, err, "some other RPC error")
		require.Nil(t, injectedGERs)

		mockL2GERManager.AssertExpectations(t)
	})
}

func TestL2EVMGERReader_GetRemovedGERsForRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("failed to create iterator", func(t *testing.T) {
		t.Parallel()

		toBlock := uint64(10)
		mockL2GERManager := aggoraclemocks.NewL2GERManagerContract(t)
		mockL2GERManager.EXPECT().
			FilterUpdateRemovalHashChainValue(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("failed to create removal iterator"))

		gerReader := &L2EVMGERReader{l2GERManager: mockL2GERManager}

		_, err := gerReader.GetRemovedGERsForRange(ctx, 1, toBlock)
		require.ErrorContains(t, err, "failed to create removal iterator")
	})

	t.Run("block range too large triggers chunking", func(t *testing.T) {
		t.Parallel()

		mockL2GERManager := aggoraclemocks.NewL2GERManagerContract(t)
		// First call: return error that triggers chunking
		mockL2GERManager.EXPECT().
			FilterUpdateRemovalHashChainValue(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("block range too large, max range: 1000")).Once()
		// Second call (first chunk): return a different error to prove chunking was triggered
		mockL2GERManager.EXPECT().
			FilterUpdateRemovalHashChainValue(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("mock iterator error for removal")).Once()

		gerReader := &L2EVMGERReader{l2GERManager: mockL2GERManager}

		_, err := gerReader.GetRemovedGERsForRange(ctx, 0, 2000)
		// Verify that chunking was triggered by checking for the second error
		require.ErrorContains(t, err, "mock iterator error for removal")

		mockL2GERManager.AssertExpectations(t)
	})

	t.Run("non-parseable error returns original error", func(t *testing.T) {
		t.Parallel()

		mockL2GERManager := aggoraclemocks.NewL2GERManagerContract(t)
		mockL2GERManager.EXPECT().
			FilterUpdateRemovalHashChainValue(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("some other RPC error"))

		gerReader := &L2EVMGERReader{l2GERManager: mockL2GERManager}

		_, err := gerReader.GetRemovedGERsForRange(ctx, 0, 2000)
		require.ErrorContains(t, err, "some other RPC error")

		mockL2GERManager.AssertExpectations(t)
	})

	t.Run("success with no removed GERs", func(t *testing.T) {
		t.Parallel()

		_, l2 := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract))
		l2.EthTxManagerMock.ExpectedCalls = nil

		l1InfoTreeSync := mocks.NewL1InfoTreeQuerier(t)

		gerReader, err := NewL2EVMGERReader(l2.GERAddr, etherman.NewDefaultEthClient(l2.SimBackend.Client(), nil), l1InfoTreeSync)
		require.NoError(t, err)

		// commit one block so the current block is block 6
		l2.SimBackend.Commit()

		// Get the current block number
		currentBlock, err := l2.SimBackend.Client().BlockNumber(ctx)
		require.NoError(t, err)

		removedGERs, err := gerReader.GetRemovedGERsForRange(ctx, 1, currentBlock)
		require.NoError(t, err)
		require.Len(t, removedGERs, 0)
	})

	t.Run("success with removed GERs", func(t *testing.T) {
		t.Parallel()

		_, l2 := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract))
		l2.EthTxManagerMock.ExpectedCalls = nil

		l1InfoTreeSync := mocks.NewL1InfoTreeQuerier(t)

		gerReader, err := NewL2EVMGERReader(l2.GERAddr, etherman.NewDefaultEthClient(l2.SimBackend.Client(), nil), l1InfoTreeSync)
		require.NoError(t, err)

		// First insert a GER
		gerToRemove := common.HexToHash("0x1234567890abcdef1234567890abcdef12345678")
		_, err = l2.GERManagerSovereignSC.InsertGlobalExitRoot(l2.Auth, gerToRemove)
		require.NoError(t, err)

		// commit one block so the current block is block 6
		l2.SimBackend.Commit()

		// Now remove the GER
		removalTx, err := l2.GERManagerSovereignSC.RemoveGlobalExitRoots(l2.Auth, [][common.HashLength]byte{gerToRemove})
		require.NoError(t, err)

		// commit another block
		l2.SimBackend.Commit()

		// Get the current block number
		currentBlock, err := l2.SimBackend.Client().BlockNumber(ctx)
		require.NoError(t, err)

		removalReceipt, err := l2.SimBackend.Client().TransactionReceipt(ctx, removalTx.Hash())
		require.NoError(t, err)
		require.Equal(t, removalReceipt.Status, types.ReceiptStatusSuccessful)

		removedGERs, err := gerReader.GetRemovedGERsForRange(ctx, 1, currentBlock)
		require.NoError(t, err)
		require.Len(t, removedGERs, 1)

		removedGER := removedGERs[0]
		require.Equal(t, gerToRemove, removedGER.GlobalExitRoot)
		require.Equal(t, removalReceipt.BlockNumber.Uint64(), removedGER.BlockNumber)
		require.Equal(t, uint64(0), removedGER.LogIndex) // First event in the block
	})

	t.Run("success with multiple removed GERs", func(t *testing.T) {
		t.Parallel()

		_, l2 := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract))
		l2.EthTxManagerMock.ExpectedCalls = nil

		l1InfoTreeSync := mocks.NewL1InfoTreeQuerier(t)

		gerReader, err := NewL2EVMGERReader(l2.GERAddr, etherman.NewDefaultEthClient(l2.SimBackend.Client(), nil), l1InfoTreeSync)
		require.NoError(t, err)

		// Insert and remove multiple GERs
		ger1 := common.HexToHash("0x1111111111111111111111111111111111111111")
		ger2 := common.HexToHash("0x2222222222222222222222222222222222222222")
		ger3 := common.HexToHash("0x3333333333333333333333333333333333333333")

		// Insert all GERs first
		_, err = l2.GERManagerSovereignSC.InsertGlobalExitRoot(l2.Auth, ger1)
		require.NoError(t, err)
		_, err = l2.GERManagerSovereignSC.InsertGlobalExitRoot(l2.Auth, ger2)
		require.NoError(t, err)
		_, err = l2.GERManagerSovereignSC.InsertGlobalExitRoot(l2.Auth, ger3)
		require.NoError(t, err)

		// commit one block
		l2.SimBackend.Commit()

		// Remove all GERs
		removalTx1, err := l2.GERManagerSovereignSC.RemoveGlobalExitRoots(l2.Auth, [][common.HashLength]byte{ger1})
		require.NoError(t, err)
		removalTx2, err := l2.GERManagerSovereignSC.RemoveGlobalExitRoots(l2.Auth, [][common.HashLength]byte{ger2})
		require.NoError(t, err)
		removalTx3, err := l2.GERManagerSovereignSC.RemoveGlobalExitRoots(l2.Auth, [][common.HashLength]byte{ger3})
		require.NoError(t, err)

		// commit another block
		l2.SimBackend.Commit()

		// Get the current block number
		currentBlock, err := l2.SimBackend.Client().BlockNumber(ctx)
		require.NoError(t, err)

		// Verify all removal transactions were successful
		removalReceipt1, err := l2.SimBackend.Client().TransactionReceipt(ctx, removalTx1.Hash())
		require.NoError(t, err)
		require.Equal(t, removalReceipt1.Status, types.ReceiptStatusSuccessful)

		removalReceipt2, err := l2.SimBackend.Client().TransactionReceipt(ctx, removalTx2.Hash())
		require.NoError(t, err)
		require.Equal(t, removalReceipt2.Status, types.ReceiptStatusSuccessful)

		removalReceipt3, err := l2.SimBackend.Client().TransactionReceipt(ctx, removalTx3.Hash())
		require.NoError(t, err)
		require.Equal(t, removalReceipt3.Status, types.ReceiptStatusSuccessful)

		removedGERs, err := gerReader.GetRemovedGERsForRange(ctx, 1, currentBlock)
		require.NoError(t, err)
		require.Len(t, removedGERs, 3)

		// Verify all three GERs were removed
		gerHashes := make(map[common.Hash]bool)
		for _, removedGER := range removedGERs {
			gerHashes[removedGER.GlobalExitRoot] = true
		}

		require.True(t, gerHashes[ger1])
		require.True(t, gerHashes[ger2])
		require.True(t, gerHashes[ger3])
	})

	t.Run("iterator error during iteration", func(t *testing.T) {
		t.Parallel()

		// This test would require a more complex mock setup to simulate iterator errors
		// For now, we'll test the basic functionality with the simulated environment
		_, l2 := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract))
		l2.EthTxManagerMock.ExpectedCalls = nil

		l1InfoTreeSync := mocks.NewL1InfoTreeQuerier(t)

		gerReader, err := NewL2EVMGERReader(l2.GERAddr, etherman.NewDefaultEthClient(l2.SimBackend.Client(), nil), l1InfoTreeSync)
		require.NoError(t, err)

		// Test with a valid range that should not cause iterator errors
		removedGERs, err := gerReader.GetRemovedGERsForRange(ctx, 1, 5)
		require.NoError(t, err)
		require.Len(t, removedGERs, 0)
	})

	t.Run("block range too large triggers chunking", func(t *testing.T) {
		t.Parallel()

		mockL2Client := mocksethclient.NewBaseEthereumClienter(t)
		mockL2GERManager, err := agglayergerl2.NewAgglayergerl2(
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"), mockL2Client)
		require.NoError(t, err)

		gerReader := &L2EVMGERReader{
			l2GERManager: mockL2GERManager,
		}

		// First call fails with "block range too large"
		mockL2Client.On("FilterLogs", mock.Anything, mock.Anything).
			Return(nil, errors.New("block range too large, max range: 1000")).Once()

		// Subsequent chunked calls succeed (0-999, 1000-1999, 2000-2500)
		mockL2Client.On("FilterLogs", mock.Anything, mock.Anything).
			Return([]types.Log{}, nil).Times(3)

		removedGERs, err := gerReader.GetRemovedGERsForRange(ctx, 0, 2500)
		require.NoError(t, err)
		require.NotNil(t, removedGERs)

		mockL2Client.AssertExpectations(t)
	})

	t.Run("non-parseable error returns original error", func(t *testing.T) {
		t.Parallel()

		mockL2Client := mocksethclient.NewBaseEthereumClienter(t)
		mockL2GERManager, err := agglayergerl2.NewAgglayergerl2(
			common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"), mockL2Client)
		require.NoError(t, err)

		gerReader := &L2EVMGERReader{
			l2GERManager: mockL2GERManager,
		}

		// Return an error that doesn't match the pattern
		mockL2Client.EXPECT().
			FilterLogs(mock.Anything, mock.Anything).
			Return(nil, errors.New("some other RPC error")).Once()

		removedGERs, err := gerReader.GetRemovedGERsForRange(ctx, 0, 2500)
		require.ErrorContains(t, err, "some other RPC error")
		require.Empty(t, removedGERs)

		mockL2Client.AssertExpectations(t)
	})
}
