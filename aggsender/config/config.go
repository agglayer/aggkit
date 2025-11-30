package config

import (
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/optimistic"
	"github.com/agglayer/aggkit/aggsender/query"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/grpc"
	signertypes "github.com/agglayer/go_signer/signer/types"
	ethCommon "github.com/ethereum/go-ethereum/common"
)

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
	// EpochNotificationPercentage indicates the percentage of the epoch
	// the AggSender should send the certificate
	// 0 -> Begin
	// 50 -> Middle
	EpochNotificationPercentage uint `mapstructure:"EpochNotificationPercentage"`
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
	// It allow to change committee URL for testing purposes
	CommitteeOverride query.CommitteeOverride `mapstructure:"CommitteeOverride"`
	// AgglayerBridgeL2Addr is the address of the bridge L2 sovereign contract on L2 sovereign chain
	AgglayerBridgeL2Addr ethCommon.Address `mapstructure:"AgglayerBridgeL2Addr"`
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
		"EpochNotificationPercentage: " + fmt.Sprintf("%d", c.EpochNotificationPercentage) + "\n" +
		"DryRun: " + fmt.Sprintf("%t", c.DryRun) + "\n" +
		"EnableRPC: " + fmt.Sprintf("%t", c.EnableRPC) + "\n" +
		"AggkitProverClient: " + c.AggkitProverClient.String() + "\n" +
		"Mode: " + c.Mode.String() + "\n" +
		"CheckStatusCertificateInterval: " + c.CheckStatusCertificateInterval.String() + "\n" +
		"RetryCertAfterInError: " + fmt.Sprintf("%t", c.RetryCertAfterInError) + "\n" +
		"SovereignRollupAddr: " + c.SovereignRollupAddr.Hex() + "\n" +
		"RequireNoFEPBlockGap: " + fmt.Sprintf("%t", c.RequireNoFEPBlockGap) + "\n" +
		"RetriesToBuildAndSendCertificate: " + c.RetriesToBuildAndSendCertificate.String() + "\n" +
		"StorageRetainCertificatesPolicy: " + c.StorageRetainCertificatesPolicy.String() + "\n"
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
	return nil
}
