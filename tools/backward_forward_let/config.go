package backward_forward_let

import (
	"fmt"
	"os"
	"strings"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitConfig "github.com/agglayer/aggkit/config"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"github.com/urfave/cli/v2"
)

// Config holds the subset of aggkit configuration fields required by the backward/forward LET tool.
type Config struct {
	// Common contains shared settings such as the L2 RPC URL.
	Common ethermanconfig.CommonConfig `mapstructure:"Common"`

	// BridgeL2Sync contains the L2 bridge contract address used to initialize the binding.
	BridgeL2Sync bridgesync.Config `mapstructure:"BridgeL2Sync"`

	// AgglayerClient is the AggLayer gRPC client configuration.
	AgglayerClient agglayer.ClientConfig `mapstructure:"AgglayerClient"`

	// BackwardForwardLET contains tool-specific settings.
	BackwardForwardLET BackwardForwardLETConfig `mapstructure:"BackwardForwardLET"`
}

// BackwardForwardLETConfig contains configuration specific to the backward/forward LET tool.
type BackwardForwardLETConfig struct {
	// GERRemoverKey is the signing key used for GER-removal and bridge admin operations.
	GERRemoverKey signertypes.SignerConfig `mapstructure:"GERRemoverKey"`

	// EmergencyPauserKey is the signing key with activateEmergencyState privileges.
	EmergencyPauserKey signertypes.SignerConfig `mapstructure:"EmergencyPauserKey"`

	// EmergencyUnpauserKey is the signing key with deactivateEmergencyState privileges.
	EmergencyUnpauserKey signertypes.SignerConfig `mapstructure:"EmergencyUnpauserKey"`

	// BridgeServiceURL is the URL of the aggkit bridge service REST API (required).
	BridgeServiceURL string `mapstructure:"BridgeServiceURL"`

	// AggsenderRPCURL is the JSON-RPC URL of the running aggsender (required for certificate queries).
	AggsenderRPCURL string `mapstructure:"AggsenderRPCURL"`

	// L2NetworkID is the network ID of the L2 chain.
	L2NetworkID uint32 `mapstructure:"L2NetworkID"`

	// CertificateExitsFile is an optional path to a JSON override file containing
	// pre-extracted bridge exits keyed by certificate height. When set, used as a
	// fallback if the aggsender RPC cannot supply bridge exits for a height.
	// Obtain the file by calling admin_getCertificate on the agglayer for each
	// cert ID reported in the tool's missing-cert output.
	CertificateExitsFile string `mapstructure:"CertificateExitsFile"`
}

// LoadConfig reads the TOML config file(s) specified by --cfg and unmarshals the
// fields required by the backward/forward LET tool. Uses the same template rendering
// pipeline as the main aggkit binary.
func LoadConfig(c *cli.Context) (*Config, error) {
	userFiles := make([]aggkitConfig.FileData, 0)
	for _, cfgFile := range c.StringSlice("cfg") {
		content, err := os.ReadFile(cfgFile)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", cfgFile, err)
		}
		userFiles = append(userFiles, aggkitConfig.FileData{Name: cfgFile, Content: string(content)})
	}

	defaultFiles := []aggkitConfig.FileData{
		{Name: "default_mandatory_vars", Content: aggkitConfig.DefaultMandatoryVars},
		{Name: "default_vars", Content: aggkitConfig.DefaultVars},
		{Name: "default_values", Content: aggkitConfig.DefaultValues},
	}
	allFiles := make([]aggkitConfig.FileData, 0, len(defaultFiles)+len(userFiles))
	allFiles = append(allFiles, defaultFiles...)
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
