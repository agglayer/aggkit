package remove_ger

import (
	"fmt"
	"os"
	"strings"

	"github.com/agglayer/aggkit/bridgesync"
	aggkitConfig "github.com/agglayer/aggkit/config"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/l2gersync"
	signertypes "github.com/agglayer/go_signer/signer/types"
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
	// SovereignAdminKey is the signing key with privileges to:
	// - activateEmergencyState / deactivateEmergencyState on the L2 bridge
	// - removeGlobalExitRoots on the L2 GER manager
	// - unsetMultipleClaims / setMultipleClaims on the L2 bridge
	// - forceEmitDetailedClaimEvent on the L2 bridge
	// Supports local keystore, AWS KMS, and GCP KMS via signertypes.SignerConfig.
	SovereignAdminKey signertypes.SignerConfig `mapstructure:"SovereignAdminKey"`

	// BridgeServiceURL is the URL of the aggkit bridge service REST API (required).
	// Used for querying claims, bridges, and proofs.
	BridgeServiceURL string `mapstructure:"BridgeServiceURL"`

	// L2NetworkID is the network ID of the L2 network served by the bridge service.
	// Required for querying L2 claims via the bridge service.
	L2NetworkID uint32 `mapstructure:"L2NetworkID"`
}

// LoadConfig reads the TOML config file(s) specified by --cfg and unmarshals the
// fields required by the remove-GER tool. Uses the same template rendering pipeline
// as the main aggkit binary so that template variables (e.g. L1URL → L1NetworkConfig.RPC.URL)
// are resolved correctly.
func LoadConfig(c *cli.Context) (*Config, error) {
	// Build FileData list for template rendering.
	userFiles := make([]aggkitConfig.FileData, 0)
	for _, cfgFile := range c.StringSlice("cfg") {
		content, err := os.ReadFile(cfgFile)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", cfgFile, err)
		}
		userFiles = append(userFiles, aggkitConfig.FileData{Name: cfgFile, Content: string(content)})
	}

	// Prepend defaults so template variables ({{L1Config.URL}}, {{L2URL}}, etc.) resolve.
	allFiles := []aggkitConfig.FileData{
		{Name: "default_mandatory_vars", Content: aggkitConfig.DefaultMandatoryVars},
		{Name: "default_vars", Content: aggkitConfig.DefaultVars},
		{Name: "default_values", Content: aggkitConfig.DefaultValues},
	}
	allFiles = append(allFiles, userFiles...)

	rendered, err := aggkitConfig.NewConfigRender(allFiles, aggkitConfig.EnvVarPrefix).Render()
	if err != nil {
		return nil, fmt.Errorf("render config: %w", err)
	}

	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader(rendered)); err != nil {
		return nil, fmt.Errorf("parse rendered config: %w", err)
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
