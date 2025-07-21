package etherman

import (
	"testing"

	"github.com/agglayer/aggkit/config"
	"github.com/stretchr/testify/require"
)

func TestNewRPCClient(t *testing.T) {
	cfg := config.L2RPCClientConfig{
		RPCClientConfig: config.RPCClientConfig{
			URL: "http://localhost:1234",
		},
		Mode: config.RPCModeBasic,
		ExtraParams: map[string]any{
			ExtraParamFieldName: "http://anotherURL:1234",
		},
	}
	ctx := t.Context()
	eth, err := NewRPCClient(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, eth)

	cfg.Mode = config.RPCModeOp
	eth, err = NewRPCClient(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, eth)

	cfg.URL = "noproto://localhost"
	_, err = NewRPCClient(ctx, cfg)
	require.ErrorContains(t, err, "no known transport for URL scheme \"noproto\"")

	cfg = config.L2RPCClientConfig{}
	_, err = NewRPCClient(ctx, cfg)
	require.ErrorContains(t, err, "invalid RPC mode")
}
