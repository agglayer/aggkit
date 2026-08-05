package config

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // not used for security: config fingerprint for the health endpoint
	"encoding/hex"
	"encoding/json"
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

// SHA1 returns the sha1sum (hex) of the effective configuration cfg was decoded into: config
// file(s), defaults, and any CDK_PROXY_* environment overrides. It identifies the effective
// configuration of an instance (exposed by the tracker health endpoint), so instances behind a
// proxy can be checked to run the same configuration; hashing only the input files would miss
// environment overrides and let differently-behaving instances report the same fingerprint.
func SHA1(cfg *Config) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("error marshalling config for fingerprint: %w", err)
	}
	sum := sha1.Sum(data) //nolint:gosec // not used for security: config fingerprint for the health endpoint
	return hex.EncodeToString(sum[:]), nil
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
