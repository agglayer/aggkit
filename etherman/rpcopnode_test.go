package etherman

import (
	"testing"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"

	"github.com/stretchr/testify/require"
)

func TestXxx(t *testing.T) {
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
