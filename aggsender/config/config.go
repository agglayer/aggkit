package config

import (
	"fmt"

	"github.com/agglayer/aggkit/aggsender/optimistic"
	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	ethCommon "github.com/ethereum/go-ethereum/common"
)

// Config is the configuration for the AggSender
type Config struct {
	// StoragePath is the path of the sqlite db on which the AggSender will store the data
	StoragePath string `mapstructure:"StoragePath"`
	// AggLayerURL is the URL of the AggLayer
	AggLayerURL string `mapstructure:"AggLayerURL"`
	// AggsenderPrivateKey is the private key which is used to sign certificates
	AggsenderPrivateKey signertypes.SignerConfig `mapstructure:"AggsenderPrivateKey"`
	// URLRPCL2 is the URL of the L2 RPC node
	URLRPCL2 string `mapstructure:"URLRPCL2"`
	// BlockFinality indicates which finality the AggLayer follows
	BlockFinality string `jsonschema:"enum=LatestBlock, enum=SafeBlock, enum=PendingBlock, enum=FinalizedBlock, enum=EarliestBlock" mapstructure:"BlockFinality"` //nolint:lll
	// EpochNotificationPercentage indicates the percentage of the epoch
	// the AggSender should send the certificate
	// 0 -> Begin
	// 50 -> Middle
	EpochNotificationPercentage uint `mapstructure:"EpochNotificationPercentage"`
	// MaxRetriesStoreCertificate is the maximum number of retries to store a certificate
	// 0 is infinite
	MaxRetriesStoreCertificate int `mapstructure:"MaxRetriesStoreCertificate"`
	// DelayBeetweenRetries is the delay between retries:
	//  is used on store Certificate and also in initial check
	DelayBeetweenRetries types.Duration `mapstructure:"DelayBeetweenRetries"`
	// KeepCertificatesHistory is a flag to keep the certificates history on storage
	KeepCertificatesHistory bool `mapstructure:"KeepCertificatesHistory"`
	// MaxCertSize is the maximum size of the certificate (the emitted certificate cannot be bigger that this size)
	// 0 is infinite
	MaxCertSize uint `mapstructure:"MaxCertSize"`
	// DryRun is a flag to enable the dry run mode
	// in this mode the AggSender will not send the certificates to Agglayer
	DryRun bool `mapstructure:"DryRun"`
	// EnableRPC is a flag to enable the RPC for aggsender
	EnableRPC bool `mapstructure:"EnableRPC"`
	// AggchainProofURL is the URL of the AggkitProver
	AggchainProofURL string `mapstructure:"AggchainProofURL"`
	// Mode is the mode of the AggSender (regular pessimistic proof mode or the aggchain proof mode)
	Mode string `jsonschema:"enum=PessimisticProof, enum=AggchainProof" mapstructure:"Mode"`
	// CheckStatusCertificateInterval is the interval at which the AggSender will check the certificate status in Agglayer
	CheckStatusCertificateInterval types.Duration `mapstructure:"CheckStatusCertificateInterval"`
	// RetryCertAfterInError when a cert pass to 'InError'
	// state the AggSender will try to resend it immediately
	RetryCertAfterInError bool `mapstructure:"RetryCertAfterInError"`
	// MaxSubmitCertificateRate is the maximum rate of certificate submission allowed
	MaxSubmitCertificateRate common.RateLimitConfig `mapstructure:"MaxSubmitCertificateRate"`
	// GlobalExitRootL2Addr is the address of the GlobalExitRootManager contract on l2 sovereign chain
	// this address is needed for the AggchainProof mode of the AggSender
	GlobalExitRootL2Addr ethCommon.Address `mapstructure:"GlobalExitRootL2"`
	// GenerateAggchainProofTimeout is the timeout to wait for the aggkit-prover to generate the AggchainProof
	GenerateAggchainProofTimeout types.Duration `mapstructure:"GenerateAggchainProofTimeout"`
	// SovereignRollupAddr is the address of the sovereign rollup contract on L1
	SovereignRollupAddr ethCommon.Address `mapstructure:"SovereignRollupAddr"`
	// RequireStorageContentCompatibility is true it's mandatory that data stored in the database
	// is compatible with the running environment
	RequireStorageContentCompatibility bool `mapstructure:"RequireStorageContentCompatibility"`
	// UseAgglayerTLS is a flag to enable the Agglayer TLS handshake in the AggSender-Agglayer gRPC connection
	UseAgglayerTLS bool `mapstructure:"UseAgglayerTLS"`
	// UseAggkitProverTLS is a flag to enable the AggkitProver TLS handshake in the AggSender-AggkitProver gRPC connection
	UseAggkitProverTLS bool `mapstructure:"UseAggkitProverTLS"`
	// RequireNoFEPBlockGap is true if the AggSender should not accept a gap between
	// lastBlock from lastCertificate and first block of FEP
	RequireNoFEPBlockGap bool `mapstructure:"RequireNoFEPBlockGap"`
	// OptimisticModeConfig is the configuration for optimistic mode (required by FEP mode)
	OptimisticModeConfig optimistic.Config `mapstructure:"OptimisticModeConfig"`
	// MaxL2BlockNumber is the last L2 block number that is going to be included in a certificate
	// 0 means disabled
	MaxL2BlockNumber uint64 `mapstructure:"MaxL2BlockNumber"`
	// StopOnFinishedSendingAllCertificates is a flag to stop the AggSender when it finishes sending all certificates
	//  up to MaxL2BlockNumber
	StopOnFinishedSendingAllCertificates bool `mapstructure:"StopOnFinishedSendingAllCertificates"`
}

func (c Config) CheckCertConfigBriefString() string {
	return fmt.Sprintf("check_interval: %s, retry: %t", c.CheckStatusCertificateInterval, c.RetryCertAfterInError)
}

// String returns a string representation of the Config
func (c Config) String() string {
	return "StoragePath: " + c.StoragePath + "\n" +
		"AggLayerURL: " + c.AggLayerURL + "\n" +
		"AggsenderPrivateKey: " + c.AggsenderPrivateKey.Method.String() + "\n" +
		"BlockFinality: " + c.BlockFinality + "\n" +
		"EpochNotificationPercentage: " + fmt.Sprintf("%d", c.EpochNotificationPercentage) + "\n" +
		"DryRun: " + fmt.Sprintf("%t", c.DryRun) + "\n" +
		"EnableRPC: " + fmt.Sprintf("%t", c.EnableRPC) + "\n" +
		"AggchainProofURL: " + c.AggchainProofURL + "\n" +
		"Mode: " + c.Mode + "\n" +
		"CheckStatusCertificateInterval: " + c.CheckStatusCertificateInterval.String() + "\n" +
		"RetryCertAfterInError: " + fmt.Sprintf("%t", c.RetryCertAfterInError) + "\n" +
		"MaxSubmitRate: " + c.MaxSubmitCertificateRate.String() + "\n" +
		"GenerateAggchainProofTimeout: " + c.GenerateAggchainProofTimeout.String() + "\n" +
		"SovereignRollupAddr: " + c.SovereignRollupAddr.Hex() + "\n" +
		"UseAgglayerTLS: " + fmt.Sprintf("%t", c.UseAgglayerTLS) + "\n" +
		"UseAggkitProverTLS: " + fmt.Sprintf("%t", c.UseAggkitProverTLS) + "\n" +
		"RequireNoFEPBlockGap: " + fmt.Sprintf("%t", c.RequireNoFEPBlockGap) + "\n"
}
