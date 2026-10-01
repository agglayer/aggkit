package config

import (
	"testing"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/config/types"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	"github.com/agglayer/aggkit/bridgeservicefinder"
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
			name: "invalid bridge detector poll interval",
			mutate: func(cfg *Config) {
				cfg.L1ToL2BridgeDetector.PollInterval.Duration = 0
			},
			wantError: "AutoClaim.L1ToL2BridgeDetector.PollInterval must be greater than 0",
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

func TestConfigValidateL2ToLxRequiresFinderConfig(t *testing.T) {
	cfg := validConfig()
	cfg.L2ToLxBridgeDetector = L2ToLxBridgeDetector{
		Enabled:      true,
		PollInterval: cfgtypes.NewDuration(3 * time.Second),
	}
	// Clear the RollupManagerAddr set by validConfig to exercise the requirement.
	cfg.BridgeServiceFinder = bridgeservicefinder.Config{}

	err := cfg.Validate()
	require.ErrorContains(t, err, "AutoClaim.BridgeServiceFinder.RollupManagerAddr is required")

	cfg.BridgeServiceFinder = bridgeservicefinder.Config{
		RollupManagerAddr: common.HexToAddress("0x2000000000000000000000000000000000000002"),
	}
	require.NoError(t, cfg.Validate())
}

func TestConfigValidateL2DestinationClaimerRequiresFinderConfig(t *testing.T) {
	// An enabled L2-destination claimer builds a GER gate against the destination bridge service, so
	// RollupManagerAddr is required even when the L2ToLx bridge detector is disabled.
	cfg := validConfig()
	require.False(t, cfg.L2ToLxBridgeDetector.Enabled)
	cfg.BridgeServiceFinder = bridgeservicefinder.Config{}

	err := cfg.Validate()
	require.ErrorContains(t, err, "AutoClaim.BridgeServiceFinder.RollupManagerAddr is required")

	cfg.BridgeServiceFinder = bridgeservicefinder.Config{
		RollupManagerAddr: common.HexToAddress("0x2000000000000000000000000000000000000002"),
	}
	require.NoError(t, cfg.Validate())
}

func TestConfigValidateL2ToLxRejectsInvalidPollingFields(t *testing.T) {
	for _, tt := range []struct {
		name      string
		mutate    func(*L2ToLxBridgeDetector)
		wantError string
	}{
		{
			name: "zero poll interval",
			mutate: func(c *L2ToLxBridgeDetector) {
				c.PollInterval.Duration = 0
			},
			wantError: "PollInterval must be greater than 0",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.L2ToLxBridgeDetector = L2ToLxBridgeDetector{
				Enabled:      true,
				PollInterval: cfgtypes.NewDuration(3 * time.Second),
			}
			cfg.BridgeServiceFinder = bridgeservicefinder.Config{
				RollupManagerAddr: common.HexToAddress("0x2000000000000000000000000000000000000002"),
			}
			tt.mutate(&cfg.L2ToLxBridgeDetector)

			require.ErrorContains(t, cfg.Validate(), tt.wantError)
		})
	}
}

func TestConfigValidateL2ToLxDisabledSkipsPollingFieldValidation(t *testing.T) {
	// A disabled L2ToLxBridgeDetector never uses its polling fields, so a zero-value config (the
	// back-compat default) must remain valid.
	cfg := validConfig()
	cfg.L2ToLxBridgeDetector = L2ToLxBridgeDetector{Enabled: false}

	require.NoError(t, cfg.Validate())
}

func TestConfigValidateAcceptsL1DestinationClaimerWhenL2ToLxEnabled(t *testing.T) {
	cfg := validConfig()
	cfg.L2ToLxBridgeDetector = L2ToLxBridgeDetector{
		Enabled:      true,
		PollInterval: cfgtypes.NewDuration(3 * time.Second),
	}
	cfg.BridgeServiceFinder = bridgeservicefinder.Config{
		RollupManagerAddr: common.HexToAddress("0x2000000000000000000000000000000000000002"),
	}
	cfg.Claimers = append(cfg.Claimers, validClaimerConfig("l1-destination", 0))

	require.NoError(t, cfg.Validate())
}

func TestConfigValidateRejectsL1DestinationClaimerWhenL2ToLxDisabled(t *testing.T) {
	cfg := validConfig()
	cfg.Claimers = append(cfg.Claimers, validClaimerConfig("l1-destination", 0))

	err := cfg.Validate()
	require.ErrorContains(t, err, "AutoClaim.L2ToLxBridgeDetector.Enabled must be true")
}

func TestApplyDefaultsFillsUnsetEthTxManagerFields(t *testing.T) {
	cfg := Config{
		Claimers: []ClaimerConfig{
			{
				ID: "l2-a",
				EthTxManager: ethtxmanager.Config{
					StoragePath: "/tmp/autoclaim-ethtxmanager.sqlite",
				},
			},
		},
	}

	cfg.ApplyDefaults()

	claimer := cfg.Claimers[0].EthTxManager
	require.Equal(t, defaultEthTxManagerFrequencyToMonitorTxs, claimer.FrequencyToMonitorTxs.Duration)
	require.Equal(t, defaultEthTxManagerWaitTxToBeMined, claimer.WaitTxToBeMined.Duration)
	require.Equal(t, defaultEthTxManagerGetReceiptMaxTime, claimer.GetReceiptMaxTime.Duration)
	require.Equal(t, defaultEthTxManagerGetReceiptWaitInterval, claimer.GetReceiptWaitInterval.Duration)
	require.Equal(t, float64(defaultEthTxManagerGasPriceMarginFactor), claimer.GasPriceMarginFactor)
	// Fields whose zero value is meaningful to the module must stay untouched.
	require.Zero(t, claimer.SafeStatusL1NumberOfBlocks)
	require.Zero(t, claimer.FinalizedStatusL1NumberOfBlocks)
	require.Zero(t, claimer.EstimateGasMaxRetries)
	// StoragePath is not a default-applied field: it must be preserved untouched.
	require.Equal(t, "/tmp/autoclaim-ethtxmanager.sqlite", claimer.StoragePath)
}

func TestApplyDefaultsPreservesExplicitlySetEthTxManagerFields(t *testing.T) {
	explicit := ethtxmanager.Config{
		StoragePath:                     "/tmp/autoclaim-ethtxmanager.sqlite",
		FrequencyToMonitorTxs:           types.NewDuration(5 * time.Second),
		WaitTxToBeMined:                 types.NewDuration(9 * time.Second),
		GetReceiptMaxTime:               types.NewDuration(3 * time.Second),
		GetReceiptWaitInterval:          types.NewDuration(4 * time.Second),
		GasPriceMarginFactor:            1.5,
		SafeStatusL1NumberOfBlocks:      7,
		FinalizedStatusL1NumberOfBlocks: 20,
		EstimateGasMaxRetries:           3,
	}
	cfg := Config{
		Claimers: []ClaimerConfig{{ID: "l2-a", EthTxManager: explicit}},
	}

	cfg.ApplyDefaults()

	require.Equal(t, explicit, cfg.Claimers[0].EthTxManager)
}

func TestApplyDefaultsCoercesNonPositiveGasPriceMarginFactor(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value float64
	}{
		{name: "zero", value: 0},
		{name: "negative", value: -1.2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Claimers: []ClaimerConfig{
					{
						ID: "l2-a",
						EthTxManager: ethtxmanager.Config{
							StoragePath:          "/tmp/autoclaim-ethtxmanager.sqlite",
							GasPriceMarginFactor: tt.value,
						},
					},
				},
			}

			cfg.ApplyDefaults()

			require.Equal(t, float64(defaultEthTxManagerGasPriceMarginFactor), cfg.Claimers[0].EthTxManager.GasPriceMarginFactor)
		})
	}
}

func validConfig() Config {
	return Config{
		StoragePath: "/tmp/autoclaim.sqlite",
		L1ToL2BridgeDetector: L1ToL2BridgeDetector{
			Enabled:      true,
			PollInterval: cfgtypes.NewDuration(2 * time.Second),
		},
		Claimers: []ClaimerConfig{
			validClaimerConfig("l2-a", 10),
		},
		// The l2-a claimer targets an L2 destination, whose GER gate resolves through the bridge
		// service finder, so a RollupManagerAddr is required.
		BridgeServiceFinder: bridgeservicefinder.Config{
			RollupManagerAddr: common.HexToAddress("0x2000000000000000000000000000000000000002"),
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

func TestValidateRejectsNegativeStartLookback(t *testing.T) {
	cfg := Config{L1ToL2BridgeDetector: L1ToL2BridgeDetector{StartLookback: cfgtypes.Duration{Duration: -time.Hour}}}
	cfg.StoragePath = "/tmp/x"
	cfg.Claimers = []ClaimerConfig{{Enabled: true}}
	cfg.L1ToL2BridgeDetector.PollInterval = cfgtypes.Duration{Duration: time.Second}
	require.ErrorContains(t, cfg.Validate(), "StartLookback must not be negative")
}
