package etherman

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/etherman/mocks"
	"github.com/agglayer/aggkit/opnode"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
)

func TestNewRPCClientModeOp(t *testing.T) {
	cfg := ethermanconfig.RPCClientConfig{
		URL:  "http://localhost:1234",
		Mode: ethermanconfig.RPCModeBasic,
		ExtraParams: map[string]interface{}{
			ExtraParamFieldName: "http://anotherURL:1234",
		},
	}
	eth, err := NewRPCClientModeOp(cfg)
	require.NoError(t, err)
	require.NotNil(t, eth)

	cfg.URL = "noproto://localhost"
	_, err = NewRPCClientModeOp(cfg)
	require.Error(t, err)
}

func TestHeaderByNumber(t *testing.T) {
	mockEth := mocks.NewEthClienter(t)
	mockOpNode := mocks.NewOpNodeClienter(t)
	sut := NewRPCOpNodeDecorator(mockEth, mockOpNode)
	ctx := context.TODO()
	finalizedBlockNumber := big.NewInt(int64(rpc.FinalizedBlockNumber))
	opBlock := &opnode.BlockInfo{
		Number: 1234,
	}
	rpcHeader := &types.Header{
		Number: big.NewInt(1234),
	}
	customError := fmt.Errorf("custom error")
	t.Run("happy path", func(t *testing.T) {
		mockOpNode.EXPECT().FinalizedL2Block().Return(opBlock, nil).Once()

		mockEth.EXPECT().HeaderByNumber(ctx, big.NewInt(1234)).Return(rpcHeader, nil).Once()
		hdr, err := sut.HeaderByNumber(ctx, finalizedBlockNumber)
		require.NoError(t, err)
		require.NotNil(t, hdr)
		require.Equal(t, rpcHeader, hdr)
	})

	t.Run("require no finalized block", func(t *testing.T) {
		mockEth.EXPECT().HeaderByNumber(ctx, big.NewInt(1234)).Return(rpcHeader, nil).Once()
		hdr, err := sut.HeaderByNumber(ctx, big.NewInt(1234))
		require.NoError(t, err)
		require.NotNil(t, hdr)
		require.Equal(t, rpcHeader, hdr)
	})

	t.Run("fails opNode call", func(t *testing.T) {
		mockOpNode.EXPECT().FinalizedL2Block().Return(nil, customError).Once()
		hdr, err := sut.HeaderByNumber(ctx, finalizedBlockNumber)
		require.Error(t, err)
		require.Nil(t, hdr)
	})

}
