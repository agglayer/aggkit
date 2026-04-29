package etherman

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"testing"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDefaultEthClientExploratory(t *testing.T) {
	t.Skip("Exploratory test, enable manually")
	l2url := os.Getenv("L2URL")
	ctx := t.Context()
	cfg := ethermanconfig.RPCClientConfig{
		URL:  l2url,
		Mode: ethermanconfig.RPCModeBasic,
	}

	client, err := NewRPCClient(ctx, nil, cfg)
	require.NoError(t, err)
	clientEth, ok := client.(*DefaultEthClient)
	require.True(t, ok)
	clientEth.HashFromJSON = true

	bn, err := aggkittypes.NewBlockNumberFinality("FinalizedBlock/10")
	require.NoError(t, err)
	header, err := clientEth.CustomHeaderByNumber(ctx, bn)
	require.NoError(t, err)
	fmt.Printf("header: %+v\n", header)
}

// testBlockWithOffsetHelper is a helper function for testing block tag resolution with offsets
func testBlockWithOffsetHelper(
	t *testing.T,
	ctx context.Context,
	blockTag string,
	blockNumFinality string,
	firstBlockNum uint64,
	firstBlockHash string,
	secondBlockNum uint64,
	secondBlockHash string,
) {
	t.Helper()
	mockEthClient := mocks.NewEthereumClienter(t)
	mockRPCClient := mocks.NewRPCClienter(t)
	client := NewDefaultEthClient(mockEthClient, mockRPCClient, nil)
	client.HashFromJSON = true

	bn, err := aggkittypes.NewBlockNumberFinality(blockNumFinality)
	require.NoError(t, err)

	firstBlock := &blockRawEth{
		Number:    fmt.Sprintf("0x%x", firstBlockNum),
		Hash:      firstBlockHash,
		Timestamp: "1234",
	}

	secondBlock := &blockRawEth{
		Number:    fmt.Sprintf("0x%x", secondBlockNum),
		Hash:      secondBlockHash,
		Timestamp: "1235",
	}

	// First call to resolve block tag
	mockRPCClient.
		EXPECT().
		CallContext(ctx, mock.Anything, "eth_getBlockByNumber", blockTag, false).
		Return(nil).
		Run(func(ctx context.Context, result interface{}, method string, args ...interface{}) {
			rawEth, ok := result.(**blockRawEth)
			require.True(t, ok)
			*rawEth = firstBlock
		}).Once()

	// Second call to get the final block after offset
	mockRPCClient.
		EXPECT().
		CallContext(ctx, mock.Anything, "eth_getBlockByNumber", fmt.Sprintf("0x%x", secondBlockNum), false).
		Return(nil).
		Run(func(ctx context.Context, result interface{}, method string, args ...interface{}) {
			rawEth, ok := result.(**blockRawEth)
			require.True(t, ok)
			*rawEth = secondBlock
		}).Once()

	header, err := client.CustomHeaderByNumber(ctx, bn)
	require.NoError(t, err)
	require.NotNil(t, header)
	require.Equal(t, secondBlockNum, header.Number)
	require.Equal(t, fmt.Sprintf("0x%064s", secondBlockHash[2:]), header.Hash.Hex())
	require.Equal(t, bn, header.RequestedBlock)
}

// testBlockWithOffsetHelperGeth is a helper function for testing block tag resolution with offsets using geth client
func testBlockWithOffsetHelperGeth(
	t *testing.T,
	ctx context.Context,
	blockNumFinality string,
	firstCallArg *big.Int,
	firstBlockNum uint64,
	secondBlockNum uint64,
) {
	t.Helper()
	mockEthClient := mocks.NewEthereumClienter(t)
	mockRPCClient := mocks.NewRPCClienter(t)
	client := NewDefaultEthClient(mockEthClient, mockRPCClient, nil)
	client.HashFromJSON = false

	bn, err := aggkittypes.NewBlockNumberFinality(blockNumFinality)
	require.NoError(t, err)

	mockEthClient.EXPECT().
		HeaderByNumber(ctx, firstCallArg).
		Return(&types.Header{
			Number: big.NewInt(int64(firstBlockNum)),
		}, nil).Once()

	mockEthClient.EXPECT().
		HeaderByNumber(ctx, big.NewInt(int64(secondBlockNum))).
		Return(&types.Header{
			Number: big.NewInt(int64(secondBlockNum)),
		}, nil).Once()

	header, err := client.CustomHeaderByNumber(ctx, bn)
	require.NoError(t, err)
	require.NotNil(t, header)
	require.Equal(t, secondBlockNum, header.Number)
	require.Equal(t, bn, header.RequestedBlock)
}

func TestDefaultEthClient_CustomHeaderByNumber(t *testing.T) {
	ctx := context.Background()

	t.Run("FinalizedBlock with offset", func(t *testing.T) {
		testBlockWithOffsetHelper(t, ctx, "finalized", "FinalizedBlock/5", 95, "0xabc123", 100, "0xabc123")
	})

	t.Run("Latest block", func(t *testing.T) {
		mockEthClient := mocks.NewEthereumClienter(t)
		mockRPCClient := mocks.NewRPCClienter(t)
		client := NewDefaultEthClient(mockEthClient, mockRPCClient, nil)
		client.HashFromJSON = true

		blockRaw95 := &blockRawEth{
			Number:    "0x5f", // 95 in hex
			Hash:      "0xabc123",
			Timestamp: "1234",
		}

		mockRPCClient.
			EXPECT().
			CallContext(ctx, mock.Anything, "eth_getBlockByNumber", "latest", false).
			Return(nil).
			Run(func(ctx context.Context, result interface{}, method string, args ...interface{}) {
				rawEth, ok := result.(**blockRawEth)
				require.True(t, ok)
				*rawEth = blockRaw95
			}).Once()
		header, err := client.CustomHeaderByNumber(ctx, nil)
		require.NoError(t, err)
		require.NotNil(t, header)
		require.Equal(t, uint64(95), header.Number)
	})

	t.Run("failed to find blockNumber for tag block", func(t *testing.T) {
		mockEthClient := mocks.NewEthereumClienter(t)
		mockRPCClient := mocks.NewRPCClienter(t)
		client := NewDefaultEthClient(mockEthClient, mockRPCClient, nil)
		client.HashFromJSON = true

		bnFinalized5, err := aggkittypes.NewBlockNumberFinality("FinalizedBlock/5")
		require.NoError(t, err)

		mockRPCClient.
			EXPECT().CallContext(ctx, mock.Anything, "eth_getBlockByNumber", "finalized", false).
			Return(fmt.Errorf("rpc error"))
		_, err = client.CustomHeaderByNumber(ctx, bnFinalized5)
		require.Error(t, err)
	})

	t.Run("use HashFromJSON=false (geth call)", func(t *testing.T) {
		mockEthClient := mocks.NewEthereumClienter(t)
		mockRPCClient := mocks.NewRPCClienter(t)
		client := NewDefaultEthClient(mockEthClient, mockRPCClient, nil)
		client.HashFromJSON = false

		mockEthClient.EXPECT().
			HeaderByNumber(ctx, (*big.Int)(nil)).
			Return(&types.Header{
				Number: big.NewInt(100),
			}, nil).Once()
		header, err := client.CustomHeaderByNumber(ctx, nil)
		require.NoError(t, err)
		require.NotNil(t, header)
	})

	t.Run("LatestBlock with negative offset", func(t *testing.T) {
		testBlockWithOffsetHelper(t, ctx, "latest", "LatestBlock/-10", 100, "0xdef456", 90, "0xabc789")
	})

	t.Run("FinalizedBlock with negative offset", func(t *testing.T) {
		testBlockWithOffsetHelper(t, ctx, "finalized", "FinalizedBlock/-5", 100, "0xfed123", 95, "0xabc456")
	})

	t.Run("SafeBlock with negative offset", func(t *testing.T) {
		testBlockWithOffsetHelper(t, ctx, "safe", "SafeBlock/-3", 50, "0x123abc", 47, "0x456def")
	})

	t.Run("PendingBlock with negative offset", func(t *testing.T) {
		testBlockWithOffsetHelper(t, ctx, "pending", "PendingBlock/-2", 101, "0x789abc", 99, "0xdef123")
	})

	t.Run("LatestBlock with negative offset (HashFromJSON=false)", func(t *testing.T) {
		testBlockWithOffsetHelperGeth(t, ctx, "LatestBlock/-10", nil, 100, 90)
	})

	t.Run("FinalizedBlock with negative offset (HashFromJSON=false)", func(t *testing.T) {
		testBlockWithOffsetHelperGeth(t, ctx, "FinalizedBlock/-5", big.NewInt(-3), 100, 95)
	})

	t.Run("SafeBlock with negative offset (HashFromJSON=false)", func(t *testing.T) {
		testBlockWithOffsetHelperGeth(t, ctx, "SafeBlock/-3", big.NewInt(-4), 50, 47)
	})
}

func TestDefaultEthClient_RetrieveBlockHeaders(t *testing.T) {
	ctx := context.Background()
	logger := log.WithFields("test", "test")
	blockNumbers := []uint64{100, 200}
	maxConcurrency := 2

	t.Run("batch path delegates to RPCClienter", func(t *testing.T) {
		mockEth := mocks.NewEthereumClienter(t)
		mockRPC := mocks.NewRPCClienter(t)
		cfg := &ethermanconfig.RPCClientConfig{BatchBlockHeaderRetrieval: true}
		client := NewDefaultEthClientWithLogger(logger, mockEth, mockRPC, cfg)

		mockRPC.EXPECT().BatchCallContext(mock.Anything, mock.Anything).
			Run(func(_ context.Context, b []rpc.BatchElem) {
				for idx := range b {
					block, ok := b[idx].Result.(*blockRawEth)
					require.True(t, ok)
					block.Number = fmt.Sprintf("0x%x", blockNumbers[idx])
					block.Hash = fmt.Sprintf("0x%064x", idx+1)
					block.Timestamp = "0x1"
				}
			}).
			Return(nil).Once()

		result, err := client.RetrieveBlockHeaders(ctx, blockNumbers, maxConcurrency)
		require.NoError(t, err)
		require.True(t, result.Success())
		require.Len(t, result.Headers, len(blockNumbers))
	})

	t.Run("batch path propagates RPC error", func(t *testing.T) {
		mockEth := mocks.NewEthereumClienter(t)
		mockRPC := mocks.NewRPCClienter(t)
		cfg := &ethermanconfig.RPCClientConfig{BatchBlockHeaderRetrieval: true}
		client := NewDefaultEthClientWithLogger(logger, mockEth, mockRPC, cfg)

		mockRPC.EXPECT().BatchCallContext(mock.Anything, mock.Anything).
			Return(errors.New("rpc unavailable")).Once()

		_, err := client.RetrieveBlockHeaders(ctx, blockNumbers, maxConcurrency)
		require.Error(t, err)
		require.Contains(t, err.Error(), "rpc unavailable")
	})

	t.Run("legacy path with HashFromJSON=false uses HeaderByNumber", func(t *testing.T) {
		mockEth := mocks.NewEthereumClienter(t)
		mockRPC := mocks.NewRPCClienter(t)
		cfg := &ethermanconfig.RPCClientConfig{BatchBlockHeaderRetrieval: false, HashFromJSON: false}
		client := NewDefaultEthClientWithLogger(logger, mockEth, mockRPC, cfg)

		for _, bn := range blockNumbers {
			mockEth.EXPECT().
				HeaderByNumber(mock.Anything, new(big.Int).SetUint64(bn)).
				Return(&types.Header{Number: new(big.Int).SetUint64(bn)}, nil).Once()
		}

		result, err := client.RetrieveBlockHeaders(ctx, blockNumbers, maxConcurrency)
		require.NoError(t, err)
		require.True(t, result.Success())
		require.Len(t, result.Headers, len(blockNumbers))
	})

	t.Run("legacy path with HashFromJSON=true uses CallContext", func(t *testing.T) {
		mockEth := mocks.NewEthereumClienter(t)
		mockRPC := mocks.NewRPCClienter(t)
		cfg := &ethermanconfig.RPCClientConfig{BatchBlockHeaderRetrieval: false, HashFromJSON: true}
		client := NewDefaultEthClientWithLogger(logger, mockEth, mockRPC, cfg)

		for _, bn := range blockNumbers {
			bnHex := fmt.Sprintf("0x%x", bn)
			mockRPC.EXPECT().
				CallContext(mock.Anything, mock.Anything, "eth_getBlockByNumber", bnHex, false).
				Run(func(_ context.Context, result interface{}, _ string, args ...interface{}) {
					raw, ok := result.(**blockRawEth)
					require.True(t, ok)
					*raw = &blockRawEth{
						Number:    bnHex,
						Hash:      fmt.Sprintf("0x%064x", bn),
						Timestamp: "0x1",
					}
				}).
				Return(nil).Once()
		}

		result, err := client.RetrieveBlockHeaders(ctx, blockNumbers, maxConcurrency)
		require.NoError(t, err)
		require.True(t, result.Success())
		require.Len(t, result.Headers, len(blockNumbers))
	})

	t.Run("legacy path collects per-block errors without failing", func(t *testing.T) {
		mockEth := mocks.NewEthereumClienter(t)
		mockRPC := mocks.NewRPCClienter(t)
		cfg := &ethermanconfig.RPCClientConfig{BatchBlockHeaderRetrieval: false, HashFromJSON: false}
		client := NewDefaultEthClientWithLogger(logger, mockEth, mockRPC, cfg)

		mockEth.EXPECT().
			HeaderByNumber(mock.Anything, mock.Anything).
			Return(nil, errors.New("not found")).Times(len(blockNumbers))

		result, err := client.RetrieveBlockHeaders(ctx, blockNumbers, maxConcurrency)
		require.NoError(t, err)
		require.False(t, result.Success())
		require.Len(t, result.Errors, len(blockNumbers))
	})
}
