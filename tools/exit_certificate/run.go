package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v2"
)

const (
	dirPermissions  = 0o755
	filePermissions = 0o600
)

// Run is the CLI entry point.
func Run(c *cli.Context) error {
	ctx := context.Background()

	cfg, err := LoadConfig(c.String("config"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := resolveBlockA(ctx, cfg); err != nil {
		return err
	}

	step := c.String("step")
	if step == "" {
		step = "all"
	}

	if step == "all" {
		return runAll(ctx, cfg)
	}
	return runSingleStep(ctx, step, cfg)
}

// resolveBlockA resolves "latest" to a concrete block number, or parses the numeric value.
func resolveBlockA(ctx context.Context, cfg *Config) error {
	if cfg.TargetBlock == "latest" || cfg.TargetBlock == "" {
		blockNum, err := resolveLatestBlock(ctx, cfg.L2RPCURL)
		if err != nil {
			return fmt.Errorf("resolve latest block: %w", err)
		}
		cfg.ResolvedTargetBlock = blockNum
		log.Infof("Resolved targetBlock=\"latest\" → %d", cfg.ResolvedTargetBlock)
		return nil
	}
	blockNum, err := parseBlockNumber(cfg.TargetBlock)
	if err != nil {
		return fmt.Errorf("invalid targetBlock %q: %w", cfg.TargetBlock, err)
	}
	cfg.ResolvedTargetBlock = blockNum
	return nil
}

func resolveLatestBlock(ctx context.Context, rpcURL string) (uint64, error) {
	result, err := singleRPC(ctx, rpcURL, "eth_blockNumber", nil, defaultRetries)
	if err != nil {
		return 0, err
	}
	var hex string
	if err := json.Unmarshal(result, &hex); err != nil {
		return 0, fmt.Errorf("parse block number: %w", err)
	}
	return hexToUint64(hex), nil
}

// parseBlockNumber parses a block number string (decimal or 0x-hex).
func parseBlockNumber(s string) (uint64, error) {
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return hexToUint64(s), nil
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not a valid block number (expected decimal or 0x-hex): %w", err)
	}
	return n, nil
}

// --- Full pipeline ---

// runAll executes: 0 → A → B → C → D → E.
func runAll(ctx context.Context, cfg *Config) error {
	dir := cfg.Options.OutputDir
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	startTime := time.Now()
	logPipelineConfig(cfg)

	lbtEntries, wrappedTokens, err := resolveOrGenerateLBT(ctx, cfg, dir)
	if err != nil {
		return fmt.Errorf("step 0 (LBT): %w", err)
	}

	stepAResult, err := runAllStepA(ctx, cfg, dir, wrappedTokens)
	if err != nil {
		return err
	}

	stepBResult, err := runAllStepB(ctx, cfg, dir, stepAResult)
	if err != nil {
		return err
	}

	stepCResult, err := runAllStepC(dir, lbtEntries, stepBResult)
	if err != nil {
		return err
	}

	stepDResult, err := runAllStepD(cfg, dir, stepBResult, stepCResult)
	if err != nil {
		return err
	}

	finalCertificate, err := runAllStepE(ctx, cfg, dir, stepDResult.Certificate)
	if err != nil {
		return err
	}

	saveJSON(dir, "exit-certificate-final.json", finalCertificate)

	if err := runAllStepF(ctx, cfg, dir, stepDResult.Certificate); err != nil {
		return err
	}

	log.Info("")
	log.Info("╔═══════════════════════════════════════════╗")
	log.Info("║             Pipeline Complete              ║")
	log.Info("╚═══════════════════════════════════════════╝")
	log.Infof("Total bridge exits:  %d", len(finalCertificate.BridgeExits))
	log.Infof("Elapsed time:        %.1fs", time.Since(startTime).Seconds())
	log.Infof("Output directory:    %s", dir)

	return nil
}

func runAllStepA(ctx context.Context, cfg *Config, dir string, wrappedTokens []WrappedToken) (*StepAResult, error) {
	stepAResult, err := RunStepA(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("step A: %w", err)
	}
	saveJSON(dir, "step-a-addresses.json", stepAResult.Addresses)
	stepAResult.WrappedTokens = wrappedTokens
	if len(wrappedTokens) > 0 {
		log.Infof("Using %d wrapped tokens for balance scanning", len(wrappedTokens))
	}
	return stepAResult, nil
}

func runAllStepB(ctx context.Context, cfg *Config, dir string, stepAResult *StepAResult) (*StepBResult, error) {
	stepBResult, err := RunStepB(ctx, cfg, stepAResult)
	if err != nil {
		return nil, fmt.Errorf("step B: %w", err)
	}
	saveJSON(dir, "step-b-eoa-balances.json", stepBResult.EOABalances)
	saveJSON(dir, "step-b-accumulated.json", stepBResult.Accumulated)
	saveJSON(dir, "step-b-contract-addresses.json", stepBResult.ContractAddresses)
	return stepBResult, nil
}

func runAllStepC(dir string, lbtEntries []LBTEntry, stepBResult *StepBResult) (*StepCResult, error) {
	if len(lbtEntries) == 0 {
		log.Warn("STEP C skipped: no LBT data available")
		return &StepCResult{}, nil
	}
	stepCResult, err := RunStepCWithEntries(lbtEntries, stepBResult)
	if err != nil {
		return nil, fmt.Errorf("step C: %w", err)
	}
	saveJSON(dir, "step-c-sc-locked-values.json", stepCResult.SCLockedValues)
	return stepCResult, nil
}

func runAllStepF(ctx context.Context, cfg *Config, dir string, certificate *agglayertypes.Certificate) error {
	result, err := RunStepF(ctx, cfg, certificate)
	if err != nil {
		return fmt.Errorf("step F: %w", err)
	}
	if !result.Skipped {
		saveJSON(dir, "step-f-token-balances.json", result.TokenBalances)
		saveJSON(dir, "step-f-checks.json", result.Checks)
	}
	return nil
}

func runAllStepD(cfg *Config, dir string, stepBResult *StepBResult, stepCResult *StepCResult) (*StepDResult, error) {
	stepDResult, err := RunStepD(cfg, stepBResult, stepCResult)
	if err != nil {
		return nil, fmt.Errorf("step D: %w", err)
	}
	saveJSON(dir, "step-d-exit-certificate.json", stepDResult.Certificate)
	return stepDResult, nil
}

func runAllStepE(ctx context.Context, cfg *Config, dir string, stepDCert *agglayertypes.Certificate) (*agglayertypes.Certificate, error) {
	if cfg.L1RPCURL == "" {
		log.Warn("STEP E skipped: no L1 RPC provided")
		return stepDCert, nil
	}
	stepEResult, err := RunStepE(ctx, cfg, stepDCert)
	if err != nil {
		return nil, fmt.Errorf("step E: %w", err)
	}
	saveJSON(dir, "step-e-unclaimed-bridges.json", stepEResult.UnclaimedBridges)
	return stepEResult.FinalCertificate, nil
}

func logPipelineConfig(cfg *Config) {
	log.Info("╔═══════════════════════════════════════════╗")
	log.Info("║   Exit Certificate Tool — Full Pipeline   ║")
	log.Info("╚═══════════════════════════════════════════╝")
	log.Infof("L2 RPC:           %s", cfg.L2RPCURL)
	if cfg.L1RPCURL != "" {
		log.Infof("L1 RPC:           %s", cfg.L1RPCURL)
	} else {
		log.Info("L1 RPC:           (not configured — step E will be skipped)")
	}
	log.Infof("L2 Bridge:        %s", cfg.L2BridgeAddress.Hex())
	log.Infof("Target Block:     %d", cfg.ResolvedTargetBlock)
	log.Infof("L2 Network ID:    %d", cfg.L2NetworkID)
	log.Infof("Exit Address:     %s", cfg.ExitAddress.Hex())
	log.Infof("Dest Network:     %d", cfg.DestinationNetwork)
	if cfg.LBTFile != "" {
		log.Infof("LBT File:         %s (pre-generated, skipping step 0)", cfg.LBTFile)
	} else {
		log.Info("LBT File:         (not configured — will generate via step 0)")
	}
	log.Infof("Output Dir:       %s", cfg.Options.OutputDir)
	log.Infof("Concurrency:      %d", cfg.Options.ConcurrencyLimit)
	log.Infof("Block Range:      %d", cfg.Options.BlockRange)
	log.Infof("RPC Batch Size:   %d", cfg.Options.RPCBatchSize)
	log.Infof("L2 Start Block:   %d", cfg.Options.L2StartBlock)
}

// --- Single step ---

func runSingleStep(ctx context.Context, step string, cfg *Config) error {
	dir := cfg.Options.OutputDir
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	switch step {
	case "0":
		return runSingle0(ctx, cfg, dir)
	case "a":
		return runSingleA(ctx, cfg, dir)
	case "b":
		return runSingleB(ctx, cfg, dir)
	case "c":
		return runSingleC(cfg, dir)
	case "d":
		return runSingleD(cfg, dir)
	case "e":
		return runSingleE(ctx, cfg, dir)
	case "f":
		return runSingleF(ctx, cfg, dir)
	default:
		return fmt.Errorf("unknown step: %s (use 0, a, b, c, d, e, f, or all)", step)
	}
}

func runSingle0(ctx context.Context, cfg *Config, dir string) error {
	entries, err := RunStep0(ctx, cfg)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-0-lbt.json", entries)
	return nil
}

func runSingleA(ctx context.Context, cfg *Config, dir string) error {
	result, err := RunStepA(ctx, cfg)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-a-addresses.json", result.Addresses)
	return nil
}

func runSingleB(ctx context.Context, cfg *Config, dir string) error {
	var addresses []common.Address
	if err := loadJSON(dir, "step-a-addresses.json", &addresses); err != nil {
		return fmt.Errorf("load step A output: %w", err)
	}
	wrappedTokens, err := loadWrappedTokensFromLBT(cfg, dir)
	if err != nil {
		return err
	}
	log.Infof("Using %d wrapped tokens for balance scanning", len(wrappedTokens))

	result, err := RunStepB(ctx, cfg, &StepAResult{
		Addresses:     addresses,
		WrappedTokens: wrappedTokens,
	})
	if err != nil {
		return err
	}
	saveJSON(dir, "step-b-eoa-balances.json", result.EOABalances)
	saveJSON(dir, "step-b-accumulated.json", result.Accumulated)
	saveJSON(dir, "step-b-contract-addresses.json", result.ContractAddresses)
	return nil
}

func runSingleC(cfg *Config, dir string) error {
	var accumulated []AccumulatedBalance
	if err := loadJSON(dir, "step-b-accumulated.json", &accumulated); err != nil {
		return fmt.Errorf("load step B output: %w", err)
	}
	result, err := RunStepC(cfg, &StepBResult{Accumulated: accumulated})
	if err != nil {
		return err
	}
	saveJSON(dir, "step-c-sc-locked-values.json", result.SCLockedValues)
	return nil
}

func runSingleD(cfg *Config, dir string) error {
	var eoaBalances []EOABalance
	if err := loadJSON(dir, "step-b-eoa-balances.json", &eoaBalances); err != nil {
		return fmt.Errorf("load step B output: %w", err)
	}
	var scLockedValues []SCLockedValue
	if err := loadJSON(dir, "step-c-sc-locked-values.json", &scLockedValues); err != nil {
		return fmt.Errorf("load step C output: %w", err)
	}
	result, err := RunStepD(cfg, &StepBResult{EOABalances: eoaBalances}, &StepCResult{SCLockedValues: scLockedValues})
	if err != nil {
		return err
	}
	saveJSON(dir, "step-d-exit-certificate.json", result.Certificate)
	return nil
}

func runSingleE(ctx context.Context, cfg *Config, dir string) error {
	if cfg.L1RPCURL == "" {
		return fmt.Errorf("step E requires l1RpcUrl in parameters")
	}
	var cert certificateJSON
	if err := loadJSON(dir, "step-d-exit-certificate.json", &cert); err != nil {
		return fmt.Errorf("load step D output: %w", err)
	}
	result, err := RunStepE(ctx, cfg, cert.toAgglayerCertificate())
	if err != nil {
		return err
	}
	saveJSON(dir, "step-e-unclaimed-bridges.json", result.UnclaimedBridges)
	saveJSON(dir, "exit-certificate-final.json", result.FinalCertificate)
	return nil
}

func runSingleF(ctx context.Context, cfg *Config, dir string) error {
	var cert certificateJSON
	if err := loadJSON(dir, "step-d-exit-certificate.json", &cert); err != nil {
		return fmt.Errorf("load step D certificate: %w", err)
	}
	result, err := RunStepF(ctx, cfg, cert.toAgglayerCertificate())
	if err != nil {
		return err
	}
	if !result.Skipped {
		saveJSON(dir, "step-f-token-balances.json", result.TokenBalances)
		saveJSON(dir, "step-f-checks.json", result.Checks)
	}
	return nil
}

// --- LBT resolution ---

// resolveOrGenerateLBT loads from lbtFile if present, otherwise runs Step 0.
func resolveOrGenerateLBT(ctx context.Context, cfg *Config, dir string) ([]LBTEntry, []WrappedToken, error) {
	if cfg.LBTFile != "" {
		if _, err := os.Stat(cfg.LBTFile); err == nil {
			entries, err := LoadLBTEntries(cfg.LBTFile)
			if err != nil {
				return nil, nil, fmt.Errorf("load LBT file: %w", err)
			}
			tokens := LBTEntriesToWrappedTokens(entries)
			log.Infof("Loaded %d LBT entries (%d wrapped tokens) from %s", len(entries), len(tokens), cfg.LBTFile)
			return entries, tokens, nil
		}
		log.Warnf("LBT file not found at %s — generating via step 0", cfg.LBTFile)
	}

	entries, err := RunStep0(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	saveJSON(dir, "step-0-lbt.json", entries)
	cfg.LBTFile = filepath.Join(dir, "step-0-lbt.json")

	return entries, LBTEntriesToWrappedTokens(entries), nil
}

// loadWrappedTokensFromLBT loads tokens from lbtFile or the step-0 output.
func loadWrappedTokensFromLBT(cfg *Config, dir string) ([]WrappedToken, error) {
	if cfg.LBTFile != "" {
		if tokens, err := LoadLBTWrappedTokens(cfg.LBTFile); err == nil && len(tokens) > 0 {
			return tokens, nil
		}
	}
	tokens, err := LoadLBTWrappedTokens(filepath.Join(dir, "step-0-lbt.json"))
	if err != nil {
		return nil, fmt.Errorf("no LBT data available: configure lbtFile or run step 0 first")
	}
	return tokens, nil
}

// --- JSON I/O ---

func saveJSON(dir, filename string, data any) {
	path := filepath.Join(dir, filename)
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		log.Errorf("Failed to marshal %s: %v", filename, err)
		return
	}
	if err := os.WriteFile(path, content, filePermissions); err != nil {
		log.Errorf("Failed to write %s: %v", path, err)
		return
	}
	log.Infof("Written: %s", path)
}

func loadJSON(dir, filename string, target any) error {
	path := filepath.Join(dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return json.Unmarshal(data, target)
}

// certificateJSON supports loading a Certificate from the step-d output file.
type certificateJSON struct {
	NetworkID         uint32          `json:"network_id"`
	Height            uint64          `json:"height"`
	PrevLocalExitRoot common.Hash     `json:"prev_local_exit_root"`
	NewLocalExitRoot  common.Hash     `json:"new_local_exit_root"`
	BridgeExits       json.RawMessage `json:"bridge_exits"`
	ImportedBridges   json.RawMessage `json:"imported_bridge_exits"`
}

func (c *certificateJSON) toAgglayerCertificate() *agglayertypes.Certificate {
	cert := &agglayertypes.Certificate{
		NetworkID:         c.NetworkID,
		Height:            c.Height,
		PrevLocalExitRoot: c.PrevLocalExitRoot,
		NewLocalExitRoot:  c.NewLocalExitRoot,
	}
	if len(c.BridgeExits) > 0 {
		_ = json.Unmarshal(c.BridgeExits, &cert.BridgeExits)
	}
	if len(c.ImportedBridges) > 0 {
		_ = json.Unmarshal(c.ImportedBridges, &cert.ImportedBridgeExits)
	}
	return cert
}
