package query

import (
	"errors"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	typesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNoOpAggchainFEPRollupQuerier(t *testing.T) {
	t.Parallel()

	querier, err := NewAggchainFEPQuerier(
		log.WithFields("test", "noOpAggchainFEPRollupQuerier"),
		types.PessimisticProofMode,
		aggkitcommon.ZeroAddress,
		nil, // No Ethereum client needed for no-op querier
	)
	require.NoError(t, err)

	require.Zero(t, querier.StartL2Block(), "StartL2Block should return 0 for no-op querier")

	lastSettledBlock, err := querier.GetLastSettledL2Block()
	require.NoError(t, err, "GetLastSettledL2Block should not return an error for no-op querier")
	require.Zero(t, lastSettledBlock, "GetLastSettledL2Block should return 0 for no-op querier")
}

func TestAggchainFEPRollupQuerier(t *testing.T) {
	t.Parallel()

	t.Run("error on constructor", func(t *testing.T) {
		t.Parallel()

		mockL1Client := typesmocks.NewBaseEthereumClienter(t)
		mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some error")).Once()

		_, err := NewAggchainFEPQuerier(
			log.WithFields("test", "aggchainFEPRollupQuerier"),
			types.AggchainProofMode,
			common.HexToAddress("0x1"),
			mockL1Client,
		)
		require.ErrorContains(t, err, "aggchainProverFlow - error AggChainFEPContract.StartingBlockNumber")
	})

	t.Run("aggchain FEP caller returns error", func(t *testing.T) {
		t.Parallel()
		mockCaller := mocks.NewFEPContractQuerier(t)
		mockCaller.EXPECT().StartingBlockNumber((*bind.CallOpts)(nil)).Return(nil, errors.New("mock error")).Once()

		_, err := newAggchainFEPQuerier(
			log.WithFields("test", "aggchainFEPRollupQuerier"),
			common.HexToAddress("0x1"),
			mockCaller,
		)
		require.Error(t, err)
		mockCaller.AssertExpectations(t)
	})

	t.Run("aggchain FEP caller returns valid starting block", func(t *testing.T) {
		t.Parallel()

		mockCaller := mocks.NewFEPContractQuerier(t)
		startingBlock := big.NewInt(1000)
		mockCaller.EXPECT().StartingBlockNumber((*bind.CallOpts)(nil)).Return(startingBlock, nil).Once()

		querier, err := newAggchainFEPQuerier(
			log.WithFields("test", "aggchainFEPRollupQuerier"),
			common.HexToAddress("0x1"),
			mockCaller,
		)
		require.NoError(t, err)

		require.Equal(t, startingBlock.Uint64(), querier.StartL2Block(), "StartL2Block should return the correct starting block")
		mockCaller.AssertExpectations(t)
	})

	t.Run("aggchain FEP caller returns error on last settled block", func(t *testing.T) {
		t.Parallel()

		mockCaller := mocks.NewFEPContractQuerier(t)
		mockCaller.EXPECT().StartingBlockNumber((*bind.CallOpts)(nil)).Return(big.NewInt(1000), nil).Once()
		mockCaller.EXPECT().LatestBlockNumber((*bind.CallOpts)(nil)).Return(nil, errors.New("mock error")).Once()

		querier, err := newAggchainFEPQuerier(
			log.WithFields("test", "aggchainFEPRollupQuerier"),
			common.HexToAddress("0x1"),
			mockCaller,
		)
		require.NoError(t, err)

		lastSettledBlock, err := querier.GetLastSettledL2Block()
		require.Error(t, err)
		require.Zero(t, lastSettledBlock, "GetLastSettledL2Block should return 0 on error")
		mockCaller.AssertExpectations(t)
	})

	t.Run("aggchain FEP caller returns valid last settled block", func(t *testing.T) {
		t.Parallel()

		mockCaller := mocks.NewFEPContractQuerier(t)
		startingBlock := big.NewInt(1000)
		lastSettledBlock := big.NewInt(2000)

		mockCaller.EXPECT().StartingBlockNumber((*bind.CallOpts)(nil)).Return(startingBlock, nil).Once()
		mockCaller.EXPECT().LatestBlockNumber((*bind.CallOpts)(nil)).Return(lastSettledBlock, nil).Once()

		querier, err := newAggchainFEPQuerier(
			log.WithFields("test", "aggchainFEPRollupQuerier"),
			common.HexToAddress("0x1"),
			mockCaller,
		)
		require.NoError(t, err)

		lastSettledBlockFromQuerier, err := querier.GetLastSettledL2Block()
		require.NoError(t, err)

		require.Equal(t, lastSettledBlock.Uint64(), lastSettledBlockFromQuerier, "GetLastSettledL2Block should return the correct last settled block")
		mockCaller.AssertExpectations(t)
	})
}
