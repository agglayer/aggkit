package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestLoadFilesDefaults(t *testing.T) {
	cfg, err := LoadFiles(nil)
	require.NoError(t, err)
	require.Equal(t, log.EnvironmentDevelopment, cfg.Log.Environment)
	require.Equal(t, "info", cfg.Log.Level)
	require.Equal(t, []string{"stderr"}, cfg.Log.Outputs)

	require.Equal(t, "http://localhost:8545", cfg.L1RPC.URL)
	require.Equal(t, ethermanconfig.RPCModeBasic, cfg.L1RPC.Mode)

	require.Equal(t, common.Address{}, cfg.BridgeServiceFinder.RollupManagerAddr)
	require.Equal(t, aggkittypes.FinalizedBlock, cfg.BridgeServiceFinder.BlockFinality)
	require.Equal(t, 30*time.Second, cfg.BridgeServiceFinder.PollInterval.Duration)
	require.Equal(t, uint64(10000), cfg.BridgeServiceFinder.BlockChunkSize)
	require.Equal(t, "/health", cfg.BridgeServiceFinder.HealthCheckPath)
	require.Equal(t, 5*time.Second, cfg.BridgeServiceFinder.HealthCheckTimeout.Duration)
	require.False(t, cfg.BridgeServiceFinder.RequireAllHealthyOnStart)

	require.Equal(t, time.Minute, cfg.Tracker.RetentionPeriod.Duration)
}

func TestLoadFilesOverridesDefaults(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "proxy.toml")
	cfgContent := `
[Log]
Environment = "production"
Level = "warn"

[L1RPC]
URL = "http://l1.example.com:8545"

[BridgeServiceFinder]
RollupManagerAddr = "0x1234567890123456789012345678901234567890"
PollInterval = "10s"

[Tracker]
RetentionPeriod = "1h"
`
	require.NoError(t, os.WriteFile(cfgFile, []byte(cfgContent), 0600))

	cfg, err := LoadFiles([]string{cfgFile})
	require.NoError(t, err)
	require.Equal(t, log.EnvironmentProduction, cfg.Log.Environment)
	require.Equal(t, "warn", cfg.Log.Level)
	require.Equal(t, "http://l1.example.com:8545", cfg.L1RPC.URL)
	require.Equal(t,
		common.HexToAddress("0x1234567890123456789012345678901234567890"),
		cfg.BridgeServiceFinder.RollupManagerAddr)
	require.Equal(t, 10*time.Second, cfg.BridgeServiceFinder.PollInterval.Duration)
	require.Equal(t, time.Hour, cfg.Tracker.RetentionPeriod.Duration)
	// not overridden fields keep the default value
	require.Equal(t, []string{"stderr"}, cfg.Log.Outputs)
	require.Equal(t, ethermanconfig.RPCModeBasic, cfg.L1RPC.Mode)
	require.Equal(t, "/health", cfg.BridgeServiceFinder.HealthCheckPath)
}

func TestValidateComponents(t *testing.T) {
	require.NoError(t, ValidateComponents(nil))
	require.NoError(t, ValidateComponents([]string{PROXY}))
	require.NoError(t, ValidateComponents([]string{PROXY, TRACKER}))

	err := ValidateComponents([]string{PROXY, "bogus"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus")
	require.Contains(t, err.Error(), PROXY)
	require.Contains(t, err.Error(), TRACKER)
}

func TestLoadFilesMissingFile(t *testing.T) {
	_, err := LoadFiles([]string{"non-existing-file.toml"})
	require.Error(t, err)
}
