package chaingerreader

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggoracle/mocks"
	"github.com/agglayer/aggkit/test/helpers"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewChainGERReader(t *testing.T) {
	t.Parallel()

	validAddress := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	invalidAddress := common.Address{}

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		mockL2GERManager := mocks.NewL2GERManagerContract(t)
		mockL2GERManager.On("BridgeAddress", (*bind.CallOpts)(nil)).Return(validAddress, nil)

		gerReader, err := newEVMChainGERReader(mockL2GERManager, validAddress)
		require.NoError(t, err)
		require.NotNil(t, gerReader)
		mockL2GERManager.AssertExpectations(t)
	})

	t.Run("failure - invalid contract address", func(t *testing.T) {
		t.Parallel()
		mockL2GERManager := mocks.NewL2GERManagerContract(t)
		mockL2GERManager.On("BridgeAddress", (*bind.CallOpts)(nil)).Return(invalidAddress, errors.New("invalid address"))

		l2Etherman, err := newEVMChainGERReader(mockL2GERManager, invalidAddress)
		require.Error(t, err)
		require.Nil(t, l2Etherman)
		mockL2GERManager.AssertExpectations(t)
	})
}

func TestGetInjectedGERsForRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("invalid block range", func(t *testing.T) {
		t.Parallel()

		mockL2GERManager := mocks.NewL2GERManagerContract(t)
		gerReader := &EVMChainGERReader{l2GERManager: mockL2GERManager}

		_, err := gerReader.GetInjectedGERsForRange(ctx, 10, 1)
		require.ErrorContains(t, err, "invalid block range: fromBlock(10) > toBlock(1)")
	})

	t.Run("failed to create iterator", func(t *testing.T) {
		t.Parallel()

		toBlock := uint64(10)
		mockL2GERManager := mocks.NewL2GERManagerContract(t)
		mockL2GERManager.On("FilterUpdateHashChainValue", &bind.FilterOpts{
			Context: ctx,
			Start:   1,
			End:     &toBlock,
		}, mock.Anything, mock.Anything).Return(nil, errors.New("failed to create iterator"))

		gerReader := &EVMChainGERReader{l2GERManager: mockL2GERManager}

		_, err := gerReader.GetInjectedGERsForRange(ctx, 1, toBlock)
		require.ErrorContains(t, err, "failed to create iterator")
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := helpers.L2Setup(t, helpers.DefaultEnvironmentConfig())
		setup.EthTxManagerMock.ExpectedCalls = nil

		gerReader, err := NewEVMChainGERReader(setup.GERAddr, setup.SimBackend.Client())
		require.NoError(t, err)

		tx, err := setup.GERContract.InsertGlobalExitRoot(setup.Auth, common.HexToHash("0x1234567890abcdef1234567890abcdef12345678"))
		require.NoError(t, err)

		// commit one block so the current block is block 6
		setup.SimBackend.Commit()

		receipt, err := setup.SimBackend.Client().TransactionReceipt(ctx, tx.Hash())
		require.NoError(t, err)
		require.Equal(t, receipt.Status, types.ReceiptStatusSuccessful)

		expectedGER := common.HexToHash("0x1234567890abcdef1234567890abcdef12345678")

		injectedGERs, err := gerReader.GetInjectedGERsForRange(ctx, 1, 10)
		require.NoError(t, err)
		require.Len(t, injectedGERs, 1)

		ger, exists := injectedGERs[expectedGER]
		require.True(t, exists)
		require.Equal(t, expectedGER, ger.GlobalExitRoot)

		// commit one block so the current block is block 7
		setup.SimBackend.Commit()

		tx, err = setup.GERContract.RemoveGlobalExitRoots(setup.Auth, [][32]byte{expectedGER})
		require.NoError(t, err)

		// commit one block so the current block is block 8
		setup.SimBackend.Commit()

		receipt, err = setup.SimBackend.Client().TransactionReceipt(ctx, tx.Hash())
		require.NoError(t, err)
		require.Equal(t, receipt.Status, types.ReceiptStatusSuccessful)

		injectedGERs, err = gerReader.GetInjectedGERsForRange(ctx, 1, 10)
		require.NoError(t, err)
		require.Empty(t, injectedGERs)
	})
}
