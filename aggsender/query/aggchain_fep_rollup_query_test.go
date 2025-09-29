package query

import (
	"errors"
	"math/big"
	"testing"

	optimisticmocks "github.com/agglayer/aggkit/aggsender/optimistic/mocks"
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
		"",
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
			"",
			mockL1Client,
		)
		require.ErrorContains(t, err, "aggchainProverFlow - error AggChainFEPContract.StartingBlockNumber")
	})

	t.Run("aggchain FEP caller returns error", func(t *testing.T) {
		t.Parallel()
		mockOpQuerier := optimisticmocks.NewOptimisticAggregationProofPublicValuesQuerier(t)
		mockCaller := optimisticmocks.NewFEPContractQuerier(t)
		mockCaller.EXPECT().StartingBlockNumber((*bind.CallOpts)(nil)).Return(nil, errors.New("mock error")).Once()

		_, err := newAggchainFEPQuerier(
			log.WithFields("test", "aggchainFEPRollupQuerier"),
			common.HexToAddress("0x1"),
			mockCaller,
			mockOpQuerier,
		)
		require.Error(t, err)
		mockCaller.AssertExpectations(t)
	})

	t.Run("aggchain FEP caller returns valid starting block", func(t *testing.T) {
		t.Parallel()

		mockOpQuerier := optimisticmocks.NewOptimisticAggregationProofPublicValuesQuerier(t)
		mockCaller := optimisticmocks.NewFEPContractQuerier(t)
		startingBlock := big.NewInt(1000)
		mockCaller.EXPECT().StartingBlockNumber((*bind.CallOpts)(nil)).Return(startingBlock, nil).Once()

		querier, err := newAggchainFEPQuerier(
			log.WithFields("test", "aggchainFEPRollupQuerier"),
			common.HexToAddress("0x1"),
			mockCaller,
			mockOpQuerier,
		)
		require.NoError(t, err)

		require.Equal(t, startingBlock.Uint64(), querier.StartL2Block(), "StartL2Block should return the correct starting block")
		mockCaller.AssertExpectations(t)
	})

	t.Run("aggchain FEP caller returns error on last settled block", func(t *testing.T) {
		t.Parallel()

		mockOpQuerier := optimisticmocks.NewOptimisticAggregationProofPublicValuesQuerier(t)
		mockCaller := optimisticmocks.NewFEPContractQuerier(t)
		mockCaller.EXPECT().StartingBlockNumber((*bind.CallOpts)(nil)).Return(big.NewInt(1000), nil).Once()
		mockCaller.EXPECT().LatestBlockNumber((*bind.CallOpts)(nil)).Return(nil, errors.New("mock error")).Once()

		querier, err := newAggchainFEPQuerier(
			log.WithFields("test", "aggchainFEPRollupQuerier"),
			common.HexToAddress("0x1"),
			mockCaller,
			mockOpQuerier,
		)
		require.NoError(t, err)

		lastSettledBlock, err := querier.GetLastSettledL2Block()
		require.Error(t, err)
		require.Zero(t, lastSettledBlock, "GetLastSettledL2Block should return 0 on error")
		mockCaller.AssertExpectations(t)
	})

	t.Run("aggchain FEP caller returns valid last settled block", func(t *testing.T) {
		t.Parallel()

		mockOpQuerier := optimisticmocks.NewOptimisticAggregationProofPublicValuesQuerier(t)
		mockCaller := optimisticmocks.NewFEPContractQuerier(t)
		startingBlock := big.NewInt(1000)
		lastSettledBlock := big.NewInt(2000)

		mockCaller.EXPECT().StartingBlockNumber((*bind.CallOpts)(nil)).Return(startingBlock, nil).Once()
		mockCaller.EXPECT().LatestBlockNumber((*bind.CallOpts)(nil)).Return(lastSettledBlock, nil).Once()

		querier, err := newAggchainFEPQuerier(
			log.WithFields("test", "aggchainFEPRollupQuerier"),
			common.HexToAddress("0x1"),
			mockCaller,
			mockOpQuerier,
		)
		require.NoError(t, err)

		lastSettledBlockFromQuerier, err := querier.GetLastSettledL2Block()
		require.NoError(t, err)

		require.Equal(t, lastSettledBlock.Uint64(), lastSettledBlockFromQuerier, "GetLastSettledL2Block should return the correct last settled block")
		mockCaller.AssertExpectations(t)
	})
}

func TestGetAggregationProofPublicValuesData(t *testing.T) {
	t.Parallel()

	mockOpQuerier := opmocks.NewOptimisticAggregationProofPublicValuesQuerier(t)
	mockCaller := mocks.NewAggchainFEPCaller(t)
	startingBlock := big.NewInt(1000)
	mockCaller.EXPECT().StartingBlockNumber((*bind.CallOpts)(nil)).Return(startingBlock, nil).Once()

	lastProvenBlock := uint64(123)
	requestedEndBlock := uint64(456)
	l1Hash := common.HexToHash("0xabc")
	expected := &types.AggregationProofPublicValues{}

	mockOpQuerier.
		EXPECT().
		GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock, l1Hash).
		Return(nil, errors.New("some error")).
		Once()

	querier, err := newAggchainFEPQuerier(
		log.WithFields("test", "GetAggregationProofPublicValuesData"),
		common.HexToAddress("0x1"),
		mockCaller,
		mockOpQuerier,
	)
	require.NoError(t, err)

	_, err = querier.GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock, l1Hash)
	require.ErrorContains(t, err, "some error")

	mockOpQuerier.
		EXPECT().
		GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock+1, l1Hash).
		Return(expected, nil).
		Once()

	got, err := querier.GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock+1, l1Hash)
	require.NoError(t, err)
	require.Equal(t, expected, got)

	mockCaller.AssertExpectations(t)
	mockOpQuerier.AssertExpectations(t)
}
