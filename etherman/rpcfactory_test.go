package etherman

import (
	"testing"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/stretchr/testify/require"
)

func TestNewRPCClient(t *testing.T) {
	cfg := ethermanconfig.L2RPCClientConfig{
		RPCClientConfig: ethermanconfig.RPCClientConfig{
			URL: "http://localhost:1234",
		},
		Mode: ethermanconfig.RPCModeBasic,
		ExtraParams: map[string]any{
			ExtraParamFieldName: "http://anotherURL:1234",
		},
	}
	ctx := t.Context()
	eth, err := NewRPCClient(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, eth)

	cfg.Mode = ethermanconfig.RPCModeOp
	eth, err = NewRPCClient(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, eth)

	cfg.URL = "noproto://localhost"
	_, err = NewRPCClient(ctx, cfg)
	require.ErrorContains(t, err, "no known transport for URL scheme \"noproto\"")

	cfg = ethermanconfig.L2RPCClientConfig{}
	_, err = NewRPCClient(ctx, cfg)
	require.ErrorContains(t, err, "invalid RPC mode")
}
