// Command ger_validator_signer is a GER validator of the AggOracleCommittee v2: a service that signs the Global Exit
// Roots that are in the AgglayerGER contract of L1 and are final.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/app"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/config"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/version"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command and returns the exit code of the process
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "run":
		return runCommand(args[1:], stderr)
	case "client":
		return clientCommand(args[1:], stdout, stderr)
	case "version":
		_, _ = fmt.Fprintln(stdout, version.Get().String())
		return exitOK
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(output io.Writer) {
	_, _ = fmt.Fprint(output, `ger_validator_signer signs the Global Exit Roots that are in the AgglayerGER contract of L1
and are final, for the AggOracleCommittee v2.

Usage:
  ger_validator_signer run --cfg <config.toml>   run the validator
  ger_validator_signer client <command> [flags]  call a validator by hand (info, health, sign): client help
  ger_validator_signer version                   print the version
  ger_validator_signer help                      print this help
`)
}

func runCommand(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cfgPath := flags.String("cfg", "", "path of the TOML configuration file (required)")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *cfgPath == "" {
		_, _ = fmt.Fprintln(stderr, "the flag --cfg is required")
		return exitUsage
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return exitError
	}
	logger, err := app.NewLogger(cfg.Log, stderr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return exitError
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg, logger); err != nil {
		logger.Error("the validator stopped with an error", "error", err)
		return exitError
	}
	logger.Info("the validator stopped")
	return exitOK
}
