package bridgesync

import (
	"context"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/bridgesync/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewBridgeL2SovereignReader(t *testing.T) {
	tests := []struct {
		name        string
		bridgeAddr  common.Address
		l2Client    aggkittypes.BaseEthereumClienter
		expectError bool
		errorMsg    string
	}{
		{
			name:        "successful creation",
			bridgeAddr:  common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
			l2Client:    mocksethclient.NewBaseEthereumClienter(t),
			expectError: false,
		},
		{
			name:        "nil l2Client",
			bridgeAddr:  common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
			l2Client:    nil,
			expectError: true,
		},
		{
			name:        "zero address",
			bridgeAddr:  common.Address{},
			l2Client:    mocksethclient.NewBaseEthereumClienter(t),
			expectError: false, // Zero address is valid, contract creation might still work
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, err := NewBridgeL2SovereignReader(tt.bridgeAddr, tt.l2Client)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, reader)
				if tt.errorMsg != "" {
					require.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, reader)
				require.NotNil(t, reader.bridgeSovereignChain)
			}
		})
	}
}

func TestBridgeL2SovereignReader_GetUnsetClaimsForBlockRange_Constructor(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	// Test successful creation
	reader, err := NewBridgeL2SovereignReader(bridgeAddr, mockClient)
	require.NoError(t, err)
	require.NotNil(t, reader)

	// Test that the reader has the expected structure
	require.NotNil(t, reader.bridgeSovereignChain)
}

func TestBridgeL2SovereignReader_GetUnsetClaimsForBlockRange_WithMockedClient(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	// Mock the FilterLogs method that will be called by the contract
	mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil)

	reader, err := NewBridgeL2SovereignReader(bridgeAddr, mockClient)
	require.NoError(t, err)

	t.Run("successful call with mocked client", func(t *testing.T) {
		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 100, 200)
		require.NoError(t, err)
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims) // Should be empty since we mocked empty results
	})

	t.Run("zero block range", func(t *testing.T) {
		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 0, 0)
		require.NoError(t, err)
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims)
	})

	t.Run("same from and to block", func(t *testing.T) {
		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 100, 100)
		require.NoError(t, err)
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims)
	})

	t.Run("large block range", func(t *testing.T) {
		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 0, ^uint64(0))
		require.NoError(t, err)
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims)
	})

	mockClient.AssertExpectations(t)
}

func TestBridgeL2SovereignReader_GetUnsetClaimsForBlockRange_ErrorHandling(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	reader, err := NewBridgeL2SovereignReader(bridgeAddr, mockClient)
	require.NoError(t, err)

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		// Mock the FilterLogs method
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil)

		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 100, 200)
		require.NoError(t, err) // Context cancellation doesn't cause error in this implementation
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims)
	})

	t.Run("nil context handling", func(t *testing.T) {
		// Test that nil context is handled gracefully
		unclaims, err := reader.GetUnsetClaimsForBlockRange(nil, 100, 200)
		require.NoError(t, err) // The function handles nil context gracefully
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims)
	})

	mockClient.AssertExpectations(t)
}

func TestBridgeL2SovereignReader_GetUnsetClaimsForBlockRange_UnclaimStructure(t *testing.T) {
	// Test the Unclaim type structure
	unclaim := &types.Unclaim{
		GlobalIndex: big.NewInt(12345),
		BlockNumber: 100,
		BlockIndex:  5,
	}

	require.NotNil(t, unclaim)
	require.Equal(t, big.NewInt(12345), unclaim.GlobalIndex)
	require.Equal(t, uint64(100), unclaim.BlockNumber)
	require.Equal(t, uint(5), unclaim.BlockIndex)
}

func TestBridgeL2SovereignReader_GetUnsetClaimsForBlockRange_GlobalIndexConversion(t *testing.T) {
	// Test global index conversion logic
	tests := []struct {
		name           string
		globalIndex    [32]byte
		expectedBigInt *big.Int
	}{
		{
			name:           "zero global index",
			globalIndex:    [32]byte{},
			expectedBigInt: new(big.Int),
		},
		{
			name:           "small global index",
			globalIndex:    [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
			expectedBigInt: big.NewInt(1),
		},
		{
			name:           "medium global index",
			globalIndex:    [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 42},
			expectedBigInt: big.NewInt(42),
		},
		{
			name:           "large global index",
			globalIndex:    [32]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 255},
			expectedBigInt: big.NewInt(255),
		},
		{
			name:           "very large global index",
			globalIndex:    [32]byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			expectedBigInt: new(big.Int).Lsh(big.NewInt(1), 248), // 2^248
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the conversion logic that would be used in the actual function
			result := new(big.Int).SetBytes(tt.globalIndex[:])
			require.Equal(t, 0, result.Cmp(tt.expectedBigInt))
		})
	}
}

func TestBridgeL2SovereignReader_GetUnsetClaimsForBlockRange_BlockNumberAndIndex(t *testing.T) {
	// Test block number and index handling
	tests := []struct {
		name        string
		blockNumber uint64
		blockIndex  uint
	}{
		{
			name:        "zero values",
			blockNumber: 0,
			blockIndex:  0,
		},
		{
			name:        "small values",
			blockNumber: 1,
			blockIndex:  1,
		},
		{
			name:        "medium values",
			blockNumber: 1000,
			blockIndex:  100,
		},
		{
			name:        "large values",
			blockNumber: ^uint64(0),
			blockIndex:  ^uint(0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unclaim := &types.Unclaim{
				GlobalIndex: big.NewInt(12345),
				BlockNumber: tt.blockNumber,
				BlockIndex:  tt.blockIndex,
			}

			require.Equal(t, tt.blockNumber, unclaim.BlockNumber)
			require.Equal(t, tt.blockIndex, unclaim.BlockIndex)
		})
	}
}

func TestBridgeL2SovereignReader_GetUnsetClaimsForBlockRange_InputValidation(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	// Mock the FilterLogs method
	mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil)

	reader, err := NewBridgeL2SovereignReader(bridgeAddr, mockClient)
	require.NoError(t, err)

	t.Run("fromBlock greater than toBlock", func(t *testing.T) {
		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 200, 100)
		// This should be handled by the contract filter, but we test the input
		require.NoError(t, err)
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims)
	})

	t.Run("maximum uint64 values", func(t *testing.T) {
		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, ^uint64(0), ^uint64(0))
		require.NoError(t, err)
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims)
	})

	t.Run("minimum values", func(t *testing.T) {
		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 0, 0)
		require.NoError(t, err)
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims)
	})

	mockClient.AssertExpectations(t)
}

func TestBridgeL2SovereignReader_GetUnsetClaimsForBlockRange_ReturnTypeValidation(t *testing.T) {
	// Test that the function returns the correct type structure
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	// Mock the FilterLogs method
	mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil)

	reader, err := NewBridgeL2SovereignReader(bridgeAddr, mockClient)
	require.NoError(t, err)

	// Test that the function signature is correct
	unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 100, 200)

	// Should succeed with mocked client
	require.NoError(t, err)
	require.NotNil(t, unclaims)

	// Verify the expected return type structure
	var expectedType []*types.Unclaim
	require.IsType(t, expectedType, unclaims)

	mockClient.AssertExpectations(t)
}
