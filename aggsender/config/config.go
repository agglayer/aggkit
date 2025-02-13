package aggsender

import (
	"fmt"
	"time"

	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
)

type SubmitCertificateConfig struct {
	// L1BlockCreationRate is the rate of creation blocks on L1
	// if it's 0 it will be autodiscover
	L1BlockCreationRate time.Duration
	// SubmitNewCertIfRemainsForNextEpoch is the minimum time before the end of the epoch
	// to submit a certificate
	SubmitIfRemainsForNextEpoch time.Duration
	// MaxTimeBeforeEndingEpoch is the maximum time before the end of the epoch
	// if pending time > that is value we are going to wait no next epoch
	NoSubmitIfRemainsForNextEpoch time.Duration
}

func (c SubmitCertificateConfig) String() string {
	return fmt.Sprintf("L1BlockCreationRate: %s, SubmitIfRemainsForNextEpoch: %s, NoSubmitIfRemainsForNextEpoch: %s",
		c.L1BlockCreationRate, c.SubmitIfRemainsForNextEpoch, c.NoSubmitIfRemainsForNextEpoch)
}

// Config is the configuration for the AggSender
type Config struct {
	// StoragePath is the path of the sqlite db on which the AggSender will store the data
	StoragePath string `mapstructure:"StoragePath"`
	// AggLayerURL is the URL of the AggLayer
	AggLayerURL string `mapstructure:"AggLayerURL"`
	// AggsenderPrivateKey is the private key which is used to sign certificates
	AggsenderPrivateKey types.KeystoreFileConfig `mapstructure:"AggsenderPrivateKey"`
	// URLRPCL2 is the URL of the L2 RPC node
	URLRPCL2 string `mapstructure:"URLRPCL2"`
	// BlockFinality indicates which finality follows AggLayer
	BlockFinality string `jsonschema:"enum=LatestBlock, enum=SafeBlock, enum=PendingBlock, enum=FinalizedBlock, enum=EarliestBlock" mapstructure:"BlockFinality"` //nolint:lll

	// SubmitCertificateConfig params of the submition of certificates
	SubmitCertificateConfig SubmitCertificateConfig

	// SaveCertificatesToFilesPath if != "" tells  the AggSender to save the certificates to a file in this path
	SaveCertificatesToFilesPath string `mapstructure:"SaveCertificatesToFilesPath"`

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
	// BridgeMetadataAsHash is a flag to import the bridge metadata as hash
	BridgeMetadataAsHash bool `mapstructure:"BridgeMetadataAsHash"`
	// DryRun is a flag to enable the dry run mode
	// in this mode the AggSender will not send the certificates to Agglayer
	DryRun bool `mapstructure:"DryRun"`
	// EnableRPC is a flag to enable the RPC for aggsender
	EnableRPC bool `mapstructure:"EnableRPC"`
	// CheckStatusCertificateInterval is the interval at which the AggSender will check the certificate status in Agglayer
	CheckStatusCertificateInterval types.Duration `mapstructure:"CheckStatusCertificateInterval"`
	// RetryCertAfterInError when a cert pass to 'InError'
	// state the AggSender will try to resend it immediately
	RetryCertAfterInError bool `mapstructure:"RetryCertAfterInError"`

	// MaxSubmitCertificateRate is the maximum rate of certificate submission allowed
	MaxSubmitCertificateRate common.RateLimitConfig `mapstructure:"MaxSubmitCertificateRate"`
}

func (c Config) CheckCertConfigBriefString() string {
	return fmt.Sprintf("check_interval: %s, retry: %t", c.CheckStatusCertificateInterval, c.RetryCertAfterInError)
}

// String returns a string representation of the Config
func (c Config) String() string {
	return "StoragePath: " + c.StoragePath + "\n" +
		"AggLayerURL: " + c.AggLayerURL + "\n" +
		"AggsenderPrivateKeyPath: " + c.AggsenderPrivateKey.Path + "\n" +
		"URLRPCL2: " + c.URLRPCL2 + "\n" +
		"BlockFinality: " + c.BlockFinality + "\n" +
		"SubmitCertificateConfig: " + c.SubmitCertificateConfig.String() + "\n" +
		"SaveCertificatesToFilesPath: " + c.SaveCertificatesToFilesPath + "\n" +
		"DryRun: " + fmt.Sprintf("%t", c.DryRun) + "\n" +
		"EnableRPC: " + fmt.Sprintf("%t", c.EnableRPC) + "\n" +
		"CheckStatusCertificateInterval: " + c.CheckStatusCertificateInterval.String() + "\n" +
		"RetryCertInmediatlyAfterInError: " + fmt.Sprintf("%t", c.RetryCertAfterInError) + "\n" +
		"MaxSubmitRate: " + c.MaxSubmitCertificateRate.String() + "\n" +
		"MaxEpochPercentageAllowedToSendCertificate: "
}
