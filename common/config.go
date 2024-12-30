package common

import "github.com/agglayer/aggkit/translator"

// Config holds the configuration for the CDK.
type Config struct {
	// NetworkID is the networkID of the CDK being run
	NetworkID uint32 `mapstructure:"NetworkID"`
	// Contract Versions: elderberry, banana
	ContractVersions string            `mapstructure:"ContractVersions"`
	Translator       translator.Config `mapstructure:"Translator"`
}
