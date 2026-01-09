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

func TestDefaultEthClient_CustomHeaderByNumber(t *testing.T) {
	mockEthClient := mocks.NewEthereumClienter(t)
	mockRPCClient := mocks.NewRPCClienter(t)

	client := NewDefaultEthClient(mockEthClient, mockRPCClient, nil)
	bnFinalized5, err := aggkittypes.NewBlockNumberFinality("FinalizedBlock/5")
	require.NoError(t, err)
	ctx := t.Context()
	blockRaw95 := &blockRawEth{
		Number:    "0x5f", // 95 in hex
		Hash:      "0xabc123",
		Timestamp: "1234",
	}

	blockRaw100 := &blockRawEth{
		Number:    "0x64", // 100 in hex
		Hash:      "0xabc123",
		Timestamp: "1234",
	}

	t.Run("FinalizedBlock with offset", func(t *testing.T) {
		client.HashFromJSON = true
		// Setup mock for rpcGetBlockByNumber
		// Call to resolve finalized block
		mockRPCClient.
			EXPECT().
			CallContext(
				ctx,
				mock.Anything,
				"eth_getBlockByNumber",
				"finalized",
				false,
			).
			Return(nil).
			Run(func(ctx context.Context, result interface{}, method string, args ...interface{}) {
				rawEth, ok := result.(**blockRawEth)
				require.True(t, ok)
				*rawEth = blockRaw95
			}).Once()

		mockRPCClient.
			EXPECT().
			CallContext(ctx, mock.Anything, "eth_getBlockByNumber", "0x64", false).
			Return(nil).
			Run(func(ctx context.Context, result interface{}, method string, args ...interface{}) {
				rawEth, ok := result.(**blockRawEth)
				require.True(t, ok)
				*rawEth = blockRaw100
			}).Once()
		// Call CustomHeaderByNumber
		header, err := client.CustomHeaderByNumber(ctx, bnFinalized5)
		require.NoError(t, err)
		require.NotNil(t, header)
		require.Equal(t, uint64(100), header.Number)
		require.Equal(t, "0x0000000000000000000000000000000000000000000000000000000000abc123", header.Hash.Hex())
		require.Equal(t, bnFinalized5, header.RequestedBlock)
	})

	t.Run("Latest block", func(t *testing.T) {
		client.HashFromJSON = true
		ctx := t.Context()

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
		mockRPCClient.
			EXPECT().CallContext(ctx, mock.Anything, "eth_getBlockByNumber", "finalized", false).
			Return(fmt.Errorf("rpc error"))
		_, err := client.CustomHeaderByNumber(ctx, bnFinalized5)
		require.Error(t, err)
	})

	t.Run("use HashFromJSON=false (geth call)", func(t *testing.T) {
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
}
