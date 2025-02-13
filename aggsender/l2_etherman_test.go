package aggsender

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/test/helpers"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewL2Etherman(t *testing.T) {
	t.Parallel()

	validAddress := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	invalidAddress := common.Address{}

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		mockL2GERManager := mocks.NewL2GERManager(t)
		mockL2GERManager.On("BridgeAddress", (*bind.CallOpts)(nil)).Return(validAddress, nil)

		l2Etherman, err := newL2Etherman(mockL2GERManager, validAddress)
		assert.NoError(t, err)
		assert.NotNil(t, l2Etherman)
		mockL2GERManager.AssertExpectations(t)
	})

	t.Run("failure - invalid contract address", func(t *testing.T) {
		t.Parallel()
		mockL2GERManager := mocks.NewL2GERManager(t)
		mockL2GERManager.On("BridgeAddress", (*bind.CallOpts)(nil)).Return(invalidAddress, errors.New("invalid address"))

		l2Etherman, err := newL2Etherman(mockL2GERManager, invalidAddress)
		assert.Error(t, err)
		assert.Nil(t, l2Etherman)
		mockL2GERManager.AssertExpectations(t)
	})
}

func TestGetInjectedGERsForRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("failed to create iterator", func(t *testing.T) {
		t.Parallel()

		toBlock := uint64(10)
		mockL2GERManager := mocks.NewL2GERManager(t)
		mockL2GERManager.On("FilterInsertGlobalExitRoot", &bind.FilterOpts{
			Context: ctx,
			Start:   1,
			End:     &toBlock,
		}, mock.Anything, mock.Anything).Return(nil, errors.New("failed to create iterator"))

		l2Etherman := &L2Etherman{l2GERManager: mockL2GERManager}

		_, err := l2Etherman.GetInjectedGERsForRange(ctx, 1, toBlock)
		require.ErrorContains(t, err, "failed to create iterator")
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		setup := helpers.L2Setup(t)
		setup.EthTxManagerMock.ExpectedCalls = nil

		l2Etherman, err := NewL2Etherman(setup.GERAddr, setup.SimBackend.Client())
		require.NoError(t, err)

		tx, err := setup.GERContract.InsertGlobalExitRoot(setup.Auth, common.HexToHash("0x1234567890abcdef1234567890abcdef12345678"))
		require.NoError(t, err)

		// commit one block
		setup.SimBackend.Commit()

		receipt, err := setup.SimBackend.Client().TransactionReceipt(ctx, tx.Hash())
		require.NoError(t, err)
		require.Equal(t, receipt.Status, types.ReceiptStatusSuccessful)

		injectedGERs, err := l2Etherman.GetInjectedGERsForRange(ctx, 1, 10)
		require.NoError(t, err)
		require.Len(t, injectedGERs, 1)
		assert.Equal(t, common.HexToHash("0x1234567890abcdef1234567890abcdef12345678"), injectedGERs[0])
	})
}
