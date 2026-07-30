package config

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // not used for security: config fingerprint for the health endpoint
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker"
	aggkitcommon "github.com/agglayer/aggkit/common"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"github.com/urfave/cli/v2"
)

const (
	// FlagCfg is the flag for the configuration file(s)
	FlagCfg = "cfg"
	// FlagComponents is the flag for the list of components to run
	FlagComponents = "components"
	// EnvVarPrefix is the prefix for the environment variables that override config values
	EnvVarPrefix = "CDK_PROXY"
	// ConfigType is the format of the configuration files
	ConfigType = "toml"
)

const (
	// PROXY name to identify the bridge-service proxy component
	PROXY = "proxy"
	// TRACKER name to identify the bridge tracker component
	TRACKER = "tracker"
)

// DefaultValues is the default configuration
const DefaultValues = `
[Log]
Environment = "development" # "production" or "development"
Level = "info"
Outputs = ["stderr"]

[L1RPC]
URL = "http://localhost:8545"
Mode = "basic"
RetryMode = "backoff"
MaxRetries = 5

[BridgeServiceFinder]
RollupManagerAddr = "0x0000000000000000000000000000000000000000"
BlockFinality = "FinalizedBlock"
PollInterval = "30s"
BlockChunkSize = 10000
HealthCheckPath = "/health"
HealthCheckTimeout = "5s"
RequireAllHealthyOnStart = false

[REST]
Host = "0.0.0.0"
Port = 8080
ReadTimeout = "5m"
WriteTimeout = "5m"
MaxRequestsPerIPAndSecond = 10

[Tracker]
RetentionPeriod = "1m"
`

// Config holds the full configuration of the proxy component
type Config struct {
	// Log configures the logger
	Log log.Config `mapstructure:"Log"`

	// L1RPC is the L1 JSON-RPC endpoint used to enumerate the rollup manager's networks and to
	// poll the on-chain events the bridge service finder watches
	L1RPC ethermanconfig.RPCClientConfig `mapstructure:"L1RPC"`

	// BridgeServiceFinder configures the networkID -> bridge service URL / JSON-RPC resolver shared
	// by the proxy and tracker components
	BridgeServiceFinder bridgeservicefinder.Config `mapstructure:"BridgeServiceFinder"`

	// REST configures the shared HTTP server where every component of this binary registers
	// its routes (tracker REST/WS endpoints, proxy routes)
	REST aggkitcommon.RESTConfig `mapstructure:"REST"`

	// Tracker configures the bridge tracker component (only its file-borne fields; the
	// programmatic ones are wired by the binary, see cmd)
	Tracker bridgetracker.Config `mapstructure:"Tracker"`
}

// ValidateComponents validates that all provided components are known/supported by the proxy binary.
func ValidateComponents(components []string) error {
	validComponents := map[string]struct{}{
		PROXY:   {},
		TRACKER: {},
	}

	// build a sorted list of valid component names for error messages
	keys := make([]string, 0, len(validComponents))
	for k := range validComponents {
		keys = append(keys, k)
	}
	sort.Strings(keys) // ensures deterministic ordering
	validList := strings.Join(keys, ", ")

	for _, component := range components {
		if _, ok := validComponents[component]; !ok {
			return fmt.Errorf("unknown component: %s. Valid components are: %s", component, validList)
		}
	}

	return nil
}

// Load loads the configuration merging the default values with the
// config file(s) passed through the cli context (flag --cfg)
func Load(ctx *cli.Context) (*Config, error) {
	return LoadFiles(ctx.StringSlice(FlagCfg))
}

// SHA1 returns the sha1sum (hex) of the configuration the binary was started with: the
// concatenation of the config files in the order they were passed. It identifies the
// effective configuration of an instance (exposed by the tracker health endpoint), so
// instances behind a proxy can be checked to run the same configuration
func SHA1(configFiles []string) (string, error) {
	hasher := sha1.New() //nolint:gosec // not used for security: config fingerprint for the health endpoint
	for _, file := range configFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("error reading config file %s: %w", file, err)
		}
		hasher.Write(content)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// LoadFiles loads the configuration merging the default values with the given config files
func LoadFiles(configFiles []string) (*Config, error) {
	v := viper.New()
	v.SetConfigType(ConfigType)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.SetEnvPrefix(EnvVarPrefix)
	v.AutomaticEnv()

	if err := v.ReadConfig(bytes.NewBufferString(DefaultValues)); err != nil {
		return nil, fmt.Errorf("error reading default config: %w", err)
	}
	for _, file := range configFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("error reading config file %s: %w", file, err)
		}
		if err := v.MergeConfig(bytes.NewBuffer(content)); err != nil {
			return nil, fmt.Errorf("error merging config file %s: %w", file, err)
		}
	}

	cfg := &Config{}
	decodeHooks := []viper.DecoderConfigOption{
		// this allows arrays to be decoded from env var separated by ",", example: MY_VAR="value1,value2,value3"
		viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
			mapstructure.TextUnmarshallerHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		)),
	}
	if err := v.Unmarshal(cfg, decodeHooks...); err != nil {
		return nil, fmt.Errorf("error unmarshalling config: %w", err)
	}
	return cfg, nil
}
