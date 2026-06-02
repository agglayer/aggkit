package exit_certificate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agglayer/aggkit/agglayer"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	aggkittypes "github.com/agglayer/aggkit/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

// Options holds tuning parameters for RPC parallelism and output.
type Options struct {
	BlockRange int `json:"blockRange"`
	// StepAWindowSize is the number of blocks loaded into memory at once during Step A
	// (address collection via debug_traceTransaction). Defaults to 5000, independently of BlockRange.
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
	// AbortOnGenesisBalance aborts the run if any EOA or contract has a non-zero ETH balance
	// at block 0, which indicates a genesis preload that would inflate the exit certificate totals.
	// Defaults to true; set to false only for Kurtosis or test environments.
	AbortOnGenesisBalance bool `json:"abortOnGenesisBalance"`
	// ContinueOnTraceError skips transactions whose debug_traceTransaction call fails instead of
	// aborting Step A. Failed tx hashes are saved to step-a-failed-traces.json for review.
	ContinueOnTraceError bool `json:"continueOnTraceError"`
	// ContinueIfBalanceMismatch suppresses the error returned by Step F when token balances
	// do not match. Set to true only when investigating discrepancies without blocking the pipeline.
	ContinueIfBalanceMismatch bool `json:"continueIfBalanceMismatch"`
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
	// DepositOrderSource selects how Step G recovers the canonical bridge deposit order from the
	// shadow-fork after the parallel replay: "events" (default — reads BridgeEvent logs directly
	// from the fork) or "bridgesync" (reuses the bridgesync component, syncing all L2 bridges).
	DepositOrderSource string `json:"depositOrderSource"`
}

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
	L1GlobalExitRootAddress common.Address           `json:"l1GlobalExitRootAddress"`
	Options                 Options                  `json:"options"`
	SignerConfig            signertypes.SignerConfig `json:"-"`
}

const (
	defaultBlockRange       = 5000
	defaultStepAWindowSize  = 150000
	defaultConcurrencyLimit = 20
	defaultRPCBatchSize     = 200
)

var defaultOptions = Options{
	BlockRange:            defaultBlockRange,
	StepAWindowSize:       defaultStepAWindowSize,
	ConcurrencyLimit:      defaultConcurrencyLimit,
	RPCBatchSize:          defaultRPCBatchSize,
	RPCDelayMs:            0,
	OutputDir:             "output",
	L1StartBlock:          0,
	L2StartBlock:          0,
	AbortOnGenesisBalance: true,
	DepositOrderSource:    DefaultDepositOrderSource,
}

// LoadConfig reads and validates the JSON config file.
func LoadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", configPath, err)
	}

	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config JSON: %w", err)
	}

	if raw.L2RPCURL == "" {
		return nil, fmt.Errorf("missing required parameter: l2RpcUrl")
	}
	if raw.L2BridgeAddress == "" {
		return nil, fmt.Errorf("missing required parameter: l2BridgeAddress")
	}

	configDir := filepath.Dir(configPath)

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
	switch cfg.Options.DepositOrderSource {
	case DepositOrderEvents, DepositOrderBridgesync:
	default:
		return nil, fmt.Errorf("invalid depositOrderSource %q (expected %q or %q)",
			cfg.Options.DepositOrderSource, DepositOrderEvents, DepositOrderBridgesync)
	}
	if len(raw.SignerConfig) > 0 {
		signerCfg, err := parseSignerConfig(raw.SignerConfig, configDir)
		if err != nil {
			return nil, fmt.Errorf("parse signerConfig: %w", err)
		}
		cfg.SignerConfig = signerCfg
	}

	return cfg, nil
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
	if raw.AgglayerClient != nil {
		clientCfg := *raw.AgglayerClient
		grpcDefaults := aggkitgrpc.DefaultConfig()
		if clientCfg.GRPC != nil {
			if clientCfg.GRPC.URL != "" {
				grpcDefaults.URL = clientCfg.GRPC.URL
			}
			if clientCfg.GRPC.MinConnectTimeout.Duration != 0 {
				grpcDefaults.MinConnectTimeout = clientCfg.GRPC.MinConnectTimeout
			}
			if clientCfg.GRPC.RequestTimeout.Duration != 0 {
				grpcDefaults.RequestTimeout = clientCfg.GRPC.RequestTimeout
			}
			if clientCfg.GRPC.UseTLS {
				grpcDefaults.UseTLS = clientCfg.GRPC.UseTLS
			}
			if clientCfg.GRPC.Retry != nil {
				grpcDefaults.Retry = clientCfg.GRPC.Retry
			}
		}
		clientCfg.GRPC = grpcDefaults
		opts.AgglayerClient = clientCfg
	}
	if raw.AbortOnGenesisBalance != nil {
		opts.AbortOnGenesisBalance = *raw.AbortOnGenesisBalance
	}
	if raw.ContinueOnTraceError != nil {
		opts.ContinueOnTraceError = *raw.ContinueOnTraceError
	}
	if raw.ContinueIfBalanceMismatch != nil {
		opts.ContinueIfBalanceMismatch = *raw.ContinueIfBalanceMismatch
	}
	if raw.IgnoreUnclaimed != nil {
		opts.IgnoreUnclaimed = *raw.IgnoreUnclaimed
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
	if raw.DepositOrderSource != "" {
		opts.DepositOrderSource = raw.DepositOrderSource
	}
	return opts
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
	Options                 *rawOpts        `json:"options"`
	SignerConfig            json.RawMessage `json:"signerConfig"`
}

type rawOpts struct {
	BlockRange                int                    `json:"blockRange"`
	StepAWindowSize           int                    `json:"stepAWindowSize"`
	ConcurrencyLimit          int                    `json:"concurrencyLimit"`
	RPCBatchSize              int                    `json:"rpcBatchSize"`
	RPCDelayMs                int                    `json:"rpcDelayMs"`
	OutputDir                 string                 `json:"outputDir"`
	L1StartBlock              uint64                 `json:"l1StartBlock"`
	L2StartBlock              uint64                 `json:"l2StartBlock"`
	AgglayerAdminURL          string                 `json:"agglayerAdminURL"`
	AgglayerAdminToken        string                 `json:"agglayerAdminToken"`
	AgglayerClient            *agglayer.ClientConfig `json:"agglayerClient"`
	AbortOnGenesisBalance     *bool                  `json:"abortOnGenesisBalance"`
	ContinueOnTraceError      *bool                  `json:"continueOnTraceError"`
	ContinueIfBalanceMismatch *bool                  `json:"continueIfBalanceMismatch"`
	IgnoreUnclaimed           *bool                  `json:"ignoreUnclaimed"`
	ExtraERC20Contracts       []string               `json:"extraErc20Contracts"`
	BridgeServiceURL          string                 `json:"bridgeServiceURL"`
	BridgeServiceType         string                 `json:"bridgeServiceType"`
	DepositOrderSource        string                 `json:"depositOrderSource"`
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
