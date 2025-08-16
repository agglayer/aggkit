package l2gersync

import (
	"context"
	"errors"
	"testing"

	aggoraclemocks "github.com/agglayer/aggkit/aggoracle/mocks"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync/mocks"
	"github.com/agglayer/aggkit/test/helpers"
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

		gerReader, err := NewL2EVMGERReader(l2.GERAddr, l2.SimBackend.Client(), l1InfoTreeSync)
		require.NoError(t, err)

		tx, err := l2.GERManagerSovereignSC.InsertGlobalExitRoot(l2.Auth, common.HexToHash("0x1234567890abcdef1234567890abcdef12345678"))
		require.NoError(t, err)

		// commit one block so the current block is block 6
		l2.SimBackend.Commit()

		receipt, err := l2.SimBackend.Client().TransactionReceipt(ctx, tx.Hash())
		require.NoError(t, err)
		require.Equal(t, receipt.Status, types.ReceiptStatusSuccessful)

		expectedGER := common.HexToHash("0x1234567890abcdef1234567890abcdef12345678")

		injectedGERs, err := gerReader.GetInjectedGERsForRange(ctx, 1, 10)
		require.NoError(t, err)
		require.Len(t, injectedGERs, 1)

		ger, exists := injectedGERs[expectedGER]
		require.True(t, exists)
		require.Equal(t, expectedGER, ger.GlobalExitRoot)
	})
}

func TestL2EVMGERReader_GetRemovedGERsForRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("invalid block range", func(t *testing.T) {
		t.Parallel()

		mockL2GERManager := aggoraclemocks.NewL2GERManagerContract(t)
		// The method doesn't validate block ranges, so it will call the contract method
		mockL2GERManager.EXPECT().
			FilterUpdateRemovalHashChainValue(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("contract error"))

		gerReader := &L2EVMGERReader{l2GERManager: mockL2GERManager}

		_, err := gerReader.GetRemovedGERsForRange(ctx, 10, 1)
		require.ErrorContains(t, err, "contract error")
	})

	t.Run("failed to create iterator", func(t *testing.T) {
		t.Parallel()

		toBlock := uint64(10)
		mockL2GERManager := aggoraclemocks.NewL2GERManagerContract(t)
		mockL2GERManager.EXPECT().
			FilterUpdateRemovalHashChainValue(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("failed to create iterator"))

		gerReader := &L2EVMGERReader{l2GERManager: mockL2GERManager}

		_, err := gerReader.GetRemovedGERsForRange(ctx, 1, toBlock)
		require.ErrorContains(t, err, "failed to create iterator")
	})

	t.Run("success - no removed GERs", func(t *testing.T) {
		t.Parallel()

		_, l2 := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract))
		l2.EthTxManagerMock.ExpectedCalls = nil

		l1InfoTreeSync := mocks.NewL1InfoTreeQuerier(t)

		gerReader, err := NewL2EVMGERReader(l2.GERAddr, l2.SimBackend.Client(), l1InfoTreeSync)
		require.NoError(t, err)

		// commit one block so the current block is block 6
		l2.SimBackend.Commit()

		removedGERs, err := gerReader.GetRemovedGERsForRange(ctx, 1, 10)
		require.NoError(t, err)
		require.Len(t, removedGERs, 0)
	})
}
