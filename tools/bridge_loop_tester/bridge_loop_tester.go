// Package bridgelooptester implements a long-running soak-test tool that continuously moves value
// around circular bridge routes (L1<->L2, L2<->L1, L2<->L2) using ETH and a deployed ERC20, driving
// every observation through the aggkit-proxy REST API (/bridge/v1, /tracker/v1) and the JSON-RPC
// endpoint of each network. See DESIGN.md for the wire contract and hop state machine.
//
// # Layers
//
// The package is usable as a library and as a CLI, and the two are strictly separated:
//
//   - Run(ctx, *Config) and NewOrchestrator/(*Orchestrator).Run are the library surface. They read
//     no file, install no signal handler, touch no package-level state and never call os.Exit; a
//     Config can be built entirely in Go, a logger and every dependency can be injected, and
//     cancelling the context is how a caller stops a run.
//   - The CmdXxx functions below are the CLI surface: they parse flags, initialise the process
//     logger, install the SIGINT/SIGTERM handler, print human output, and return an error for
//     cmd/main.go to turn into an exit code. They are the only place in the package that mutates
//     process-wide state.
package bridgelooptester

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v2"
)

// CLI flag names, shared between the command actions and cmd/main.go's flag definitions.
const (
	flagCfg          = "cfg"
	flagJSON         = "json"
	flagDryRun       = "dry-run"
	flagIterations   = "iterations"
	flagResumeHalted = "resume-halted"
	flagNetwork      = "network"
	flagName         = "name"
	flagSymbol       = "symbol"
	flagMint         = "mint"
	flagLoop         = "loop"
	flagSource       = "source"
	flagDestination  = "destination"
	flagDepositCount = "deposit-count"
	flagGasLimit     = "gas-limit"
)

// CmdRun drives every enabled loop of the configuration until the configured Iterations are done
// or the process is asked to stop.
//
// SIGINT and SIGTERM cancel the run's context, which stops every loop at its next safe point (a
// hop in progress finishes or fails, no new hop starts), flushes the state file, and shuts the
// metrics server down; a second signal aborts the process the way Go's default handler would, so a
// wedged shutdown is still killable with one more Ctrl-C.
func CmdRun(c *cli.Context) error {
	cfg, err := configFromCLI(c)
	if err != nil {
		return err
	}

	if c.IsSet(flagDryRun) {
		cfg.Global.DryRun = c.Bool(flagDryRun)
	}
	if c.IsSet(flagIterations) {
		cfg.Global.Iterations = c.Uint64(flagIterations)
	}
	initProcessLogger(cfg)

	ctx, stopSignals := signal.NotifyContext(c.Context, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	orchestrator, err := NewOrchestrator(ctx, cfg, OrchestratorDeps{
		ResumeHalted: c.Bool(flagResumeHalted),
	})
	if err != nil {
		return fmt.Errorf("run command: %w", err)
	}
	defer orchestrator.Close()

	report, runErr := orchestrator.Run(ctx)
	if report != nil {
		printReportSummary(c.App.Writer, report)
	}
	if runErr != nil {
		return fmt.Errorf("run command: %w", runErr)
	}

	return nil
}

// CmdValidate runs the full preflight: the static configuration checks plus every live, read-only
// check the tool can make (RPC reachability and chain IDs, the proxy's health, the bridge address
// each network publishes, the signer balances against MinNativeReserve, the gas token of every
// network, and whether the proxy can route network 0 at all). It sends no transaction.
//
// It is strict on purpose: anything that would make a run misleading is reported as a refusal, not
// a warning - including a configuration in which no loop is enabled, which would otherwise produce
// a run that does nothing and exits successfully.
func CmdValidate(c *cli.Context) error {
	cfg, err := configFromCLI(c)
	if err != nil {
		return err
	}
	initProcessLogger(cfg)

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate command: configuration is invalid:\n%w", err)
	}
	for _, warning := range cfg.Warnings() {
		log.Warnf("validate command: %s", warning)
	}

	orchestrator, err := NewOrchestrator(c.Context, cfg, OrchestratorDeps{})
	if err != nil {
		return fmt.Errorf("validate command: %w", err)
	}
	defer orchestrator.Close()

	report, err := orchestrator.Preflight(c.Context, true)
	if err != nil {
		return fmt.Errorf("validate command: %w", err)
	}

	if c.Bool(flagJSON) {
		if err := writeJSON(c.App.Writer, report); err != nil {
			return fmt.Errorf("validate command: %w", err)
		}
	} else {
		printPreflight(c.App.Writer, cfg, report)
	}

	if err := report.Err(); err != nil {
		return fmt.Errorf("validate command: the configuration cannot be run:\n%w", err)
	}

	_, _ = fmt.Fprintf(c.App.Writer, "\nOK: %d network(s), %d enabled loop(s); no transaction was sent.\n",
		len(cfg.Networks), len(cfg.EnabledLoops()))

	return nil
}

// CmdDeployToken deploys the freely-mintable test ERC20 on one configured network, optionally
// mints to that network's signing account, and optionally records the address as a loop's token so
// a later run reuses it instead of deploying another one.
func CmdDeployToken(c *cli.Context) error {
	cfg, err := configFromCLI(c)
	if err != nil {
		return err
	}
	if !c.IsSet(flagNetwork) {
		return fmt.Errorf("deploy-token command: --%s is required", flagNetwork)
	}
	initProcessLogger(cfg)

	mint, err := parseWeiFlag(c, flagMint)
	if err != nil {
		return fmt.Errorf("deploy-token command: %w", err)
	}

	orchestrator, err := NewOrchestrator(c.Context, cfg, OrchestratorDeps{})
	if err != nil {
		return fmt.Errorf("deploy-token command: %w", err)
	}
	defer orchestrator.Close()

	result, err := orchestrator.DeployToken(c.Context, DeployTokenRequest{
		NetworkID: uint32(c.Uint(flagNetwork)),
		Name:      c.String(flagName),
		Symbol:    c.String(flagSymbol),
		Mint:      mint,
		LoopName:  c.String(flagLoop),
	})
	if err != nil {
		return fmt.Errorf("deploy-token command: %w", err)
	}

	if c.Bool(flagJSON) {
		return writeJSON(c.App.Writer, result)
	}

	_, _ = fmt.Fprintf(c.App.Writer, "Deployed %s (%s/%s) on network %d (%s)\n  owner:   %s\n"+
		"  minted:  %s\n  balance: %s\n  loop:    %s\n",
		result.Address, result.Name, result.Symbol, result.NetworkID, result.NetworkName, result.Owner,
		bigIntString(result.Minted), bigIntString(result.Balance), orNone(result.RecordedForLoop))

	return nil
}

// CmdClaim submits one recovery claim for a deposit identified by --source/--destination/
// --deposit-count: the manual counterpart to a hop's claim step, for a deposit a crashed run, a
// paused autoclaim service or an operator's own bridge transaction left unclaimed.
//
// It is safe to run twice: it reads the destination bridge's isClaimed first and reports "already
// claimed" instead of submitting a transaction that would revert.
func CmdClaim(c *cli.Context) error {
	cfg, err := configFromCLI(c)
	if err != nil {
		return err
	}
	for _, required := range []string{flagSource, flagDestination, flagDepositCount} {
		if !c.IsSet(required) {
			return fmt.Errorf("claim command: --%s is required", required)
		}
	}
	initProcessLogger(cfg)

	orchestrator, err := NewOrchestrator(c.Context, cfg, OrchestratorDeps{})
	if err != nil {
		return fmt.Errorf("claim command: %w", err)
	}
	defer orchestrator.Close()

	result, err := orchestrator.Claim(c.Context, RecoveryClaimRequest{
		Source:       uint32(c.Uint(flagSource)),
		Destination:  uint32(c.Uint(flagDestination)),
		DepositCount: uint32(c.Uint(flagDepositCount)),
		GasLimit:     c.Uint64(flagGasLimit),
	})
	if result != nil && c.Bool(flagJSON) {
		if jsonErr := writeJSON(c.App.Writer, result); jsonErr != nil {
			return fmt.Errorf("claim command: %w", jsonErr)
		}
	} else if result != nil {
		printClaimResult(c.App.Writer, result)
	}
	if err != nil {
		return fmt.Errorf("claim command: %w", err)
	}

	return nil
}

// CmdStatus prints where every loop stands according to the state file: how many cycles it
// completed, whether a hop was in flight, whether it is halted, and - because the routes are
// circular - which network its value is sitting on and whether that means it is stranded part-way
// round its ring.
//
// It reads no RPC endpoint and no proxy, so it still works when the environment under test is
// down, which is when it is most useful.
func CmdStatus(c *cli.Context) error {
	cfg, err := configFromCLI(c)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("status command: configuration is invalid:\n%w", err)
	}

	report, err := Status(c.Context, cfg, nil)
	if err != nil {
		return fmt.Errorf("status command: %w", err)
	}

	if c.Bool(flagJSON) {
		return writeJSON(c.App.Writer, report)
	}
	printStatus(c.App.Writer, report)

	return nil
}

// configFromCLI loads and merges the --cfg files the command was given.
func configFromCLI(c *cli.Context) (*Config, error) {
	cfgFiles := c.StringSlice(flagCfg)
	if len(cfgFiles) == 0 {
		return nil, fmt.Errorf("%s command: no config file(s) given, use --%s/-c", c.Command.Name, flagCfg)
	}

	cfg, err := LoadConfig(cfgFiles...)
	if err != nil {
		return nil, fmt.Errorf("%s command: %w", c.Command.Name, err)
	}

	return cfg, nil
}

// initProcessLogger points the process-wide aggkit logger at the configured level. This is the CLI
// layer's job, not the library's: Run and NewOrchestrator take an injected logger and never touch
// the log package's global.
func initProcessLogger(cfg *Config) {
	level := cfg.Global.LogLevel
	if level == "" {
		level = defaultLogLevel
	}
	log.Init(log.Config{
		Environment: log.EnvironmentDevelopment,
		Level:       level,
		Outputs:     []string{"stderr"},
	})
}

// parseWeiFlag reads a wei-scale flag as a decimal string, so a value beyond float64's or int64's
// exact range is not silently mangled the way a numeric flag would mangle it.
func parseWeiFlag(c *cli.Context, name string) (*big.Int, error) {
	raw := strings.TrimSpace(c.String(name))
	if raw == "" {
		return nil, nil //nolint:nilnil // an unset amount is legitimately "no amount", not an error
	}

	var amount WeiAmount
	if err := amount.UnmarshalText([]byte(raw)); err != nil {
		return nil, fmt.Errorf("--%s: %w", name, err)
	}

	return amount.BigInt(), nil
}

// writeJSON writes v as indented JSON.
func writeJSON(out io.Writer, v any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return fmt.Errorf("encode JSON output: %w", err)
	}

	return nil
}

// orNone renders an empty string as "(none)".
func orNone(value string) string {
	if value == "" {
		return "(none)"
	}

	return value
}

// printPreflight renders a preflight report for a human.
func printPreflight(out io.Writer, cfg *Config, report *PreflightReport) {
	_, _ = fmt.Fprintf(out, "Proxy %s: healthy=%t l1_bridge_service=%t (network 0 in use: %t)\n",
		cfg.Global.ProxyURL, report.ProxyHealthy, report.L1BridgeServiceAvailable,
		report.NetworkZeroParticipates)

	for _, entry := range report.Networks {
		_, _ = fmt.Fprintf(out, "\nNetwork %d (%s)\n  chain id:      %d\n  signer:        %s\n"+
			"  native:        %s wei (min reserve %s)\n  gas token:     %s (ether: %t)\n"+
			"  WETH:          %s\n  bridge:        %s (bridge networkID() = %d)\n"+
			"  proxy bridge:  %s (reachable: %t)\n",
			entry.NetworkID, entry.Name, entry.ChainID, entry.From, bigIntString(entry.NativeBalance),
			bigIntString(entry.MinNativeReserve), entry.GasTokenAddress, entry.GasTokenIsEther,
			addressString(entry.WETHToken), entry.BridgeAddrConfigured, entry.BridgeNetworkID,
			addressString(entry.BridgeAddrReported), entry.ProxyReachable)
		for _, note := range entry.Notes {
			_, _ = fmt.Fprintf(out, "  note:          %s\n", note)
		}
	}

	for _, loop := range cfg.Loops {
		_, _ = fmt.Fprintf(out, "\nLoop %q: asset=%s amount=%s enabled=%t route=%s claims=%s\n",
			loop.Name, loop.Asset, loop.Amount, loop.Enabled, strings.Join(routeOf(loop), " "),
			claimModesOf(loop))
	}

	for _, warning := range report.Warnings {
		_, _ = fmt.Fprintf(out, "\nWARNING: %s\n", warning)
	}
	for _, problem := range report.Errors {
		_, _ = fmt.Fprintf(out, "\nREFUSED: %s\n", problem)
	}
}

// printReportSummary renders a finished run's totals and each loop's outcome.
func printReportSummary(out io.Writer, report *Report) {
	_, _ = fmt.Fprintf(out, "\nRun %s\n", report.Summary())
	if report.Cancelled {
		_, _ = fmt.Fprintf(out, "The run was cancelled; its state was flushed and can be resumed.\n")
	}

	for i := range report.Loops {
		loop := &report.Loops[i]
		_, _ = fmt.Fprintf(out, "\nLoop %q (%s, %s)\n  route:   %s\n  cycles:  %d completed / %d attempted\n"+
			"  hops:    %d\n  halted:  %t %s\n  value:   %s\n",
			loop.Name, loop.Asset, bigIntString(loop.Amount), strings.Join(loop.Route, " "),
			loop.CyclesCompleted, loop.CyclesAttempted, len(loop.AllHops()), loop.Halted,
			string(loop.HaltClass), loop.ValueLocation.Detail)
		if loop.Err != "" {
			_, _ = fmt.Fprintf(out, "  error:   %s\n", loop.Err)
		}
		for _, plan := range loop.Plan {
			_, _ = fmt.Fprintf(out, "  DRY RUN hop %d %s claim=%s amount=%s fundable=%t %s\n",
				plan.HopIndex, plan.Route(), plan.Claim, bigIntString(plan.Amount), plan.Fundable, plan.Note)
		}
	}
}

// printClaimResult renders a recovery claim's outcome.
func printClaimResult(out io.Writer, result *RecoveryClaimResult) {
	if result.AlreadyClaimed {
		_, _ = fmt.Fprintf(out, "deposit_count=%d from network %d is already claimed on network %d; "+
			"nothing was submitted.\n", result.DepositCount, result.Source, result.Destination)

		return
	}

	_, _ = fmt.Fprintf(out, "Claimed deposit_count=%d (network %d -> %d)\n  global index:  %s\n"+
		"  leaf type:     %d\n  leaf index:    %d (injected %d)\n  amount:        %s\n"+
		"  destination:   %s\n  claim tx:      %s (block %d, gas %d)\n  isClaimed:     %t\n",
		result.DepositCount, result.Source, result.Destination, globalIndexString(result.GlobalIndex),
		result.LeafType, result.L1InfoTreeIndex, result.InjectedLeafIndex, bigIntString(result.Amount),
		result.DestinationAddress, result.ClaimTxHash, result.ClaimBlockNumber, result.ClaimGasUsed,
		result.Claimed)
}

// printStatus renders the offline status report.
func printStatus(out io.Writer, report *StatusReport) {
	if !report.Exists {
		_, _ = fmt.Fprintf(out, "No state has been persisted yet")
		if report.StatePath == "" {
			_, _ = fmt.Fprintf(out, " (Global.StatePath is empty, so persistence is disabled).\n")
		} else {
			_, _ = fmt.Fprintf(out, " at %s.\n", report.StatePath)
		}
	} else {
		_, _ = fmt.Fprintf(out, "State %s, last written %s\n", report.StatePath,
			report.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"))
	}

	for i := range report.Loops {
		loop := &report.Loops[i]
		_, _ = fmt.Fprintf(out, "\nLoop %q (%s, %s, enabled=%t)\n  route:     %s\n"+
			"  cycles:    %d completed / %d attempted\n  in flight: %s %s\n  halted:    %t %s\n"+
			"  value:     %s\n",
			loop.Name, loop.Asset, loop.Amount, loop.Enabled, strings.Join(loop.Route, " "),
			loop.CyclesCompleted, loop.CyclesAttempted, orNone(string(loop.InFlightState)),
			inFlightTxNote(loop), loop.Halted, string(loop.HaltClass), loop.ValueLocation.Detail)
		if loop.LastError != "" {
			_, _ = fmt.Fprintf(out, "  last error: %s\n", loop.LastError)
		}
		if loop.TokenAddress != (common.Address{}) {
			_, _ = fmt.Fprintf(out, "  token:     %s\n", loop.TokenAddress)
			for networkID, wrapped := range loop.WrappedTokens {
				_, _ = fmt.Fprintf(out, "    wrapped on network %s: %s\n", networkID, wrapped)
			}
		}
	}
}

// inFlightTxNote renders the in-flight hop's bridge transaction, when it has one.
func inFlightTxNote(loop *LoopStatus) string {
	if loop.InFlightBridgeTx == (common.Hash{}) {
		return ""
	}

	return fmt.Sprintf("(bridge tx %s)", loop.InFlightBridgeTx)
}
