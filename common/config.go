package common

import (
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/translator"
)

// Config holds the configuration for the CDK.
type Config struct {
	// IsValidiumMode has the value true if the sequence sender is running in validium mode.
	IsValidiumMode bool `mapstructure:"IsValidiumMode"`
	// NetworkID is the networkID of the CDK being run
	NetworkID uint32 `mapstructure:"NetworkID"`
	// Contract Versions: elderberry, banana
	ContractVersions string            `mapstructure:"ContractVersions"`
	Translator       translator.Config `mapstructure:"Translator"`
	// L2URL is the URL of the L2 node
	L2RPC ethermanconfig.RPCClientConfig `mapstructure:"L2RPC"`
}
