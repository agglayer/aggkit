package etherman

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	mockaggkittypes "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBlockRawEth_ToBlockHeader(t *testing.T) {
	t.Run("populates logs bloom when present", func(t *testing.T) {
		var bloom ethtypes.Bloom
		bloom.Add(common.HexToAddress("0xaaaa").Bytes())
		raw := &blockRawEth{
			Number:     "0x1",
			Hash:       "0xabc",
			Timestamp:  "0x123",
			ParentHash: "0xdef",
			LogsBloom:  "0x" + common.Bytes2Hex(bloom.Bytes()),
		}

		header, err := raw.ToBlockHeader()

		require.NoError(t, err)
		require.NotNil(t, header.LogsBloom)
		require.Equal(t, bloom, *header.LogsBloom)
	})

	t.Run("leaves logs bloom nil when absent", func(t *testing.T) {
		raw := &blockRawEth{
			Number:     "0x1",
			Hash:       "0xabc",
			Timestamp:  "0x123",
			ParentHash: "0xdef",
		}

		header, err := raw.ToBlockHeader()

		require.NoError(t, err)
		require.Nil(t, header.LogsBloom)
	})
}

func TestRetrieveBlockHeadersBatchExploratory(t *testing.T) {
	t.Skip("This test is for exploratory purposes to check the behavior of batch requests" +
		" It requires a real RPC endpoint because simulated doesn't support batch calls")
	ctx := t.Context()
	logger := log.WithFields("modules", "test")
	// Get L1URL from environment variable
	l1url := os.Getenv("L1URL")
	ethClient, err := ethclient.Dial(l1url)
	require.NoError(t, err)
	latestBlockNumber, err := ethClient.BlockNumber(ctx)
	require.NoError(t, err)
	log.Infof("Latest block number: %d", latestBlockNumber)
	rpcClient, err := rpc.DialContext(ctx, l1url)
	require.NoError(t, err)
	requestedBlockNumbers := []uint64{latestBlockNumber - 10, latestBlockNumber, latestBlockNumber + 10}

	res, err := RetrieveBlockHeadersBatch(ctx, logger,
		rpcClient,
		requestedBlockNumbers, 10)
	require.NoError(t, err)
	require.False(t, res.Success())
	require.True(t, res.PartialSuccess())
	require.Equal(t, 2, len(res.Headers))
	for _, number := range requestedBlockNumbers {
		err, ok := res.Errors[number]
		if ok {
			isNotFound := IsErrNotFound(err)
			require.True(t, isNotFound, "Expected error for block %d to be not found, got: %s", number, err.Error())
			log.Infof("Error retrieving block header for block %d: %s", number, err.Error())
			continue
		}
		require.NotNil(t, res.Headers[number])
		log.Infof(" Retrieved block header for block %d: hash %s", number, res.Headers[number].Hash.Hex())
	}
}

func TestRetrieveBlockHeaders(t *testing.T) {
	ctx := t.Context()
	logger := log.WithFields("test", "test")
	blockNumbers := []uint64{
		100,
		200,
	}
	maxConcurrency := 5

	t.Run("uses batch when rpcClient is provided", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		mockRPCClient := mockaggkittypes.NewRPCClienter(t)
		mockRPCClient.EXPECT().BatchCallContext(mock.Anything, mock.Anything).
			Run(func(ctx context.Context, b []rpc.BatchElem) {
				for idx := range b {
					require.Equal(t, b[idx].Method, "eth_getBlockByNumber")
					bn, ok := b[idx].Args[0].(string)
					require.True(t, ok)
					block, ok := b[idx].Result.(*blockRawEth)
					require.True(t, ok)
					block.Number = bn
					hash := common.BytesToHash([]byte{byte(idx + 1)})
					block.Hash = hash.Hex()
					block.Timestamp = "0x123"
				}
			}).
			Return(nil).Once()
		result, err := RetrieveBlockHeaders(ctx, logger, mockEthClient, mockRPCClient, blockNumbers, maxConcurrency)

		require.NoError(t, err)
		require.True(t, result.Success())
		assert.Equal(t, len(blockNumbers), len(result.Headers))
	})

	t.Run("uses legacy when rpcClient is nil", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		for _, bn := range blockNumbers {
			mockEthClient.EXPECT().
				CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(bn)).
				Return(&aggkittypes.BlockHeader{Number: bn}, nil).Once()
		}
		result, err := RetrieveBlockHeaders(ctx, logger, mockEthClient, nil, blockNumbers, maxConcurrency)

		require.NoError(t, err)
		require.True(t, result.Success())
		assert.Equal(t, len(blockNumbers), len(result.Headers))
	})

	t.Run("propagates error from batch method", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		mockRPCClient := mockaggkittypes.NewRPCClienter(t)
		mockRPCClient.EXPECT().BatchCallContext(mock.Anything, mock.Anything).Return(errors.New("batch error")).Once()
		_, err := RetrieveBlockHeaders(ctx, logger, mockEthClient, mockRPCClient, blockNumbers, maxConcurrency)
		require.Error(t, err)
		require.Contains(t, err.Error(), "batch error")
	})

	t.Run("collects errors from legacy method", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(nil, errors.New("legacy error")).Times(len(blockNumbers))
		result, err := RetrieveBlockHeaders(ctx, logger, mockEthClient, nil, blockNumbers, maxConcurrency)

		require.NoError(t, err) // No catastrophic error
		require.False(t, result.Success())
		require.Equal(t, len(blockNumbers), len(result.Errors))
		for _, blockErr := range result.Errors {
			require.Contains(t, blockErr.Error(), "legacy error")
		}
	})
}

func TestRetrieveBlockHeadersLegacy(t *testing.T) {
	ctx := t.Context()
	logger := log.WithFields("test", "test")
	blockNumbers := []uint64{100, 200, 400, 500}
	maxConcurrency := 1

	t.Run("successful retrieval", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		for _, bn := range blockNumbers {
			mockEthClient.EXPECT().
				CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(bn)).
				Return(&aggkittypes.BlockHeader{Number: bn}, nil).Once()
		}
		result, err := RetrieveBlockHeadersLegacy(ctx, logger, mockEthClient, blockNumbers, maxConcurrency)

		require.NoError(t, err)
		require.True(t, result.Success())
		assert.Equal(t, len(blockNumbers), len(result.Headers))
		for _, bn := range blockNumbers {
			require.Equal(t, bn, result.Headers[bn].Number)
		}
	})

	t.Run("partial failure", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(blockNumbers[0])).
			Return(&aggkittypes.BlockHeader{Number: blockNumbers[0]}, nil).Once()
		for _, bn := range blockNumbers[1:] {
			mockEthClient.EXPECT().
				CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(bn)).
				Return(nil, errors.New("rpc error")).Once()
		}
		result, err := RetrieveBlockHeadersLegacy(ctx, logger, mockEthClient, blockNumbers, maxConcurrency)

		require.NoError(t, err)
		require.False(t, result.Success())
		require.True(t, result.PartialSuccess())
		require.Len(t, result.Headers, 1)
		require.Len(t, result.Errors, len(blockNumbers)-1)
	})

	t.Run("all fail", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(nil, errors.New("not found")).Times(len(blockNumbers))
		result, err := RetrieveBlockHeadersLegacy(ctx, logger, mockEthClient, blockNumbers, maxConcurrency)

		require.NoError(t, err)
		require.False(t, result.Success())
		require.Len(t, result.Errors, len(blockNumbers))
	})
}

func TestRetrieveBlockHeadersInBatchParallel(t *testing.T) {
	ctx := t.Context()
	logger := log.WithFields("test", "test")
	blockNumbers := []uint64{
		100,
		200,
		300,
		400,
	}
	maxConcurrency := 1

	result, err := retrieveBlockHeadersInBatchParallel(
		ctx,
		logger,
		func(ctx context.Context, blocks []uint64) (*BlockHeadersResult, error) {
			t.Logf("Retrieving blocks in batch: %v", blocks)
			result := NewBlockHeadersResult()
			for _, bn := range blocks {
				result.AddHeader(bn, &aggkittypes.BlockHeader{
					Number: bn,
				})
			}
			return result, nil
		}, blockNumbers, 2, maxConcurrency)

	require.NoError(t, err)
	require.True(t, result.Success())
	assert.Equal(t, len(blockNumbers), len(result.Headers))
	for _, bn := range blockNumbers {
		header, exists := result.Headers[bn]
		require.True(t, exists)
		require.NotNil(t, header)
		assert.Equal(t, bn, header.Number)
	}
}

func TestSplitBlockNumbersIntoChunks(t *testing.T) {
	tests := []struct {
		name      string
		input     []uint64
		chunkSize int
		expected  [][]uint64
	}{

		{
			name:      "single chunk",
			input:     []uint64{1, 2, 3},
			chunkSize: 5,
			expected: [][]uint64{
				{1, 2, 3},
			},
		},
		{
			name:      "single exact chunk",
			input:     []uint64{1, 2, 3},
			chunkSize: 3,
			expected: [][]uint64{
				{1, 2, 3},
			},
		},
		{
			name:      "double chunk",
			input:     []uint64{1, 2, 3, 4},
			chunkSize: 3,
			expected: [][]uint64{
				{1, 2, 3},
				{4},
			},
		},
		{
			name:      "empty input",
			input:     []uint64{},
			chunkSize: 3,
			expected:  [][]uint64{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blockMap := tt.input
			chunks := splitBlockNumbersIntoChunks(blockMap, tt.chunkSize)

			var result [][]uint64
			for _, chunk := range chunks {
				var chunkList []uint64
				chunkList = append(chunkList, chunk...)
				result = append(result, chunkList)
			}

			assert.Equal(t, len(tt.expected), len(result), "Number of chunks should match: "+tt.name)
			for i := range tt.expected {
				assert.ElementsMatch(t, tt.expected[i], result[i], "Chunk %d should match:", i, tt.name)
			}
		})
	}
}

func TestBlockHeadersResult_AreAllErrorsNotFound(t *testing.T) {
	tests := []struct {
		name     string
		errors   map[uint64]error
		expected bool
	}{
		{
			name:     "no errors",
			errors:   map[uint64]error{},
			expected: true,
		},
		{
			name: "all errors are ErrNotFound",
			errors: map[uint64]error{
				100: ErrNotFound,
				200: ErrNotFound,
				300: ErrNotFound,
			},
			expected: true,
		},
		{
			name: "all errors have exact 'not found' message",
			errors: map[uint64]error{
				100: errors.New("not found"),
				200: errors.New("not found"),
			},
			expected: true,
		},
		{
			name: "mixed - some ErrNotFound, some other errors",
			errors: map[uint64]error{
				100: ErrNotFound,
				200: errors.New("connection timeout"),
				300: ErrNotFound,
			},
			expected: false,
		},
		{
			name: "errors with 'not found' in message but not exact match",
			errors: map[uint64]error{
				100: errors.New("batch element error: not found"),
				200: errors.New("converting block: not found"),
			},
			expected: true, // IsErrNotFound requires exact "not found" message
		},
		{
			name: "no not found errors",
			errors: map[uint64]error{
				100: errors.New("connection error"),
				200: errors.New("timeout"),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &BlockHeadersResult{
				Headers: make(map[uint64]*aggkittypes.BlockHeader),
				Errors:  tt.errors,
			}
			assert.Equal(t, tt.expected, result.AreAllErrorsNotFound())
		})
	}
}

func TestBlockHeadersResult_ListBlocksNumberNotFound(t *testing.T) {
	tests := []struct {
		name     string
		errors   map[uint64]error
		expected []uint64
	}{
		{
			name:     "no errors",
			errors:   map[uint64]error{},
			expected: nil,
		},
		{
			name: "all errors are ErrNotFound",
			errors: map[uint64]error{
				300: ErrNotFound,
				100: ErrNotFound,
				200: ErrNotFound,
			},
			expected: []uint64{100, 200, 300}, // Should be sorted
		},
		{
			name: "all errors have exact 'not found' message",
			errors: map[uint64]error{
				300: errors.New("not found"),
				100: errors.New("not found"),
			},
			expected: []uint64{100, 300}, // Should be sorted
		},
		{
			name: "mixed errors - some not found, some other",
			errors: map[uint64]error{
				100: ErrNotFound,
				200: errors.New("connection timeout"),
				300: ErrNotFound,
				150: errors.New("other error"),
				250: errors.New("not found"),
			},
			expected: []uint64{100, 250, 300}, // Only not found, sorted
		},
		{
			name: "no not found errors",
			errors: map[uint64]error{
				100: errors.New("connection error"),
				200: errors.New("timeout"),
			},
			expected: nil,
		},
		{
			name: "errors containing no 'not found'",
			errors: map[uint64]error{
				500: errors.New("batch element error"),
				100: errors.New("converting block"),
				300: errors.New("some other error"),
			},
			expected: nil, // IsErrNotFound requires exact "not found" message
		},
		{
			name: "mixed not found with others",
			errors: map[uint64]error{
				100: ErrNotFound,                                  // Exact match
				200: errors.New("not found"),                      // Exact message
				300: errors.New("batch element error: not found"), // Not exact
				400: errors.New("timeout"),                        // Other error
			},
			expected: []uint64{100, 200, 300}, // Only "not found"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &BlockHeadersResult{
				Headers: make(map[uint64]*aggkittypes.BlockHeader),
				Errors:  tt.errors,
			}
			got := result.ListBlocksNumberNotFound()
			assert.Equal(t, tt.expected, got)
		})
	}
}
