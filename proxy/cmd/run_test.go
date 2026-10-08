package main

import (
	"testing"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/stretchr/testify/require"
)

func TestValidateL2RPC(t *testing.T) {
	t.Run("default config is valid without URL", func(t *testing.T) {
		cfg := ethermanconfig.NewDefaultRPCClientConfig()
		cfg.Mode = ethermanconfig.RPCModeBasic
		require.NoError(t, validateL2RPC(*cfg))
	})

	t.Run("non basic mode is rejected", func(t *testing.T) {
		cfg := ethermanconfig.NewDefaultRPCClientConfig()
		cfg.Mode = ethermanconfig.RPCModeOp
		require.ErrorContains(t, validateL2RPC(*cfg), "only basic RPC mode")
	})

	t.Run("invalid mode is rejected", func(t *testing.T) {
		cfg := ethermanconfig.NewDefaultRPCClientConfig()
		cfg.Mode = ethermanconfig.RPCModeBasic
		cfg.Mode = "bogus"
		require.Error(t, validateL2RPC(*cfg))
	})
}
