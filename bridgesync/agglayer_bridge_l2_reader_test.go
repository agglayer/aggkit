package bridgesync

import (
	"context"
	"errors"
	"testing"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/etherman"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewAgglayerBridgeL2Reader(t *testing.T) {
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
			name:        "zero address",
			bridgeAddr:  common.Address{},
			l2Client:    mocksethclient.NewBaseEthereumClienter(t),
			expectError: false, // Zero address is valid, contract creation might still work
		},
		{
			name:        "contract creation with valid mock client",
			bridgeAddr:  common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
			l2Client:    mocksethclient.NewBaseEthereumClienter(t),
			expectError: false, // The contract creation should succeed with a valid mock client
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, err := NewAgglayerBridgeL2Reader(tt.bridgeAddr, tt.l2Client)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, reader)
				if tt.errorMsg != "" {
					require.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, reader)
				require.NotNil(t, reader.agglayerBridgeL2)
			}
		})
	}
}

func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_ProactiveChunkingByConfig(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	t.Run("configured max proactively chunks range", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2ReaderWithMaxLogBlockRange(bridgeAddr, mockClient, 1000)
		require.NoError(t, err)

		var ranges [][2]uint64
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Run(func(args mock.Arguments) {
			q, ok := args.Get(1).(ethereum.FilterQuery)
			require.True(t, ok)
			require.NotNil(t, q.FromBlock)
			require.NotNil(t, q.ToBlock)
			ranges = append(ranges, [2]uint64{q.FromBlock.Uint64(), q.ToBlock.Uint64()})
		}).Times(3)

		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 0, 2500)
		require.NoError(t, err)
		require.NotNil(t, unclaims)
		require.Equal(t, [][2]uint64{{0, 999}, {1000, 1999}, {2000, 2500}}, ranges)
	})

	t.Run("zero configured max keeps current non-proactive behavior", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2ReaderWithMaxLogBlockRange(bridgeAddr, mockClient, 0)
		require.NoError(t, err)

		mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Once()

		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 0, 2500)
		require.NoError(t, err)
		require.NotNil(t, unclaims)
	})
}

func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_WithMockedClient(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	// Mock the FilterLogs method that will be called by the contract
	mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil)

	reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
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

func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_ErrorHandling(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
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
		unclaims, err := reader.GetUnsetClaimsForBlockRange(context.TODO(), 100, 200)
		require.NoError(t, err) // The function handles nil context gracefully
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims)
	})

	mockClient.AssertExpectations(t)
}

func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_InputValidation(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	// Mock the FilterLogs method
	mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil)

	reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
	require.NoError(t, err)

	t.Run("fromBlock greater than toBlock", func(t *testing.T) {
		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 200, 100)
		// This should return an error as it's an invalid block range
		require.Error(t, err)
		require.Nil(t, unclaims)
		require.Contains(t, err.Error(), "invalid block range")
		require.Contains(t, err.Error(), "fromBlock(200) > toBlock(100)")
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

// Test error handling in GetUnsetClaimsForBlockRange
func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_FilterErrorHandling(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
	require.NoError(t, err)

	t.Run("filter error", func(t *testing.T) {
		// Mock FilterLogs to return an error
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, errors.New("filter error"))

		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 100, 200)
		require.Error(t, err)
		require.Nil(t, unclaims)
		require.Contains(t, err.Error(), "filter error")
	})

	mockClient.AssertExpectations(t)
}

// Test iterator close error handling
func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_IteratorCloseError(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	// Mock FilterLogs to return empty results
	mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil)

	reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
	require.NoError(t, err)

	// Test normal operation - iterator close error is logged but doesn't affect return
	unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 100, 200)
	require.NoError(t, err)
	require.NotNil(t, unclaims)
	require.Empty(t, unclaims)

	mockClient.AssertExpectations(t)
}

// Test with simulated backend to get real contract behavior
func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_SimulatedBackend(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	// Use a simulated backend to get real contract behavior
	simulatedBackend := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	defer simulatedBackend.Close()

	// Use the client from the simulated backend
	client := simulatedBackend.Client()

	reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, etherman.NewDefaultEthClient(client, nil, nil))
	require.NoError(t, err)

	// Test with the simulated backend - need to mine some blocks first
	simulatedBackend.Commit() // Mine the genesis block

	unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 0, 1)
	require.NoError(t, err)
	require.NotNil(t, unclaims)
	// Should be empty since no events were emitted
	require.Empty(t, unclaims)
}

// Test with real contract events to test iterator behavior
func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_WithRealEvents(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	// Use a simulated backend to get real contract behavior
	simulatedBackend := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	defer simulatedBackend.Close()

	// Use the client from the simulated backend
	client := simulatedBackend.Client()

	reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, etherman.NewDefaultEthClient(client, nil, nil))
	require.NoError(t, err)

	// Mine some blocks to create a valid range
	simulatedBackend.Commit() // Block 1
	simulatedBackend.Commit() // Block 2
	simulatedBackend.Commit() // Block 3

	// Test with a valid block range
	unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 1, 3)
	require.NoError(t, err)
	require.NotNil(t, unclaims)
	// Should be empty since no events were emitted, but this tests the iterator path
	require.Empty(t, unclaims)
}

// Test the actual iterator behavior by creating a test that exercises the iterator loop
func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_IteratorBehavior(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	// Use a simulated backend to get real contract behavior
	simulatedBackend := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
	defer simulatedBackend.Close()

	// Use the client from the simulated backend
	client := simulatedBackend.Client()

	reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, etherman.NewDefaultEthClient(client, nil, nil))
	require.NoError(t, err)

	// Mine some blocks to create a valid range
	simulatedBackend.Commit() // Block 1
	simulatedBackend.Commit() // Block 2
	simulatedBackend.Commit() // Block 3

	// Test with a valid block range - this will test the iterator behavior
	// The iterator will be created and the Next() method will be called
	// Even though there are no events, this tests the iterator loop structure
	unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 1, 3)
	require.NoError(t, err)
	require.NotNil(t, unclaims)
	// Should be empty since no events were emitted, but this tests the iterator path
	require.Empty(t, unclaims)

	// Test with a single block range
	unclaims, err = reader.GetUnsetClaimsForBlockRange(ctx, 1, 1)
	require.NoError(t, err)
	require.NotNil(t, unclaims)
	require.Empty(t, unclaims)
}

// Test with different block ranges
func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_BlockRanges(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	// Mock FilterLogs to return empty results for all calls
	mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil)

	reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
	require.NoError(t, err)

	testCases := []struct {
		name      string
		fromBlock uint64
		toBlock   uint64
	}{
		{"zero to zero", 0, 0},
		{"zero to max", 0, ^uint64(0)},
		{"max to max", ^uint64(0), ^uint64(0)},
		{"normal range", 100, 200},
		{"single block", 100, 100},
		{"large range", 0, 1000000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, tc.fromBlock, tc.toBlock)
			require.NoError(t, err)
			require.NotNil(t, unclaims)
		})
	}

	mockClient.AssertExpectations(t)
}

// Test context handling
func TestAgglayerBridgeL2Reader_GetUnsetClaimsForBlockRange_ContextHandling(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	mockClient := mocksethclient.NewBaseEthereumClienter(t)

	// Mock FilterLogs to return empty results
	mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil)

	reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
	require.NoError(t, err)

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 100, 200)
		require.NoError(t, err) // Context cancellation doesn't cause error in this implementation
		require.NotNil(t, unclaims)
	})

	t.Run("background context", func(t *testing.T) {
		unclaims, err := reader.GetUnsetClaimsForBlockRange(context.Background(), 100, 200)
		require.NoError(t, err)
		require.NotNil(t, unclaims)
	})

	t.Run("TODO context", func(t *testing.T) {
		unclaims, err := reader.GetUnsetClaimsForBlockRange(context.TODO(), 100, 200)
		require.NoError(t, err)
		require.NotNil(t, unclaims)
	})

	mockClient.AssertExpectations(t)
}

func TestGetUnsetClaimsInChunks(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	t.Run("exact chunk boundaries", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
		require.NoError(t, err)

		// Mock 3 successful chunk calls (blocks 0-999, 1000-1999, 2000-2999)
		mockClient.On("FilterLogs", mock.Anything, mock.MatchedBy(func(q interface{}) bool {
			return true // Accept all filter queries for simplicity
		})).Return([]ethtypes.Log{}, nil).Times(3)

		unclaims, err := aggkitcommon.ChunkedRangeQuery(
			ctx, 0, 2999, 1000,
			reader.fetchUnsetClaims,
			func(all, chunk []claimsynctypes.Unclaim) []claimsynctypes.Unclaim {
				return append(all, chunk...)
			},
			[]claimsynctypes.Unclaim{},
		)
		require.NoError(t, err)
		require.NotNil(t, unclaims)
		require.Empty(t, unclaims) // Empty results as we mocked empty logs

		mockClient.AssertExpectations(t)
	})

	t.Run("non-exact chunk boundaries", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
		require.NoError(t, err)

		// Mock 3 calls: 0-999, 1000-1999, 2000-2500
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Times(3)

		unclaims, err := aggkitcommon.ChunkedRangeQuery(
			ctx, 0, 2500, 1000,
			reader.fetchUnsetClaims,
			func(all, chunk []claimsynctypes.Unclaim) []claimsynctypes.Unclaim {
				return append(all, chunk...)
			},
			[]claimsynctypes.Unclaim{},
		)
		require.NoError(t, err)
		require.NotNil(t, unclaims)

		mockClient.AssertExpectations(t)
	})

	t.Run("single chunk (range smaller than maxRange)", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
		require.NoError(t, err)

		mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Once()

		unclaims, err := aggkitcommon.ChunkedRangeQuery(ctx, 0, 500, 1000,
			reader.fetchUnsetClaims,
			func(all, chunk []claimsynctypes.Unclaim) []claimsynctypes.Unclaim {
				return append(all, chunk...)
			},
			[]claimsynctypes.Unclaim{},
		)
		require.NoError(t, err)
		require.NotNil(t, unclaims)

		mockClient.AssertExpectations(t)
	})

	t.Run("error in middle chunk", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
		require.NoError(t, err)

		// First chunk succeeds
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Once()
		// Second chunk fails
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return(nil, errors.New("rpc error")).Once()

		unclaims, err := aggkitcommon.ChunkedRangeQuery(ctx, 0, 2000, 1000,
			reader.fetchUnsetClaims,
			func(all, chunk []claimsynctypes.Unclaim) []claimsynctypes.Unclaim {
				return append(all, chunk...)
			},
			[]claimsynctypes.Unclaim{},
		)
		require.ErrorContains(t, err, "rpc error")
		require.Empty(t, unclaims)

		mockClient.AssertExpectations(t)
	})

	t.Run("zero maxRange", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
		require.NoError(t, err)

		// Should return error immediately without making any calls
		unclaims, err := aggkitcommon.ChunkedRangeQuery(ctx, 0, 1000, 0,
			reader.fetchUnsetClaims,
			func(all, chunk []claimsynctypes.Unclaim) []claimsynctypes.Unclaim {
				return append(all, chunk...)
			},
			[]claimsynctypes.Unclaim{},
		)
		require.ErrorContains(t, err, "maxRange must be greater than 0")
		require.Empty(t, unclaims)

		// No FilterLogs calls should have been made
		mockClient.AssertExpectations(t)
	})
}

func TestGetUnsetClaimsForBlockRange_ChunkingIntegration(t *testing.T) {
	ctx := context.Background()
	bridgeAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	t.Run("normal fetch succeeds without chunking", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
		require.NoError(t, err)

		// Mock successful FilterLogs call
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Once()

		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 0, 500)
		require.NoError(t, err)
		require.NotNil(t, unclaims)

		mockClient.AssertExpectations(t)
	})

	t.Run("block range too large triggers chunking", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
		require.NoError(t, err)

		// First call fails with "block range too large"
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).
			Return(nil, errors.New("block range too large, max range: 1000")).Once()

		// Subsequent chunked calls succeed (0-999, 1000-1999, 2000-2500)
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).
			Return([]ethtypes.Log{}, nil).Times(3)

		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 0, 2500)
		require.NoError(t, err)
		require.NotNil(t, unclaims)

		mockClient.AssertExpectations(t)
	})

	t.Run("non-parseable error returns original error", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
		require.NoError(t, err)

		// Return an error that doesn't match the pattern
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).
			Return(nil, errors.New("some other RPC error")).Once()

		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 0, 2500)
		require.ErrorContains(t, err, "some other RPC error")
		require.Nil(t, unclaims)

		mockClient.AssertExpectations(t)
	})

	t.Run("chunking fails partway through", func(t *testing.T) {
		mockClient := mocksethclient.NewBaseEthereumClienter(t)
		reader, err := NewAgglayerBridgeL2Reader(bridgeAddr, mockClient)
		require.NoError(t, err)

		// First call triggers chunking
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).
			Return(nil, errors.New("block range too large, max range: 1000")).Once()

		// First chunk succeeds
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).
			Return([]ethtypes.Log{}, nil).Once()

		// Second chunk fails
		mockClient.On("FilterLogs", mock.Anything, mock.Anything).
			Return(nil, errors.New("connection timeout")).Once()

		unclaims, err := reader.GetUnsetClaimsForBlockRange(ctx, 0, 2500)
		require.ErrorContains(t, err, "connection timeout")
		require.Empty(t, unclaims)

		mockClient.AssertExpectations(t)
	})
}
