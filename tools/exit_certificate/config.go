package exit_certificate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
)

// Options holds tuning parameters for RPC parallelism and output.
type Options struct {
	BlockRange       int    `json:"blockRange"`
	ConcurrencyLimit int    `json:"concurrencyLimit"`
	RPCBatchSize     int    `json:"rpcBatchSize"`
	RPCDelayMs       int    `json:"rpcDelayMs"`
	OutputDir        string `json:"outputDir"`
	L1StartBlock     uint64 `json:"l1StartBlock"`
}

// Config holds all parameters required by the exit certificate tool.
type Config struct {
	L2RPCURL           string         `json:"l2RpcUrl"`
	L1RPCURL           string         `json:"l1RpcUrl"`
	L2BridgeAddress    common.Address `json:"l2BridgeAddress"`
	L1BridgeAddress    common.Address `json:"l1BridgeAddress"`
	L2NetworkID        uint32         `json:"l2NetworkId"`
	TargetBlock        string         `json:"targetBlock"`
	ExitAddress        common.Address `json:"exitAddress"`
	LBTFile            string         `json:"lbtFile"`
	DestinationNetwork uint32         `json:"destinationNetwork"`
	Options            Options        `json:"options"`

	// ResolvedTargetBlock is populated at runtime after resolving "latest".
	ResolvedTargetBlock uint64 `json:"-"`
}

const (
	defaultBlockRange       = 5000
	defaultConcurrencyLimit = 20
	defaultRPCBatchSize     = 200
)

var defaultOptions = Options{
	BlockRange:       defaultBlockRange,
	ConcurrencyLimit: defaultConcurrencyLimit,
	RPCBatchSize:     defaultRPCBatchSize,
	RPCDelayMs:       0,
	OutputDir:        "output",
	L1StartBlock:     0,
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
		L2RPCURL:           raw.L2RPCURL,
		L1RPCURL:           raw.L1RPCURL,
		L2BridgeAddress:    common.HexToAddress(raw.L2BridgeAddress),
		L2NetworkID:        raw.L2NetworkID,
		ExitAddress:        common.HexToAddress(raw.ExitAddress),
		DestinationNetwork: raw.DestinationNetwork,
		TargetBlock:        raw.TargetBlock,
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

	return cfg, nil
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
	return opts
}

// rawConfig mirrors the JSON structure with string addresses.
type rawConfig struct {
	L2RPCURL           string   `json:"l2RpcUrl"`
	L1RPCURL           string   `json:"l1RpcUrl"`
	L2BridgeAddress    string   `json:"l2BridgeAddress"`
	L1BridgeAddress    string   `json:"l1BridgeAddress"`
	L2NetworkID        uint32   `json:"l2NetworkId"`
	TargetBlock        string   `json:"targetBlock"`
	ExitAddress        string   `json:"exitAddress"`
	LBTFile            string   `json:"lbtFile"`
	DestinationNetwork uint32   `json:"destinationNetwork"`
	Options            *rawOpts `json:"options"`
}

type rawOpts struct {
	BlockRange       int    `json:"blockRange"`
	ConcurrencyLimit int    `json:"concurrencyLimit"`
	RPCBatchSize     int    `json:"rpcBatchSize"`
	RPCDelayMs       int    `json:"rpcDelayMs"`
	OutputDir        string `json:"outputDir"`
	L1StartBlock     uint64 `json:"l1StartBlock"`
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
