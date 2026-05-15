package exit_certificate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

// Options holds tuning parameters for RPC parallelism and output.
type Options struct {
	BlockRange             int    `json:"blockRange"`
	ConcurrencyLimit       int    `json:"concurrencyLimit"`
	RPCBatchSize           int    `json:"rpcBatchSize"`
	RPCDelayMs             int    `json:"rpcDelayMs"`
	OutputDir              string `json:"outputDir"`
	L1StartBlock           uint64 `json:"l1StartBlock"`
	L2StartBlock           uint64 `json:"l2StartBlock"`
	AgglayerAdminURL string `json:"agglayerAdminURL"`
	AgglayerGRPCURL  string `json:"agglayerGrpcUrl"`
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
	// BridgeServiceURL is the base URL of the bridge service REST API.
	// When set, Step E queries the bridge service for pending bridges targeting this L2 and returns an
	// error if any unclaimed deposits are found.
	// Aggkit example:  "http://127.0.0.1:32970"
	// zkevm example:   "http://127.0.0.1:33019"
	BridgeServiceURL string `json:"bridgeServiceURL"`
	// BridgeServiceType selects the bridge service API flavour: "aggkit" (default) or "zkevm".
	BridgeServiceType string `json:"bridgeServiceType"`
}

// Config holds all parameters required by the exit certificate tool.
type Config struct {
	L2RPCURL           string         `json:"l2RpcUrl"`
	L1RPCURL           string         `json:"l1RpcUrl"`
	L2BridgeAddress    common.Address `json:"l2BridgeAddress"`
	L1BridgeAddress    common.Address `json:"l1BridgeAddress"`
	L2NetworkID        uint32         `json:"l2NetworkId"`
	TargetBlock        string         `json:"targetBlock"`
	ExitAddress             common.Address `json:"exitAddress"`
	LBTFile                 string         `json:"lbtFile"`
	DestinationNetwork      uint32         `json:"destinationNetwork"`
	SovereignRollupAddr     common.Address `json:"sovereignRollupAddr"`
	// L1GlobalExitRootAddress is the address of the PolygonZkEVMGlobalExitRootV2 contract on L1.
	// Required for Step I to fetch the L1InfoTreeLeafCount from UpdateL1InfoTreeV2 events.
	L1GlobalExitRootAddress common.Address `json:"l1GlobalExitRootAddress"`
	Options              Options                `json:"options"`
	SignerConfig         signertypes.SignerConfig `json:"-"`

	// ResolvedTargetBlock is populated at runtime after resolving "latest".
	ResolvedTargetBlock uint64 `json:"-"`
}

const (
	defaultBlockRange       = 5000
	defaultConcurrencyLimit = 20
	defaultRPCBatchSize     = 200
)

var defaultOptions = Options{
	BlockRange:            defaultBlockRange,
	ConcurrencyLimit:      defaultConcurrencyLimit,
	RPCBatchSize:          defaultRPCBatchSize,
	RPCDelayMs:            0,
	OutputDir:             "output",
	L1StartBlock:          0,
	L2StartBlock:          0,
	AbortOnGenesisBalance: true,
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

	cfg := &Config{
		L2RPCURL:                raw.L2RPCURL,
		L1RPCURL:                raw.L1RPCURL,
		L2BridgeAddress:         common.HexToAddress(raw.L2BridgeAddress),
		L2NetworkID:             raw.L2NetworkID,
		ExitAddress:             common.HexToAddress(raw.ExitAddress),
		DestinationNetwork:      raw.DestinationNetwork,
		TargetBlock:             raw.TargetBlock,
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

	cfg.LBTFile = resolvePath(configDir, raw.LBTFile)
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
	if raw.AgglayerGRPCURL != "" {
		opts.AgglayerGRPCURL = raw.AgglayerGRPCURL
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
	if raw.BridgeServiceURL != "" {
		opts.BridgeServiceURL = raw.BridgeServiceURL
	}
	if raw.BridgeServiceType != "" {
		opts.BridgeServiceType = raw.BridgeServiceType
	}
	return opts
}

// rawConfig mirrors the JSON structure with string addresses.
type rawConfig struct {
	L2RPCURL                string   `json:"l2RpcUrl"`
	L1RPCURL                string   `json:"l1RpcUrl"`
	L2BridgeAddress         string   `json:"l2BridgeAddress"`
	L1BridgeAddress         string   `json:"l1BridgeAddress"`
	L2NetworkID             uint32   `json:"l2NetworkId"`
	TargetBlock             string   `json:"targetBlock"`
	ExitAddress             string   `json:"exitAddress"`
	LBTFile                 string   `json:"lbtFile"`
	DestinationNetwork      uint32   `json:"destinationNetwork"`
	SovereignRollupAddr     string   `json:"sovereignRollupAddr"`
	L1GlobalExitRootAddress string   `json:"l1GlobalExitRootAddress"`
	Options      *rawOpts        `json:"options"`
	SignerConfig json.RawMessage `json:"signerConfig"`
}

type rawOpts struct {
	BlockRange             int    `json:"blockRange"`
	ConcurrencyLimit       int    `json:"concurrencyLimit"`
	RPCBatchSize           int    `json:"rpcBatchSize"`
	RPCDelayMs             int    `json:"rpcDelayMs"`
	OutputDir              string `json:"outputDir"`
	L1StartBlock           uint64 `json:"l1StartBlock"`
	L2StartBlock           uint64 `json:"l2StartBlock"`
	AgglayerAdminURL string `json:"agglayerAdminURL"`
	AgglayerGRPCURL  string `json:"agglayerGrpcUrl"`
	AbortOnGenesisBalance     *bool  `json:"abortOnGenesisBalance"`
	ContinueOnTraceError      *bool  `json:"continueOnTraceError"`
	ContinueIfBalanceMismatch *bool  `json:"continueIfBalanceMismatch"`
	IgnoreUnclaimed           *bool  `json:"ignoreUnclaimed"`
	BridgeServiceURL          string `json:"bridgeServiceURL"`
	BridgeServiceType         string `json:"bridgeServiceType"`
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
