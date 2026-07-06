package exit_certificate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/agglayer/aggkit/agglayer"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	aggkittypes "github.com/agglayer/aggkit/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/pelletier/go-toml/v2"
)

// Options holds tuning parameters for RPC parallelism and output.
type Options struct {
	BlockRange int `json:"blockRange"`
	// StepAWindowSize is the number of blocks loaded into memory at once during Step A
	// (address collection via debug_traceTransaction). Defaults to 150000, independently of BlockRange.
	// Tune independently when trace calls need a different chunk size than log queries.
	StepAWindowSize  int    `json:"stepAWindowSize"`
	ConcurrencyLimit int    `json:"concurrencyLimit"`
	RPCBatchSize     int    `json:"rpcBatchSize"`
	RPCDelayMs       int    `json:"rpcDelayMs"`
	OutputDir        string `json:"outputDir"`
	L1StartBlock     uint64 `json:"l1StartBlock"`
	L2StartBlock     uint64 `json:"l2StartBlock"`
	AgglayerAdminURL string `json:"agglayerAdminURL"`
	// AgglayerAdminToken is an optional Bearer token for authenticating requests to agglayerAdminURL.
	// Required when the admin endpoint is protected by Google Cloud IAP.
	// Obtain it with: gcloud auth print-identity-token --impersonate-service-account=<SA>
	// --audiences=<AUDIENCE> --include-email
	AgglayerAdminToken string                `json:"agglayerAdminToken"`
	AgglayerClient     agglayer.ClientConfig `json:"agglayerClient"`
	// UseAgglayerAdminToStepFCheck, when true (the default), runs Step F: it queries the agglayer
	// admin API (admin_getTokenBalance) and verifies the per-token balances against the certificate
	// and LBT. When false, Step F is skipped entirely (no agglayer admin query, no balance check).
	UseAgglayerAdminToStepFCheck bool `json:"useAgglayerAdminToStepFCheck"`
	// IgnoreGenesisBalance, when true, suppresses the abort that fires when any EOA or contract has a
	// non-zero ETH balance at block 0 (a genesis preload that would inflate the exit certificate
	// totals): the check still runs and warns, but the run continues. Defaults to false (abort); set
	// to true only for Kurtosis or test environments.
	IgnoreGenesisBalance bool `json:"ignoreGenesisBalance"`
	// IgnoreOnTraceError skips transactions whose debug_traceTransaction call fails instead of
	// aborting Step A. Failed tx hashes are saved to step-a-failed-traces.json for review.
	IgnoreOnTraceError bool `json:"ignoreOnTraceError"`
	// NativeSCLockedFromContracts, when true (the default), computes the native-token SC-locked value
	// in Step C from the actual ETH balances held by contract accounts (summed, excluding the L2
	// bridge) rather than from LBT − EOA_accumulated. That formula underflows on chains with a native
	// genesis premint, clamping to 0 and silently dropping contract-held ETH. Set to false to fall
	// back to the LBT − EOA derivation for the native token.
	NativeSCLockedFromContracts bool `json:"nativeSCLockedFromContracts"`
	// IgnoreBalanceMismatch suppresses the error returned by Step F when token balances
	// do not match. Set to true only when investigating discrepancies without blocking the pipeline.
	IgnoreBalanceMismatch bool `json:"ignoreBalanceMismatch"`
	// IgnoreUnclaimed skips adding unclaimed L1→L2 deposits to the certificate in Step E.
	// The step still detects and warns about any unclaimed deposits, but the certificate is left unchanged.
	IgnoreUnclaimed bool `json:"ignoreUnclaimed"`
	// ExtraERC20Contracts is an optional list of ERC-20 contract addresses whose token holders
	// are decomposed in Step B3. Each contract is queried with balanceOf for every EOA address
	// collected in Step A.
	ExtraERC20Contracts []common.Address `json:"extraErc20Contracts,omitempty"`
	// BridgeServiceURL is the base URL of the bridge service REST API.
	// When set, Step E queries the bridge service for pending bridges targeting this L2 and returns an
	// error if any unclaimed deposits are found.
	// Aggkit example:  "http://127.0.0.1:32970"
	// zkevm example:   "http://127.0.0.1:33019"
	BridgeServiceURL string `json:"bridgeServiceURL"`
	// BridgeServiceType selects the bridge service API flavour: "aggkit" (default) or "zkevm".
	BridgeServiceType string `json:"bridgeServiceType"`
	// IgnoreUnsupportedL2Events, when true, makes the Step G lite syncer log a warning
	// and continue instead of aborting when it sees an L2 event that would invalidate a
	// BridgeEvent-only reconstruction (SetSovereignTokenAddress, MigrateLegacyToken,
	// RemoveLegacySovereignTokenAddress, BackwardLET, ForwardLET). The computed NewLocalExitRoot may
	// then be incorrect; enable only to inspect such a chain knowingly. Defaults to false.
	IgnoreUnsupportedL2Events bool `json:"ignoreUnsupportedL2Events"`
	// VerifyNewLocalExitRootUsingShadowFork, when true (the default), makes Step G2 spin up the Anvil
	// shadow-fork, replay every bridge exit against the real bridge contract, and verify the computed
	// NewLocalExitRoot against the contract's getRoot(). When false, Step G2 computes the
	// NewLocalExitRoot purely off-chain from the lite exit tree (Step G1's genesis→fork bridges plus
	// the certificate's bridge exits) without launching Anvil — much faster, but it trusts the
	// off-chain leaf encoding (notably each exit's metadata) rather than verifying it on-chain.
	VerifyNewLocalExitRootUsingShadowFork bool `json:"verifyNewLocalExitRootUsingShadowFork"`
	// CapMode selects how bridge exits are trimmed when Step F caps a certificate whose token totals
	// exceed the allowed budget (only reached with IgnoreBalanceMismatch=true). "amount" (the default)
	// allocates each token's budget to its smallest-amount exits first, so the largest holders are the
	// first to be capped/dropped once the budget runs out. "appearance" allocates to its exits in the
	// order they appear, capping/dropping the ones that no longer fit. In both modes the surviving
	// exits are emitted in their original order.
	CapMode string `json:"capMode"`
	// GenesisPrefundETHWei is an optional amount of native token (in Wei, as a decimal string) that was
	// pre-funded at genesis. Those funds sit in accounts — and therefore in the certificate's bridge
	// exits — without a matching agglayer deposit, so Step F subtracts this value from the native-token
	// certificate sum before comparing it against the agglayer balance and the LBT (which only count
	// genuinely bridged funds), logging the certificate total, the pre-fund and the difference. The
	// pre-fund has no agglayer collateral and can never be bridged out: even when the checks match,
	// Step F produces a capped certificate trimming the native exits to min(agglayer, LBT). The Step 0
	// LBT and Step C SC-locked totals are untouched. Step B verifies the declared value against the
	// detected genesis ETH preload total. Empty means 0. Typical testnet value:
	// 100000 ETH = "100000000000000000000000".
	GenesisPrefundETHWei string `json:"genesisPrefundETHWei"`
}

// Cap modes for Options.CapMode (how Step F trims exits when capping a certificate).
const (
	// CapModeByAppearance allocates each token's cap budget to its exits in appearance order.
	CapModeByAppearance = "appearance"
	// CapModeByAmount allocates each token's cap budget to its smallest-amount exits first, so the
	// largest-amount exits are the first to be capped/dropped.
	CapModeByAmount = "amount"
)

// Config holds all parameters required by the exit certificate tool.
type Config struct {
	L2RPCURL            string                          `json:"l2RpcUrl"`
	L1RPCURL            string                          `json:"l1RpcUrl"`
	L2BridgeAddress     common.Address                  `json:"l2BridgeAddress"`
	L1BridgeAddress     common.Address                  `json:"l1BridgeAddress"`
	L2NetworkID         uint32                          `json:"l2NetworkId"`
	TargetBlock         aggkittypes.BlockNumberFinality `json:"targetBlock"`
	ExitAddress         common.Address                  `json:"exitAddress"`
	DestinationNetwork  uint32                          `json:"destinationNetwork"`
	SovereignRollupAddr common.Address                  `json:"sovereignRollupAddr"`
	// L1GlobalExitRootAddress is the address of the PolygonZkEVMGlobalExitRootV2 contract on L1.
	// Required for Step I to fetch the L1InfoTreeLeafCount from UpdateL1InfoTreeV2 events.
	L1GlobalExitRootAddress common.Address `json:"l1GlobalExitRootAddress"`
	// RollupManagerAddress is the optional address of the PolygonRollupManager (AgglayerManager)
	// contract on L1. Used by Step WAIT to confirm the certificate was settled on L1 by scanning for
	// the VerifyBatchesTrustedAggregator event matching the rollupID and the certificate's exit root.
	// When unset it is resolved on-chain from SovereignRollupAddr.rollupManager() (PolygonConsensusBase).
	RollupManagerAddress common.Address           `json:"rollupManagerAddress"`
	Options              Options                  `json:"options"`
	SignerConfig         signertypes.SignerConfig `json:"-"`

	// ConfigPath is the path the config was loaded from, and ConfigSHA256 is the
	// hex sha256 of the exact on-disk bytes that produced this Config. Both are set
	// by LoadConfig and used by the startup traceability banner. Not serialized.
	ConfigPath   string `json:"-"`
	ConfigSHA256 string `json:"-"`
}

const (
	defaultBlockRange       = 5000
	defaultStepAWindowSize  = 150000
	defaultConcurrencyLimit = 20
	defaultRPCBatchSize     = 200
)

var defaultOptions = Options{
	BlockRange:                            defaultBlockRange,
	StepAWindowSize:                       defaultStepAWindowSize,
	ConcurrencyLimit:                      defaultConcurrencyLimit,
	RPCBatchSize:                          defaultRPCBatchSize,
	RPCDelayMs:                            0,
	OutputDir:                             "output",
	L1StartBlock:                          0,
	L2StartBlock:                          0,
	UseAgglayerAdminToStepFCheck:          true,
	VerifyNewLocalExitRootUsingShadowFork: true,
	CapMode:                               CapModeByAmount,
	NativeSCLockedFromContracts:           true,
	// IgnoreGenesisBalance defaults to false (do abort on a genesis preload).
}

// LoadConfig reads and validates the config file. The format is selected by file extension:
// ".toml" is parsed as TOML, anything else (".json" or no extension) as JSON.
func LoadConfig(configPath string) (*Config, error) {
	raw, rawBytes, err := readRawConfig(configPath)
	if err != nil {
		return nil, err
	}
	if err := validateRawConfig(raw); err != nil {
		return nil, err
	}
	cfg, err := buildConfig(raw, filepath.Dir(configPath))
	if err != nil {
		return nil, err
	}
	cfg.ConfigPath = configPath
	cfg.ConfigSHA256 = fmt.Sprintf("%x", sha256.Sum256(rawBytes))
	return cfg, nil
}

// readRawConfig reads the config file at configPath, normalizing TOML to JSON so a single code path
// handles both formats (including the signerConfig json.RawMessage and agglayerClient custom JSON
// unmarshalling), then unmarshals it into a rawConfig. It also returns the original on-disk bytes
// (before any TOML→JSON normalization) so callers can hash the exact file content that was loaded.
func readRawConfig(configPath string) (*rawConfig, []byte, error) {
	rawBytes, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read config file %s: %w", configPath, err)
	}

	data := rawBytes
	if strings.EqualFold(filepath.Ext(configPath), ".toml") {
		data, err = tomlToJSON(rawBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("parse config TOML %s: %w", configPath, err)
		}
	}

	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("parse config JSON: %w", err)
	}
	return &raw, rawBytes, nil
}

// validateRawConfig checks the required parameters and the exitAddress format/value.
func validateRawConfig(raw *rawConfig) error {
	if raw.L2RPCURL == "" {
		return fmt.Errorf("missing required parameter: l2RpcUrl")
	}
	if raw.L2BridgeAddress == "" {
		return fmt.Errorf("missing required parameter: l2BridgeAddress")
	}
	if raw.ExitAddress == "" {
		return fmt.Errorf("missing required parameter: exitAddress")
	}
	// Validate the hex format explicitly: common.HexToAddress silently returns the zero address on
	// any malformed input, so without this check a typo would surface as the (misleading) zero-address
	// error below instead of pointing at the real problem.
	if !common.IsHexAddress(raw.ExitAddress) {
		return fmt.Errorf("invalid exitAddress %q: not a valid hex address", raw.ExitAddress)
	}
	if common.HexToAddress(raw.ExitAddress) == (common.Address{}) {
		return fmt.Errorf("invalid exitAddress: the zero address (0x00...00) is not allowed; " +
			"set an address whose private key you control so the SC-locked funds can be recovered")
	}
	// capMode, when set, must be one of the known modes.
	if raw.Options != nil && raw.Options.CapMode != "" &&
		raw.Options.CapMode != CapModeByAppearance && raw.Options.CapMode != CapModeByAmount {
		return fmt.Errorf("invalid options.capMode %q: must be %q or %q",
			raw.Options.CapMode, CapModeByAppearance, CapModeByAmount)
	}
	// genesisPrefundETHWei, when set, must be a non-negative base-10 integer (Wei).
	if raw.Options != nil && raw.Options.GenesisPrefundETHWei != "" {
		v, ok := new(big.Int).SetString(raw.Options.GenesisPrefundETHWei, decimalBase)
		if !ok {
			return fmt.Errorf("invalid options.genesisPrefundETHWei %q: must be a base-10 integer amount in Wei",
				raw.Options.GenesisPrefundETHWei)
		}
		if v.Sign() < 0 {
			return fmt.Errorf("invalid options.genesisPrefundETHWei %q: must not be negative",
				raw.Options.GenesisPrefundETHWei)
		}
	}
	// Step F (the agglayer admin balance check) needs agglayerAdminURL. When the check is enabled
	// (useAgglayerAdminToStepFCheck, default true), the URL must be set; otherwise set the flag to
	// false to skip Step F entirely.
	if useAgglayerAdminToStepFCheckEnabled(raw.Options) &&
		(raw.Options == nil || raw.Options.AgglayerAdminURL == "") {
		return fmt.Errorf("options.agglayerAdminURL is required when options.useAgglayerAdminToStepFCheck " +
			"is true (the default); set agglayerAdminURL, or set useAgglayerAdminToStepFCheck=false to skip Step F")
	}
	return nil
}

// useAgglayerAdminToStepFCheckEnabled reports the effective value of
// options.useAgglayerAdminToStepFCheck, mirroring the default applied by mergeOptions: it is true
// when the option is absent (nil rawOpts or unset tri-state flag) and otherwise takes the explicit value.
func useAgglayerAdminToStepFCheckEnabled(raw *rawOpts) bool {
	if raw == nil || raw.UseAgglayerAdminToStepFCheck == nil {
		return defaultOptions.UseAgglayerAdminToStepFCheck
	}
	return *raw.UseAgglayerAdminToStepFCheck
}

// buildConfig assembles a *Config from an already-validated rawConfig, applying defaults
// (l1BridgeAddress, l2NetworkId) and parsing the targetBlock, options and signerConfig.
func buildConfig(raw *rawConfig, configDir string) (*Config, error) {
	targetBlock, err := parseTargetBlock(raw.TargetBlock)
	if err != nil {
		return nil, fmt.Errorf("invalid targetBlock %q: %w", raw.TargetBlock, err)
	}

	cfg := &Config{
		L2RPCURL:                raw.L2RPCURL,
		L1RPCURL:                raw.L1RPCURL,
		L2BridgeAddress:         common.HexToAddress(raw.L2BridgeAddress),
		L2NetworkID:             raw.L2NetworkID,
		ExitAddress:             common.HexToAddress(raw.ExitAddress),
		DestinationNetwork:      raw.DestinationNetwork,
		TargetBlock:             targetBlock,
		SovereignRollupAddr:     common.HexToAddress(raw.SovereignRollupAddr),
		L1GlobalExitRootAddress: common.HexToAddress(raw.L1GlobalExitRootAddress),
		RollupManagerAddress:    common.HexToAddress(raw.RollupManagerAddress),
	}

	if raw.L1BridgeAddress != "" {
		cfg.L1BridgeAddress = common.HexToAddress(raw.L1BridgeAddress)
	} else {
		cfg.L1BridgeAddress = cfg.L2BridgeAddress
	}

	if cfg.L2NetworkID == 0 {
		cfg.L2NetworkID = 1
	}

	cfg.Options = mergeOptions(raw.Options, configDir)
	if len(raw.SignerConfig) > 0 {
		signerCfg, err := parseSignerConfig(raw.SignerConfig, configDir)
		if err != nil {
			return nil, fmt.Errorf("parse signerConfig: %w", err)
		}
		cfg.SignerConfig = signerCfg
	}

	return cfg, nil
}

// tomlToJSON decodes TOML into a generic map and re-encodes it as JSON, so the existing JSON
// unmarshalling (rawConfig) can handle both formats from one code path.
func tomlToJSON(data []byte) ([]byte, error) {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal TOML: %w", err)
	}
	out, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("re-encode config as JSON: %w", err)
	}
	return out, nil
}

// parseTargetBlock converts the raw JSON string to a BlockNumberFinality.
// An empty value resolves to LatestBlock; any other invalid value returns an error.
func parseTargetBlock(s string) (aggkittypes.BlockNumberFinality, error) {
	if s == "" {
		return aggkittypes.LatestBlock, nil
	}
	tb, err := aggkittypes.NewBlockNumberFinality(s)
	if err != nil {
		return aggkittypes.LatestBlock, err
	}
	return *tb, nil
}

// parseSignerConfig converts the flat JSON signer config into a SignerConfig.
// The JSON format mirrors the TOML used by aggsender:
//
//	{ "Method": "local", "Path": "keystore.json", "Password": "pass" }
func parseSignerConfig(data json.RawMessage, configDir string) (signertypes.SignerConfig, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return signertypes.SignerConfig{}, fmt.Errorf("unmarshal signer config: %w", err)
	}
	method, _ := raw["Method"].(string)

	// The go_signer library looks up config keys in lowercase (e.g. "path", "password").
	// Normalize all non-Method keys to lowercase so JSON with "Path"/"Password" works.
	cfg := make(map[string]any, len(raw))
	for k, v := range raw {
		if k == "Method" {
			continue
		}
		key := strings.ToLower(k)
		if key == "path" {
			if s, ok := v.(string); ok {
				v = resolvePath(configDir, s)
			}
		}
		cfg[key] = v
	}
	return signertypes.SignerConfig{
		Method: signertypes.SignMethod(method),
		Config: cfg,
	}, nil
}

func resolvePath(baseDir, path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}

func mergeOptions(raw *rawOpts, configDir string) Options {
	opts := defaultOptions
	if raw == nil {
		return opts
	}
	mergeScalarOptions(&opts, raw, configDir)
	mergeFlagOptions(&opts, raw)
	if raw.AgglayerClient != nil {
		opts.AgglayerClient = mergeAgglayerClient(raw.AgglayerClient)
	}
	return opts
}

// mergeScalarOptions overrides the non-boolean option fields with any non-zero raw values.
func mergeScalarOptions(opts *Options, raw *rawOpts, configDir string) {
	if raw.BlockRange > 0 {
		opts.BlockRange = raw.BlockRange
	}
	if raw.StepAWindowSize > 0 {
		opts.StepAWindowSize = raw.StepAWindowSize
	}
	if raw.ConcurrencyLimit > 0 {
		opts.ConcurrencyLimit = raw.ConcurrencyLimit
	}
	if raw.RPCBatchSize > 0 {
		opts.RPCBatchSize = raw.RPCBatchSize
	}
	if raw.RPCDelayMs > 0 {
		opts.RPCDelayMs = raw.RPCDelayMs
	}
	if raw.OutputDir != "" {
		opts.OutputDir = resolvePath(configDir, raw.OutputDir)
	}
	if raw.L1StartBlock > 0 {
		opts.L1StartBlock = raw.L1StartBlock
	}
	if raw.L2StartBlock > 0 {
		opts.L2StartBlock = raw.L2StartBlock
	}
	if raw.AgglayerAdminURL != "" {
		opts.AgglayerAdminURL = raw.AgglayerAdminURL
	}
	if raw.AgglayerAdminToken != "" {
		opts.AgglayerAdminToken = raw.AgglayerAdminToken
	}
	if len(raw.ExtraERC20Contracts) > 0 {
		addrs := make([]common.Address, 0, len(raw.ExtraERC20Contracts))
		for _, s := range raw.ExtraERC20Contracts {
			addrs = append(addrs, common.HexToAddress(s))
		}
		opts.ExtraERC20Contracts = addrs
	}
	if raw.BridgeServiceURL != "" {
		opts.BridgeServiceURL = raw.BridgeServiceURL
	}
	if raw.BridgeServiceType != "" {
		opts.BridgeServiceType = raw.BridgeServiceType
	}
	if raw.CapMode != "" {
		opts.CapMode = raw.CapMode
	}
	if raw.GenesisPrefundETHWei != "" {
		opts.GenesisPrefundETHWei = raw.GenesisPrefundETHWei
	}
}

// mergeFlagOptions overrides the boolean (tri-state *bool) option flags that were explicitly set.
func mergeFlagOptions(opts *Options, raw *rawOpts) {
	if raw.UseAgglayerAdminToStepFCheck != nil {
		opts.UseAgglayerAdminToStepFCheck = *raw.UseAgglayerAdminToStepFCheck
	}
	if raw.IgnoreGenesisBalance != nil {
		opts.IgnoreGenesisBalance = *raw.IgnoreGenesisBalance
	}
	if raw.IgnoreOnTraceError != nil {
		opts.IgnoreOnTraceError = *raw.IgnoreOnTraceError
	}
	if raw.NativeSCLockedFromContracts != nil {
		opts.NativeSCLockedFromContracts = *raw.NativeSCLockedFromContracts
	}
	if raw.IgnoreBalanceMismatch != nil {
		opts.IgnoreBalanceMismatch = *raw.IgnoreBalanceMismatch
	}
	if raw.IgnoreUnclaimed != nil {
		opts.IgnoreUnclaimed = *raw.IgnoreUnclaimed
	}
	if raw.IgnoreUnsupportedL2Events != nil {
		opts.IgnoreUnsupportedL2Events = *raw.IgnoreUnsupportedL2Events
	}
	if raw.VerifyNewLocalExitRootUsingShadowFork != nil {
		opts.VerifyNewLocalExitRootUsingShadowFork = *raw.VerifyNewLocalExitRootUsingShadowFork
	}
}

// mergeAgglayerClient overlays the raw agglayer client config onto the gRPC defaults, keeping each
// default when its corresponding raw field is unset.
func mergeAgglayerClient(raw *agglayer.ClientConfig) agglayer.ClientConfig {
	clientCfg := *raw
	grpcDefaults := aggkitgrpc.DefaultConfig()
	if g := clientCfg.GRPC; g != nil {
		if g.URL != "" {
			grpcDefaults.URL = g.URL
		}
		if g.MinConnectTimeout.Duration != 0 {
			grpcDefaults.MinConnectTimeout = g.MinConnectTimeout
		}
		if g.RequestTimeout.Duration != 0 {
			grpcDefaults.RequestTimeout = g.RequestTimeout
		}
		if g.UseTLS {
			grpcDefaults.UseTLS = g.UseTLS
		}
		if g.Retry != nil {
			grpcDefaults.Retry = g.Retry
		}
	}
	clientCfg.GRPC = grpcDefaults
	return clientCfg
}

// rawConfig mirrors the JSON structure with string addresses.
type rawConfig struct {
	L2RPCURL                string          `json:"l2RpcUrl"`
	L1RPCURL                string          `json:"l1RpcUrl"`
	L2BridgeAddress         string          `json:"l2BridgeAddress"`
	L1BridgeAddress         string          `json:"l1BridgeAddress"`
	L2NetworkID             uint32          `json:"l2NetworkId"`
	TargetBlock             string          `json:"targetBlock"`
	ExitAddress             string          `json:"exitAddress"`
	DestinationNetwork      uint32          `json:"destinationNetwork"`
	SovereignRollupAddr     string          `json:"sovereignRollupAddr"`
	L1GlobalExitRootAddress string          `json:"l1GlobalExitRootAddress"`
	RollupManagerAddress    string          `json:"rollupManagerAddress"`
	Options                 *rawOpts        `json:"options"`
	SignerConfig            json.RawMessage `json:"signerConfig"`
}

type rawOpts struct {
	BlockRange                            int                    `json:"blockRange"`
	StepAWindowSize                       int                    `json:"stepAWindowSize"`
	ConcurrencyLimit                      int                    `json:"concurrencyLimit"`
	RPCBatchSize                          int                    `json:"rpcBatchSize"`
	RPCDelayMs                            int                    `json:"rpcDelayMs"`
	OutputDir                             string                 `json:"outputDir"`
	L1StartBlock                          uint64                 `json:"l1StartBlock"`
	L2StartBlock                          uint64                 `json:"l2StartBlock"`
	AgglayerAdminURL                      string                 `json:"agglayerAdminURL"`
	AgglayerAdminToken                    string                 `json:"agglayerAdminToken"`
	AgglayerClient                        *agglayer.ClientConfig `json:"agglayerClient"`
	UseAgglayerAdminToStepFCheck          *bool                  `json:"useAgglayerAdminToStepFCheck"`
	IgnoreGenesisBalance                  *bool                  `json:"ignoreGenesisBalance"`
	IgnoreOnTraceError                    *bool                  `json:"ignoreOnTraceError"`
	NativeSCLockedFromContracts           *bool                  `json:"nativeSCLockedFromContracts"`
	IgnoreBalanceMismatch                 *bool                  `json:"ignoreBalanceMismatch"`
	IgnoreUnclaimed                       *bool                  `json:"ignoreUnclaimed"`
	ExtraERC20Contracts                   []string               `json:"extraErc20Contracts"`
	CapMode                               string                 `json:"capMode"`
	GenesisPrefundETHWei                  string                 `json:"genesisPrefundETHWei"`
	BridgeServiceURL                      string                 `json:"bridgeServiceURL"`
	BridgeServiceType                     string                 `json:"bridgeServiceType"`
	IgnoreUnsupportedL2Events             *bool                  `json:"ignoreUnsupportedL2Events"`
	VerifyNewLocalExitRootUsingShadowFork *bool                  `json:"verifyNewLocalExitRootUsingShadowFork"`
}

// --- LBT file parsing ---

// rawLBTEntry handles both string-encoded ("0") and numeric (0) originNetwork via json.Number.
type rawLBTEntry struct {
	WrappedTokenAddress string      `json:"wrappedTokenAddress"`
	OriginNetwork       json.Number `json:"originNetwork"`
	OriginTokenAddress  string      `json:"originTokenAddress"`
	Balance             string      `json:"balance"`
}

func (r rawLBTEntry) toLBTEntry() LBTEntry {
	return LBTEntry{
		WrappedTokenAddress: common.HexToAddress(r.WrappedTokenAddress),
		OriginNetwork:       parseJSONNumber(r.OriginNetwork),
		OriginTokenAddress:  common.HexToAddress(r.OriginTokenAddress),
		Balance:             r.Balance,
	}
}

func parseJSONNumber(n json.Number) uint32 {
	v, err := n.Int64()
	if err != nil {
		return 0
	}
	return uint32(v)
}

// LoadLBTWrappedTokens reads the LBT JSON file and returns only non-zero-address tokens.
func LoadLBTWrappedTokens(lbtFilePath string) ([]WrappedToken, error) {
	if lbtFilePath == "" {
		return nil, nil
	}
	entries, err := LoadLBTEntries(lbtFilePath)
	if err != nil {
		return nil, err
	}
	return LBTEntriesToWrappedTokens(entries), nil
}

// LoadLBTEntries reads the full LBT JSON file.
func LoadLBTEntries(lbtFilePath string) ([]LBTEntry, error) {
	if lbtFilePath == "" {
		return nil, nil
	}

	f, err := os.Open(lbtFilePath)
	if err != nil {
		return nil, fmt.Errorf("read LBT file %s: %w", lbtFilePath, err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.UseNumber()

	var raw []rawLBTEntry
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse LBT JSON: %w", err)
	}

	entries := make([]LBTEntry, len(raw))
	for i, r := range raw {
		entries[i] = r.toLBTEntry()
	}
	return entries, nil
}

// LBTEntriesToWrappedTokens extracts the wrapped token list from LBT entries,
// filtering out entries with a zero wrappedTokenAddress (native token entry).
func LBTEntriesToWrappedTokens(entries []LBTEntry) []WrappedToken {
	var tokens []WrappedToken
	for _, e := range entries {
		if e.WrappedTokenAddress != (common.Address{}) {
			tokens = append(tokens, WrappedToken{
				WrappedTokenAddress: e.WrappedTokenAddress,
				OriginNetwork:       e.OriginNetwork,
				OriginTokenAddress:  e.OriginTokenAddress,
			})
		}
	}
	return tokens
}
