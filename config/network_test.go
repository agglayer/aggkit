package config

import (
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGetString(t *testing.T) {
	cfg := L2RPCClientConfig{
		RPCClientConfig: RPCClientConfig{
			URL:            "http://localhost:8123",
			MaxRetries:     3,
			InitialBackoff: types.Duration{Duration: 1000},
		},
		ExtraParams: map[string]any{
			"key":         "value",
			"another_key": 1234,
		},
		Mode: RPCModeBasic,
	}
	value, err := cfg.GetString("key")
	require.NoError(t, err)
	require.Equal(t, "value", value)
	_, err = cfg.GetString("another_key")
	require.Error(t, err)
	_, err = cfg.GetString("dont_exists_key")
	require.Error(t, err)
}

func TestL1NetworkConfig_Validate(t *testing.T) {
	validAddr := common.HexToAddress("0xDEAD")

	tests := []struct {
		name    string
		cfg     L1NetworkConfig
		wantErr error
	}{
		{
			name:    "missing RPC config",
			cfg:     L1NetworkConfig{},
			wantErr: ErrMissingRPCConfig,
		},
		{
			name: "missing RPC URL",
			cfg: L1NetworkConfig{
				RPC: RPCClientConfig{MaxRetries: 1}, // empty URL
			},
			wantErr: fmt.Errorf("invalid RPC configuration: %w", ErrMissingRPCURL),
		},
		{
			name: "missing RollupAddr",
			cfg: L1NetworkConfig{
				RPC: RPCClientConfig{URL: "http://localhost:8545"},
			},
			wantErr: ErrMissingRollupAddress,
		},
		{
			name: "missing RollupManagerAddr",
			cfg: L1NetworkConfig{
				RPC:        RPCClientConfig{URL: "http://localhost:8545"},
				RollupAddr: validAddr,
			},
			wantErr: ErrMissingRollupManagerAddress,
		},
		{
			name: "missing POLTokenAddr",
			cfg: L1NetworkConfig{
				RPC:               RPCClientConfig{URL: "http://localhost:8545"},
				RollupAddr:        validAddr,
				RollupManagerAddr: validAddr,
			},
			wantErr: ErrMissingPOLTokenAddress,
		},
		{
			name: "missing GlobalExitRootManagerAddr",
			cfg: L1NetworkConfig{
				RPC:               RPCClientConfig{URL: "http://localhost:8545"},
				RollupAddr:        validAddr,
				RollupManagerAddr: validAddr,
				POLTokenAddr:      validAddr,
			},
			wantErr: ErrMissingGlobalExitRootManagerAddress,
		},
		{
			name: "valid config",
			cfg: L1NetworkConfig{
				RPC:                       RPCClientConfig{URL: "http://localhost:8545"},
				RollupAddr:                validAddr,
				RollupManagerAddr:         validAddr,
				POLTokenAddr:              validAddr,
				GlobalExitRootManagerAddr: validAddr,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			require.Equal(t, tt.wantErr, err)
		})
	}
}

func TestL2RPCClientConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     L2RPCClientConfig
		wantErr error
	}{
		{
			name:    "missing RPC config",
			cfg:     L2RPCClientConfig{},
			wantErr: ErrMissingRPCConfig,
		},
		{
			name: "missing RPC URL",
			cfg: L2RPCClientConfig{
				RPCClientConfig: RPCClientConfig{MaxRetries: 1}, // empty URL
			},
			wantErr: fmt.Errorf("invalid RPC configuration: %w", ErrMissingRPCURL),
		},
		{
			name: "invalid RPC mode",
			cfg: L2RPCClientConfig{
				RPCClientConfig: RPCClientConfig{URL: "http://localhost:8545"},
				Mode:            "invalid_mode",
			},
			wantErr: fmt.Errorf("invalid RPC mode: %s", "invalid_mode"),
		},
		{
			name: "valid config with basic mode",
			cfg: L2RPCClientConfig{
				RPCClientConfig: RPCClientConfig{URL: "http://localhost:8545"},
				Mode:            RPCModeBasic,
			},
			wantErr: nil,
		},
		{
			name: "valid config with OP mode",
			cfg: L2RPCClientConfig{
				RPCClientConfig: RPCClientConfig{URL: "http://localhost:8545"},
				Mode:            RPCModeOp,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			require.Equal(t, tt.wantErr, err)
		})
	}
}
