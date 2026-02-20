package remove_ger

import (
	"fmt"

	"github.com/agglayer/aggkit/config"
	"github.com/spf13/viper"
	"github.com/urfave/cli/v2"
)

// Config extends the main aggkit config with fields specific to the remove-GER tool.
type Config struct {
	config.Config

	RemoveGER RemoveGERConfig
}

// RemoveGERConfig contains configuration specific to the remove-GER tool.
type RemoveGERConfig struct {
	// SovereignAdminPrivateKey is the private key with privileges to:
	// - activateEmergencyState / deactivateEmergencyState on the L2 bridge
	// - removeGlobalExitRoots on the L2 GER manager
	// - unsetMultipleClaims / setMultipleClaims on the L2 bridge
	// - forceEmitDetailedClaimEvent on the L2 bridge
	SovereignAdminPrivateKey KeyConfig `mapstructure:"SovereignAdminPrivateKey"`

	// BridgeServiceURL is the URL of the aggkit bridge service REST API.
	// Used for querying claims, bridges, and proofs.
	BridgeServiceURL string `mapstructure:"BridgeServiceURL"`
}

// KeyConfig holds keystore path and password for the sovereign admin key.
type KeyConfig struct {
	Path     string `mapstructure:"Path"`
	Password string `mapstructure:"Password"`
}

// LoadConfig loads the extended config using the same pipeline as the main aggkit binary.
// After config.Load(c), viper still holds the merged config, so we unmarshal the [RemoveGER] section.
func LoadConfig(c *cli.Context) (*Config, error) {
	baseCfg, err := config.Load(c)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	var removeGER RemoveGERConfig
	// Viper typically lowercases keys; TOML [RemoveGER] becomes "removeger".
	if err := viper.UnmarshalKey("removeger", &removeGER); err != nil {
		return nil, fmt.Errorf("unmarshal RemoveGER config: %w", err)
	}

	return &Config{
		Config:    *baseCfg,
		RemoveGER: removeGER,
	}, nil
}
