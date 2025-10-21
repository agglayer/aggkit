package query

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
)

func TestGetAggregationProofPublicValuesData_Success(t *testing.T) {
	mockFEPContract := mocks.NewFEPContractQuerier(t)
	mockOPNodeClient := mocks.NewOpNodeClienter(t)

	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	proverAddress := common.HexToAddress("0x0987654321098765432109876543210987654321")
	sut := NewAggProofPublicValuesQuery(mockFEPContract, contractAddr, mockOPNodeClient, proverAddress)

	lastProvenBlock := uint64(1)
	requestedEndBlock := uint64(2)
	l1InfoTreeLeafHash := common.HexToHash("0xbeef")

	expectedL2PreRoot := common.HexToHash("0xdeadbeef")
	expectedClaimRoot := common.HexToHash("0xcafebabe")
	expectedRollupConfigHash := [32]byte{0x01}
	expectedMultiBlockVKey := [32]byte{0x02}

	mockOPNodeClient.EXPECT().OutputAtBlockRoot(lastProvenBlock).Return(expectedL2PreRoot, nil)
	mockOPNodeClient.EXPECT().OutputAtBlockRoot(requestedEndBlock).Return(expectedClaimRoot, nil)
	mockFEPContract.EXPECT().SelectedOpSuccinctConfigName((*bind.CallOpts)(nil)).Return([32]byte{0x00}, nil).Once()
	mockFEPContract.EXPECT().OpSuccinctConfigs((*bind.CallOpts)(nil), [32]byte{0x00}).Return(struct {
		AggregationVkey     [32]byte
		RangeVkeyCommitment [32]byte
		RollupConfigHash    [32]byte
	}{
		AggregationVkey:     [32]byte{},
		RangeVkeyCommitment: expectedMultiBlockVKey,
		RollupConfigHash:    expectedRollupConfigHash,
	}, nil).Once()

	result, err := sut.GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock, l1InfoTreeLeafHash)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, l1InfoTreeLeafHash, result.L1Head)
	assert.Equal(t, expectedL2PreRoot, result.L2PreRoot)
	assert.Equal(t, expectedClaimRoot, result.ClaimRoot)
	assert.Equal(t, requestedEndBlock, result.L2BlockNumber)
	assert.Equal(t, expectedRollupConfigHash[:], result.RollupConfigHash.Bytes())
	assert.Equal(t, expectedMultiBlockVKey[:], result.MultiBlockVKey.Bytes())
	assert.Equal(t, proverAddress, result.TrustedSigner)
}

func TestGetAggregationProofPublicValuesData_Failure(t *testing.T) {
	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	proverAddress := common.HexToAddress("0x0987654321098765432109876543210987654321")

	lastProvenBlock := uint64(1)
	requestedEndBlock := uint64(2)
	l1InfoTreeLeafHash := common.HexToHash("0xbeef")
	t.Run("opNodeClient.OutputAtBlockRoot error on l2PreRoot", func(t *testing.T) {
		mockFEPContract := mocks.NewFEPContractQuerier(t)
		mockOPNodeClient := mocks.NewOpNodeClienter(t)
		sut := NewAggProofPublicValuesQuery(mockFEPContract, contractAddr, mockOPNodeClient, proverAddress)

		mockOPNodeClient.EXPECT().OutputAtBlockRoot(lastProvenBlock).Return(common.Hash{}, errors.New("mock error")).Once()

		result, err := sut.GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock, l1InfoTreeLeafHash)

		assert.ErrorContains(t, err, "l2PreRoot")
		assert.Nil(t, result)
	})
	t.Run("opNodeClient.OutputAtBlockRoot error on claimRoot", func(t *testing.T) {
		mockFEPContract := mocks.NewFEPContractQuerier(t)
		mockOPNodeClient := mocks.NewOpNodeClienter(t)
		sut := NewAggProofPublicValuesQuery(mockFEPContract, contractAddr, mockOPNodeClient, proverAddress)

		mockOPNodeClient.EXPECT().OutputAtBlockRoot(lastProvenBlock).Return(common.Hash{}, nil)
		mockOPNodeClient.EXPECT().OutputAtBlockRoot(requestedEndBlock).Return(common.Hash{}, errors.New("mock error"))

		result, err := sut.GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock, l1InfoTreeLeafHash)

		assert.ErrorContains(t, err, "claimRoot")
		assert.Nil(t, result)
	})

	t.Run("opNodeClient.OutputAtBlockRoot error on contract.SelectedOpSuccinctConfigName", func(t *testing.T) {
		mockFEPContract := mocks.NewFEPContractQuerier(t)
		mockOPNodeClient := mocks.NewOpNodeClienter(t)
		sut := NewAggProofPublicValuesQuery(mockFEPContract, contractAddr, mockOPNodeClient, proverAddress)

		mockOPNodeClient.EXPECT().OutputAtBlockRoot(lastProvenBlock).Return(common.Hash{}, nil)
		mockOPNodeClient.EXPECT().OutputAtBlockRoot(requestedEndBlock).Return(common.Hash{}, nil)
		mockFEPContract.EXPECT().SelectedOpSuccinctConfigName((*bind.CallOpts)(nil)).Return([32]byte{0x00}, errors.New("mock error")).Once()

		result, err := sut.GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock, l1InfoTreeLeafHash)

		assert.ErrorContains(t, err, "SelectedOpSuccinctConfigName")
		assert.Nil(t, result)
	})

	t.Run("opNodeClient.OutputAtBlockRoot error on contract.OpSuccinctConfigs", func(t *testing.T) {
		mockFEPContract := mocks.NewFEPContractQuerier(t)
		mockOPNodeClient := mocks.NewOpNodeClienter(t)
		sut := NewAggProofPublicValuesQuery(mockFEPContract, contractAddr, mockOPNodeClient, proverAddress)

		mockOPNodeClient.EXPECT().OutputAtBlockRoot(lastProvenBlock).Return(common.Hash{}, nil)
		mockOPNodeClient.EXPECT().OutputAtBlockRoot(requestedEndBlock).Return(common.Hash{}, nil)
		mockFEPContract.EXPECT().SelectedOpSuccinctConfigName((*bind.CallOpts)(nil)).Return([32]byte{0x00}, nil).Once()
		mockFEPContract.EXPECT().OpSuccinctConfigs((*bind.CallOpts)(nil), [32]byte{0x00}).Return(struct {
			AggregationVkey     [32]byte
			RangeVkeyCommitment [32]byte
			RollupConfigHash    [32]byte
		}{}, errors.New("mock error")).Once()
		result, err := sut.GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock, l1InfoTreeLeafHash)

		assert.ErrorContains(t, err, "OpSuccinctConfigs")
		assert.Nil(t, result)
	})
}

func TestGetAggregationProofPublicValuesData_GetTrustedSequencerFromContract(t *testing.T) {
	mockFEPContract := mocks.NewFEPContractQuerier(t)
	mockOPNodeClient := mocks.NewOpNodeClienter(t)

	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	sut := NewAggProofPublicValuesQuery(mockFEPContract, contractAddr, mockOPNodeClient, common.Address{})

	lastProvenBlock := uint64(1)
	requestedEndBlock := uint64(2)
	l1InfoTreeLeafHash := common.HexToHash("0xbeef")

	expectedL2PreRoot := common.HexToHash("0xdeadbeef")
	expectedClaimRoot := common.HexToHash("0xcafebabe")
	expectedRollupConfigHash := [32]byte{0x01}
	expectedMultiBlockVKey := [32]byte{0x02}
	expectedTrustedSequencer := common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd")

	mockOPNodeClient.EXPECT().OutputAtBlockRoot(lastProvenBlock).Return(expectedL2PreRoot, nil)
	mockOPNodeClient.EXPECT().OutputAtBlockRoot(requestedEndBlock).Return(expectedClaimRoot, nil)
	mockFEPContract.EXPECT().SelectedOpSuccinctConfigName((*bind.CallOpts)(nil)).Return([32]byte{0x00}, nil).Once()
	mockFEPContract.EXPECT().OpSuccinctConfigs((*bind.CallOpts)(nil), [32]byte{0x00}).Return(struct {
		AggregationVkey     [32]byte
		RangeVkeyCommitment [32]byte
		RollupConfigHash    [32]byte
	}{
		AggregationVkey:     [32]byte{0x01},
		RangeVkeyCommitment: [32]byte{0x02},
		RollupConfigHash:    expectedRollupConfigHash,
	}, nil).Once()
	mockFEPContract.EXPECT().GetAggchainSigners((*bind.CallOpts)(nil)).Return([]common.Address{expectedTrustedSequencer}, nil)

	result, err := sut.GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock, l1InfoTreeLeafHash)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, l1InfoTreeLeafHash, result.L1Head)
	assert.Equal(t, expectedL2PreRoot, result.L2PreRoot)
	assert.Equal(t, expectedClaimRoot, result.ClaimRoot)
	assert.Equal(t, requestedEndBlock, result.L2BlockNumber)
	assert.Equal(t, expectedRollupConfigHash[:], result.RollupConfigHash.Bytes())
	assert.Equal(t, expectedMultiBlockVKey[:], result.MultiBlockVKey.Bytes())
	assert.Equal(t, expectedTrustedSequencer, result.TrustedSigner)
}
