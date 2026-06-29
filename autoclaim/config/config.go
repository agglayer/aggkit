package config

import (
	"fmt"
	"strings"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	gethcommon "github.com/ethereum/go-ethereum/common"
)

// NetworkType identifies the destination chain family a claimer targets.
type NetworkType string

const (
	// NetworkTypeEVM identifies EVM-compatible destination networks.
	NetworkTypeEVM NetworkType = "EVM"
)

// PolicyName identifies an Auto Claim policy by its config name.
type PolicyName string

const (
	// PolicyNameAllowAll approves every eligible request automatically.
	PolicyNameAllowAll PolicyName = "allow-all"
	// PolicyNameAPIApprove requires approval through the Auto Claim API.
	PolicyNameAPIApprove PolicyName = "api-approve"
	// PolicyNameNoMessage rejects message-bridge requests.
	PolicyNameNoMessage PolicyName = "no-message"
	// PolicyNameBasicFilter applies configured gas and nested bridge-call filters.
	PolicyNameBasicFilter PolicyName = "basic-filter"
)

// Config is the top-level Auto Claim configuration. Whether Auto Claim runs is decided by the
// process components list (the "autoclaim" component), not by a config flag.
type Config struct {
	// DryRun runs the full Auto Claim pipeline (discovery, policy, proof preparation) but skips
	// submitting the claim transaction; matching requests end in the "dry-run" terminal status.
	DryRun               bool                   `mapstructure:"DryRun"`
	StoragePath          string                 `mapstructure:"StoragePath"`
	API                  APIConfig              `mapstructure:"API"`
	Claimers             []ClaimerConfig        `mapstructure:"Claimers"`
	L1ToL2BridgeDetector L1ToL2BridgeDetector   `mapstructure:"L1ToL2BridgeDetector"`
	L2ToLxBridgeDetector DisabledBridgeDetector `mapstructure:"L2ToLxBridgeDetector"`
}

// APIConfig configures the optional Auto Claim admin API.
// The server address comes from the global AdminAPI config.
type APIConfig struct {
	Enabled bool `mapstructure:"Enabled"`
}

// L1ToL2BridgeDetector configures L1-to-L2 bridge exit discovery.
type L1ToL2BridgeDetector struct {
	Enabled                    bool              `mapstructure:"Enabled"`
	StartBlock                 uint64            `mapstructure:"StartBlock"`
	PollInterval               cfgtypes.Duration `mapstructure:"PollInterval"`
	RetryAfterErrorPeriod      cfgtypes.Duration `mapstructure:"RetryAfterErrorPeriod"`
	MaxRetryAttemptsAfterError int               `mapstructure:"MaxRetryAttemptsAfterError"`
	EtrogL1UpgradeBlock        uint64            `mapstructure:"EtrogL1UpgradeBlock"`
}

// DisabledBridgeDetector reserves config for disabled bridge detector directions.
type DisabledBridgeDetector struct {
	Enabled bool `mapstructure:"Enabled"`
}

// ClaimerConfig configures one destination-network claimer.
type ClaimerConfig struct {
	Enabled      bool                `mapstructure:"Enabled"`
	ID           string              `mapstructure:"ID"`
	NetworkType  NetworkType         `mapstructure:"NetworkType"`
	NetworkID    uint32              `mapstructure:"NetworkID"`
	URLRPC       string              `mapstructure:"URLRPC"`
	BridgeAddr   gethcommon.Address  `mapstructure:"BridgeAddr"`
	PolicyName   PolicyName          `mapstructure:"PolicyName"`
	Policy       PolicyConfig        `mapstructure:"Policy"`
	GasOffset    uint64              `mapstructure:"GasOffset"`
	WaitPeriod   cfgtypes.Duration   `mapstructure:"WaitPeriod"`
	RetryAfter   cfgtypes.Duration   `mapstructure:"RetryAfter"`
	MaxRetries   uint64              `mapstructure:"MaxRetries"`
	EthTxManager ethtxmanager.Config `mapstructure:"EthTxManager"`
	// BlockFinality optionally overrides the shared [L2GERSync] block finality for this claimer's
	// destination-L2 GER syncer. Empty means inherit the shared value.
	BlockFinality aggkittypes.BlockNumberFinality `mapstructure:"BlockFinality"`
	// InitialBlockNum optionally overrides the shared [L2GERSync] initial sync block for this
	// claimer's destination-L2 GER syncer. Zero means inherit the shared value.
	InitialBlockNum uint64 `mapstructure:"InitialBlockNum"`
}

// PolicyConfig configures named policy behavior.
type PolicyConfig struct {
	AllowMessageClaims bool     `mapstructure:"AllowMessageClaims"`
	AllowedOrigins     []uint32 `mapstructure:"AllowedOrigins"`
	AllowedTokens      []string `mapstructure:"AllowedTokens"`
	ManualFallback     bool     `mapstructure:"ManualFallback"`
	MaxGas             uint64   `mapstructure:"MaxGas"`
}

// Validate checks whether the Auto Claim config is usable. When no claimer is enabled the component
// is effectively inert (e.g. the default config), so validation is skipped.
func (c Config) Validate() error {
	hasEnabledClaimer := false
	for _, claimer := range c.Claimers {
		if claimer.Enabled {
			hasEnabledClaimer = true
			break
		}
	}
	if !hasEnabledClaimer {
		return nil
	}
	if strings.TrimSpace(c.StoragePath) == "" {
		return fmt.Errorf("AutoClaim.StoragePath is required when AutoClaim is enabled")
	}
	if c.L1ToL2BridgeDetector.PollInterval.Duration <= 0 {
		return fmt.Errorf("AutoClaim.L1ToL2BridgeDetector.PollInterval must be greater than 0")
	}
	if c.L1ToL2BridgeDetector.RetryAfterErrorPeriod.Duration <= 0 {
		return fmt.Errorf("AutoClaim.L1ToL2BridgeDetector.RetryAfterErrorPeriod must be greater than 0")
	}
	return validateEnabledClaimers(c.Claimers)
}

// validateEnabledClaimers validates each enabled claimer and checks for duplicate IDs and NetworkIDs.
func validateEnabledClaimers(claimers []ClaimerConfig) error {
	seenIDs := make(map[string]struct{})
	seenNetworkIDs := make(map[uint32]struct{})
	for i, claimer := range claimers {
		if !claimer.Enabled {
			continue
		}
		if err := claimer.Validate(); err != nil {
			return fmt.Errorf("AutoClaim.Claimers[%d]: %w", i, err)
		}
		if _, ok := seenIDs[claimer.ID]; ok {
			return fmt.Errorf("duplicate enabled AutoClaim claimer ID: %s", claimer.ID)
		}
		seenIDs[claimer.ID] = struct{}{}
		if _, ok := seenNetworkIDs[claimer.NetworkID]; ok {
			return fmt.Errorf("duplicate enabled AutoClaim claimer NetworkID: %d", claimer.NetworkID)
		}
		seenNetworkIDs[claimer.NetworkID] = struct{}{}
	}
	return nil
}

// Validate checks whether an enabled claimer config is usable.
func (c ClaimerConfig) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("ID is required")
	}
	if c.NetworkType != NetworkTypeEVM {
		return fmt.Errorf("unsupported NetworkType: %s", c.NetworkType)
	}
	if strings.TrimSpace(c.URLRPC) == "" {
		return fmt.Errorf("URLRPC is required")
	}
	if c.BridgeAddr == (gethcommon.Address{}) {
		return fmt.Errorf("BridgeAddr is required")
	}
	if !isKnownPolicyName(c.PolicyName) {
		return fmt.Errorf("unknown PolicyName: %s", c.PolicyName)
	}
	if c.WaitPeriod.Duration <= 0 {
		return fmt.Errorf("WaitPeriod must be greater than 0")
	}
	if c.RetryAfter.Duration < 0 {
		return fmt.Errorf("RetryAfter must be greater than or equal to 0")
	}
	if strings.TrimSpace(c.EthTxManager.StoragePath) == "" {
		return fmt.Errorf("EthTxManager.StoragePath is required")
	}
	return nil
}

func isKnownPolicyName(policyName PolicyName) bool {
	switch policyName {
	case PolicyNameAllowAll, PolicyNameAPIApprove, PolicyNameNoMessage, PolicyNameBasicFilter:
		return true
	default:
		return false
	}
}
