package optimistic

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/optimistic/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
)

func TestGetAggregationProofPublicValuesData_Success(t *testing.T) {
	mockFEPContract := mocks.NewFEPContractQuerier(t)
	mockOPNodeClient := mocks.NewOpNodeClienter(t)

	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	proverAddress := common.HexToAddress("0x0987654321098765432109876543210987654321")
	sut := NewOptimisticAggregationProofPublicValuesQuery(mockFEPContract, contractAddr, mockOPNodeClient, proverAddress)

	lastProvenBlock := uint64(1)
	requestedEndBlock := uint64(2)
	l1InfoTreeLeafHash := common.HexToHash("0xbeef")

	expectedL2PreRoot := common.HexToHash("0xdeadbeef")
	expectedClaimRoot := common.HexToHash("0xcafebabe")
	expectedRollupConfigHash := [32]byte{0x01}
	expectedMultiBlockVKey := [32]byte{0x02}

	mockOPNodeClient.On("OutputAtBlockRoot", lastProvenBlock).Return(expectedL2PreRoot, nil)
	mockOPNodeClient.On("OutputAtBlockRoot", requestedEndBlock).Return(expectedClaimRoot, nil)
	mockFEPContract.On("RollupConfigHash", (*bind.CallOpts)(nil)).Return(expectedRollupConfigHash, nil)
	mockFEPContract.On("RangeVkeyCommitment", (*bind.CallOpts)(nil)).Return(expectedMultiBlockVKey, nil)

	result, err := sut.GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock, l1InfoTreeLeafHash)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, l1InfoTreeLeafHash, result.L1Head)
	assert.Equal(t, expectedL2PreRoot, result.L2PreRoot)
	assert.Equal(t, expectedClaimRoot, result.ClaimRoot)
	assert.Equal(t, requestedEndBlock, result.L2BlockNumber)
	assert.Equal(t, expectedRollupConfigHash[:], result.RollupConfigHash.Bytes())
	assert.Equal(t, expectedMultiBlockVKey[:], result.MultiBlockVKey.Bytes())
	assert.Equal(t, proverAddress, result.ProverAddress)
}

func TestGetAggregationProofPublicValuesData_Failure(t *testing.T) {
	mockFEPContract := mocks.NewFEPContractQuerier(t)
	mockOPNodeClient := mocks.NewOpNodeClienter(t)

	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	proverAddress := common.HexToAddress("0x0987654321098765432109876543210987654321")
	sut := NewOptimisticAggregationProofPublicValuesQuery(mockFEPContract, contractAddr, mockOPNodeClient, proverAddress)

	lastProvenBlock := uint64(1)
	requestedEndBlock := uint64(2)
	l1InfoTreeLeafHash := common.HexToHash("0xbeef")

	mockOPNodeClient.On("OutputAtBlockRoot", lastProvenBlock).Return(common.Hash{}, errors.New("mock error"))

	result, err := sut.GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock, l1InfoTreeLeafHash)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "l2PreRoot")
}
