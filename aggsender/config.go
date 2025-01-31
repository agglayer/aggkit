package aggsender

import (
	"fmt"

	"github.com/agglayer/aggkit/config/types"
)

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
	// MaxCertSize is the maximum size of the certificate (the emitted certificate can be bigger that this size)
	// 0 is infinite
	MaxCertSize uint `mapstructure:"MaxCertSize"`
	// BridgeMetadataAsHash is a flag to import the bridge metadata as hash
	BridgeMetadataAsHash bool `mapstructure:"BridgeMetadataAsHash"`
	// DryRun is a flag to enable the dry run mode
	// in this mode the AggSender will not send the certificates to Agglayer
	DryRun bool `mapstructure:"DryRun"`
	// EnableRPC is a flag to enable the RPC for aggsender
	EnableRPC bool `mapstructure:"EnableRPC"`
}

// String returns a string representation of the Config
func (c Config) String() string {
	return "StoragePath: " + c.StoragePath + "\n" +
		"AggLayerURL: " + c.AggLayerURL + "\n" +
		"AggsenderPrivateKeyPath: " + c.AggsenderPrivateKey.Path + "\n" +
		"URLRPCL2: " + c.URLRPCL2 + "\n" +
		"BlockFinality: " + c.BlockFinality + "\n" +
		"EpochNotificationPercentage: " + fmt.Sprintf("%d", c.EpochNotificationPercentage) + "\n" +
		"SaveCertificatesToFilesPath: " + c.SaveCertificatesToFilesPath + "\n"
}
