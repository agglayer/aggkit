package etherman

import (
	"testing"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func TestNewRPCClient(t *testing.T) {
	cfg := ethermanconfig.RPCClientConfig{

		URL:  "http://localhost:1234",
		Mode: ethermanconfig.RPCModeBasic,
		ExtraParams: map[string]any{
			ExtraParamFieldName: "http://anotherURL:1234",
		},
	}
	logger := log.WithFields("module", "test")
	ctx := t.Context()
	eth, err := NewRPCClient(ctx, logger, cfg)
	require.NoError(t, err)
	require.NotNil(t, eth)

	cfg.Mode = ethermanconfig.RPCModeOp
	eth, err = NewRPCClient(ctx, logger, cfg)
	require.NoError(t, err)
	require.NotNil(t, eth)

	cfg.URL = "noproto://localhost"
	_, err = NewRPCClient(ctx, logger, cfg)
	require.ErrorContains(t, err, "no known transport for URL scheme \"noproto\"")

	cfg = ethermanconfig.RPCClientConfig{
		Mode: "invalid_mode",
	}
	_, err = NewRPCClient(ctx, logger, cfg)
	require.ErrorContains(t, err, "invalid RPC mode")

	cfg = ethermanconfig.RPCClientConfig{
		Mode: "",
	}
	// This is the default mode
	_, err = NewRPCClient(ctx, logger, cfg)
	require.ErrorContains(t, err, "dial unix: missing address")
}
