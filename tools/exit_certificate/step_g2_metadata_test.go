package exit_certificate

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestCallGetTokenMetadata(t *testing.T) {
	t.Parallel()
	bridge := common.HexToAddress("0xbridge")
	token := common.HexToAddress("0xtoken")
	want := []byte{0xde, 0xad, 0xbe, 0xef}
	out, err := bridgeABI.Methods["getTokenMetadata"].Outputs.Pack(want)
	require.NoError(t, err)

	srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		require.Equal(t, rpcMethodEthCall, method)
		return hexResult(out), nil
	})
	got, err := callGetTokenMetadata(context.Background(), srv.URL, bridge, token)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestCallGasTokenMetadata(t *testing.T) {
	t.Parallel()
	bridge := common.HexToAddress("0xbridge")
	out, err := bridgeABI.Methods["gasTokenMetadata"].Outputs.Pack([]byte{})
	require.NoError(t, err)

	srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return hexResult(out), nil
	})
	got, err := callGasTokenMetadata(context.Background(), srv.URL, bridge)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestGenerateMetadataNativeAndERC20(t *testing.T) {
	t.Parallel()
	backend := newMockBackend()
	backend.gasTokenMeta = func(context.Context) ([]byte, error) { return []byte{}, nil }
	backend.tokenMeta = func(_ context.Context, _ common.Address) ([]byte, error) {
		return []byte{0x01, 0x02}, nil
	}

	origin := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	wrapped := common.HexToAddress("0x00000000000000000000000000000000000000bb")
	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			nativeAssetExit(common.HexToAddress("0xdead"), 5),
			{
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 99, OriginTokenAddress: origin},
				DestinationNetwork: 0,
				DestinationAddress: common.HexToAddress("0xbeef"),
				Amount:             big.NewInt(7),
			},
		},
	}
	// LBT maps the external-origin token to its L2 wrapped address (avoids a getTokenWrappedAddress RPC).
	lbt := []LBTEntry{{OriginNetwork: 99, OriginTokenAddress: origin, WrappedTokenAddress: wrapped}}
	cfg := &Config{L2NetworkID: 1, L2RPCURL: gasTokenStubURL(t)}

	metas, err := generateMetadata(context.Background(), backend, cfg, cert, lbt)
	require.NoError(t, err)
	require.Len(t, metas, 2)
	require.Empty(t, metas[0])                     // native → gas token metadata (empty)
	require.Equal(t, []byte{0x01, 0x02}, metas[1]) // ERC-20 → getTokenMetadata
}

func TestWaitForAnvilReady(t *testing.T) {
	t.Parallel()
	srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		require.Equal(t, rpcMethodEthBlockNumber, method)
		return quoted("0x1"), nil
	})
	require.NoError(t, waitForAnvil(context.Background(), srv.URL))
}

func TestWaitForAnvilContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // first probe against an unreachable URL fails, then the select sees ctx.Done
	err := waitForAnvil(ctx, "http://127.0.0.1:1")
	require.ErrorIs(t, err, context.Canceled)
}

func TestEthCallBytesErrors(t *testing.T) {
	t.Parallel()
	bridge := common.HexToAddress("0xbridge")
	callData, err := bridgeABI.Pack("gasTokenMetadata")
	require.NoError(t, err)

	t.Run("rpc error", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return nil, revertErr()
		})
		_, err := ethCallBytes(context.Background(), srv.URL, bridge, callData, "gasTokenMetadata")
		require.Error(t, err)
	})

	t.Run("invalid hex result", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return quoted("0xzz"), nil
		})
		_, err := ethCallBytes(context.Background(), srv.URL, bridge, callData, "gasTokenMetadata")
		require.ErrorContains(t, err, "decode gasTokenMetadata hex")
	})

	t.Run("undecodable abi", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return quoted("0x00"), nil // too short to unpack a bytes return
		})
		_, err := ethCallBytes(context.Background(), srv.URL, bridge, callData, "gasTokenMetadata")
		require.ErrorContains(t, err, "unpack gasTokenMetadata")
	})
}
