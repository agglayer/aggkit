package grpc

import (
	"errors"
	"testing"

	agglayer "github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/l1infotreesync"
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

	mockClient.On("GenerateAggchainProof", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	result, err := client.GenerateAggchainProof(100, 200, common.Hash{}, l1infotreesync.L1InfoTreeLeaf{}, [32]common.Hash{}, nil, nil)

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

	mockClient.On("GenerateAggchainProof", mock.Anything, mock.Anything).Return((*types.GenerateAggchainProofResponse)(nil), expectedError)

	result, err := client.GenerateAggchainProof(300, 400, common.Hash{}, l1infotreesync.L1InfoTreeLeaf{}, [32]common.Hash{}, nil, make([]*agglayer.ImportedBridgeExit, 0))

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "Generate error", err.Error())
	mockClient.AssertExpectations(t)
}
