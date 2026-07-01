package exit_certificate

import (
	"context"
	"encoding/json"
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
	saveJSON(dir, fileStepCheckResult, checkResult)

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
		saveJSON(dir, fileSignedCertificate, signedCert)
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
	saveJSON(dir, fileStepA1Addresses, a1Result.Addresses)
	saveJSON(dir, fileStepA1FailedTrace, a1Result.FailedTraces)

	a2Result, err := RunStepA2(ctx, cfg, a1Result.FailedTraces)
	if err != nil {
		return nil, fmt.Errorf("step A2: %w", err)
	}
	saveJSON(dir, fileStepA2Addresses, a2Result.Addresses)

	combined := mergeAddresses(a1Result.Addresses, a2Result.Addresses)
	log.Infof("STEP A complete: %d addresses (A1: %d, A2 new: %d)",
		len(combined), len(a1Result.Addresses), len(combined)-len(a1Result.Addresses))
	saveJSON(dir, fileStepAAddresses, combined)

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
	saveJSON(dir, fileStepBEOABalances, stepBResult.EOABalances)
	saveJSON(dir, fileStepBAccumulated, stepBResult.Accumulated)
	saveJSON(dir, fileStepBContractAddresses, stepBResult.ContractAddresses)
	if len(stepBResult.IgnoredBalances) > 0 {
		saveJSON(dir, fileStepBIgnoredBalances, stepBResult.IgnoredBalances)
	}
	saveJSON(dir, fileStepB2DetectedERC20s, stepBResult.DetectedERC20s)
	saveJSON(dir, fileStepB2DiscardedERC20s, stepBResult.DiscardedERC20s)
	saveJSON(dir, fileStepB3ERC20Holders, stepBResult.ERC20HolderBreakdowns)
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
	saveJSON(dir, fileStepCSCLockedValues, stepCResult.SCLockedValues)
	saveJSON(dir, fileStepCHolderBridges, stepCResult.HolderBridges)
	return stepCResult, nil
}

func runAllStepF(
	ctx context.Context, cfg *Config, dir string,
	lbtEntries []LBTEntry,
	stepDCert *agglayertypes.Certificate,
	finalCert *agglayertypes.Certificate,
) (*agglayertypes.Certificate, error) {
	// RunStepF itself honours useAgglayerAdminToStepFCheck: when false it runs the offline LBT vs
	// certificate comparison instead of the agglayer admin query.
	result, err := RunStepF(ctx, cfg, stepDCert, lbtEntries)
	if err != nil {
		return nil, fmt.Errorf("step F: %w", err)
	}
	if result.TokenBalances != nil {
		saveJSON(dir, fileStepFTokenBalances, result.TokenBalances)
	}
	saveJSON(dir, fileStepFChecks, result.Checks)
	if result.CappedCertificate != nil {
		// Apply the same per-token caps to the final certificate (which may include step E exits).
		cappedFinal := *finalCert
		cappedFinal.BridgeExits = capCertificateExits(finalCert.BridgeExits, result.Checks, cfg.Options.CapMode)
		saveJSON(dir, fileStepFCappedCertificate, &cappedFinal)
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
	saveJSON(dir, fileStepG1ShadowForkBlock, g1Result)

	result, err := RunStepG2(ctx, cfg, g1Result.ShadowForkBlock, certificate, lbtEntries)
	if err != nil {
		return nil, fmt.Errorf("step G2: %w", err)
	}
	saveJSON(dir, fileStepGNewLocalExitRoot, result)
	// RunStepG2 reorders certificate.BridgeExits to the shadow-fork deposit order; persist the
	// reordered certificate for inspection and parity with single-step mode.
	saveJSON(dir, fileStepGReorderedCertificate, certificate)
	return result, nil
}

func runAllStepH(ctx context.Context, cfg *Config, dir string, gResult *StepGResult) (*StepHResult, error) {
	result, err := RunStepH(ctx, cfg, gResult)
	if err != nil {
		return nil, fmt.Errorf("step H: %w", err)
	}
	saveJSON(dir, fileStepHPreviousLocalExitRoot, result)
	return result, nil
}

func runAllStepI(
	ctx context.Context, cfg *Config, dir string,
	certificate *agglayertypes.Certificate, gResult *StepGResult, hResult *StepHResult,
) error {
	if err := RunStepI(ctx, cfg, certificate, gResult, hResult); err != nil {
		return fmt.Errorf("step I: %w", err)
	}
	saveJSON(dir, fileFinalCertificate, certificate)
	return nil
}

func runAllStepD(cfg *Config, dir string, stepBResult *StepBResult, stepCResult *StepCResult) (*StepDResult, error) {
	stepDResult, err := RunStepD(cfg, stepBResult, stepCResult)
	if err != nil {
		return nil, fmt.Errorf("step D: %w", err)
	}
	saveJSON(dir, fileStepDCertificate, stepDResult.Certificate)
	return stepDResult, nil
}

// saveStepEFiles persists step E outputs to disk. Always writes the unclaimed bridges and
// messages files; only writes the certificate when it is non-nil.
func saveStepEFiles(dir string, result *StepEResult) {
	if result == nil {
		return
	}
	saveJSON(dir, fileStepEUnclaimedBridges, result.UnclaimedBridges)
	saveJSON(dir, fileStepEUnclaimedMsgs, result.UnclaimedMessages)
	if result.FinalCertificate != nil {
		saveJSON(dir, fileStepECertificate, result.FinalCertificate)
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
	saveJSON(dir, fileStepCheckResult, result)
	return nil
}

func runSingle0(ctx context.Context, cfg *Config, dir string) error {
	result, err := RunStep0(ctx, cfg)
	if err != nil {
		return err
	}
	saveJSON(dir, fileStep0TargetBlock, result.TargetBlock)
	saveJSON(dir, fileStep0LBT, result.Entries)
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
	saveJSON(dir, fileStepA1Addresses, result.Addresses)
	saveJSON(dir, fileStepA1FailedTrace, result.FailedTraces)
	return nil
}

// runSingleA2 runs Step A2 and writes step-a2-addresses.json and step-a-addresses.json.
// Legacy step-a-* files are migrated to step-a1-* at startup (see Run), so they will
// already be in the correct location by the time this function is called.
func runSingleA2(ctx context.Context, cfg *Config, dir string) error {
	var failedTraces []FailedTrace
	if err := loadJSON(dir, fileStepA1FailedTrace, &failedTraces); err != nil {
		return fmt.Errorf("load step A1 failed traces (run step a1 first): %w", err)
	}

	a2Result, err := RunStepA2(ctx, cfg, failedTraces)
	if err != nil {
		return err
	}
	saveJSON(dir, fileStepA2Addresses, a2Result.Addresses)

	var a1Addresses []common.Address
	if err := loadJSON(dir, fileStepA1Addresses, &a1Addresses); err != nil {
		return fmt.Errorf("load step A1 addresses: %w", err)
	}
	log.Debugf("STEP A2 merging %d A2 addresses with %d A1 addresses", len(a2Result.Addresses), len(a1Addresses))
	combined := mergeAddresses(a1Addresses, a2Result.Addresses)
	log.Infof("STEP A complete: %d addresses (A1: %d, A2 new: %d)",
		len(combined), len(a1Addresses), len(combined)-len(a1Addresses))
	saveJSON(dir, fileStepAAddresses, combined)
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
	if err := rename(fileStepAAddresses, fileStepA1Addresses); err != nil {
		return err
	}
	return rename(fileStepAFailedTraces, fileStepA1FailedTrace)
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
	saveJSON(dir, fileStepBEOABalances, result.EOABalances)
	saveJSON(dir, fileStepBAccumulated, result.Accumulated)
	saveJSON(dir, fileStepBContractAddresses, result.ContractAddresses)
	if len(result.IgnoredBalances) > 0 {
		saveJSON(dir, fileStepBIgnoredBalances, result.IgnoredBalances)
	}
	return nil
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
	saveJSON(dir, fileStepB2DetectedERC20s, result.DetectedERC20s)
	saveJSON(dir, fileStepB2DiscardedERC20s, result.DiscardedERC20s)
	return nil
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
	saveJSON(dir, fileStepB3ERC20Holders, result.Breakdowns)
	return nil
}

func runSingleC(dir string) error {
	var accumulated []AccumulatedBalance
	if err := loadJSON(dir, fileStepBAccumulated, &accumulated); err != nil {
		return fmt.Errorf("load step B output: %w", err)
	}
	var lbtEntries []LBTEntry
	if err := loadJSON(dir, fileStep0LBT, &lbtEntries); err != nil {
		return fmt.Errorf("load LBT data (step 0): %w", err)
	}
	// Load holder breakdowns from B3 if available; absence is not an error.
	var breakdowns []ERC20HolderBreakdown
	_ = loadJSON(dir, fileStepB3ERC20Holders, &breakdowns)

	result, err := RunStepC(lbtEntries, &StepBResult{
		Accumulated:           accumulated,
		ERC20HolderBreakdowns: breakdowns,
	})
	if err != nil {
		return err
	}
	saveJSON(dir, fileStepCSCLockedValues, result.SCLockedValues)
	saveJSON(dir, fileStepCHolderBridges, result.HolderBridges)
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
	var holderBridges []HolderBridge
	_ = loadJSON(dir, fileStepCHolderBridges, &holderBridges)

	result, err := RunStepD(cfg, &StepBResult{EOABalances: eoaBalances}, &StepCResult{
		SCLockedValues: scLockedValues,
		HolderBridges:  holderBridges,
	})
	if err != nil {
		return err
	}
	saveJSON(dir, fileStepDCertificate, result.Certificate)
	return nil
}

func runSingleE(ctx context.Context, cfg *Config, dir string) error {
	if cfg.L1RPCURL == "" {
		return fmt.Errorf("step E requires l1RpcUrl in parameters")
	}
	var cert certificateJSON
	if err := loadJSON(dir, fileStepDCertificate, &cert); err != nil {
		return fmt.Errorf("load step D output: %w", err)
	}
	result, err := RunStepE(ctx, cfg, cert.toAgglayerCertificate())
	saveStepEFiles(dir, result)
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
	saveJSON(dir, fileSignedCertificate, signed)
	return nil
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
	saveJSON(dir, fileStepSubmitResult, result)
	return nil
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
	saveJSON(dir, fileStepWaitResult, result)
	return nil
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

	result, err := RunStepF(ctx, cfg, cert.toAgglayerCertificate(), lbtEntries)
	if err != nil {
		return err
	}
	if result.TokenBalances != nil {
		saveJSON(dir, fileStepFTokenBalances, result.TokenBalances)
	}
	saveJSON(dir, fileStepFChecks, result.Checks)
	if result.CappedCertificate != nil {
		saveJSON(dir, fileStepFCappedCertificate, result.CappedCertificate)
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
	saveJSON(dir, fileStepG1ShadowForkBlock, result)
	return nil
}

// runSingleG2 runs Step G2: it loads the shadow-fork block from G1, the certificate (capped from F
// or from E), and the LBT entries, then writes step-g-new-local-exit-root.json and the reordered
// step-g-reordered-certificate.json.
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

	aggCert := cert.toAgglayerCertificate()
	result, err := RunStepG2(ctx, cfg, g1Result.ShadowForkBlock, aggCert, lbtEntries)
	if err != nil {
		return err
	}
	saveJSON(dir, fileStepGNewLocalExitRoot, result)
	// RunStepG2 reorders aggCert.BridgeExits to the shadow-fork deposit order. Persist it so the
	// single-step Step I picks up the reordered exits instead of the pre-G ordering.
	saveJSON(dir, fileStepGReorderedCertificate, aggCert)
	return nil
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
	saveJSON(dir, fileStepHPreviousLocalExitRoot, result)
	return nil
}

func runSingleI(ctx context.Context, cfg *Config, dir string) error {
	// Step I always builds on the Step G reordered certificate: Step G2 reorders the bridge exits
	// to the shadow-fork deposit order (the authoritative ordering that matches the computed
	// NewLocalExitRoot) and always writes step-g-reordered-certificate.json. Run Step G first.
	var cert certificateJSON
	if err := loadJSON(dir, fileStepGReorderedCertificate, &cert); err != nil {
		return fmt.Errorf("load step G reordered certificate (run step g first): %w", err)
	}
	log.Info("Using reordered certificate from step G (step-g-reordered-certificate.json)")
	var gResult StepGResult
	if err := loadJSON(dir, fileStepGNewLocalExitRoot, &gResult); err != nil {
		return fmt.Errorf("load step G result: %w", err)
	}
	var hResult StepHResult
	if err := loadJSON(dir, fileStepHPreviousLocalExitRoot, &hResult); err != nil {
		return fmt.Errorf("load step H result: %w", err)
	}
	aggCert := cert.toAgglayerCertificate()
	if err := RunStepI(ctx, cfg, aggCert, &gResult, &hResult); err != nil {
		return err
	}
	saveJSON(dir, fileFinalCertificate, aggCert)
	return nil
}

// --- LBT resolution ---

// resolveOrGenerateLBT always runs Step 0 and saves step-0-lbt.json.
func resolveOrGenerateLBT(ctx context.Context, cfg *Config, dir string) ([]LBTEntry, []WrappedToken, uint64, error) {
	result, err := RunStep0(ctx, cfg)
	if err != nil {
		return nil, nil, 0, err
	}
	saveJSON(dir, fileStep0TargetBlock, result.TargetBlock)
	saveJSON(dir, fileStep0LBT, result.Entries)
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
