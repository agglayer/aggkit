package force_ger_update

import (
	"fmt"
	"os"
	"strings"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	aggkitConfig "github.com/agglayer/aggkit/config"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"github.com/urfave/cli/v2"
)

// Config is the root, standalone configuration for the force_ger_update tool.
type Config struct {
	// ForceGERUpdate contains all the settings the tool needs.
	ForceGERUpdate ForceGERUpdateConfig `mapstructure:"ForceGERUpdate"`
}

// ForceGERUpdateConfig contains configuration specific to the force-GER-update tool.
type ForceGERUpdateConfig struct {
	// L1URL is the L1 HTTP RPC URL (mandatory).
	L1URL string `mapstructure:"L1URL"`

	// L1WSURL is an optional L1 websocket RPC URL. When set, event watching uses a subscription
	// (WatchUpdateL1InfoTree); otherwise the monitor polls via FilterLogs.
	L1WSURL string `mapstructure:"L1WSURL"`

	// GlobalExitRootManagerAddr is the L1 PolygonZkEVMGlobalExitRootV2 (agglayerger binding) address.
	GlobalExitRootManagerAddr common.Address `mapstructure:"GlobalExitRootManagerAddr"`

	// BridgeAddr is the L1 PolygonZkEVMBridgeV2 (agglayerbridge binding) address.
	BridgeAddr common.Address `mapstructure:"BridgeAddr"`

	// MaxTimeWithoutGERUpdate (X) is the max time allowed to elapse since the last GER update
	// before a forced update is sent.
	MaxTimeWithoutGERUpdate configtypes.Duration `mapstructure:"MaxTimeWithoutGERUpdate"`

	// CheckInterval is how often the timer loop evaluates the elapsed time.
	CheckInterval configtypes.Duration `mapstructure:"CheckInterval"`

	// EventPollInterval is, in polling mode (L1WSURL unset), how often to FilterLogs for new
	// UpdateL1InfoTree events.
	EventPollInterval configtypes.Duration `mapstructure:"EventPollInterval"`

	// InitialLookbackBlocks bounds how far back (in FilterLogsChunkSize chunks) the boot scan for
	// the last UpdateL1InfoTree event looks.
	InitialLookbackBlocks uint64 `mapstructure:"InitialLookbackBlocks"`

	// FilterLogsChunkSize is the block range used per FilterLogs call.
	FilterLogsChunkSize uint64 `mapstructure:"FilterLogsChunkSize"`

	// DestinationNetwork is the bridgeMessage destinationNetwork. Must not be 0 (L1 itself).
	DestinationNetwork uint32 `mapstructure:"DestinationNetwork"`

	// DestinationAddress is the bridgeMessage destinationAddress. Defaults to the sender address
	// (ethTxManager.From()) when left unset (zero address).
	DestinationAddress common.Address `mapstructure:"DestinationAddress"`

	// DryRun logs the calldata instead of sending the transaction when true.
	DryRun bool `mapstructure:"DryRun"`

	// EthTxManager is the standard zkevm-ethtx-manager configuration used to send and track the
	// forced-update transaction.
	EthTxManager ethtxmanager.Config `mapstructure:"EthTxManager"`
}

// Validate checks the mandatory fields of the configuration.
func (c *ForceGERUpdateConfig) Validate() error {
	if c.L1URL == "" {
		return fmt.Errorf("ForceGERUpdate.L1URL is required")
	}
	if c.DestinationNetwork == 0 {
		return fmt.Errorf("ForceGERUpdate.DestinationNetwork must not be 0 (L1 itself)")
	}
	if c.BridgeAddr == (common.Address{}) {
		return fmt.Errorf("ForceGERUpdate.BridgeAddr is required and must not be the zero address")
	}
	if c.GlobalExitRootManagerAddr == (common.Address{}) {
		return fmt.Errorf("ForceGERUpdate.GlobalExitRootManagerAddr is required and must not be the zero address")
	}

	return nil
}

// LoadConfig reads the TOML config file(s) specified by --cfg and unmarshals the fields required by
// the force-GER-update tool. Uses the same template rendering pipeline as the main aggkit binary so
// that default template variables (unrelated to this tool) resolve without extra ceremony.
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

	return loadConfigFromString(rendered)
}

// loadConfigFromString parses an already-rendered TOML document into a Config. Split out from
// LoadConfig so tests can exercise decoding (including CDK_-prefixed env var overrides) without
// going through the full template render pipeline.
func loadConfigFromString(rendered string) (*Config, error) {
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
