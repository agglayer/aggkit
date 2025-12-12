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
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDefaultEthClientExploratory(t *testing.T) {
	l2url := os.Getenv("L2URL")
	ctx := t.Context()
	cfg := ethermanconfig.L2RPCClientConfig{
		RPCClientConfig: ethermanconfig.RPCClientConfig{
			URL: l2url,
		},
		Mode: ethermanconfig.RPCModeBasic,
	}

	client, err := NewRPCClient(ctx, cfg)
	require.NoError(t, err)
	clientEth, ok := client.(*DefaultEthClient)
	require.True(t, ok)
	clientEth.HashFromJSON = true
	number := big.NewInt(123)
	fmt.Printf("block: %s\n", rpc.BlockNumber(number.Int64()).String())
	fmt.Printf("block: %s\n", rpc.BlockNumber(rpc.SafeBlockNumber).String())
	fmt.Printf("block: %s\n", rpc.BlockNumber(rpc.LatestBlockNumber).String())
	bn, err := aggkittypes.NewBlockNumberFinality("FinalizedBlock/10")
	require.NoError(t, err)
	header, err := clientEth.CustomHeaderByNumber(ctx, &bn)
	require.NoError(t, err)
	fmt.Printf("header: %+v\n", header)

	//client.CustomHeaderByNumber(ctx, number)
}

func TestDefaultEthClient_CustomHeaderByNumber(t *testing.T) {
	mockEthClient := mocks.NewEthereumClienter(t)
	mockRPCClient := mocks.NewRPCClienter(t)

	client := NewDefaultEthClient(mockEthClient, mockRPCClient)
	bn, err := aggkittypes.NewBlockNumberFinality("FinalizedBlock/5")
	require.NoError(t, err)

	// Setup mock for rpcGetBlockByNumber
	mockRPCClient.
		EXPECT().
		CallContext(
			mock.Anything,
			mock.AnythingOfType("*etherman.blockRawEth"),
			"eth_getBlockByNumber",
			rpc.BlockNumber(100).String(),
		).
		Return(nil).
		Run(func(ctx context.Context, result interface{}, method string, args ...interface{}) {
			rawEth := args[0].(**blockRawEth)
			*rawEth = &blockRawEth{
				Number: "0x64", // 100 in hex
				Hash:   "0xabc123",
			}
		})
	// Call CustomHeaderByNumber
	header, err := client.CustomHeaderByNumber(context.Background(), &bn)
	require.NoError(t, err)
	require.NotNil(t, header)
	require.Equal(t, uint64(100), header.Number)
	require.Equal(t, "0xabc123", header.Hash.Hex())
	require.Equal(t, &bn, header.RequestedBlock)
}
