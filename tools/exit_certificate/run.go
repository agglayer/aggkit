package exit_certificate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	aggkit "github.com/agglayer/aggkit"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v2"
)

const (
	dirPermissions  = 0o755
	filePermissions = 0o600
)

// printStartupBanner logs a one-off traceability banner with the build version
// info, the config file path + sha256, and the (shell-escaped) command line.
// cfg.ConfigSHA256 is computed from the exact bytes LoadConfig parsed, so the
// hash always matches the config actually used for the run.
func printStartupBanner(cfg *Config) {
	v := aggkit.GetVersion()

	log.Info("╔═══════════════════════════════════════════╗")
	log.Info("║   Exit Certificate Tool — Traceability    ║")
	log.Info("╚═══════════════════════════════════════════╝")
	log.Infof("Version:      %s", v.Version)
	log.Infof("Git revision: %s", v.GitRev)
	log.Infof("Git branch:   %s", v.GitBranch)
	log.Infof("Built:        %s", v.BuildDate)
	log.Infof("Go version:   %s", v.GoVersion)
	log.Infof("OS/Arch:      %s/%s", v.OS, v.Arch)
	log.Infof("Config file:  %s (sha256: %s)", cfg.ConfigPath, cfg.ConfigSHA256)
	log.Infof("Command line: %s", shellQuoteArgs(os.Args))
}

// shellQuoteArgs joins argv into a single, copy-pasteable command line,
// single-quoting any argument that contains characters the shell would
// otherwise interpret (spaces, quotes, globs, …) so argument boundaries are
// preserved. Empty arguments become ”.
func shellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// shellQuote returns a POSIX-shell-safe representation of a single argument.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool { return !isShellSafeRune(r) }) < 0 {
		return s // only safe characters, no quoting needed
	}
	// Wrap in single quotes, escaping any embedded single quote as '\''.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isShellSafeRune reports whether r can appear unquoted in a POSIX shell word.
func isShellSafeRune(r rune) bool {
	return r == '_' || r == '-' || r == '.' || r == '/' || r == '=' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

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

	printStartupBanner(cfg)

	if err := os.MkdirAll(cfg.Options.OutputDir, dirPermissions); err != nil {
		return fmt.Errorf("create output dir: %w", err)
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
// "b" and "g" are aliases for their sub-steps and are handled in parseStepList; not listed here.
var orderedSteps = []string{
	"check", "0", "a", "b1", "b2", "b3", "c", "d", "e", "f", "g1", "g2", "h", "i", "sign", "submit", "wait",
}

// lastAutoStep is the implicit end for open ranges (X-).
// "submit" and "wait" must always be specified explicitly.
const lastAutoStep = "sign"

// parseStepList splits a comma-separated step list, expanding range notation.
// "f-i"  → ["f", "g", "h", "i"]
// "f-"   → ["f", "g", "h", "i", "sign", "submit", "wait"]
// "h, i, sign" → ["h", "i", "sign"]
// "b"    → ["b1", "b2", "b3"] (alias for all three sub-steps)
// "g"    → ["g1", "g2"] (alias for both sub-steps)
// "a-b"  → ["a", "b1", "b2", "b3"] ("b"→"b3" as range end)
func parseStepList(raw string) ([]string, error) {
	var steps []string
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if strings.Contains(token, "-") {
			// Map "b"/"g" to their sub-step boundaries before expanding ranges.
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
	// When the range starts at or after submit/wait (i.e. past lastAutoStep), the user has
	// explicitly opted into those steps, so an open range extends to the last step instead.
	if fromIdx > toIdx {
		toIdx = len(orderedSteps) - 1
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
	return hexToUint64(hex)
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
	if err := saveJSON(dir, fileStepCheckResult, checkResult); err != nil {
		return err
	}

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

	stepCResult, err := runAllStepC(ctx, cfg, dir, lbtEntries, stepBResult)
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

	finalCertificate, err = runAllStepF(
		ctx, cfg, dir, lbtEntries, stepCResult.SCLockedValues, stepDResult.Certificate, finalCertificate)
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
		if err := saveJSON(dir, fileSignedCertificate, signedCert); err != nil {
			return err
		}
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
	result, err := RunStepA(ctx, cfg, targetBlock, wrappedTokens)
	if err != nil {
		return nil, fmt.Errorf("step A: %w", err)
	}
	if err := saveJSON(dir, fileStepAAddresses, result.Addresses); err != nil {
		return nil, err
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
	if err := saveStepBFiles(dir, stepBResult); err != nil {
		return nil, err
	}
	return stepBResult, nil
}

// saveStepBFiles persists every Step B output file (B1 + B2 + B3).
func saveStepBFiles(dir string, stepBResult *StepBResult) error {
	b1 := &StepB1Result{
		EOABalances:       stepBResult.EOABalances,
		Accumulated:       stepBResult.Accumulated,
		ContractAddresses: stepBResult.ContractAddresses,
	}
	if err := saveStepB1Files(dir, b1); err != nil {
		return err
	}
	if err := saveJSON(dir, fileStepB2DetectedERC20s, stepBResult.DetectedERC20s); err != nil {
		return err
	}
	if err := saveJSON(dir, fileStepB2DiscardedERC20s, stepBResult.DiscardedERC20s); err != nil {
		return err
	}
	return saveJSON(dir, fileStepB3ERC20Holders, stepBResult.ERC20HolderBreakdowns)
}

// saveStepB1Files persists the Step B1 output files.
func saveStepB1Files(dir string, result *StepB1Result) error {
	if err := saveJSON(dir, fileStepBEOABalances, result.EOABalances); err != nil {
		return err
	}
	if err := saveJSON(dir, fileStepBAccumulated, result.Accumulated); err != nil {
		return err
	}
	return saveJSON(dir, fileStepBContractAddresses, result.ContractAddresses)
}

func runAllStepC(
	ctx context.Context, cfg *Config, dir string, lbtEntries []LBTEntry, stepBResult *StepBResult,
) (*StepCResult, error) {
	if len(lbtEntries) == 0 {
		log.Warn("STEP C skipped: no LBT data available")
		return &StepCResult{}, nil
	}
	if err := applyNativeContractLocked(ctx, cfg, dir, stepBResult); err != nil {
		return nil, fmt.Errorf("step C: %w", err)
	}
	stepCResult, err := RunStepC(lbtEntries, stepBResult)
	if err != nil {
		return nil, fmt.Errorf("step C: %w", err)
	}
	if err := saveStepCFiles(dir, stepCResult); err != nil {
		return nil, err
	}
	return stepCResult, nil
}

// saveStepCFiles persists the Step C output files.
func saveStepCFiles(dir string, stepCResult *StepCResult) error {
	if err := saveJSON(dir, fileStepCSCLockedValues, stepCResult.SCLockedValues); err != nil {
		return err
	}
	return saveJSON(dir, fileStepCHolderBridges, stepCResult.HolderBridges)
}

func runAllStepF(
	ctx context.Context, cfg *Config, dir string,
	lbtEntries []LBTEntry,
	scLockedValues []SCLockedValue,
	stepDCert *agglayertypes.Certificate,
	finalCert *agglayertypes.Certificate,
) (*agglayertypes.Certificate, error) {
	// RunStepF itself honours useAgglayerAdminToStepFCheck: when false it runs the offline LBT vs
	// certificate comparison instead of the agglayer admin query.
	result, err := RunStepF(ctx, cfg, stepDCert, lbtEntries, scLockedValues)
	if err != nil {
		return nil, fmt.Errorf("step F: %w", err)
	}
	if result.TokenBalances != nil {
		if err := saveJSON(dir, fileStepFTokenBalances, result.TokenBalances); err != nil {
			return nil, err
		}
	}
	if err := saveJSON(dir, fileStepFChecks, result.Checks); err != nil {
		return nil, err
	}
	if result.CappedCertificate != nil {
		// Apply the same per-token caps to the final certificate (which may include step E exits).
		cappedFinal := *finalCert
		cappedExits, err := capCertificateExits(finalCert.BridgeExits, result.Checks, cfg.Options.CapMode)
		if err != nil {
			return nil, fmt.Errorf("step F: capping the final certificate: %w", err)
		}
		cappedFinal.BridgeExits = cappedExits
		if err := saveJSON(dir, fileStepFCappedCertificate, &cappedFinal); err != nil {
			return nil, err
		}
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
	if err := saveJSON(dir, fileStepG1ShadowForkBlock, g1Result); err != nil {
		return nil, err
	}

	result, err := RunStepG2(ctx, cfg, g1Result.ShadowForkBlock, certificate, lbtEntries)
	if err != nil {
		return nil, fmt.Errorf("step G2: %w", err)
	}
	if err := saveJSON(dir, fileStepGNewLocalExitRoot, result); err != nil {
		return nil, err
	}
	// RunStepG2 keeps the certificate's deterministic exit order (it never reorders); persist the
	// certificate as Step G left it for inspection and parity with single-step mode (the file name is
	// kept for compatibility).
	if err := saveJSON(dir, fileStepGReorderedCertificate, certificate); err != nil {
		return nil, err
	}
	return result, nil
}

func runAllStepH(ctx context.Context, cfg *Config, dir string, gResult *StepGResult) (*StepHResult, error) {
	result, err := RunStepH(ctx, cfg, gResult)
	if err != nil {
		return nil, fmt.Errorf("step H: %w", err)
	}
	if err := saveJSON(dir, fileStepHPreviousLocalExitRoot, result); err != nil {
		return nil, err
	}
	return result, nil
}

func runAllStepI(
	ctx context.Context, cfg *Config, dir string,
	certificate *agglayertypes.Certificate, gResult *StepGResult, hResult *StepHResult,
) error {
	if err := RunStepI(ctx, cfg, certificate, gResult, hResult); err != nil {
		return fmt.Errorf("step I: %w", err)
	}
	return saveJSON(dir, fileFinalCertificate, certificate)
}

func runAllStepD(cfg *Config, dir string, stepBResult *StepBResult, stepCResult *StepCResult) (*StepDResult, error) {
	stepDResult, err := RunStepD(cfg, stepBResult, stepCResult)
	if err != nil {
		return nil, fmt.Errorf("step D: %w", err)
	}
	if err := saveJSON(dir, fileStepDCertificate, stepDResult.Certificate); err != nil {
		return nil, err
	}
	return stepDResult, nil
}

// saveStepEFiles persists step E outputs to disk. Always writes the unclaimed bridges and
// messages files; only writes the certificate when it is non-nil.
func saveStepEFiles(dir string, result *StepEResult) error {
	if result == nil {
		return nil
	}
	if err := saveJSON(dir, fileStepEUnclaimedBridges, result.UnclaimedBridges); err != nil {
		return err
	}
	if err := saveJSON(dir, fileStepEUnclaimedMsgs, result.UnclaimedMessages); err != nil {
		return err
	}
	if result.FinalCertificate != nil {
		return saveJSON(dir, fileStepECertificate, result.FinalCertificate)
	}
	return nil
}

func runAllStepE(
	ctx context.Context, cfg *Config, dir string, stepDCert *agglayertypes.Certificate,
) (*agglayertypes.Certificate, error) {
	if cfg.L1RPCURL == "" {
		log.Warn("STEP E skipped: no L1 RPC provided")
		return stepDCert, nil
	}
	result, err := RunStepE(ctx, cfg, stepDCert)
	if saveErr := saveStepEFiles(dir, result); saveErr != nil && err == nil {
		return nil, fmt.Errorf("step E: %w", saveErr)
	}
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
	if cfg.Options.SkipSCLockedValue {
		log.Info("Exit Address:     (unused — skipSCLockedValue=true, SC-locked funds left behind)")
	} else {
		log.Infof("Exit Address:     %s", cfg.ExitAddress.Hex())
	}
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
	case "b":
		return runSingleB(ctx, cfg, dir)
	case "b1":
		return runSingleB1(ctx, cfg, dir)
	case "b2":
		return runSingleB2(ctx, cfg, dir)
	case "b3":
		return runSingleB3(ctx, cfg, dir)
	case "c":
		return runSingleC(ctx, cfg, dir)
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
			"unknown step: %s (use check, 0, a, b, b1, b2, b3, c, d, e, f, g, g1, g2, h, i, sign, submit, wait, or all)",
			step,
		)
	}
}

func runSingleCheck(ctx context.Context, cfg *Config, dir string) error {
	result, err := RunStepCheck(ctx, cfg)
	if err != nil {
		return err
	}
	return saveJSON(dir, fileStepCheckResult, result)
}

func runSingle0(ctx context.Context, cfg *Config, dir string) error {
	result, err := RunStep0(ctx, cfg)
	if err != nil {
		return err
	}
	if err := saveJSON(dir, fileStep0TargetBlock, result.TargetBlock); err != nil {
		return err
	}
	return saveJSON(dir, fileStep0LBT, result.Entries)
}

// runSingleA runs Step A (state dump + Transfer logs) and writes step-a-addresses.json.
// It needs the target block and the LBT from Step 0; without the LBT the Transfer-log
// holder discovery is skipped with a warning.
func runSingleA(ctx context.Context, cfg *Config, dir string) error {
	targetBlock, err := loadTargetBlock(dir)
	if err != nil {
		return err
	}
	wrappedTokens, err := loadWrappedTokensFromLBT(dir)
	if err != nil {
		log.Warnf("STEP A: no LBT wrapped tokens available (%v); "+
			"Transfer-log holder discovery will be skipped", err)
	}
	result, err := RunStepA(ctx, cfg, targetBlock, wrappedTokens)
	if err != nil {
		return err
	}
	return saveJSON(dir, fileStepAAddresses, result.Addresses)
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
	if err := loadJSON(dir, fileStepAAddresses, &addresses); err != nil {
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
	return saveStepB1Files(dir, result)
}

// runSingleB2 runs Step B2 and writes step-b2-detected-erc20s.json and
// step-b2-discarded-erc20s.json.
// Requires step-b-contract-addresses.json from B1, step-a-addresses.json, and step-0-lbt.json.
func runSingleB2(ctx context.Context, cfg *Config, dir string) error {
	var contractAddrs []common.Address
	if err := loadJSON(dir, fileStepBContractAddresses, &contractAddrs); err != nil {
		return fmt.Errorf("load step B1 contract addresses (run step b1 first): %w", err)
	}
	var allAddresses []common.Address
	if err := loadJSON(dir, fileStepAAddresses, &allAddresses); err != nil {
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
	if err := saveJSON(dir, fileStepB2DetectedERC20s, result.DetectedERC20s); err != nil {
		return err
	}
	return saveJSON(dir, fileStepB2DiscardedERC20s, result.DiscardedERC20s)
}

// runSingleB3 runs Step B3 and writes step-b3-erc20-holders.json.
// Requires step-b2-detected-erc20s.json from B2, step-b-contract-addresses.json from B1,
// step-a-addresses.json, and step-0-l2_target_block.json.
func runSingleB3(ctx context.Context, cfg *Config, dir string) error {
	var contractAddrs []common.Address
	if err := loadJSON(dir, fileStepBContractAddresses, &contractAddrs); err != nil {
		return fmt.Errorf("load step B1 contract addresses (run step b1 first): %w", err)
	}
	var allAddresses []common.Address
	if err := loadJSON(dir, fileStepAAddresses, &allAddresses); err != nil {
		return fmt.Errorf("load step A addresses: %w", err)
	}
	eoaAddrs := filterEOAs(allAddresses, contractAddrs)

	var detectedERC20s []DetectedERC20
	if err := loadJSON(dir, fileStepB2DetectedERC20s, &detectedERC20s); err != nil {
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
	return saveJSON(dir, fileStepB3ERC20Holders, result.Breakdowns)
}

func runSingleC(ctx context.Context, cfg *Config, dir string) error {
	var accumulated []AccumulatedBalance
	if err := loadJSON(dir, fileStepBAccumulated, &accumulated); err != nil {
		return fmt.Errorf("load step B output: %w", err)
	}
	var lbtEntries []LBTEntry
	if err := loadJSON(dir, fileStep0LBT, &lbtEntries); err != nil {
		return fmt.Errorf("load LBT data (step 0): %w", err)
	}
	// Load holder breakdowns from B3 if available; absence is not an error, corruption is.
	var breakdowns []ERC20HolderBreakdown
	if err := loadOptionalJSON(dir, fileStepB3ERC20Holders, &breakdowns); err != nil {
		return fmt.Errorf("load step B3 output: %w", err)
	}

	stepB := &StepBResult{Accumulated: accumulated, ERC20HolderBreakdowns: breakdowns}
	if err := applyNativeContractLocked(ctx, cfg, dir, stepB); err != nil {
		return err
	}

	result, err := RunStepC(lbtEntries, stepB)
	if err != nil {
		return err
	}
	return saveStepCFiles(dir, result)
}

// applyNativeContractLocked computes the native token's SC-locked value from contract ETH balances
// (options.nativeSCLockedFromContracts) and stores it on stepB. Contract addresses are taken from
// stepB (in-memory pipeline) or loaded from step-b-contract-addresses.json (single-step runs). No-op
// when the option is disabled.
func applyNativeContractLocked(ctx context.Context, cfg *Config, dir string, stepB *StepBResult) error {
	if !cfg.Options.NativeSCLockedFromContracts {
		return nil
	}
	contractAddrs := stepB.ContractAddresses
	if len(contractAddrs) == 0 {
		if err := loadJSON(dir, fileStepBContractAddresses, &contractAddrs); err != nil {
			return fmt.Errorf("load contract addresses for native SC-locked (run step b first): %w", err)
		}
	}
	targetBlock, err := loadTargetBlock(dir)
	if err != nil {
		return err
	}
	total, err := sumContractNativeBalances(ctx, cfg, contractAddrs, toBlockTag(targetBlock))
	if err != nil {
		return fmt.Errorf("compute native SC-locked from contracts: %w", err)
	}
	stepB.NativeContractLocked = total.String()
	return nil
}

func runSingleD(cfg *Config, dir string) error {
	var eoaBalances []EOABalance
	if err := loadJSON(dir, fileStepBEOABalances, &eoaBalances); err != nil {
		return fmt.Errorf("load step B output: %w", err)
	}
	var scLockedValues []SCLockedValue
	if err := loadJSON(dir, fileStepCSCLockedValues, &scLockedValues); err != nil {
		return fmt.Errorf("load step C output: %w", err)
	}
	// Holder bridges from Step C are optional; absence is not an error, corruption is.
	var holderBridges []HolderBridge
	if err := loadOptionalJSON(dir, fileStepCHolderBridges, &holderBridges); err != nil {
		return fmt.Errorf("load step C holder bridges: %w", err)
	}

	result, err := RunStepD(cfg, &StepBResult{EOABalances: eoaBalances}, &StepCResult{
		SCLockedValues: scLockedValues,
		HolderBridges:  holderBridges,
	})
	if err != nil {
		return err
	}
	return saveJSON(dir, fileStepDCertificate, result.Certificate)
}

func runSingleE(ctx context.Context, cfg *Config, dir string) error {
	if cfg.L1RPCURL == "" {
		return fmt.Errorf("step E requires l1RpcUrl in parameters")
	}
	var cert certificateJSON
	if err := loadJSON(dir, fileStepDCertificate, &cert); err != nil {
		return fmt.Errorf("load step D output: %w", err)
	}
	aggCert, err := cert.toAgglayerCertificate()
	if err != nil {
		return fmt.Errorf("load step D certificate: %w", err)
	}
	result, err := RunStepE(ctx, cfg, aggCert)
	if saveErr := saveStepEFiles(dir, result); saveErr != nil && err == nil {
		return saveErr
	}
	return err
}

func runSingleSign(ctx context.Context, cfg *Config, dir string) error {
	var cert agglayertypes.Certificate
	if err := loadJSON(dir, fileFinalCertificate, &cert); err != nil {
		return fmt.Errorf("load final certificate: %w", err)
	}
	signed, err := RunStepSign(ctx, cfg, &cert)
	if err != nil {
		return err
	}
	return saveJSON(dir, fileSignedCertificate, signed)
}

func runSingleSubmit(ctx context.Context, cfg *Config, dir string) error {
	var cert agglayertypes.Certificate
	if err := loadJSON(dir, fileSignedCertificate, &cert); err != nil {
		return fmt.Errorf("load signed certificate: %w", err)
	}
	result, err := RunStepSubmit(ctx, cfg, &cert)
	if err != nil {
		return err
	}
	return saveJSON(dir, fileStepSubmitResult, result)
}

func runSingleWait(ctx context.Context, cfg *Config, dir string) error {
	var submitResult StepSubmitResult
	if err := loadJSON(dir, fileStepSubmitResult, &submitResult); err != nil {
		return fmt.Errorf("load step submit result: %w", err)
	}
	result, err := RunStepWait(ctx, cfg, &submitResult)
	if err != nil {
		return err
	}
	return saveJSON(dir, fileStepWaitResult, result)
}

func runSingleF(ctx context.Context, cfg *Config, dir string) error {
	var cert certificateJSON
	if err := loadJSON(dir, fileStepDCertificate, &cert); err != nil {
		return fmt.Errorf("load step D certificate: %w", err)
	}

	// Load LBT entries: used for the three-way comparison (agglayer mode) or the offline LBT vs
	// certificate comparison (useAgglayerAdminToStepFCheck=false). nil disables the LBT check.
	var lbtEntries []LBTEntry
	lbtPath := filepath.Join(dir, fileStep0LBT)
	if entries, err := LoadLBTEntries(lbtPath); err == nil {
		lbtEntries = entries
	} else {
		log.Warnf("STEP F: LBT data not available, falling back to two-way comparison: %v", err)
	}

	// With skipSCLockedValue enabled, Step D omitted the SC-locked exits from the certificate, so
	// Step F needs the Step C amounts to discount them from the LBT/agglayer budgets. Required in
	// that case: without them every affected token would report a misleading mismatch.
	var scLockedValues []SCLockedValue
	if cfg.Options.SkipSCLockedValue {
		if err := loadJSON(dir, fileStepCSCLockedValues, &scLockedValues); err != nil {
			return fmt.Errorf("load step C output (required by skipSCLockedValue, run step c first): %w", err)
		}
	}

	aggCert, err := cert.toAgglayerCertificate()
	if err != nil {
		return fmt.Errorf("load step D certificate: %w", err)
	}
	result, err := RunStepF(ctx, cfg, aggCert, lbtEntries, scLockedValues)
	if err != nil {
		return err
	}
	if result.TokenBalances != nil {
		if err := saveJSON(dir, fileStepFTokenBalances, result.TokenBalances); err != nil {
			return err
		}
	}
	if err := saveJSON(dir, fileStepFChecks, result.Checks); err != nil {
		return err
	}
	if result.CappedCertificate != nil {
		if err := saveJSON(dir, fileStepFCappedCertificate, result.CappedCertificate); err != nil {
			return err
		}
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
	return saveJSON(dir, fileStepG1ShadowForkBlock, result)
}

// runSingleG2 runs Step G2: it loads the shadow-fork block from G1, the certificate (capped from F
// or from E), and the LBT entries, then writes step-g-new-local-exit-root.json and
// step-g-reordered-certificate.json (the certificate as Step G left it — same deterministic exit
// order it came in with; the file name is kept for compatibility).
func runSingleG2(ctx context.Context, cfg *Config, dir string) error {
	var g1Result StepG1Result
	if err := loadJSON(dir, fileStepG1ShadowForkBlock, &g1Result); err != nil {
		return fmt.Errorf("load step G1 result (run step g1 first): %w", err)
	}

	var cert certificateJSON
	cappedPath := filepath.Join(dir, fileStepFCappedCertificate)
	if _, err := os.Stat(cappedPath); err == nil {
		if err := loadJSON(dir, fileStepFCappedCertificate, &cert); err != nil {
			return fmt.Errorf("load step F capped certificate: %w", err)
		}
		log.Warn("⚠️  Using capped certificate from step F (step-f-capped-certificate.json)")
	} else {
		if err := loadJSON(dir, fileStepECertificate, &cert); err != nil {
			return fmt.Errorf("load step E certificate: %w", err)
		}
		log.Info("Using certificate from step E (step-e-exit-certificate.json)")
	}

	lbtPath := filepath.Join(dir, fileStep0LBT)
	var lbtEntries []LBTEntry
	if entries, err := LoadLBTEntries(lbtPath); err == nil {
		lbtEntries = entries
		log.Infof("STEP G2: loaded %d LBT entries for token resolution", len(lbtEntries))
	} else {
		log.Warnf("STEP G2: LBT not available, falling back to getTokenWrappedAddress: %v", err)
	}

	aggCert, err := cert.toAgglayerCertificate()
	if err != nil {
		return fmt.Errorf("load certificate for step G2: %w", err)
	}
	result, err := RunStepG2(ctx, cfg, g1Result.ShadowForkBlock, aggCert, lbtEntries)
	if err != nil {
		return err
	}
	if err := saveJSON(dir, fileStepGNewLocalExitRoot, result); err != nil {
		return err
	}
	// RunStepG2 keeps the exits' deterministic order but updates each exit's Metadata (hash). Persist
	// it so the single-step Step I picks up the certificate that matches the computed NewLocalExitRoot.
	return saveJSON(dir, fileStepGReorderedCertificate, aggCert)
}

func runSingleH(ctx context.Context, cfg *Config, dir string) error {
	var gResult StepGResult
	if err := loadJSON(dir, fileStepGNewLocalExitRoot, &gResult); err != nil {
		return fmt.Errorf("load step G result: %w", err)
	}
	result, err := RunStepH(ctx, cfg, &gResult)
	if err != nil {
		return err
	}
	return saveJSON(dir, fileStepHPreviousLocalExitRoot, result)
}

func runSingleI(ctx context.Context, cfg *Config, dir string) error {
	// Step I always builds on the Step G certificate: Step G2 keeps the exits' deterministic order
	// but sets each exit's Metadata (hash), so this is the certificate that matches the computed
	// NewLocalExitRoot. It always writes step-g-reordered-certificate.json (name kept for
	// compatibility). Run Step G first.
	var cert certificateJSON
	if err := loadJSON(dir, fileStepGReorderedCertificate, &cert); err != nil {
		return fmt.Errorf("load step G certificate (run step g first): %w", err)
	}
	log.Info("Using certificate from step G (step-g-reordered-certificate.json)")
	var gResult StepGResult
	if err := loadJSON(dir, fileStepGNewLocalExitRoot, &gResult); err != nil {
		return fmt.Errorf("load step G result: %w", err)
	}
	var hResult StepHResult
	if err := loadJSON(dir, fileStepHPreviousLocalExitRoot, &hResult); err != nil {
		return fmt.Errorf("load step H result: %w", err)
	}
	aggCert, err := cert.toAgglayerCertificate()
	if err != nil {
		return fmt.Errorf("load step G reordered certificate: %w", err)
	}
	if err := RunStepI(ctx, cfg, aggCert, &gResult, &hResult); err != nil {
		return err
	}
	return saveJSON(dir, fileFinalCertificate, aggCert)
}

// --- LBT resolution ---

// resolveOrGenerateLBT always runs Step 0 and saves step-0-lbt.json.
func resolveOrGenerateLBT(ctx context.Context, cfg *Config, dir string) ([]LBTEntry, []WrappedToken, uint64, error) {
	result, err := RunStep0(ctx, cfg)
	if err != nil {
		return nil, nil, 0, err
	}
	if err := saveJSON(dir, fileStep0TargetBlock, result.TargetBlock); err != nil {
		return nil, nil, 0, err
	}
	if err := saveJSON(dir, fileStep0LBT, result.Entries); err != nil {
		return nil, nil, 0, err
	}
	return result.Entries, LBTEntriesToWrappedTokens(result.Entries), result.TargetBlock, nil
}

// loadTargetBlock reads the resolved L2 target block number saved by Step 0.
func loadTargetBlock(dir string) (uint64, error) {
	var n uint64
	if err := loadJSON(dir, fileStep0TargetBlock, &n); err != nil {
		return 0, fmt.Errorf("load target block (run step 0 first): %w", err)
	}
	return n, nil
}

// loadWrappedTokensFromLBT loads tokens from the step-0 output.
func loadWrappedTokensFromLBT(dir string) ([]WrappedToken, error) {
	tokens, err := LoadLBTWrappedTokens(filepath.Join(dir, fileStep0LBT))
	if err != nil {
		return nil, fmt.Errorf("no LBT data available: run step 0 first")
	}
	return tokens, nil
}

// --- JSON I/O ---

// saveJSON marshals data and writes it to dir/filename. Marshal/write failures are returned (not
// just logged): in split-step workflows a silently missing file would make the next step load a
// stale or absent input.
func saveJSON(dir, filename string, data any) error {
	path := filepath.Join(dir, filename)
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filename, err)
	}
	if err := os.WriteFile(path, content, filePermissions); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	log.Infof("Written: %s", path)
	return nil
}

func loadJSON(dir, filename string, target any) error {
	path := filepath.Join(dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// loadOptionalJSON loads dir/filename into target when the file exists. Absence is fine (the step
// that writes it is optional), but any other failure — unreadable file or corrupted JSON — is an
// error rather than being silently treated as absence.
func loadOptionalJSON(dir, filename string, target any) error {
	err := loadJSON(dir, filename, target)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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

func (c *certificateJSON) toAgglayerCertificate() (*agglayertypes.Certificate, error) {
	cert := &agglayertypes.Certificate{
		NetworkID:         c.NetworkID,
		Height:            c.Height,
		PrevLocalExitRoot: c.PrevLocalExitRoot,
		NewLocalExitRoot:  c.NewLocalExitRoot,
	}
	// A corrupted section must not silently load as a certificate with zero exits.
	if len(c.BridgeExits) > 0 {
		if err := json.Unmarshal(c.BridgeExits, &cert.BridgeExits); err != nil {
			return nil, fmt.Errorf("parse certificate bridge_exits: %w", err)
		}
	}
	if len(c.ImportedBridges) > 0 {
		if err := json.Unmarshal(c.ImportedBridges, &cert.ImportedBridgeExits); err != nil {
			return nil, fmt.Errorf("parse certificate imported_bridge_exits: %w", err)
		}
	}
	return cert, nil
}
