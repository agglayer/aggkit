package config

import (
	"fmt"
	"strings"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	gethcommon "github.com/ethereum/go-ethereum/common"
)

// l1DestinationNetworkID is the NetworkID of an L1-destination claimer. Only the L2ToLx bridge
// detector can ever route a request to such a claimer, since the L1ToL2 detector only discovers
// L1-origin bridges (which always target an L2 destination).
const l1DestinationNetworkID = uint32(0)

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
	DryRun               bool                 `mapstructure:"DryRun"`
	StoragePath          string               `mapstructure:"StoragePath"`
	API                  APIConfig            `mapstructure:"API"`
	Claimers             []ClaimerConfig      `mapstructure:"Claimers"`
	L1ToL2BridgeDetector L1ToL2BridgeDetector `mapstructure:"L1ToL2BridgeDetector"`
	L2ToLxBridgeDetector L2ToLxBridgeDetector `mapstructure:"L2ToLxBridgeDetector"`
	// BridgeServiceFinder configures resolution of each source rollup's bridge service URL, used by
	// the L2ToLx bridge detector and by the rollup-origin proof preparer's staleness refresh. It is
	// only required when L2ToLxBridgeDetector.Enabled is true.
	BridgeServiceFinder bridgeservicefinder.Config `mapstructure:"BridgeServiceFinder"`
}

// APIConfig configures the optional Auto Claim admin API.
// The server address comes from the global AdminREST config.
type APIConfig struct {
	Enabled bool `mapstructure:"Enabled"`
}

// L1ToL2BridgeDetector configures L1-to-L2 bridge exit discovery. A failed poll is logged and
// retried on the next PollInterval tick; there is no separate error-retry policy.
type L1ToL2BridgeDetector struct {
	Enabled             bool              `mapstructure:"Enabled"`
	StartBlock          uint64            `mapstructure:"StartBlock"`
	PollInterval        cfgtypes.Duration `mapstructure:"PollInterval"`
	EtrogL1UpgradeBlock uint64            `mapstructure:"EtrogL1UpgradeBlock"`
}

// L2ToLxBridgeDetector configures L2-to-Lx (rollup-origin) bridge exit discovery, covering both
// L2-to-L1 and L2-to-L2 bridges. Enabling it requires AutoClaim.BridgeServiceFinder to be configured
// with a valid RollupManagerAddr, since the detector resolves each source rollup's bridge service
// through it. A failed poll is logged and retried on the next PollInterval tick; there is no
// separate error-retry policy.
type L2ToLxBridgeDetector struct {
	Enabled bool `mapstructure:"Enabled"`
	// StartL1Block is the L1 block used to derive a newly discovered source network's initial LER
	// cursor (via the GER at that block). 0 means full history (from_ler omitted on first fetch).
	StartL1Block uint64            `mapstructure:"StartL1Block"`
	PollInterval cfgtypes.Duration `mapstructure:"PollInterval"`
}

// Validate checks whether an enabled L2ToLxBridgeDetector config is usable. It is a no-op when
// disabled, since a disabled detector never uses these fields.
func (c L2ToLxBridgeDetector) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.PollInterval.Duration <= 0 {
		return fmt.Errorf("PollInterval must be greater than 0")
	}
	return nil
}

// ClaimerConfig configures one destination-network claimer.
type ClaimerConfig struct {
	Enabled     bool        `mapstructure:"Enabled"`
	ID          string      `mapstructure:"ID"`
	NetworkType NetworkType `mapstructure:"NetworkType"`
	// NetworkID is the destination network this claimer targets. 0 means L1: such a claimer is only
	// reachable when AutoClaim.L2ToLxBridgeDetector is enabled, since only it discovers requests
	// destined for L1.
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
	hasL1DestinationClaimer := false
	hasL2DestinationClaimer := false
	for _, claimer := range c.Claimers {
		if claimer.Enabled {
			hasEnabledClaimer = true
			if claimer.NetworkID == l1DestinationNetworkID {
				hasL1DestinationClaimer = true
			} else {
				hasL2DestinationClaimer = true
			}
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
	if err := c.L2ToLxBridgeDetector.Validate(); err != nil {
		return fmt.Errorf("AutoClaim.L2ToLxBridgeDetector: %w", err)
	}
	if (c.L2ToLxBridgeDetector.Enabled || hasL2DestinationClaimer) &&
		c.BridgeServiceFinder.RollupManagerAddr == (gethcommon.Address{}) {
		return fmt.Errorf(
			"AutoClaim.BridgeServiceFinder.RollupManagerAddr is required when AutoClaim.L2ToLxBridgeDetector " +
				"is enabled or an L2-destination claimer is configured")
	}
	if hasL1DestinationClaimer && !c.L2ToLxBridgeDetector.Enabled {
		return fmt.Errorf(
			"AutoClaim.L2ToLxBridgeDetector.Enabled must be true when an L1-destination " +
				"(NetworkID=0) claimer is configured, since only it can route requests to L1")
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
