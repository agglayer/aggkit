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
		ExtraParams: map[string]interface{}{
			ExtraParamFieldName: "http://anotherURL:1234",
		},
	}
	eth, err := NewRPCClientModeOp(cfg)
	require.NoError(t, err)
	require.NotNil(t, eth)

	cfg.Mode = config.RPCModeOp
	eth, err = NewRPCClientModeOp(cfg)
	require.NoError(t, err)
	require.NotNil(t, eth)

	cfg.URL = "noproto://localhost"
	_, err = NewRPCClientModeOp(cfg)
	require.Error(t, err)

	cfg = config.L2RPCClientConfig{}
	_, err = NewRPCClientModeOp(cfg)
	require.Error(t, err)
}
