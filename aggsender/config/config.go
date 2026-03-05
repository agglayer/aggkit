package config

import (
	"fmt"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/optimistic"
	"github.com/agglayer/aggkit/aggsender/query"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/grpc"
	aggkittypes "github.com/agglayer/aggkit/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	ethCommon "github.com/ethereum/go-ethereum/common"
)

type TriggerASAPConfig struct {
	// DelayBetweenCertificates is the delay to wait before sending a new certificate after the previous one is settled
	DelayBetweenCertificates types.Duration `mapstructure:"DelayBetweenCertificates"`
	// MinimumNewCertificateInterval is the minimum interval between two new certificates triggers
	MinimumNewCertificateInterval types.Duration `mapstructure:"MinimumNewCertificateInterval"`
	// OnNewL2Bridge indicates whether to trigger a new certificate when a new L2 bridge exit is detected
	OnNewL2Bridge bool `mapstructure:"OnNewL2Bridge"`
}

func NewTriggerASAPConfigDefault() *TriggerASAPConfig {
	return &TriggerASAPConfig{
		DelayBetweenCertificates:      types.Duration{Duration: time.Second},
		MinimumNewCertificateInterval: types.Duration{Duration: time.Hour},
	}
}

func (c *TriggerASAPConfig) String() string {
	return fmt.Sprintf("DelayBetweenCertificates: %s, MinimumNewCertificateInterval: %s, OnNewL2Bridge: %t",
		c.DelayBetweenCertificates.String(),
		c.MinimumNewCertificateInterval.String(),
		c.OnNewL2Bridge)
}

func (c *TriggerASAPConfig) Validate() error {
	if c.DelayBetweenCertificates.Duration < 0 {
		return fmt.Errorf("DelayBetweenCertificates cannot be negative")
	}
	if c.MinimumNewCertificateInterval.Duration <= 0 {
		return fmt.Errorf("MinimumNewCertificateInterval must be >= 0")
	}
	return nil
}

type TriggerEpochBasedConfig struct {
	// EpochNotificationPercentage indicates the percentage of the epoch
	// at which the AggSender should send the certificate
	// 0 -> Begin
	// 50 -> Middle
	EpochNotificationPercentage uint `mapstructure:"EpochNotificationPercentage"`
}

// String returns a string representation of the Config
func (c TriggerEpochBasedConfig) String() string {
	return fmt.Sprintf("EpochNotificationPercentage: %d", c.EpochNotificationPercentage)
}

// Config is the configuration for the AggSender
type Config struct {
	// StoragePath is the path of the sqlite db on which the AggSender will store the data
	StoragePath string `mapstructure:"StoragePath"`
	// RetainCertificatesPolicy is the policy to retain certificates in the database
	StorageRetainCertificatesPolicy db.StorageRetainCertificatesPolicy `mapstructure:"StorageRetainCertificatesPolicy"`
	// CertificatesDir is the directory where certificate JSON files will be stored
	CertificatesDir string `mapstructure:"CertificatesDir"`
	// AgglayerClient is the Agglayer gRPC client configuration
	AgglayerClient agglayer.ClientConfig `mapstructure:"AgglayerClient"`
	// AggsenderPrivateKey is the private key which is used to sign certificates
	AggsenderPrivateKey signertypes.SignerConfig `mapstructure:"AggsenderPrivateKey"`
	// URLRPCL2 is the URL of the L2 RPC node
	URLRPCL2 string `mapstructure:"URLRPCL2"`
	// MaxRetriesStoreCertificate is the maximum number of retries to store a certificate
	// 0 is infinite
	MaxRetriesStoreCertificate int `mapstructure:"MaxRetriesStoreCertificate"`
	// DelayBetweenRetries is the delay between retries:
	//  is used on store Certificate and also in initial check
	DelayBetweenRetries types.Duration `mapstructure:"DelayBetweenRetries"`
	// MaxCertSize is the maximum size of the certificate (the emitted certificate cannot be bigger that this size)
	// 0 is infinite
	MaxCertSize uint `mapstructure:"MaxCertSize"`
	// DryRun is a flag to enable the dry run mode
	// in this mode the AggSender will not send the certificates to Agglayer
	DryRun bool `mapstructure:"DryRun"`
	// EnableRPC is a flag to enable the RPC for aggsender
	EnableRPC bool `mapstructure:"EnableRPC"`
	// AggkitProverClient is the config for the AggkitProver client
	AggkitProverClient *grpc.ClientConfig `mapstructure:"AggkitProverClient"`
	// Mode is the mode of the AggSender (regular pessimistic proof mode or the aggchain proof mode)
	Mode aggsendertypes.AggsenderMode `jsonschema:"enum=PessimisticProof, enum=AggchainProof, enum=Auto" mapstructure:"Mode"` //nolint:lll
	// CheckStatusCertificateInterval is the interval at which the AggSender will check the certificate status in Agglayer
	CheckStatusCertificateInterval types.Duration `mapstructure:"CheckStatusCertificateInterval"`
	// RetryCertAfterInError when a cert pass to 'InError'
	// state the AggSender will try to resend it immediately
	RetryCertAfterInError bool `mapstructure:"RetryCertAfterInError"`
	// GlobalExitRootL2Addr is the address of the GlobalExitRootManager contract on l2 sovereign chain
	// this address is needed for the AggchainProof mode of the AggSender
	GlobalExitRootL2Addr ethCommon.Address `mapstructure:"GlobalExitRootL2"`
	// GlobalExitRootL1Addr is the address of the GlobalExitRootManager contract on L1 (main chain)
	// this address is needed for the AggchainProof mode of the AggSender
	GlobalExitRootL1Addr ethCommon.Address `mapstructure:"GlobalExitRootL1Addr"`
	// SovereignRollupAddr is the address of the sovereign rollup contract on L1
	SovereignRollupAddr ethCommon.Address `mapstructure:"SovereignRollupAddr"`
	// RequireStorageContentCompatibility is true it's mandatory that data stored in the database
	// is compatible with the running environment
	RequireStorageContentCompatibility bool `mapstructure:"RequireStorageContentCompatibility"`
	// RequireNoFEPBlockGap is true if the AggSender should not accept a gap between
	// lastBlock from lastCertificate and first block of FEP
	RequireNoFEPBlockGap bool `mapstructure:"RequireNoFEPBlockGap"`
	// OptimisticModeConfig is the configuration for optimistic mode (required by FEP mode)
	OptimisticModeConfig optimistic.Config `mapstructure:"OptimisticModeConfig"`
	// RequireOneBridgeInPPCertificate is a flag to force the AggSender to have at least one bridge exit
	// for the Pessimistic Proof certificates
	RequireOneBridgeInPPCertificate bool `mapstructure:"RequireOneBridgeInPPCertificate"`
	// RollupManagerAddr is the address of the RollupManager contract on L1
	RollupManagerAddr ethCommon.Address `mapstructure:"RollupManagerAddr"`
	// RollupCreationBlockL1 is the block number when the rollup was created on L1
	RollupCreationBlockL1 uint64 `mapstructure:"RollupCreationBlockL1"`
	// MaxL2BlockNumber is the last L2 block number that is going to be included in a certificate
	// 0 means disabled
	MaxL2BlockNumber uint64 `mapstructure:"MaxL2BlockNumber"`
	// StopOnFinishedSendingAllCertificates is a flag to stop the AggSender when it finishes sending all certificates
	// up to MaxL2BlockNumber
	StopOnFinishedSendingAllCertificates bool `mapstructure:"StopOnFinishedSendingAllCertificates"`
	// ValidatorClient is the configuration for the ValidatorClient
	ValidatorClient *grpc.ClientConfig `mapstructure:"ValidatorClient"`
	// RetriesToBuildAndSendCertificate is the configuration for the retries to build and send a certificate
	RetriesToBuildAndSendCertificate common.RetryPolicyGenericConfig `mapstructure:"RetriesToBuildAndSendCertificate"`
	// RequireCommitteeMembershipCheck indicates whether to check if the signer is part of the committee
	RequireCommitteeMembershipCheck bool `mapstructure:"RequireCommitteeMembershipCheck"`
	// Allows changing the committee URL for testing purposes
	CommitteeOverride query.CommitteeOverride `mapstructure:"CommitteeOverride"`
	// AgglayerBridgeL2Addr is the address of the bridge L2 sovereign contract on L2 sovereign chain
	AgglayerBridgeL2Addr ethCommon.Address `mapstructure:"AgglayerBridgeL2Addr"`
	// UnsetClaimsMaxLogBlockRange is the proactive max block range for eth_getLogs queries when fetching unset claims.
	// 0 means disabled.
	UnsetClaimsMaxLogBlockRange uint64 `mapstructure:"UnsetClaimsMaxLogBlockRange"`
	// BlockFinalityForL1InfoTree indicates the block finality to use when querying for L1InfoRoot to use
	BlockFinalityForL1InfoTree aggkittypes.BlockNumberFinality `jsonschema:"enum=LatestBlock, enum=SafeBlock, enum=PendingBlock, enum=FinalizedBlock, enum=EarliestBlock" mapstructure:"BlockFinalityForL1InfoTree"` //nolint:lll
	// TriggerCertMode is the mode used to trigger certificate sending
	TriggerCertMode aggsendertypes.CertificateSendTriggerMode `jsonschema:"enum=EpochBased, enum=NewBridge, enum=ASAP, enum=Auto" mapstructure:"TriggerCertMode"` //nolint:lll
	// TriggerEpochBased is the configuration for the EpochBased trigger mode (TriggerCertMode==EpochBased)
	TriggerEpochBased TriggerEpochBasedConfig `mapstructure:"TriggerEpochBased"`
	// TriggerASAP is the configuration for the ASAP trigger mode (TriggerCertMode==ASAP)
	TriggerASAP TriggerASAPConfig `mapstructure:"TriggerASAP"`
	// EnableDebugSendCertificate enables the debug RPC endpoint for sending arbitrary certificates.
	// When true, the aggsender's normal certificate-sending loop is disabled.
	// Default false. NEVER enable in production.
	EnableDebugSendCertificate bool `mapstructure:"EnableDebugSendCertificate"`
	// DebugSendCertificateAuthAddress is the Ethereum address authorized to sign debug send requests.
	// Only used when EnableDebugSendCertificate is true.
	DebugSendCertificateAuthAddress ethCommon.Address `mapstructure:"DebugSendCertificateAuthAddress"`
}

func (c Config) CheckCertConfigBriefString() string {
	return fmt.Sprintf("check_interval: %s, retry: %t", c.CheckStatusCertificateInterval, c.RetryCertAfterInError)
}

// String returns a string representation of the Config
func (c Config) String() string {
	return "StoragePath: " + c.StoragePath + "\n" +
		"CertificatesDir: " + c.CertificatesDir + "\n" +
		"AgglayerClient: " + c.AgglayerClient.String() + "\n" +
		"AggsenderPrivateKey: " + c.AggsenderPrivateKey.Method.String() + "\n" +
		"DryRun: " + fmt.Sprintf("%t", c.DryRun) + "\n" +
		"EnableRPC: " + fmt.Sprintf("%t", c.EnableRPC) + "\n" +
		"AggkitProverClient: " + c.AggkitProverClient.String() + "\n" +
		"Mode: " + c.Mode.String() + "\n" +
		"CheckStatusCertificateInterval: " + c.CheckStatusCertificateInterval.String() + "\n" +
		"RetryCertAfterInError: " + fmt.Sprintf("%t", c.RetryCertAfterInError) + "\n" +
		"SovereignRollupAddr: " + c.SovereignRollupAddr.Hex() + "\n" +
		"RequireNoFEPBlockGap: " + fmt.Sprintf("%t", c.RequireNoFEPBlockGap) + "\n" +
		"RetriesToBuildAndSendCertificate: " + c.RetriesToBuildAndSendCertificate.String() + "\n" +
		"StorageRetainCertificatesPolicy: " + c.StorageRetainCertificatesPolicy.String() + "\n" +
		"BlockFinalityForL1InfoTree: " + c.BlockFinalityForL1InfoTree.String() + "\n" +
		"TriggerCertMode: " + c.TriggerCertMode.String() + "\n" +
		"TriggerEpochBased: " + c.TriggerEpochBased.String() + "\n" +
		"EnableDebugSendCertificate: " + fmt.Sprintf("%t", c.EnableDebugSendCertificate) + "\n"
}

// Validate checks if the configuration is valid
func (c Config) Validate() error {
	if err := c.AgglayerClient.Validate(); err != nil {
		return fmt.Errorf("invalid agglayer client config: %w", err)
	}

	if c.Mode == aggsendertypes.AggchainProofMode {
		if err := c.AggkitProverClient.Validate(); err != nil {
			return fmt.Errorf("invalid aggkit prover client config: %w", err)
		}
	}
	if err := c.RetriesToBuildAndSendCertificate.Validate(); err != nil {
		return fmt.Errorf("invalid RetriesToBuildAndSendCertificate config: %w", err)
	}
	if err := c.StorageRetainCertificatesPolicy.Validate(); err != nil {
		return fmt.Errorf("invalid StorageRetainCertificatesPolicy config: %w", err)
	}
	if err := c.BlockFinalityForL1InfoTree.Validate(); err != nil {
		return fmt.Errorf("invalid BlockFinalityForL1InfoTree configuration: %w", err)
	}
	if err := c.TriggerCertMode.Validate(); err != nil {
		return fmt.Errorf("invalid TriggerCertMode config: %w", err)
	}
	return nil
}
