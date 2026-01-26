package etherman

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"testing"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/core/types"
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
		mockEthClient := mocks.NewEthereumClienter(t)
		mockRPCClient := mocks.NewRPCClienter(t)
		client := NewDefaultEthClient(mockEthClient, mockRPCClient, nil)
		client.HashFromJSON = false

		bnLatestMinus10, err := aggkittypes.NewBlockNumberFinality("LatestBlock/-10")
		require.NoError(t, err)

		// First call to resolve latest block (returns 100)
		mockEthClient.EXPECT().
			HeaderByNumber(ctx, (*big.Int)(nil)).
			Return(&types.Header{
				Number: big.NewInt(100),
			}, nil).Once()

		// Second call to get block 90 (100 - 10)
		mockEthClient.EXPECT().
			HeaderByNumber(ctx, big.NewInt(90)).
			Return(&types.Header{
				Number: big.NewInt(90),
			}, nil).Once()

		header, err := client.CustomHeaderByNumber(ctx, bnLatestMinus10)
		require.NoError(t, err)
		require.NotNil(t, header)
		require.Equal(t, uint64(90), header.Number)
		require.Equal(t, bnLatestMinus10, header.RequestedBlock)
	})

	t.Run("FinalizedBlock with negative offset (HashFromJSON=false)", func(t *testing.T) {
		mockEthClient := mocks.NewEthereumClienter(t)
		mockRPCClient := mocks.NewRPCClienter(t)
		client := NewDefaultEthClient(mockEthClient, mockRPCClient, nil)
		client.HashFromJSON = false

		bnFinalizedMinus5, err := aggkittypes.NewBlockNumberFinality("FinalizedBlock/-5")
		require.NoError(t, err)

		// First call to resolve finalized block (returns 100)
		mockEthClient.EXPECT().
			HeaderByNumber(ctx, big.NewInt(-3)).
			Return(&types.Header{
				Number: big.NewInt(100),
			}, nil).Once()

		// Second call to get block 95 (100 - 5)
		mockEthClient.EXPECT().
			HeaderByNumber(ctx, big.NewInt(95)).
			Return(&types.Header{
				Number: big.NewInt(95),
			}, nil).Once()

		header, err := client.CustomHeaderByNumber(ctx, bnFinalizedMinus5)
		require.NoError(t, err)
		require.NotNil(t, header)
		require.Equal(t, uint64(95), header.Number)
		require.Equal(t, bnFinalizedMinus5, header.RequestedBlock)
	})

	t.Run("SafeBlock with negative offset (HashFromJSON=false)", func(t *testing.T) {
		mockEthClient := mocks.NewEthereumClienter(t)
		mockRPCClient := mocks.NewRPCClienter(t)
		client := NewDefaultEthClient(mockEthClient, mockRPCClient, nil)
		client.HashFromJSON = false

		bnSafeMinus3, err := aggkittypes.NewBlockNumberFinality("SafeBlock/-3")
		require.NoError(t, err)

		// First call to resolve safe block (returns 50)
		mockEthClient.EXPECT().
			HeaderByNumber(ctx, big.NewInt(-4)).
			Return(&types.Header{
				Number: big.NewInt(50),
			}, nil).Once()

		// Second call to get block 47 (50 - 3)
		mockEthClient.EXPECT().
			HeaderByNumber(ctx, big.NewInt(47)).
			Return(&types.Header{
				Number: big.NewInt(47),
			}, nil).Once()

		header, err := client.CustomHeaderByNumber(ctx, bnSafeMinus3)
		require.NoError(t, err)
		require.NotNil(t, header)
		require.Equal(t, uint64(47), header.Number)
		require.Equal(t, bnSafeMinus3, header.RequestedBlock)
	})
}
