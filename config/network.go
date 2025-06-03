package config

import (
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
)

// NetworkConfig is the configuration struct for the different environments
type NetworkConfig struct {
	// L1: Configuration related to L1
	L1Config ethermanconfig.L1Config `mapstructure:"L1"`
}
