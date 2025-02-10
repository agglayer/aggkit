package grpc

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	treeTypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockAggchainProofServiceClient is a mock implementation of the AggchainProofServiceClient
type MockAggchainProofServiceClient struct {
	mock.Mock
}

func TestGenerateAggchainProof_Success(t *testing.T) {
	mockClient := mocks.NewAggchainProofServiceClient(t)
	client := &AggchainProofClient{client: mockClient}

	expectedResponse := &types.GenerateAggchainProofResponse{
		AggchainProof: []byte("dummy-proof"),
		StartBlock:    100,
		EndBlock:      200,
	}

	convertedMerkleProof := make([][]byte, treeTypes.DefaultHeight)
	for i := 0; i < int(treeTypes.DefaultHeight); i++ {
		convertedMerkleProof[i] = common.Hash{}.Bytes()
	}

	mockClient.On("GenerateAggchainProof", mock.Anything, &types.GenerateAggchainProofRequest{
		StartBlock:            100,
		MaxEndBlock:           200,
		L1InfoTreeRootHash:    common.Hash{}.Bytes(),
		L1InfoTreeLeafHash:    common.Hash{}.Bytes(),
		L1InfoTreeMerkleProof: convertedMerkleProof,
		GerInclusionProofs:    make(map[string]*types.InclusionProof),
	}).Return(expectedResponse, nil)

	result, err := client.GenerateAggchainProof(100, 200, common.Hash{}, common.Hash{}, [32]common.Hash{}, nil)

	assert.NoError(t, err)
	assert.Equal(t, []byte("dummy-proof"), result.Proof)
	assert.Equal(t, uint64(100), result.StartBlock)
	assert.Equal(t, uint64(200), result.EndBlock)
	mockClient.AssertExpectations(t)
}

func TestGenerateAggchainProof_Error(t *testing.T) {
	mockClient := mocks.NewAggchainProofServiceClient(t)
	client := &AggchainProofClient{client: mockClient}

	expectedError := errors.New("Generate error")

	convertedMerkleProof := make([][]byte, treeTypes.DefaultHeight)
	for i := 0; i < int(treeTypes.DefaultHeight); i++ {
		convertedMerkleProof[i] = common.Hash{}.Bytes()
	}

	mockClient.On("GenerateAggchainProof", mock.Anything, &types.GenerateAggchainProofRequest{
		StartBlock:            300,
		MaxEndBlock:           400,
		L1InfoTreeRootHash:    common.Hash{}.Bytes(),
		L1InfoTreeLeafHash:    common.Hash{}.Bytes(),
		L1InfoTreeMerkleProof: convertedMerkleProof,
		GerInclusionProofs:    make(map[string]*types.InclusionProof),
	}).Return((*types.GenerateAggchainProofResponse)(nil), expectedError)

	result, err := client.GenerateAggchainProof(300, 400, common.Hash{}, common.Hash{}, [32]common.Hash{}, nil)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "Generate error", err.Error())
	mockClient.AssertExpectations(t)
}
