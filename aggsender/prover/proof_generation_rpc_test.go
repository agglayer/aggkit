package prover

import (
	"errors"
	"testing"

	"github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGenerateAggchainProofRPC(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		aggchainProofGen := mocks.NewAggchainProofGeneration(t)
		genRPC := NewAggchainProofGenerationToolRPC(aggchainProofGen)

		fromBlock := uint64(1)
		toBlock := uint64(10)
		expectedProof := []byte("mockProof")

		aggchainProofGen.EXPECT().GenerateAggchainProof(mock.Anything, fromBlock, toBlock).Return(expectedProof, nil)

		result, err := genRPC.GenerateAggchainProof(fromBlock, toBlock)
		require.NoError(t, err)
		require.Equal(t, expectedProof, result)

		aggchainProofGen.AssertExpectations(t)
	})

	t.Run("Error", func(t *testing.T) {
		t.Parallel()

		aggchainProofGen := mocks.NewAggchainProofGeneration(t)
		genRPC := NewAggchainProofGenerationToolRPC(aggchainProofGen)

		fromBlock := uint64(1)
		toBlock := uint64(10)
		expectedError := errors.New("mock error")

		aggchainProofGen.EXPECT().GenerateAggchainProof(mock.Anything, fromBlock, toBlock).Return(nil, expectedError)

		result, err := genRPC.GenerateAggchainProof(fromBlock, toBlock)
		require.Nil(t, result)
		require.ErrorContains(t, err, rpc.NewRPCError(rpc.DefaultErrorCode, expectedError.Error()).Error())

		aggchainProofGen.AssertExpectations(t)
	})
}
