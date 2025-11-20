package etherman

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	mockaggkittypes "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestConvertMapBlockRawEth(t *testing.T) {
	tests := []struct {
		name          string
		blocks        []*blockRawEth
		expected      []*aggkittypes.BlockHeader
		expectedError bool
	}{
		{
			name:          "empty map",
			blocks:        []*blockRawEth{},
			expected:      []*aggkittypes.BlockHeader{},
			expectedError: false,
		},
		{
			name: "single valid block",
			blocks: []*blockRawEth{
				{
					Number:     "0x7b",
					Hash:       "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
					Timestamp:  "0x5f5e100",
					ParentHash: "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
				},
			},
			expected: []*aggkittypes.BlockHeader{
				{
					Number: 123,
					Hash:   common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
					Time:   100000000,
					ParentHash: func() *common.Hash {
						h := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
						return &h
					}(),
				},
			},
			expectedError: false,
		},
		{
			name: "multiple valid blocks",
			blocks: []*blockRawEth{
				{
					Number:     "0x64",
					Hash:       "0x1111111111111111111111111111111111111111111111111111111111111111",
					Timestamp:  "0x1000",
					ParentHash: "0x2222222222222222222222222222222222222222222222222222222222222222",
				},
				{
					Number:     "0xc8",
					Hash:       "0x3333333333333333333333333333333333333333333333333333333333333333",
					Timestamp:  "0x2000",
					ParentHash: "0x4444444444444444444444444444444444444444444444444444444444444444",
				},
			},
			expected: []*aggkittypes.BlockHeader{
				{
					Number: 100,
					Hash:   common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
					Time:   4096,
					ParentHash: func() *common.Hash {
						h := common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
						return &h
					}(),
				},
				{
					Number: 200,
					Hash:   common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333"),
					Time:   8192,
					ParentHash: func() *common.Hash {
						h := common.HexToHash("0x4444444444444444444444444444444444444444444444444444444444444444")
						return &h
					}(),
				},
			},
			expectedError: false,
		},
		{
			name: "invalid block number format",
			blocks: []*blockRawEth{
				{
					Number:     "invalid",
					Hash:       "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
					Timestamp:  "0x5f5e100",
					ParentHash: "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
				},
			},
			expected:      nil,
			expectedError: true,
		},
		{
			name: "invalid timestamp format",
			blocks: []*blockRawEth{
				{
					Number:     "0x7b",
					Hash:       "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
					Timestamp:  "invalid",
					ParentHash: "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
				},
			},
			expected:      nil,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertSliceBlockRawEth(tt.blocks)

			if tt.expectedError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "convert: converting block number")
			} else {
				require.NoError(t, err)
				assert.Equal(t, len(tt.expected), len(result))
				for i, expectedHeader := range tt.expected {
					actualHeader := result[i]
					assert.Equal(t, expectedHeader.Number, actualHeader.Number)
					assert.Equal(t, expectedHeader.Hash, actualHeader.Hash)
					assert.Equal(t, expectedHeader.Time, actualHeader.Time)
					assert.Equal(t, expectedHeader.ParentHash, actualHeader.ParentHash)
				}
			}
		})
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
		assert.Equal(t, len(blockNumbers), len(result))
	})

	t.Run("uses legacy when rpcClient is nil", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		for _, bn := range blockNumbers {
			mockEthClient.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(int64(bn))).
				Return(&types.Header{
					Number: big.NewInt(int64(bn)),
					Time:   123,
				}, nil).Once()
		}
		result, err := RetrieveBlockHeaders(ctx, logger, mockEthClient, nil, blockNumbers, maxConcurrency)

		require.NoError(t, err)
		assert.Equal(t, len(blockNumbers), len(result))
	})

	t.Run("propagates error from batch method", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		mockRPCClient := mockaggkittypes.NewRPCClienter(t)
		mockRPCClient.EXPECT().BatchCallContext(mock.Anything, mock.Anything).Return(errors.New("batch error")).Once()
		_, err := RetrieveBlockHeaders(ctx, logger, mockEthClient, mockRPCClient, blockNumbers, maxConcurrency)
		require.Error(t, err)
		require.Contains(t, err.Error(), "batch error")
	})

	t.Run("propagates error from legacy method", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		mockEthClient.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(nil, errors.New("legacy error")).Maybe()
		_, err := RetrieveBlockHeaders(ctx, logger, mockEthClient, nil, blockNumbers, maxConcurrency)

		require.Error(t, err)
		require.Contains(t, err.Error(), "legacy error")
	})
}
func TestRetrieveBlockHeadersLegacy(t *testing.T) {
	ctx := t.Context()
	logger := log.WithFields("test", "test")
	blockNumbers := []uint64{
		100,
		200,
		400,
		500,
	}
	maxConcurrency := 1

	t.Run("successful retrieval", func(t *testing.T) {
		mockEthClient := mockaggkittypes.NewBaseEthereumClienter(t)
		for _, bn := range blockNumbers {
			mockEthClient.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(int64(bn))).
				Return(&types.Header{
					Number: big.NewInt(int64(bn)),
					Time:   123,
				}, nil).Once()
		}
		result, err := RetrieveBlockHeadersLegacy(ctx, logger, mockEthClient, blockNumbers, maxConcurrency)

		require.NoError(t, err)
		assert.Equal(t, len(blockNumbers), len(result))
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
		func(ctx context.Context, blocks []uint64) ([]*aggkittypes.BlockHeader, error) {
			t.Logf("Retrieving blocks in batch: %v", blocks)
			headers := make([]*aggkittypes.BlockHeader, len(blocks))
			for i, bn := range blocks {
				headers[i] = &aggkittypes.BlockHeader{
					Number: bn,
				}
			}
			return headers, nil
		}, blockNumbers, 2, maxConcurrency)

	require.NoError(t, err)
	assert.Equal(t, len(blockNumbers), len(result))
	for _, bn := range blockNumbers {
		header := getBlockHeader(bn, result)
		require.NotNil(t, header)
		assert.Equal(t, bn, header.Number)
	}
}

func getBlockHeader(bn uint64, headers []*aggkittypes.BlockHeader) *aggkittypes.BlockHeader {
	for _, h := range headers {
		if h.Number == bn {
			return h
		}
	}
	return nil
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
