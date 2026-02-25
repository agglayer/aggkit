package remove_ger

import (
	"fmt"

	"github.com/agglayer/aggkit/bridgesync"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"github.com/urfave/cli/v2"
)

// Config holds the subset of aggkit configuration fields needed by the remove-GER tool,
// plus tool-specific settings in the RemoveGER section.
type Config struct {
	// L1NetworkConfig contains the L1 RPC URL and contract addresses.
	L1NetworkConfig ethermanconfig.L1NetworkConfig `mapstructure:"L1NetworkConfig"`

	// Common contains shared settings such as the L2 RPC URL.
	Common ethermanconfig.CommonConfig `mapstructure:"Common"`

	// BridgeL2Sync contains the L2 bridge contract address used to initialize the binding.
	BridgeL2Sync bridgesync.Config `mapstructure:"BridgeL2Sync"`

	// L2GERSync contains the L2/L1 GER contract addresses.
	L2GERSync l2gersync.Config `mapstructure:"L2GERSync"`

	RemoveGER RemoveGERConfig `mapstructure:"RemoveGER"`
}

// RemoveGERConfig contains configuration specific to the remove-GER tool.
type RemoveGERConfig struct {
	// SovereignAdminPrivateKey is the private key with privileges to:
	// - activateEmergencyState / deactivateEmergencyState on the L2 bridge
	// - removeGlobalExitRoots on the L2 GER manager
	// - unsetMultipleClaims / setMultipleClaims on the L2 bridge
	// - forceEmitDetailedClaimEvent on the L2 bridge
	SovereignAdminPrivateKey KeyConfig `mapstructure:"SovereignAdminPrivateKey"`

	// BridgeServiceURL is the URL of the aggkit bridge service REST API (required).
	// Used for querying claims, bridges, and proofs.
	BridgeServiceURL string `mapstructure:"BridgeServiceURL"`

	// L2NetworkID is the network ID of the L2 network served by the bridge service.
	// Required for querying L2 claims via the bridge service.
	L2NetworkID uint32 `mapstructure:"L2NetworkID"`
}

// KeyConfig holds keystore path and password for the sovereign admin key.
type KeyConfig struct {
	Path     string `mapstructure:"Path"`
	Password string `mapstructure:"Password"`
}

// LoadConfig reads the TOML config file(s) specified by --cfg and unmarshals only
// the fields required by the remove-GER tool.
func LoadConfig(c *cli.Context) (*Config, error) {
	v := viper.New()
	v.SetConfigType("toml")

	for _, cfgFile := range c.StringSlice("cfg") {
		v.SetConfigFile(cfgFile)
		if err := v.MergeInConfig(); err != nil {
			return nil, fmt.Errorf("read config %s: %w", cfgFile, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.TextUnmarshallerHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &cfg, nil
}
