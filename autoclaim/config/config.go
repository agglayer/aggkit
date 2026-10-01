package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	gethcommon "github.com/ethereum/go-ethereum/common"
)

// l1DestinationNetworkID is the NetworkID of an L1-destination claimer. Only the L2ToLx bridge
// detector can ever route a request to such a claimer, since the L1ToL2 detector only discovers
// L1-origin bridges (which always target an L2 destination).
const l1DestinationNetworkID = uint32(0)

// Default values applied to every claimer's EthTxManager by ApplyDefaults, mirroring aggkit's own
// AggOracle.EVMSender.EthTxManager opinion (config/default.go). TOML array-of-tables elements like
// AutoClaim.Claimers get no per-element defaults from the config loader, so without these an
// operator who omits the EthTxManager block gets a zero-value one: FrequencyToMonitorTxs=0 spins
// the tx-monitor loop with no delay, and GasPriceMarginFactor=0 zeroes every tx's gas price
// ("transaction underpriced").
const (
	defaultEthTxManagerFrequencyToMonitorTxs  = 1 * time.Second
	defaultEthTxManagerWaitTxToBeMined        = 2 * time.Second
	defaultEthTxManagerGetReceiptMaxTime      = 250 * time.Millisecond
	defaultEthTxManagerGetReceiptWaitInterval = 1 * time.Second
	defaultEthTxManagerGasPriceMarginFactor   = 1
)

// DefaultStartLookback is the fallback StartLookback for both bridge detectors when left unset. A
// bridge only becomes claimable after certificate settlement and destination GER injection, which
// has been observed in production to take over an hour; 24h keeps that comfortably covered while
// still being a tiny fraction of a chain's full history (e.g. ~7,000 Sepolia blocks vs. the ~6.9M
// blocks a genesis backfill actually scanned).
const DefaultStartLookback = 24 * time.Hour

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
	Enabled bool `mapstructure:"Enabled"`
	// StartBlock is the first L1 block scanned when no durable cursor exists. Nil (the field is
	// absent from config) means "resolve it automatically from StartLookback"; an explicit value --
	// including 0, meaning genesis -- is honored verbatim and never adjusted or clamped. Leaving
	// this absent is the recommended default: a hand-pinned block number goes stale the moment the
	// deployment it was computed for slips in time.
	StartBlock *uint64 `mapstructure:"StartBlock"`
	// StartLookback is how far back from "now" StartBlock is resolved when left unset (see
	// StartBlock). Ignored when StartBlock is set. Defaults to DefaultStartLookback.
	StartLookback       cfgtypes.Duration `mapstructure:"StartLookback"`
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
	// cursor (via the GER at that block). Nil (absent from config) means "resolve it automatically
	// from StartLookback"; an explicit value is honored verbatim, including 0, which means full
	// history (from_ler omitted on first fetch) and is never adjusted or clamped.
	StartL1Block *uint64 `mapstructure:"StartL1Block"`
	// StartLookback is how far back from "now" StartL1Block is resolved when left unset (see
	// StartL1Block). Ignored when StartL1Block is set. Defaults to DefaultStartLookback.
	StartLookback cfgtypes.Duration `mapstructure:"StartLookback"`
	PollInterval  cfgtypes.Duration `mapstructure:"PollInterval"`
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
	if c.StartLookback.Duration < 0 {
		return fmt.Errorf("StartLookback must not be negative")
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

// ApplyDefaults fills every claimer's EthTxManager fields left at their Go zero value with
// aggkit's own established EthTxManager opinion (see the Default* constants above), and fills each
// bridge detector's StartLookback with DefaultStartLookback when left unset. It never overrides a
// value the operator did set -- including a detector's StartBlock/StartL1Block pointer, which stays
// nil here; resolving it into a concrete block happens later, once an L1 client is available (see
// autoclaim/bridgedetector.ResolveStartBlock). Callers should invoke it once, right after
// unmarshalling and before Validate.
func (c *Config) ApplyDefaults() {
	if c.L1ToL2BridgeDetector.StartLookback.Duration == 0 {
		c.L1ToL2BridgeDetector.StartLookback.Duration = DefaultStartLookback
	}
	if c.L2ToLxBridgeDetector.StartLookback.Duration == 0 {
		c.L2ToLxBridgeDetector.StartLookback.Duration = DefaultStartLookback
	}
	for i := range c.Claimers {
		applyEthTxManagerDefaults(&c.Claimers[i].EthTxManager)
	}
}

// applyEthTxManagerDefaults fills cfg's zero-valued fields in place.
//
// Only fields whose zero value is always broken are defaulted. SafeStatusL1NumberOfBlocks,
// FinalizedStatusL1NumberOfBlocks and EstimateGasMaxRetries are deliberately left alone: the
// vendored zkevm-ethtx-manager module gives their zero value a meaning ("use the network's own
// safe/finalized tag", "retry forever"), and an unmarshalled struct cannot tell an omitted field
// from an explicit 0, so defaulting them would silently override a deliberate operator choice.
func applyEthTxManagerDefaults(cfg *ethtxmanager.Config) {
	if cfg.FrequencyToMonitorTxs.Duration == 0 {
		cfg.FrequencyToMonitorTxs.Duration = defaultEthTxManagerFrequencyToMonitorTxs
	}
	if cfg.WaitTxToBeMined.Duration == 0 {
		cfg.WaitTxToBeMined.Duration = defaultEthTxManagerWaitTxToBeMined
	}
	if cfg.GetReceiptMaxTime.Duration == 0 {
		cfg.GetReceiptMaxTime.Duration = defaultEthTxManagerGetReceiptMaxTime
	}
	if cfg.GetReceiptWaitInterval.Duration == 0 {
		cfg.GetReceiptWaitInterval.Duration = defaultEthTxManagerGetReceiptWaitInterval
	}
	// A zero (or negative) GasPriceMarginFactor is never a legitimate request: it means "multiply
	// the suggested gas price by zero", i.e. never send a valid tx.
	if cfg.GasPriceMarginFactor <= 0 {
		cfg.GasPriceMarginFactor = defaultEthTxManagerGasPriceMarginFactor
	}
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
	if c.L1ToL2BridgeDetector.StartLookback.Duration < 0 {
		return fmt.Errorf("AutoClaim.L1ToL2BridgeDetector.StartLookback must not be negative")
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
