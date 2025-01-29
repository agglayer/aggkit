package grpc

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
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

	mockClient.On("GenerateAggchainProof", mock.Anything, &types.GenerateAggchainProofRequest{
		StartBlock:  100,
		MaxEndBlock: 200,
	}).Return(expectedResponse, nil)

	result, err := client.GenerateAggchainProof(100, 200)

	assert.NoError(t, err)
	assert.Equal(t, "dummy-proof", result.Proof)
	assert.Equal(t, uint64(100), result.StartBlock)
	assert.Equal(t, uint64(200), result.EndBlock)
	mockClient.AssertExpectations(t)
}

func TestGenerateAggchainProof_Error(t *testing.T) {
	mockClient := mocks.NewAggchainProofServiceClient(t)
	client := &AggchainProofClient{client: mockClient}

	expectedError := errors.New("Generate error")

	mockClient.On("GenerateAggchainProof", mock.Anything, &types.GenerateAggchainProofRequest{
		StartBlock:  300,
		MaxEndBlock: 400,
	}).Return((*types.GenerateAggchainProofResponse)(nil), expectedError)

	result, err := client.GenerateAggchainProof(300, 400)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "Generate error", err.Error())
	mockClient.AssertExpectations(t)
}
