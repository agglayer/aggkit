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

func TestFetchAggchainProof_Success(t *testing.T) {
	mockClient := mocks.NewAggchainProofServiceClient(t)
	client := &AggchainProofClient{client: mockClient}

	expectedResponse := &types.FetchAggchainProofResponse{
		AggchainProof: []byte("dummy-proof"),
		StartBlock:    100,
		EndBlock:      200,
	}

	mockClient.On("FetchAggchainProof", mock.Anything, &types.FetchAggchainProofRequest{
		StartBlock:  100,
		MaxEndBlock: 200,
	}).Return(expectedResponse, nil)

	result, err := client.FetchAggchainProof(100, 200)

	assert.NoError(t, err)
	assert.Equal(t, "dummy-proof", result.Proof)
	assert.Equal(t, uint64(100), result.StartBlock)
	assert.Equal(t, uint64(200), result.EndBlock)
	mockClient.AssertExpectations(t)
}

func TestFetchAggchainProof_Error(t *testing.T) {
	mockClient := mocks.NewAggchainProofServiceClient(t)
	client := &AggchainProofClient{client: mockClient}

	expectedError := errors.New("fetch error")

	mockClient.On("FetchAggchainProof", mock.Anything, &types.FetchAggchainProofRequest{
		StartBlock:  300,
		MaxEndBlock: 400,
	}).Return((*types.FetchAggchainProofResponse)(nil), expectedError)

	result, err := client.FetchAggchainProof(300, 400)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "fetch error", err.Error())
	mockClient.AssertExpectations(t)
}
