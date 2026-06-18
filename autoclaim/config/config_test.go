package config

import (
	"testing"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestConfigValidateDisabledSkipsRequiredFields(t *testing.T) {
	cfg := Config{}

	require.NoError(t, cfg.Validate())
}

func TestConfigValidateAcceptsEnabledConfig(t *testing.T) {
	cfg := validConfig()

	require.NoError(t, cfg.Validate())
}

func TestConfigValidateRejectsInvalidEnabledConfig(t *testing.T) {
	for _, tt := range []struct {
		name      string
		mutate    func(*Config)
		wantError string
	}{
		{
			name: "missing storage path",
			mutate: func(cfg *Config) {
				cfg.StoragePath = ""
			},
			wantError: "AutoClaim.StoragePath is required",
		},
		{
			name: "invalid watchdog poll interval",
			mutate: func(cfg *Config) {
				cfg.L1ToL2Watchdog.PollInterval.Duration = 0
			},
			wantError: "AutoClaim.L1ToL2Watchdog.PollInterval must be greater than 0",
		},
		{
			name: "invalid watchdog retry period",
			mutate: func(cfg *Config) {
				cfg.L1ToL2Watchdog.RetryAfterErrorPeriod.Duration = 0
			},
			wantError: "AutoClaim.L1ToL2Watchdog.RetryAfterErrorPeriod must be greater than 0",
		},
		{
			name: "duplicate enabled claimer id",
			mutate: func(cfg *Config) {
				duplicate := validClaimerConfig("l2-a", 11)
				cfg.Claimers = append(cfg.Claimers, duplicate)
			},
			wantError: "duplicate enabled AutoClaim claimer ID: l2-a",
		},
		{
			name: "duplicate enabled claimer network id",
			mutate: func(cfg *Config) {
				duplicate := validClaimerConfig("l2-b", 10)
				cfg.Claimers = append(cfg.Claimers, duplicate)
			},
			wantError: "duplicate enabled AutoClaim claimer NetworkID: 10",
		},
		{
			name: "disabled duplicate claimer is ignored",
			mutate: func(cfg *Config) {
				duplicate := validClaimerConfig("l2-a", 10)
				duplicate.Enabled = false
				cfg.Claimers = append(cfg.Claimers, duplicate)
			},
			wantError: "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestClaimerConfigValidateRejectsInvalidEnabledConfig(t *testing.T) {
	for _, tt := range []struct {
		name      string
		mutate    func(*ClaimerConfig)
		wantError string
	}{
		{
			name: "missing id",
			mutate: func(cfg *ClaimerConfig) {
				cfg.ID = ""
			},
			wantError: "ID is required",
		},
		{
			name: "unsupported network type",
			mutate: func(cfg *ClaimerConfig) {
				cfg.NetworkType = "unsupported"
			},
			wantError: "unsupported NetworkType: unsupported",
		},
		{
			name: "missing rpc url",
			mutate: func(cfg *ClaimerConfig) {
				cfg.URLRPC = ""
			},
			wantError: "URLRPC is required",
		},
		{
			name: "missing bridge address",
			mutate: func(cfg *ClaimerConfig) {
				cfg.BridgeAddr = common.Address{}
			},
			wantError: "BridgeAddr is required",
		},
		{
			name: "unknown policy",
			mutate: func(cfg *ClaimerConfig) {
				cfg.PolicyName = "unknown"
			},
			wantError: "unknown PolicyName: unknown",
		},
		{
			name: "invalid wait period",
			mutate: func(cfg *ClaimerConfig) {
				cfg.WaitPeriod.Duration = 0
			},
			wantError: "WaitPeriod must be greater than 0",
		},
		{
			name: "invalid retry period",
			mutate: func(cfg *ClaimerConfig) {
				cfg.RetryAfter.Duration = -time.Second
			},
			wantError: "RetryAfter must be greater than or equal to 0",
		},
		{
			name: "missing ethtxmanager storage path",
			mutate: func(cfg *ClaimerConfig) {
				cfg.EthTxManager.StoragePath = ""
			},
			wantError: "EthTxManager.StoragePath is required",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validClaimerConfig("l2-a", 10)
			tt.mutate(&cfg)

			require.ErrorContains(t, cfg.Validate(), tt.wantError)
		})
	}
}

func validConfig() Config {
	return Config{
		StoragePath: "/tmp/autoclaim.sqlite",
		L1ToL2Watchdog: L1ToL2Watchdog{
			Enabled:                    true,
			PollInterval:               cfgtypes.NewDuration(2 * time.Second),
			RetryAfterErrorPeriod:      cfgtypes.NewDuration(5 * time.Second),
			MaxRetryAttemptsAfterError: 3,
		},
		Claimers: []ClaimerConfig{
			validClaimerConfig("l2-a", 10),
		},
	}
}

func validClaimerConfig(id string, networkID uint32) ClaimerConfig {
	return ClaimerConfig{
		Enabled:     true,
		ID:          id,
		NetworkType: NetworkTypeEVM,
		NetworkID:   networkID,
		URLRPC:      "http://127.0.0.1:8545",
		BridgeAddr:  common.HexToAddress("0x1000000000000000000000000000000000000000"),
		PolicyName:  PolicyNameAllowAll,
		WaitPeriod:  cfgtypes.NewDuration(time.Second),
		RetryAfter:  cfgtypes.NewDuration(2 * time.Second),
		MaxRetries:  3,
		EthTxManager: ethtxmanager.Config{
			StoragePath: "/tmp/autoclaim-ethtxmanager.sqlite",
		},
	}
}
