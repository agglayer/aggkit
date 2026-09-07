// Package bridgelooptester implements a long-running soak-test tool that continuously moves value
// around circular bridge routes (L1<->L2, L2<->L1, L2<->L2) using ETH and a deployed ERC20, driving
// every observation through the aggkit-proxy REST API (/bridge/v1, /tracker/v1) and the JSON-RPC
// endpoint of each network. See DESIGN.md for the wire contract and hop state machine.
package bridgelooptester

import (
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"strings"
	"time"

	cfgtypes "github.com/agglayer/aggkit/config/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// AssetKind identifies which asset a Loop moves around its circular route.
type AssetKind string

const (
	// AssetETH selects the network's native currency (bridged via bridgeAsset with the zero token
	// address and msg.value, per DESIGN.md §6). Only origin networks whose gasTokenAddress() is the
	// zero address are supported for this asset kind.
	AssetETH AssetKind = "eth"
	// AssetERC20 selects a deployed ERC20 token (see CmdDeployToken / test/contracts/mintableerc20),
	// bridged via ordinary bridgeAsset + Approve semantics.
	AssetERC20 AssetKind = "erc20"
)

// ClaimMode identifies who is expected to claim a hop's bridged value on its destination network.
type ClaimMode string

const (
	// ClaimAuto means an autoclaim service is expected to claim the hop; the tool only waits and
	// asserts the claim happened (see DESIGN.md §4, state S4).
	ClaimAuto ClaimMode = "auto"
	// ClaimManual means the tool asserts nothing claims the hop during Global.ManualGracePeriod and
	// then submits claimAsset/claimMessage itself (see DESIGN.md §4, state S5submit).
	ClaimManual ClaimMode = "manual"
)

// WeiAmount is a non-negative, arbitrary-precision integer amount expressed in wei. It is always
// configured in TOML as a quoted decimal string (e.g. Amount = "1000000000000000000"), never as a
// bare TOML integer or float: wei-scale bridge amounts routinely exceed both float64's 53-bit
// exact-integer range and, for large token supplies, a plain int64's 63-bit range, so round-tripping
// through either risks silent precision loss. WeiAmount always decodes from text (via
// UnmarshalText) into an underlying big.Int and is never decoded through a numeric path. The zero
// value represents 0 wei and requires no explicit configuration.
type WeiAmount struct {
	big.Int
}

// NewWeiAmount builds a WeiAmount from an int64, for tests and programmatic Config construction
// (see (*Config).Validate doc comment on library-callable use).
func NewWeiAmount(v int64) WeiAmount {
	var w WeiAmount
	w.SetInt64(v)
	return w
}

// decimalBase is the only base WeiAmount.UnmarshalText ever parses with.
const decimalBase = 10

// UnmarshalText implements encoding.TextUnmarshaler, parsing a base-10 decimal string into the
// underlying big.Int. It shadows big.Int's own UnmarshalText (which auto-detects base from a
// prefix, e.g. treating a leading "0" as octal) so that every WeiAmount decodes as plain decimal,
// with no surprising base-detection behavior for zero-padded amounts. An empty string decodes to 0.
func (w *WeiAmount) UnmarshalText(text []byte) error {
	s := strings.TrimSpace(string(text))
	if s == "" {
		w.SetInt64(0)
		return nil
	}
	if _, ok := w.SetString(s, decimalBase); !ok {
		return fmt.Errorf("invalid wei amount %q: not a base-10 integer", s)
	}
	if w.Sign() < 0 {
		return fmt.Errorf("invalid wei amount %q: must not be negative", s)
	}
	return nil
}

// String returns the decimal representation of the amount.
func (w WeiAmount) String() string {
	return w.Int.String()
}

// IsPositive reports whether the amount is strictly greater than zero.
func (w WeiAmount) IsPositive() bool {
	return w.Sign() > 0
}

// BigInt returns a copy of the underlying *big.Int, so callers cannot mutate the WeiAmount's state
// through the returned pointer.
func (w WeiAmount) BigInt() *big.Int {
	return new(big.Int).Set(&w.Int)
}

// Global holds process-wide settings for the bridge loop tester, independent of any one network or
// loop.
type Global struct {
	// ProxyURL is the base URL of the aggkit-proxy REST API exposing /bridge/v1 and /tracker/v1
	// (required, e.g. "http://127.0.0.1:15601").
	ProxyURL string `mapstructure:"ProxyURL"`
	// LogLevel is the aggkit/log level (debug, info, warn, error, dpanic, panic, fatal). Defaults to
	// "info".
	LogLevel string `mapstructure:"LogLevel"`
	// Iterations bounds how many cycles of every enabled Loop the tool runs before exiting; 0 (the
	// default) means run forever.
	Iterations uint64 `mapstructure:"Iterations"`
	// LoopDelay is how long to sleep between successive cycles of a Loop. Defaults to 5s.
	LoopDelay cfgtypes.Duration `mapstructure:"LoopDelay"`
	// HopTimeout bounds how long a single hop's state machine (DESIGN.md §4) may take end-to-end
	// before it is reported as stuck. Defaults to 10m.
	HopTimeout cfgtypes.Duration `mapstructure:"HopTimeout"`
	// PollInterval is how often /bridge/v1/* readiness endpoints are re-polled while a hop is
	// waiting on cross-network indexing. Defaults to 5s.
	PollInterval cfgtypes.Duration `mapstructure:"PollInterval"`
	// ManualGracePeriod is, for Claim = "manual" hops, how long the tool waits and asserts nothing
	// else claims the hop before submitting the claim itself; for Claim = "auto" hops, how long the
	// tool waits for the autoclaim service to claim it before treating it as a policy violation.
	// Defaults to 2m.
	ManualGracePeriod cfgtypes.Duration `mapstructure:"ManualGracePeriod"`
	// StatePath is an optional file path used to persist/resume hop state across restarts. Empty
	// (the default) disables persistence.
	StatePath string `mapstructure:"StatePath"`
	// MetricsAddr is an optional "host:port" to serve aggkit/prometheus metrics on. Empty (the
	// default) disables the metrics server.
	MetricsAddr string `mapstructure:"MetricsAddr"`
	// DryRun, when true, logs the actions the tool would take without submitting any on-chain
	// transaction. Defaults to false.
	DryRun bool `mapstructure:"DryRun"`
}

// Network describes one network (L1 or an L2) the tool can bridge to/from.
type Network struct {
	// NetworkID is the aggkit network ID (0 for L1, 1..N for rollups/L2s). Required, must be unique
	// across Networks.
	NetworkID uint32 `mapstructure:"NetworkID"`
	// Name is a human-readable label used in logs and error messages. Required.
	Name string `mapstructure:"Name"`
	// RPCURL is the JSON-RPC endpoint of this network. Required.
	RPCURL string `mapstructure:"RPCURL"`
	// BridgeAddr is the PolygonZkEVMBridgeV2 (agglayerbridgel2 binding) contract address on this
	// network. Required, must not be the zero address.
	BridgeAddr common.Address `mapstructure:"BridgeAddr"`
	// ChainID is the EVM chain ID of this network. Optional: 0 (the default) means the tool resolves
	// it live via eth_chainId at startup instead of trusting a possibly-stale configured value.
	ChainID uint64 `mapstructure:"ChainID"`
	// MinNativeReserve is the minimum native-currency balance (wei) the signer on this network must
	// keep in reserve for gas; the tool refuses to spend below it. Defaults to 0 (no reserve).
	MinNativeReserve WeiAmount `mapstructure:"MinNativeReserve"`
	// GasOffset is added to every gas estimate made against this network's RPC before submitting a
	// transaction, as a safety margin. Defaults to 0 (no offset).
	GasOffset WeiAmount `mapstructure:"GasOffset"`
	// Signer is the key used to sign transactions submitted on this network (local keystore, AWS
	// KMS, or GCP KMS - see github.com/agglayer/go_signer/signer/types). Required.
	Signer signertypes.SignerConfig `mapstructure:"Signer"`
}

// Hop is one leg of a Loop's circular route: value moves from Source to Destination.
type Hop struct {
	// Source is the NetworkID the bridge transaction is submitted on. Required, must match a
	// configured Network.
	Source uint32 `mapstructure:"Source"`
	// Destination is the NetworkID the bridged value is claimed on. Required, must match a
	// configured Network.
	Destination uint32 `mapstructure:"Destination"`
	// Claim selects who is expected to claim this hop: ClaimAuto or ClaimManual. Required.
	Claim ClaimMode `mapstructure:"Claim"`
}

// Loop describes one circular route the tool repeatedly drives value around.
type Loop struct {
	// Name is a human-readable label used in logs, metrics, and error messages. Required.
	Name string `mapstructure:"Name"`
	// Asset selects what is bridged around this Loop's route: AssetETH or AssetERC20. Required.
	Asset AssetKind `mapstructure:"Asset"`
	// Amount is how much (wei-scale) is bridged on every hop of every cycle. Required, must be > 0.
	Amount WeiAmount `mapstructure:"Amount"`
	// Enabled toggles whether this Loop is driven by CmdRun. There is no implicit default: a Loop
	// with Enabled unset decodes to false (Go's zero value for bool) and is skipped, so every Loop
	// intended to run must set Enabled = true explicitly.
	Enabled bool `mapstructure:"Enabled"`
	// TokenOriginNetwork is the NetworkID the ERC20 token was originally deployed/minted on (see
	// CmdDeployToken). Required when Asset == AssetERC20 (used to compute wrapped-token addresses on
	// the other networks via ComputeTokenProxyAddress); must be left unset for AssetETH loops.
	TokenOriginNetwork *uint32 `mapstructure:"TokenOriginNetwork"`
	// Hops is the ordered list of legs making up this Loop's circular route. Required, must contain
	// at least 2 hops, must be contiguous (Hops[i].Destination == Hops[i+1].Source), and must close
	// (the last hop's Destination == the first hop's Source).
	Hops []Hop `mapstructure:"Hops"`
}

// Config is the root, standalone configuration for the bridge_loop_tester tool. It is not derived
// from, and does not reuse, the main aggkit binary's config template/defaults pipeline.
//
// Config and every nested type is exported with exported fields so it can be constructed
// programmatically (e.g. by an e2e test targeting the anvil-2chains env) without loading a file:
// build a *Config by hand and call (*Config).Validate() directly. LoadConfig (file -> *Config) is
// strictly a convenience on top of that: it never mutates global state, never calls os.Exit, and
// never validates on the caller's behalf.
type Config struct {
	// Global holds process-wide settings.
	Global Global `mapstructure:"Global"`
	// Networks lists every network the tool may bridge to/from.
	Networks []Network `mapstructure:"Networks"`
	// Loops lists the circular routes the tool drives.
	Loops []Loop `mapstructure:"Loops"`

	// warnings collects non-fatal problems found by the most recent call to Validate. Unexported:
	// not part of the wire schema, read back via Warnings().
	warnings []string
}

// Default values applied by LoadConfig for Global fields left unset in the TOML source. These are
// not applied by (*Config).Validate: a programmatically-built Config that leaves a duration at its
// Go zero value fails validation rather than silently picking up a hidden default, so validation
// behaves identically regardless of how the Config was constructed.
const (
	defaultLogLevel          = "info"
	defaultLoopDelay         = 5 * time.Second
	defaultHopTimeout        = 10 * time.Minute
	defaultPollInterval      = 5 * time.Second
	defaultManualGracePeriod = 2 * time.Minute
)

// mainnetNetworkID is the aggkit network ID reserved for L1 (see DESIGN.md §1/§6).
const mainnetNetworkID uint32 = 0

// LoadConfig reads and merges the TOML config file(s) at paths (in order; later files override
// earlier ones for keys they both set) and unmarshals them into a Config. It applies Global
// defaults for fields left unset (see the defaultXxx constants) but performs no validation itself -
// call (*Config).Validate() on the result. LoadConfig touches only the given paths; it does not
// read environment variables, other files, or any aggkit-wide config template.
func LoadConfig(paths ...string) (*Config, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("load config: at least one config file path is required")
	}

	v := viper.New()
	v.SetConfigType("toml")

	for i, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("load config: read %s: %w", path, err)
		}

		reader := strings.NewReader(string(content))
		if i == 0 {
			if err := v.ReadConfig(reader); err != nil {
				return nil, fmt.Errorf("load config: parse %s: %w", path, err)
			}
		} else if err := v.MergeConfig(reader); err != nil {
			return nil, fmt.Errorf("load config: merge %s: %w", path, err)
		}
	}

	applyDefaults(v)

	var cfg Config
	decodeHook := viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.TextUnmarshallerHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))
	if err := v.Unmarshal(&cfg, decodeHook); err != nil {
		return nil, fmt.Errorf("load config: unmarshal: %w", err)
	}

	return &cfg, nil
}

// applyDefaults registers viper defaults for every Global field that has a non-zero default,
// so a TOML source that omits them still unmarshals to a usable value.
func applyDefaults(v *viper.Viper) {
	v.SetDefault("Global.LogLevel", defaultLogLevel)
	v.SetDefault("Global.LoopDelay", defaultLoopDelay.String())
	v.SetDefault("Global.HopTimeout", defaultHopTimeout.String())
	v.SetDefault("Global.PollInterval", defaultPollInterval.String())
	v.SetDefault("Global.ManualGracePeriod", defaultManualGracePeriod.String())
}

// Validate checks every field and cross-field invariant of Config, aggregating every problem it
// finds (rather than stopping at the first) via errors.Join, so a single run reports every fix
// needed instead of one at a time. It performs no network or filesystem I/O: it is safe to call on
// a Config built entirely in Go, e.g. by a test targeting a live env - see the Config doc comment.
//
// Non-fatal issues (currently: a multi-network config whose hops don't exercise all three bridge
// directions) are reported separately by Warnings and never cause Validate to return an error.
func (c *Config) Validate() error {
	c.warnings = nil
	var errs []error

	errs = append(errs, c.validateGlobal()...)

	networksByID, networkErrs := c.validateNetworks()
	errs = append(errs, networkErrs...)

	errs = append(errs, c.validateLoops(networksByID)...)

	return errors.Join(errs...)
}

func (c *Config) validateGlobal() []error {
	var errs []error

	if c.Global.ProxyURL == "" {
		errs = append(errs, fmt.Errorf("Global.ProxyURL is required"))
	} else if u, err := url.Parse(c.Global.ProxyURL); err != nil {
		errs = append(errs, fmt.Errorf("Global.ProxyURL %q does not parse as a URL: %w", c.Global.ProxyURL, err))
	} else if u.Scheme == "" || u.Host == "" {
		errs = append(errs, fmt.Errorf("Global.ProxyURL %q must be an absolute URL with a scheme and host",
			c.Global.ProxyURL))
	}

	type namedDuration struct {
		name string
		d    time.Duration
	}
	for _, nd := range []namedDuration{
		{"Global.LoopDelay", c.Global.LoopDelay.Duration},
		{"Global.HopTimeout", c.Global.HopTimeout.Duration},
		{"Global.PollInterval", c.Global.PollInterval.Duration},
		{"Global.ManualGracePeriod", c.Global.ManualGracePeriod.Duration},
	} {
		if nd.d <= 0 {
			errs = append(errs, fmt.Errorf("%s must be greater than 0, got %s", nd.name, nd.d))
		}
	}

	return errs
}

func (c *Config) validateNetworks() (map[uint32]Network, []error) {
	var errs []error
	networksByID := make(map[uint32]Network, len(c.Networks))

	if len(c.Networks) == 0 {
		errs = append(errs, fmt.Errorf("networks: at least one network is required"))
	}

	for i, n := range c.Networks {
		if _, dup := networksByID[n.NetworkID]; dup {
			errs = append(errs, fmt.Errorf("networks[%d] (%s): duplicate NetworkID %d", i, n.Name, n.NetworkID))
			continue
		}
		networksByID[n.NetworkID] = n

		if n.Name == "" {
			errs = append(errs, fmt.Errorf("networks[%d] (NetworkID %d): Name is required", i, n.NetworkID))
		}
		if n.RPCURL == "" {
			errs = append(errs, fmt.Errorf("networks[%d] (%s): RPCURL is required", i, n.Name))
		}
		if n.BridgeAddr == (common.Address{}) {
			errs = append(errs, fmt.Errorf("networks[%d] (%s): BridgeAddr is required and must not be the zero "+
				"address", i, n.Name))
		}
		if n.Signer.Method == "" {
			errs = append(errs, fmt.Errorf("networks[%d] (%s): Signer.Method is required", i, n.Name))
		}
	}

	return networksByID, errs
}

func (c *Config) validateLoops(networksByID map[uint32]Network) []error {
	var errs []error
	seenDirections := map[string]bool{}

	for i, loop := range c.Loops {
		label := loop.Name
		if label == "" {
			label = fmt.Sprintf("#%d", i)
		}

		errs = append(errs, validateLoopFields(label, loop)...)
		errs = append(errs, validateLoopHops(label, loop, networksByID, seenDirections)...)
	}

	if len(networksByID) > 2 { //nolint:mnd // "more than two networks" is the plan's own threshold, not a magic number
		for _, want := range []string{directionL1toL2, directionL2toL1, directionL2toL2} {
			if !seenDirections[want] {
				c.warnings = append(c.warnings, fmt.Sprintf(
					"config declares %d networks but no configured hop covers direction %s "+
						"(expected coverage of L1->L2, L2->L1 and L2->L2 with more than two networks)",
					len(networksByID), want))
			}
		}
	}

	return errs
}

func validateLoopFields(label string, loop Loop) []error {
	var errs []error

	if loop.Name == "" {
		errs = append(errs, fmt.Errorf("loops[%s]: Name is required", label))
	}

	switch loop.Asset {
	case AssetETH:
		if loop.TokenOriginNetwork != nil {
			errs = append(errs, fmt.Errorf(
				"loops[%s]: TokenOriginNetwork must not be set for Asset = %q", label, AssetETH))
		}
	case AssetERC20:
		if loop.TokenOriginNetwork == nil {
			errs = append(errs, fmt.Errorf(
				"loops[%s]: TokenOriginNetwork is required for Asset = %q", label, AssetERC20))
		}
	default:
		errs = append(errs, fmt.Errorf(
			"loops[%s]: Asset must be %q or %q, got %q", label, AssetETH, AssetERC20, loop.Asset))
	}

	if !loop.Amount.IsPositive() {
		errs = append(errs, fmt.Errorf("loops[%s]: Amount must be greater than 0, got %s", label, loop.Amount))
	}

	return errs
}

const (
	directionL1toL2 = "L1->L2"
	directionL2toL1 = "L2->L1"
	directionL2toL2 = "L2->L2"
	minHopsPerLoop  = 2
)

func validateLoopHops(
	label string, loop Loop, networksByID map[uint32]Network, seenDirections map[string]bool,
) []error {
	var errs []error

	if len(loop.Hops) < minHopsPerLoop {
		errs = append(errs, fmt.Errorf(
			"loops[%s]: Hops must contain at least %d hops to form a circular route, got %d",
			label, minHopsPerLoop, len(loop.Hops)))
		return errs
	}

	if loop.Asset == AssetERC20 && loop.TokenOriginNetwork != nil {
		if _, ok := networksByID[*loop.TokenOriginNetwork]; !ok {
			errs = append(errs, fmt.Errorf(
				"loops[%s]: TokenOriginNetwork %d has no matching [[Networks]] entry",
				label, *loop.TokenOriginNetwork))
		}
	}

	for hopIdx, hop := range loop.Hops {
		hopLabel := fmt.Sprintf("%s.Hops[%d]", label, hopIdx)

		if _, ok := networksByID[hop.Source]; !ok {
			errs = append(errs, fmt.Errorf("%s: Source %d has no matching [[Networks]] entry", hopLabel, hop.Source))
		}
		if _, ok := networksByID[hop.Destination]; !ok {
			errs = append(errs, fmt.Errorf("%s: Destination %d has no matching [[Networks]] entry",
				hopLabel, hop.Destination))
		}
		if hop.Source == hop.Destination {
			errs = append(errs, fmt.Errorf("%s: Source and Destination must differ, both are %d",
				hopLabel, hop.Source))
		}
		switch hop.Claim {
		case ClaimAuto, ClaimManual:
		default:
			errs = append(errs, fmt.Errorf("%s: Claim must be %q or %q, got %q",
				hopLabel, ClaimAuto, ClaimManual, hop.Claim))
		}

		if hopIdx > 0 {
			prev := loop.Hops[hopIdx-1]
			if prev.Destination != hop.Source {
				errs = append(errs, fmt.Errorf(
					"loops[%s]: Hops[%d].Destination (%d) must equal Hops[%d].Source (%d): hop chain is not "+
						"contiguous", label, hopIdx-1, prev.Destination, hopIdx, hop.Source))
			}
		}

		seenDirections[hopDirection(hop.Source, hop.Destination)] = true
	}

	first, last := loop.Hops[0], loop.Hops[len(loop.Hops)-1]
	if last.Destination != first.Source {
		errs = append(errs, fmt.Errorf(
			"loops[%s]: route is not closed: last hop's Destination (%d) must equal first hop's Source (%d)",
			label, last.Destination, first.Source))
	}

	return errs
}

// hopDirection classifies a hop by whether its Source/Destination is L1 (network 0) or an L2, for
// the "covers all three bridge directions" warning (see validateLoops).
func hopDirection(source, destination uint32) string {
	switch {
	case source == mainnetNetworkID && destination != mainnetNetworkID:
		return directionL1toL2
	case source != mainnetNetworkID && destination == mainnetNetworkID:
		return directionL2toL1
	default:
		return directionL2toL2
	}
}

// Warnings returns non-fatal problems found by the most recent call to Validate (e.g. a
// multi-network config whose hops don't exercise all three bridge directions). It returns nil until
// Validate has been called at least once, and is reset on every call to Validate.
func (c *Config) Warnings() []string {
	return c.warnings
}
