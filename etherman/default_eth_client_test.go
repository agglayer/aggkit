package etherman

import (
	"context"
	"fmt"
	"os"
	"testing"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDefaultEthClientExploratory(t *testing.T) {
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
	client.HashFromJSON = true
	bn, err := aggkittypes.NewBlockNumberFinality("FinalizedBlock/5")
	require.NoError(t, err)
	ctx := t.Context()
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
			*rawEth = &blockRawEth{
				Number:    "0x5f", // 95 in hex
				Hash:      "0xabc123",
				Timestamp: "1234",
			}
		})

	mockRPCClient.
		EXPECT().
		CallContext(
			ctx,
			mock.Anything,
			"eth_getBlockByNumber",
			"0x64",
			false,
		).
		Return(nil).
		Run(func(ctx context.Context, result interface{}, method string, args ...interface{}) {
			rawEth, ok := result.(**blockRawEth)
			require.True(t, ok)
			*rawEth = &blockRawEth{
				Number:    "0x64", // 100 in hex
				Hash:      "0xabc123",
				Timestamp: "1234",
			}
		})
	// Call CustomHeaderByNumber
	header, err := client.CustomHeaderByNumber(ctx, bn)
	require.NoError(t, err)
	require.NotNil(t, header)
	require.Equal(t, uint64(100), header.Number)
	require.Equal(t, "0x0000000000000000000000000000000000000000000000000000000000abc123", header.Hash.Hex())
	require.Equal(t, bn, header.RequestedBlock)
}
