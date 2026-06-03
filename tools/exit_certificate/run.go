package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	logLevel := "info"
	if c.Bool("verbose") {
		logLevel = "debug"
	}
	log.Init(log.Config{
		Environment: log.EnvironmentDevelopment,
		Level:       logLevel,
		Outputs:     []string{"stderr"},
	})

	cfg, err := LoadConfig(c.String("config"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := os.MkdirAll(cfg.Options.OutputDir, dirPermissions); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := migrateStepAToA1(cfg.Options.OutputDir); err != nil {
		return err
	}

	step := c.String("step")
	if step == "" || step == "all" {
		return runAll(ctx, cfg)
	}

	steps, err := parseStepList(step)
	if err != nil {
		return err
	}
	for _, s := range steps {
		if err := runSingleStep(ctx, s, cfg); err != nil {
			return err
		}
	}
	return nil
}

// orderedSteps is the canonical pipeline order used for range expansion.
// "a" and "b" are aliases for their sub-steps and are handled in parseStepList; not listed here.
var orderedSteps = []string{
	"check", "0", "a1", "a2", "b1", "b2", "b3", "c", "d", "e", "f", "g1", "g2", "h", "i", "sign", "submit", "wait",
}

// lastAutoStep is the implicit end for open ranges (X-).
// "submit" and "wait" must always be specified explicitly.
const lastAutoStep = "sign"

// parseStepList splits a comma-separated step list, expanding range notation.
// "f-i"  → ["f", "g", "h", "i"]
// "f-"   → ["f", "g", "h", "i", "sign", "submit", "wait"]
// "h, i, sign" → ["h", "i", "sign"]
// "a"    → ["a1", "a2"] (alias for both sub-steps)
// "b"    → ["b1", "b2", "b3"] (alias for all three sub-steps)
// "g"    → ["g1", "g2"] (alias for both sub-steps)
// "a-b"  → ["a1", "a2", "b1", "b2", "b3"] ("a"→"a1" start, "b"→"b3" end)
// "0-a"  → ["0", "a1", "a2"] ("a" expands to "a2" as range end)
func parseStepList(raw string) ([]string, error) {
	var steps []string
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if strings.Contains(token, "-") {
			// Map "a"/"b"/"g" to their sub-step boundaries before expanding ranges.
			parts := strings.SplitN(token, "-", splitInTwo)
			from, to := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			from = aliasRangeStart(from)
			to = aliasRangeEnd(to)
			expanded, err := expandStepRange(from + "-" + to)
			if err != nil {
				return nil, err
			}
			steps = append(steps, expanded...)
		} else if sub, ok := stepAliases[token]; ok {
			steps = append(steps, sub...)
		} else {
			steps = append(steps, token)
		}
	}
	return steps, nil
}

// stepAliases maps a step alias to the ordered sub-steps it expands to.
var stepAliases = map[string][]string{
	"a": {"a1", "a2"},
	"b": {"b1", "b2", "b3"},
	"g": {"g1", "g2"},
}

// aliasRangeStart maps an alias used as a range start to its first sub-step.
func aliasRangeStart(s string) string {
	if sub, ok := stepAliases[s]; ok {
		return sub[0]
	}
	return s
}

// aliasRangeEnd maps an alias used as a range end to its last sub-step.
func aliasRangeEnd(s string) string {
	if sub, ok := stepAliases[s]; ok {
		return sub[len(sub)-1]
	}
	return s
}

func expandStepRange(token string) ([]string, error) {
	parts := strings.SplitN(token, "-", splitInTwo)
	from, to := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

	fromIdx := -1
	for i, s := range orderedSteps {
		if s == from {
			fromIdx = i
			break
		}
	}
	if fromIdx == -1 {
		return nil, fmt.Errorf("unknown step in range %q: %q", token, from)
	}

	// Open range: stop at lastAutoStep (submit/wait require explicit opt-in).
	toIdx := -1
	for i, s := range orderedSteps {
		if s == lastAutoStep {
			toIdx = i
			break
		}
	}
	if to != "" {
		toIdx = -1
		for i, s := range orderedSteps {
			if s == to {
				toIdx = i
				break
			}
		}
		if toIdx == -1 {
			return nil, fmt.Errorf("unknown step in range %q: %q", token, to)
		}
		if toIdx < fromIdx {
			return nil, fmt.Errorf("invalid range %q: %q comes before %q in the pipeline", token, to, from)
		}
	}

	return orderedSteps[fromIdx : toIdx+1], nil
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

// --- Full pipeline ---

// runAll executes: CHECK → 0 → A → B → C → D → E → F → G → H → I.
func runAll(ctx context.Context, cfg *Config) error {
	dir := cfg.Options.OutputDir
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	startTime := time.Now()
	logPipelineConfig(cfg)

	checkResult, err := RunStepCheck(ctx, cfg)
	if err != nil {
		return fmt.Errorf("step CHECK: %w", err)
	}
	saveJSON(dir, "step-check-result.json", checkResult)

	lbtEntries, wrappedTokens, targetBlock, err := resolveOrGenerateLBT(ctx, cfg, dir)
	if err != nil {
		return fmt.Errorf("step 0 (LBT): %w", err)
	}

	stepAResult, err := runAllStepA(ctx, cfg, dir, targetBlock, wrappedTokens)
	if err != nil {
		return err
	}

	stepBResult, err := runAllStepB(ctx, cfg, dir, targetBlock, stepAResult)
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

	finalCertificate, err = runAllStepF(ctx, cfg, dir, lbtEntries, stepDResult.Certificate, finalCertificate)
	if err != nil {
		return err
	}

	gResult, err := runAllStepG(ctx, cfg, dir, targetBlock, finalCertificate, lbtEntries)
	if err != nil {
		return err
	}

	hResult, err := runAllStepH(ctx, cfg, dir, gResult)
	if err != nil {
		return err
	}

	if err := runAllStepI(ctx, cfg, dir, finalCertificate, gResult, hResult); err != nil {
		return err
	}

	if cfg.SignerConfig.Method != "" {
		signedCert, err := RunStepSign(ctx, cfg, finalCertificate)
		if err != nil {
			return fmt.Errorf("step SIGN: %w", err)
		}
		saveJSON(dir, "exit-certificate-signed.json", signedCert)
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

func runAllStepA(
	ctx context.Context, cfg *Config, dir string, targetBlock uint64, wrappedTokens []WrappedToken,
) (*StepAResult, error) {
	a1Result, err := RunStepA1(ctx, cfg, targetBlock)
	if err != nil {
		return nil, fmt.Errorf("step A1: %w", err)
	}
	saveJSON(dir, "step-a1-addresses.json", a1Result.Addresses)
	saveJSON(dir, "step-a1-failed-traces.json", a1Result.FailedTraces)

	a2Result, err := RunStepA2(ctx, cfg, a1Result.FailedTraces)
	if err != nil {
		return nil, fmt.Errorf("step A2: %w", err)
	}
	saveJSON(dir, "step-a2-addresses.json", a2Result.Addresses)

	combined := mergeAddresses(a1Result.Addresses, a2Result.Addresses)
	log.Infof("STEP A complete: %d addresses (A1: %d, A2 new: %d)",
		len(combined), len(a1Result.Addresses), len(combined)-len(a1Result.Addresses))
	saveJSON(dir, "step-a-addresses.json", combined)

	result := &StepAResult{
		Addresses:     combined,
		FailedTraces:  a1Result.FailedTraces,
		WrappedTokens: wrappedTokens,
	}
	if len(wrappedTokens) > 0 {
		log.Infof("Using %d wrapped tokens for balance scanning", len(wrappedTokens))
	}
	return result, nil
}

func runAllStepB(
	ctx context.Context, cfg *Config, dir string, targetBlock uint64, stepAResult *StepAResult,
) (*StepBResult, error) {
	stepBResult, err := RunStepB(ctx, cfg, targetBlock, stepAResult)
	if err != nil {
		return nil, fmt.Errorf("step B: %w", err)
	}
	saveJSON(dir, "step-b-eoa-balances.json", stepBResult.EOABalances)
	saveJSON(dir, "step-b-accumulated.json", stepBResult.Accumulated)
	saveJSON(dir, "step-b-contract-addresses.json", stepBResult.ContractAddresses)
	saveJSON(dir, "step-b2-detected-erc20s.json", stepBResult.DetectedERC20s)
	saveJSON(dir, "step-b2-discarded-erc20s.json", stepBResult.DiscardedERC20s)
	saveJSON(dir, "step-b3-erc20-holders.json", stepBResult.ERC20HolderBreakdowns)
	return stepBResult, nil
}

func runAllStepC(dir string, lbtEntries []LBTEntry, stepBResult *StepBResult) (*StepCResult, error) {
	if len(lbtEntries) == 0 {
		log.Warn("STEP C skipped: no LBT data available")
		return &StepCResult{}, nil
	}
	stepCResult, err := RunStepC(lbtEntries, stepBResult)
	if err != nil {
		return nil, fmt.Errorf("step C: %w", err)
	}
	saveJSON(dir, "step-c-sc-locked-values.json", stepCResult.SCLockedValues)
	saveJSON(dir, "step-c-holder-bridges.json", stepCResult.HolderBridges)
	return stepCResult, nil
}

func runAllStepF(
	ctx context.Context, cfg *Config, dir string,
	lbtEntries []LBTEntry,
	stepDCert *agglayertypes.Certificate,
	finalCert *agglayertypes.Certificate,
) (*agglayertypes.Certificate, error) {
	result, err := RunStepF(ctx, cfg, stepDCert, lbtEntries)
	if err != nil {
		return nil, fmt.Errorf("step F: %w", err)
	}
	saveJSON(dir, "step-f-token-balances.json", result.TokenBalances)
	saveJSON(dir, "step-f-checks.json", result.Checks)
	if result.CappedCertificate != nil {
		// Apply the same per-token caps to the final certificate (which may include step E exits).
		cappedFinal := *finalCert
		cappedFinal.BridgeExits = capCertificateExits(finalCert.BridgeExits, result.Checks)
		saveJSON(dir, "step-f-capped-certificate.json", &cappedFinal)
		log.Infof("🔧 Capped final certificate saved (%d → %d bridge exits)",
			len(finalCert.BridgeExits), len(cappedFinal.BridgeExits))
		return &cappedFinal, nil
	}
	return finalCert, nil
}

func runAllStepG(
	ctx context.Context, cfg *Config, dir string, targetBlock uint64,
	certificate *agglayertypes.Certificate, lbtEntries []LBTEntry,
) (*StepGResult, error) {
	g1Result, err := RunStepG1(ctx, cfg, targetBlock)
	if err != nil {
		return nil, fmt.Errorf("step G1: %w", err)
	}
	saveJSON(dir, "step-g1-shadow-fork-block.json", g1Result)

	result, err := RunStepG2(ctx, cfg, g1Result.ShadowForkBlock, certificate, lbtEntries)
	if err != nil {
		return nil, fmt.Errorf("step G2: %w", err)
	}
	saveJSON(dir, "step-g-new-local-exit-root.json", result)
	// RunStepG2 reorders certificate.BridgeExits to the shadow-fork deposit order; persist the
	// reordered certificate for inspection and parity with single-step mode.
	saveJSON(dir, "step-g-reordered-certificate.json", certificate)
	return result, nil
}

func runAllStepH(ctx context.Context, cfg *Config, dir string, gResult *StepGResult) (*StepHResult, error) {
	result, err := RunStepH(ctx, cfg, gResult)
	if err != nil {
		return nil, fmt.Errorf("step H: %w", err)
	}
	saveJSON(dir, "step-h-previous-local-exit-root.json", result)
	return result, nil
}

func runAllStepI(
	ctx context.Context, cfg *Config, dir string,
	certificate *agglayertypes.Certificate, gResult *StepGResult, hResult *StepHResult,
) error {
	if err := RunStepI(ctx, cfg, certificate, gResult, hResult); err != nil {
		return fmt.Errorf("step I: %w", err)
	}
	saveJSON(dir, "exit-certificate-final.json", certificate)
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

// saveStepEFiles persists step E outputs to disk. Always writes the unclaimed bridges and
// messages files; only writes the certificate when it is non-nil.
func saveStepEFiles(dir string, result *StepEResult) {
	if result == nil {
		return
	}
	saveJSON(dir, "step-e-unclaimed-bridges.json", result.UnclaimedBridges)
	saveJSON(dir, "step-e-unclaimed-messages.json", result.UnclaimedMessages)
	if result.FinalCertificate != nil {
		saveJSON(dir, "step-e-exit-certificate.json", result.FinalCertificate)
	}
}

func runAllStepE(
	ctx context.Context, cfg *Config, dir string, stepDCert *agglayertypes.Certificate,
) (*agglayertypes.Certificate, error) {
	if cfg.L1RPCURL == "" {
		log.Warn("STEP E skipped: no L1 RPC provided")
		return stepDCert, nil
	}
	result, err := RunStepE(ctx, cfg, stepDCert)
	saveStepEFiles(dir, result)
	if err != nil {
		return nil, fmt.Errorf("step E: %w", err)
	}
	return result.FinalCertificate, nil
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
	log.Infof("Target Block:     %s", cfg.TargetBlock.String())
	log.Infof("L2 Network ID:    %d", cfg.L2NetworkID)
	log.Infof("Exit Address:     %s", cfg.ExitAddress.Hex())
	log.Infof("Dest Network:     %d", cfg.DestinationNetwork)
	log.Infof("Output Dir:       %s", cfg.Options.OutputDir)
	log.Infof("Concurrency:      %d", cfg.Options.ConcurrencyLimit)
	log.Infof("Block Range:      %d", cfg.Options.BlockRange)
	log.Infof("RPC Batch Size:   %d", cfg.Options.RPCBatchSize)
	log.Infof("L2 Start Block:   %d", cfg.Options.L2StartBlock)
	if cfg.Options.AgglayerClient.GRPC != nil && cfg.Options.AgglayerClient.GRPC.URL != "" {
		log.Infof("Agglayer gRPC:    %s", cfg.Options.AgglayerClient.GRPC.URL)
	} else {
		log.Info("Agglayer gRPC:    (not configured — step submit will fail)")
	}
	if cfg.SignerConfig.Method != "" {
		log.Infof("Signer:           method=%s", cfg.SignerConfig.Method)
	} else {
		log.Info("Signer:           (not configured — certificate will not be signed)")
	}
}

// --- Single step ---

func runSingleStep(ctx context.Context, step string, cfg *Config) error {
	dir := cfg.Options.OutputDir
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	switch step {
	case "check":
		return runSingleCheck(ctx, cfg, dir)
	case "0":
		return runSingle0(ctx, cfg, dir)
	case "a":
		return runSingleA(ctx, cfg, dir)
	case "a1":
		return runSingleA1(ctx, cfg, dir)
	case "a2":
		return runSingleA2(ctx, cfg, dir)
	case "b":
		return runSingleB(ctx, cfg, dir)
	case "b1":
		return runSingleB1(ctx, cfg, dir)
	case "b2":
		return runSingleB2(ctx, cfg, dir)
	case "b3":
		return runSingleB3(ctx, cfg, dir)
	case "c":
		return runSingleC(dir)
	case "d":
		return runSingleD(cfg, dir)
	case "e":
		return runSingleE(ctx, cfg, dir)
	case "f":
		return runSingleF(ctx, cfg, dir)
	case "g":
		return runSingleG(ctx, cfg, dir)
	case "g1":
		return runSingleG1(ctx, cfg, dir)
	case "g2":
		return runSingleG2(ctx, cfg, dir)
	case "h":
		return runSingleH(ctx, cfg, dir)
	case "i":
		return runSingleI(ctx, cfg, dir)
	case "sign":
		return runSingleSign(ctx, cfg, dir)
	case "submit":
		return runSingleSubmit(ctx, cfg, dir)
	case "wait":
		return runSingleWait(ctx, cfg, dir)
	default:
		return fmt.Errorf(
			"unknown step: %s (use check, 0, a, a1, a2, b, b1, b2, b3, c, d, e, f, g, g1, g2, h, i, sign, submit, wait, or all)",
			step,
		)
	}
}

func runSingleCheck(ctx context.Context, cfg *Config, dir string) error {
	result, err := RunStepCheck(ctx, cfg)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-check-result.json", result)
	return nil
}

func runSingle0(ctx context.Context, cfg *Config, dir string) error {
	result, err := RunStep0(ctx, cfg)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-0-l2_target_block.json", result.TargetBlock)
	saveJSON(dir, "step-0-lbt.json", result.Entries)
	return nil
}

// runSingleA runs A1 then A2, producing all four output files.
func runSingleA(ctx context.Context, cfg *Config, dir string) error {
	if err := runSingleA1(ctx, cfg, dir); err != nil {
		return err
	}
	return runSingleA2(ctx, cfg, dir)
}

// runSingleA1 runs Step A1 and writes step-a1-addresses.json and step-a1-failed-traces.json.
func runSingleA1(ctx context.Context, cfg *Config, dir string) error {
	targetBlock, err := loadTargetBlock(dir)
	if err != nil {
		return err
	}
	result, err := RunStepA1(ctx, cfg, targetBlock)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-a1-addresses.json", result.Addresses)
	saveJSON(dir, "step-a1-failed-traces.json", result.FailedTraces)
	return nil
}

// runSingleA2 runs Step A2 and writes step-a2-addresses.json and step-a-addresses.json.
// Legacy step-a-* files are migrated to step-a1-* at startup (see Run), so they will
// already be in the correct location by the time this function is called.
func runSingleA2(ctx context.Context, cfg *Config, dir string) error {
	var failedTraces []FailedTrace
	if err := loadJSON(dir, "step-a1-failed-traces.json", &failedTraces); err != nil {
		return fmt.Errorf("load step A1 failed traces (run step a1 first): %w", err)
	}

	a2Result, err := RunStepA2(ctx, cfg, failedTraces)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-a2-addresses.json", a2Result.Addresses)

	var a1Addresses []common.Address
	if err := loadJSON(dir, "step-a1-addresses.json", &a1Addresses); err != nil {
		return fmt.Errorf("load step A1 addresses: %w", err)
	}
	log.Debugf("STEP A2 merging %d A2 addresses with %d A1 addresses", len(a2Result.Addresses), len(a1Addresses))
	combined := mergeAddresses(a1Addresses, a2Result.Addresses)
	log.Infof("STEP A complete: %d addresses (A1: %d, A2 new: %d)",
		len(combined), len(a1Addresses), len(combined)-len(a1Addresses))
	saveJSON(dir, "step-a-addresses.json", combined)
	return nil
}

// migrateStepAToA1 renames legacy step-a-* output files to step-a1-* when the A1 files
// are absent. This allows step A2 to be run after a pipeline that predates the A1/A2 split.
func migrateStepAToA1(dir string) error {
	rename := func(oldName, newName string) error {
		oldPath := filepath.Join(dir, oldName)
		newPath := filepath.Join(dir, newName)
		if _, err := os.Stat(newPath); err == nil {
			return nil // new file already exists — nothing to do
		}
		if _, err := os.Stat(oldPath); err != nil {
			return nil // old file also absent — nothing to do
		}
		log.Infof("Migrating %s → %s", oldName, newName)
		if err := os.Rename(oldPath, newPath); err != nil {
			return fmt.Errorf("rename %s: %w", oldName, err)
		}
		return nil
	}
	if err := rename("step-a-addresses.json", "step-a1-addresses.json"); err != nil {
		return err
	}
	return rename("step-a-failed-traces.json", "step-a1-failed-traces.json")
}

// runSingleB runs B1 then B2 then B3, producing all step-b* output files.
func runSingleB(ctx context.Context, cfg *Config, dir string) error {
	if err := runSingleB1(ctx, cfg, dir); err != nil {
		return err
	}
	if err := runSingleB2(ctx, cfg, dir); err != nil {
		return err
	}
	return runSingleB3(ctx, cfg, dir)
}

// runSingleB1 runs Step B1 and writes step-b-eoa-balances.json,
// step-b-accumulated.json, and step-b-contract-addresses.json.
func runSingleB1(ctx context.Context, cfg *Config, dir string) error {
	var addresses []common.Address
	if err := loadJSON(dir, "step-a-addresses.json", &addresses); err != nil {
		return fmt.Errorf("load step A output: %w", err)
	}
	wrappedTokens, err := loadWrappedTokensFromLBT(dir)
	if err != nil {
		return err
	}
	log.Infof("Using %d wrapped tokens for balance scanning", len(wrappedTokens))

	targetBlock, err := loadTargetBlock(dir)
	if err != nil {
		return err
	}
	result, err := RunStepB1(ctx, cfg, targetBlock, &StepAResult{
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

// runSingleB2 runs Step B2 and writes step-b2-detected-erc20s.json and
// step-b2-discarded-erc20s.json.
// Requires step-b-contract-addresses.json from B1, step-a-addresses.json, and step-0-lbt.json.
func runSingleB2(ctx context.Context, cfg *Config, dir string) error {
	var contractAddrs []common.Address
	if err := loadJSON(dir, "step-b-contract-addresses.json", &contractAddrs); err != nil {
		return fmt.Errorf("load step B1 contract addresses (run step b1 first): %w", err)
	}
	var allAddresses []common.Address
	if err := loadJSON(dir, "step-a-addresses.json", &allAddresses); err != nil {
		return fmt.Errorf("load step A addresses: %w", err)
	}
	eoaAddrs := filterEOAs(allAddresses, contractAddrs)

	wrappedTokens, err := loadWrappedTokensFromLBT(dir)
	if err != nil {
		return err
	}

	targetBlock, err := loadTargetBlock(dir)
	if err != nil {
		return err
	}
	result, err := RunStepB2(ctx, cfg, targetBlock, contractAddrs, eoaAddrs, wrappedTokens)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-b2-detected-erc20s.json", result.DetectedERC20s)
	saveJSON(dir, "step-b2-discarded-erc20s.json", result.DiscardedERC20s)
	return nil
}

// runSingleB3 runs Step B3 and writes step-b3-erc20-holders.json.
// Requires step-b2-detected-erc20s.json from B2, step-b-contract-addresses.json from B1,
// step-a-addresses.json, and step-0-l2_target_block.json.
func runSingleB3(ctx context.Context, cfg *Config, dir string) error {
	var contractAddrs []common.Address
	if err := loadJSON(dir, "step-b-contract-addresses.json", &contractAddrs); err != nil {
		return fmt.Errorf("load step B1 contract addresses (run step b1 first): %w", err)
	}
	var allAddresses []common.Address
	if err := loadJSON(dir, "step-a-addresses.json", &allAddresses); err != nil {
		return fmt.Errorf("load step A addresses: %w", err)
	}
	eoaAddrs := filterEOAs(allAddresses, contractAddrs)

	var detectedERC20s []DetectedERC20
	if err := loadJSON(dir, "step-b2-detected-erc20s.json", &detectedERC20s); err != nil {
		return fmt.Errorf("load step B2 detected ERC-20s (run step b2 first): %w", err)
	}
	b2Result := &StepB2Result{DetectedERC20s: detectedERC20s}

	targetBlock, err := loadTargetBlock(dir)
	if err != nil {
		return err
	}
	result, err := RunStepB3(ctx, cfg, targetBlock, eoaAddrs, b2Result)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-b3-erc20-holders.json", result.Breakdowns)
	return nil
}

func runSingleC(dir string) error {
	var accumulated []AccumulatedBalance
	if err := loadJSON(dir, "step-b-accumulated.json", &accumulated); err != nil {
		return fmt.Errorf("load step B output: %w", err)
	}
	var lbtEntries []LBTEntry
	if err := loadJSON(dir, "step-0-lbt.json", &lbtEntries); err != nil {
		return fmt.Errorf("load LBT data (step 0): %w", err)
	}
	// Load holder breakdowns from B3 if available; absence is not an error.
	var breakdowns []ERC20HolderBreakdown
	_ = loadJSON(dir, "step-b3-erc20-holders.json", &breakdowns)

	result, err := RunStepC(lbtEntries, &StepBResult{
		Accumulated:           accumulated,
		ERC20HolderBreakdowns: breakdowns,
	})
	if err != nil {
		return err
	}
	saveJSON(dir, "step-c-sc-locked-values.json", result.SCLockedValues)
	saveJSON(dir, "step-c-holder-bridges.json", result.HolderBridges)
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
	var holderBridges []HolderBridge
	_ = loadJSON(dir, "step-c-holder-bridges.json", &holderBridges)

	result, err := RunStepD(cfg, &StepBResult{EOABalances: eoaBalances}, &StepCResult{
		SCLockedValues: scLockedValues,
		HolderBridges:  holderBridges,
	})
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
	saveStepEFiles(dir, result)
	return err
}

func runSingleSign(ctx context.Context, cfg *Config, dir string) error {
	var cert agglayertypes.Certificate
	if err := loadJSON(dir, "exit-certificate-final.json", &cert); err != nil {
		return fmt.Errorf("load final certificate: %w", err)
	}
	signed, err := RunStepSign(ctx, cfg, &cert)
	if err != nil {
		return err
	}
	saveJSON(dir, "exit-certificate-signed.json", signed)
	return nil
}

func runSingleSubmit(ctx context.Context, cfg *Config, dir string) error {
	var cert agglayertypes.Certificate
	if err := loadJSON(dir, "exit-certificate-signed.json", &cert); err != nil {
		return fmt.Errorf("load signed certificate: %w", err)
	}
	result, err := RunStepSubmit(ctx, cfg, &cert)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-submit-result.json", result)
	return nil
}

func runSingleWait(ctx context.Context, cfg *Config, dir string) error {
	var submitResult StepSubmitResult
	if err := loadJSON(dir, "step-submit-result.json", &submitResult); err != nil {
		return fmt.Errorf("load step submit result: %w", err)
	}
	result, err := RunStepWait(ctx, cfg, submitResult.CertificateHash)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-wait-result.json", result)
	return nil
}

func runSingleF(ctx context.Context, cfg *Config, dir string) error {
	var cert certificateJSON
	if err := loadJSON(dir, "step-d-exit-certificate.json", &cert); err != nil {
		return fmt.Errorf("load step D certificate: %w", err)
	}

	// Try to load LBT entries for three-way comparison; nil disables LBT check.
	var lbtEntries []LBTEntry
	lbtPath := filepath.Join(dir, "step-0-lbt.json")
	if entries, err := LoadLBTEntries(lbtPath); err == nil {
		lbtEntries = entries
	} else {
		log.Warnf("STEP F: LBT data not available, falling back to two-way comparison: %v", err)
	}

	result, err := RunStepF(ctx, cfg, cert.toAgglayerCertificate(), lbtEntries)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-f-token-balances.json", result.TokenBalances)
	saveJSON(dir, "step-f-checks.json", result.Checks)
	if result.CappedCertificate != nil {
		saveJSON(dir, "step-f-capped-certificate.json", result.CappedCertificate)
	}
	return nil
}

// fileExists reports whether path exists and is accessible.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runSingleG runs G1 then G2, producing the step-g1 and step-g output files.
func runSingleG(ctx context.Context, cfg *Config, dir string) error {
	if err := runSingleG1(ctx, cfg, dir); err != nil {
		return err
	}
	return runSingleG2(ctx, cfg, dir)
}

// runSingleG1 runs Step G1 and writes step-g1-shadow-fork-block.json (the block Step G2 forks at).
func runSingleG1(ctx context.Context, cfg *Config, dir string) error {
	targetBlock, err := loadTargetBlock(dir)
	if err != nil {
		return err
	}
	result, err := RunStepG1(ctx, cfg, targetBlock)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-g1-shadow-fork-block.json", result)
	return nil
}

// runSingleG2 runs Step G2: it loads the shadow-fork block from G1, the certificate (capped from F
// or from E), and the LBT entries, then writes step-g-new-local-exit-root.json and the reordered
// step-g-reordered-certificate.json.
func runSingleG2(ctx context.Context, cfg *Config, dir string) error {
	var g1Result StepG1Result
	if err := loadJSON(dir, "step-g1-shadow-fork-block.json", &g1Result); err != nil {
		return fmt.Errorf("load step G1 result (run step g1 first): %w", err)
	}

	var cert certificateJSON
	cappedPath := filepath.Join(dir, "step-f-capped-certificate.json")
	if _, err := os.Stat(cappedPath); err == nil {
		if err := loadJSON(dir, "step-f-capped-certificate.json", &cert); err != nil {
			return fmt.Errorf("load step F capped certificate: %w", err)
		}
		log.Warn("⚠️  Using capped certificate from step F (step-f-capped-certificate.json)")
	} else {
		if err := loadJSON(dir, "step-e-exit-certificate.json", &cert); err != nil {
			return fmt.Errorf("load step E certificate: %w", err)
		}
		log.Info("Using certificate from step E (step-e-exit-certificate.json)")
	}

	lbtPath := filepath.Join(dir, "step-0-lbt.json")
	var lbtEntries []LBTEntry
	if entries, err := LoadLBTEntries(lbtPath); err == nil {
		lbtEntries = entries
		log.Infof("STEP G2: loaded %d LBT entries for token resolution", len(lbtEntries))
	} else {
		log.Warnf("STEP G2: LBT not available, falling back to getTokenWrappedAddress: %v", err)
	}

	aggCert := cert.toAgglayerCertificate()
	result, err := RunStepG2(ctx, cfg, g1Result.ShadowForkBlock, aggCert, lbtEntries)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-g-new-local-exit-root.json", result)
	// RunStepG2 reorders aggCert.BridgeExits to the shadow-fork deposit order. Persist it so the
	// single-step Step I picks up the reordered exits instead of the pre-G ordering.
	saveJSON(dir, "step-g-reordered-certificate.json", aggCert)
	return nil
}

func runSingleH(ctx context.Context, cfg *Config, dir string) error {
	var gResult StepGResult
	if err := loadJSON(dir, "step-g-new-local-exit-root.json", &gResult); err != nil {
		return fmt.Errorf("load step G result: %w", err)
	}
	result, err := RunStepH(ctx, cfg, &gResult)
	if err != nil {
		return err
	}
	saveJSON(dir, "step-h-previous-local-exit-root.json", result)
	return nil
}

func runSingleI(ctx context.Context, cfg *Config, dir string) error {
	var cert certificateJSON
	reorderedPath := filepath.Join(dir, "step-g-reordered-certificate.json")
	cappedPath := filepath.Join(dir, "step-f-capped-certificate.json")
	switch {
	case fileExists(reorderedPath):
		// Step G reorders the bridge exits to the shadow-fork deposit order; this is the
		// authoritative ordering that matches the computed NewLocalExitRoot.
		if err := loadJSON(dir, "step-g-reordered-certificate.json", &cert); err != nil {
			return fmt.Errorf("load step G reordered certificate: %w", err)
		}
		log.Info("Using reordered certificate from step G (step-g-reordered-certificate.json)")
	case fileExists(cappedPath):
		if err := loadJSON(dir, "step-f-capped-certificate.json", &cert); err != nil {
			return fmt.Errorf("load step F capped certificate: %w", err)
		}
		log.Warn("⚠️  Using capped certificate from step F (step-f-capped-certificate.json)")
	default:
		if err := loadJSON(dir, "step-e-exit-certificate.json", &cert); err != nil {
			return fmt.Errorf("load step E certificate: %w", err)
		}
		log.Info("Using certificate from step E (step-e-exit-certificate.json)")
	}
	var gResult StepGResult
	if err := loadJSON(dir, "step-g-new-local-exit-root.json", &gResult); err != nil {
		return fmt.Errorf("load step G result: %w", err)
	}
	var hResult StepHResult
	if err := loadJSON(dir, "step-h-previous-local-exit-root.json", &hResult); err != nil {
		return fmt.Errorf("load step H result: %w", err)
	}
	aggCert := cert.toAgglayerCertificate()
	if err := RunStepI(ctx, cfg, aggCert, &gResult, &hResult); err != nil {
		return err
	}
	saveJSON(dir, "exit-certificate-final.json", aggCert)
	return nil
}

// --- LBT resolution ---

// resolveOrGenerateLBT always runs Step 0 and saves step-0-lbt.json.
func resolveOrGenerateLBT(ctx context.Context, cfg *Config, dir string) ([]LBTEntry, []WrappedToken, uint64, error) {
	result, err := RunStep0(ctx, cfg)
	if err != nil {
		return nil, nil, 0, err
	}
	saveJSON(dir, "step-0-l2_target_block.json", result.TargetBlock)
	saveJSON(dir, "step-0-lbt.json", result.Entries)
	return result.Entries, LBTEntriesToWrappedTokens(result.Entries), result.TargetBlock, nil
}

// loadTargetBlock reads the resolved L2 target block number saved by Step 0.
func loadTargetBlock(dir string) (uint64, error) {
	var n uint64
	if err := loadJSON(dir, "step-0-l2_target_block.json", &n); err != nil {
		return 0, fmt.Errorf("load target block (run step 0 first): %w", err)
	}
	return n, nil
}

// loadWrappedTokensFromLBT loads tokens from the step-0 output.
func loadWrappedTokensFromLBT(dir string) ([]WrappedToken, error) {
	tokens, err := LoadLBTWrappedTokens(filepath.Join(dir, "step-0-lbt.json"))
	if err != nil {
		return nil, fmt.Errorf("no LBT data available: run step 0 first")
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
