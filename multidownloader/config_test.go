package multidownloader

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func TestNewConfigDefault(t *testing.T) {
	cfg := NewConfigDefault("l1", "/tmp/aggkit/")
	require.Equal(t, false, cfg.Enabled)
	require.Equal(t, "/tmp/aggkit/l1_multidownloader.sqlite", cfg.StoragePath)
	require.Equal(t, uint32(10000), cfg.BlockChunkSize, "BlockChunkSize should be 10000")
	require.Equal(t, 30, cfg.MaxParallelBlockHeaderRetrieval, "MaxParallelBlockHeaderRetrieval should be 30")
	require.Equal(t, aggkittypes.FinalizedBlock, cfg.BlockFinality, "BlockFinality should be FinalizedBlock")
	require.Equal(t, types.NewDuration(time.Second*10), cfg.WaitPeriodToCheckCatchUp, "WaitPeriodToCheckCatchUp should be 10 seconds")
	require.False(t, cfg.Enabled, "Enabled should be false by default")
}

func TestNewConfigDefault_ValidatesCorrectly(t *testing.T) {
	cfg := NewConfigDefault("l1", "")

	err := cfg.Validate()
	require.NoError(t, err, "Default configuration should be valid")
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name         string
		modifyConfig func(*Config)
		expectError  bool
		errorMsg     string
	}{
		{
			name: "valid default config",
			modifyConfig: func(cfg *Config) {
				// No modifications - use default
			},
			expectError: false,
		},
		{
			name: "zero BlockChunkSize",
			modifyConfig: func(cfg *Config) {
				cfg.BlockChunkSize = 0
			},
			expectError: true,
			errorMsg:    "MultidownloaderConfig.BlockChunkSize",
		},
		{
			name: "zero MaxParallelBlockHeaderRetrieval",
			modifyConfig: func(cfg *Config) {
				cfg.MaxParallelBlockHeaderRetrieval = 0
			},
			expectError: true,
			errorMsg:    "MultidownloaderConfig.MaxParallelBlockHeaderRetrieval",
		},
		{
			name: "zero WaitPeriodToCheckCatchUp",
			modifyConfig: func(cfg *Config) {
				cfg.WaitPeriodToCheckCatchUp = types.NewDuration(0)
			},
			expectError: true,
			errorMsg:    "MultidownloaderConfig.WaitPeriodToCheckCatchUp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewConfigDefault("l1", "")
			tt.modifyConfig(&cfg)

			err := cfg.Validate()
			if tt.expectError {
				require.Error(t, err, "Expected validation error")
				if tt.errorMsg != "" {
					require.Contains(t, err.Error(), tt.errorMsg, "Error message should contain expected text")
				}
			} else {
				require.NoError(t, err, "Expected no validation error")
			}
		})
	}
}

func TestConfig_String(t *testing.T) {
	cfg := NewConfigDefault("l1", "")

	str := cfg.String()
	require.NotEmpty(t, str, "String() should not return empty string")
	require.Contains(t, str, "BlockChunkSize", "String() should contain BlockChunkSize")
	require.Contains(t, str, "MaxParallelBlockHeaderRetrieval", "String() should contain MaxParallelBlockHeaderRetrieval")
	require.Contains(t, str, "BlockFinality", "String() should contain BlockFinality")
	require.Contains(t, str, "WaitPeriodToCheckCatchUp", "String() should contain WaitPeriodToCheckCatchUp")
	require.Contains(t, str, "Enabled", "String() should contain Enabled")
}
