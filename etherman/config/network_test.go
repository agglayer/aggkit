package config

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

// Test for issue: 1389
func TestEthClientExploratory(t *testing.T) {
	t.Skip("exploratory test")
	l2url := os.Getenv("L2URL")
	ethRawClient, err := ethclient.Dial(l2url)
	require.NoError(t, err)
	defer ethRawClient.Close()
	ctx := t.Context()
	number := big.NewInt(34797856)
	header, err := ethRawClient.HeaderByNumber(ctx, number)
	require.NoError(t, err)
	fmt.Printf("block number: %d\n", header.Number.Uint64())
	hash := header.Hash()
	fmt.Printf("block hash: %s\n", hash.Hex())

	err = ethRawClient.Client().BatchCall(nil)
	require.NoError(t, err)
}

func TestGetString(t *testing.T) {
	cfg := RPCClientConfig{

		URL: "http://localhost:8123",
		RetryPolicyGenericConfig: aggkitcommon.RetryPolicyGenericConfig{
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
			wantErr: fmt.Errorf("invalid RPC configuration: %w", ErrMissingRPCURL),
		},
		{
			name: "missing RPC URL",
			cfg: L1NetworkConfig{
				RPC: RPCClientConfig{
					RetryPolicyGenericConfig: aggkitcommon.RetryPolicyGenericConfig{
						// empty URL
						MaxRetries: 1,
					},
				},
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
			name: "invalid BlocksChunkSize",
			cfg: L1NetworkConfig{
				RPC:                       RPCClientConfig{URL: "http://localhost:8545"},
				RollupAddr:                validAddr,
				RollupManagerAddr:         validAddr,
				POLTokenAddr:              validAddr,
				GlobalExitRootManagerAddr: validAddr,
				BlocksChunkSize:           0,
			},
			wantErr: ErrInvalidBlocksChunkSize,
		},
		{
			name: "invalid RollupManagerCreationBlock",
			cfg: L1NetworkConfig{
				RPC:                        RPCClientConfig{URL: "http://localhost:8545"},
				RollupAddr:                 validAddr,
				RollupManagerAddr:          validAddr,
				POLTokenAddr:               validAddr,
				GlobalExitRootManagerAddr:  validAddr,
				BlocksChunkSize:            100,
				RollupManagerCreationBlock: 0,
			},
			wantErr: ErrInvalidRollupManagerCreationBlock,
		},
		{
			name: "valid config",
			cfg: L1NetworkConfig{
				RPC:                        RPCClientConfig{URL: "http://localhost:8545"},
				RollupAddr:                 validAddr,
				RollupManagerAddr:          validAddr,
				POLTokenAddr:               validAddr,
				GlobalExitRootManagerAddr:  validAddr,
				BlocksChunkSize:            100,
				RollupManagerCreationBlock: 10,
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
		cfg     RPCClientConfig
		wantErr error
	}{
		{
			name:    "missing RPC config",
			cfg:     RPCClientConfig{},
			wantErr: fmt.Errorf("invalid RPC configuration: %w", ErrMissingRPCURL),
		},
		{
			name: "missing RPC URL",
			cfg: RPCClientConfig{
				RetryPolicyGenericConfig: aggkitcommon.RetryPolicyGenericConfig{
					// empty URL
					MaxRetries: 1,
				},
			},
			wantErr: fmt.Errorf("invalid RPC configuration: %w", ErrMissingRPCURL),
		},
		{
			name: "invalid RPC mode",
			cfg: RPCClientConfig{
				URL:  "http://localhost:8545",
				Mode: "invalid_mode",
			},
			wantErr: fmt.Errorf("invalid RPC mode: %s", "invalid_mode"),
		},
		{
			name: "valid config with basic mode",
			cfg: RPCClientConfig{
				URL:  "http://localhost:8545",
				Mode: RPCModeBasic,
			},
			wantErr: nil,
		},
		{
			name: "valid config with OP mode",
			cfg: RPCClientConfig{
				URL:  "http://localhost:8545",
				Mode: RPCModeOp,
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

func TestRPCClientConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  RPCClientConfig
		wantErr error
	}{
		{
			name:    "missing URL",
			config:  RPCClientConfig{},
			wantErr: ErrMissingRPCURL,
		},
		{
			name: "negative MaxRetries",
			config: RPCClientConfig{
				URL: "http://localhost:8545",
				RetryPolicyGenericConfig: aggkitcommon.RetryPolicyGenericConfig{
					Mode:       aggkitcommon.RetryConfigModeBackoff,
					MaxRetries: -2,
				},
			},
			wantErr: errors.New("max retries -2 cannot be less than -1"),
		},
		{
			name: "initial backoff is zero",
			config: RPCClientConfig{
				URL: "http://localhost:8545",
				RetryPolicyGenericConfig: aggkitcommon.RetryPolicyGenericConfig{
					Mode:              aggkitcommon.RetryConfigModeBackoff,
					MaxRetries:        3,
					InitialBackoff:    types.Duration{Duration: 0},
					MaxBackoff:        types.Duration{Duration: time.Second},
					BackoffMultiplier: 2.0,
				},
			},
			wantErr: errors.New("initial backoff must be greater than 0, got 0s"),
		},
		{
			name: "max backoff is zero",
			config: RPCClientConfig{
				URL: "http://localhost:8545",
				RetryPolicyGenericConfig: aggkitcommon.RetryPolicyGenericConfig{
					Mode:              aggkitcommon.RetryConfigModeBackoff,
					MaxRetries:        3,
					InitialBackoff:    types.Duration{Duration: time.Second},
					MaxBackoff:        types.Duration{Duration: 0},
					BackoffMultiplier: 2.0,
				},
			},
			wantErr: errors.New("max backoff must be greater than 0, got 0s"),
		},
		{
			name: "max backoff < initial backoff",
			config: RPCClientConfig{
				URL: "http://localhost:8545",
				RetryPolicyGenericConfig: aggkitcommon.RetryPolicyGenericConfig{
					Mode:              aggkitcommon.RetryConfigModeBackoff,
					MaxRetries:        3,
					InitialBackoff:    types.Duration{Duration: 2 * time.Second},
					MaxBackoff:        types.Duration{Duration: time.Second},
					BackoffMultiplier: 2.0,
				},
			},
			wantErr: errors.New("max backoff 1s must be greater than or equal to initial backoff 2s"),
		},
		{
			name: "backoff multiplier <= 1.0",
			config: RPCClientConfig{
				URL: "http://localhost:8545",
				RetryPolicyGenericConfig: aggkitcommon.RetryPolicyGenericConfig{
					Mode:              aggkitcommon.RetryConfigModeBackoff,
					MaxRetries:        3,
					InitialBackoff:    types.Duration{Duration: time.Second},
					MaxBackoff:        types.Duration{Duration: 5 * time.Second},
					BackoffMultiplier: 1.0,
				},
			},
			wantErr: errors.New("backoff multiplier must be greater than 1.0, got 1.000000"),
		},
		{
			name: "valid config",
			config: RPCClientConfig{
				URL: "http://localhost:8545",
				RetryPolicyGenericConfig: aggkitcommon.RetryPolicyGenericConfig{
					Mode:              aggkitcommon.RetryConfigModeBackoff,
					MaxRetries:        3,
					InitialBackoff:    types.Duration{Duration: time.Second},
					MaxBackoff:        types.Duration{Duration: 5 * time.Second},
					BackoffMultiplier: 2.0,
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.wantErr == nil {
				require.NoError(t, err, "expected no error, got: %v", err)
			} else {
				require.ErrorContains(t, err, tt.wantErr.Error())
			}
		})
	}
}
