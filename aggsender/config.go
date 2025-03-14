package aggsender

import (
	"fmt"

	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/signer"
)

// Config is the configuration for the AggSender
type Config struct {
	// StoragePath is the path of the sqlite db on which the AggSender will store the data
	StoragePath string `mapstructure:"StoragePath"`
	// AggLayerURL is the URL of the AggLayer
	AggLayerURL string `mapstructure:"AggLayerURL"`
	// AggsenderPrivateKey is the private key which is used to sign certificates
	AggsenderPrivateKey signer.SignerConfig `mapstructure:"AggsenderPrivateKey"`
	// BlockFinality indicates which finality follows AggLayer
	BlockFinality string `jsonschema:"enum=LatestBlock, enum=SafeBlock, enum=PendingBlock, enum=FinalizedBlock, enum=EarliestBlock" mapstructure:"BlockFinality"` //nolint:lll
	// EpochNotificationPercentage indicates the percentage of the epoch
	// the AggSender should send the certificate
	// 0 -> Begin
	// 50 -> Middle
	EpochNotificationPercentage uint `mapstructure:"EpochNotificationPercentage"`
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
	// MaxEpochPercentageAllowedToSendCertificate is the percentage of the epoch
	// after which the AggSender is forbidden to send the certificate.
	// If value==0  || value >= 100 the check is disabled
	// 0 -> Begin
	// 50 -> Middle
	// 100 -> End
	MaxEpochPercentageAllowedToSendCertificate uint `mapstructure:"MaxEpochPercentageAllowedToSendCertificate"`
	// MaxSubmitCertificateRate is the maximum rate of certificate submission allowed
	MaxSubmitCertificateRate common.RateLimitConfig `mapstructure:"MaxSubmitCertificateRate"`
	// RequireStorageContentCompatibility is true it's mandatory that data stored in the database
	// is compatible with the running environment
	RequireStorageContentCompatibility bool `mapstructure:"RequireStorageContentCompatibility"`
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
		"SaveCertificatesToFilesPath: " + c.SaveCertificatesToFilesPath + "\n" +
		"DryRun: " + fmt.Sprintf("%t", c.DryRun) + "\n" +
		"EnableRPC: " + fmt.Sprintf("%t", c.EnableRPC) + "\n" +
		"CheckStatusCertificateInterval: " + c.CheckStatusCertificateInterval.String() + "\n" +
		"RetryCertImmediatelyAfterInError: " + fmt.Sprintf("%t", c.RetryCertAfterInError) + "\n" +
		"MaxSubmitRate: " + c.MaxSubmitCertificateRate.String() + "\n" +
		"MaxEpochPercentageAllowedToSendCertificate: " +
		fmt.Sprintf("%d", c.MaxEpochPercentageAllowedToSendCertificate) + "\n"
}
