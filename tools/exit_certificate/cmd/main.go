package main

import (
	"fmt"
	"os"

	aggkit "github.com/agglayer/aggkit"
	exit_certificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/urfave/cli/v2"
)

func main() {
	app := cli.NewApp()
	app.Name = "exit-certificate"
	app.Usage = "Generate exit certificates for zkEVM chain migration"
	app.Version = aggkit.Version
	app.Description = `Builds an exit certificate by running a multi-step pipeline against an L2 chain.

Pipeline steps (run in order by default):

  0  Generate the Locked Balance Table (LBT) by scanning the L2 bridge contract
     for wrapped token mappings. Skipped when lbtFile is set in the config.

  A  Collect all unique sender/receiver addresses from bridge events up to the
     target block.

  B  Scan EOA native-token balances and wrapped-token balances for every address
     found in step A.

  C  Scan smart-contract locked values using the LBT from step 0.

  D  Aggregate step B and C results into a draft exit certificate.

  E  Cross-check the draft certificate against L1 to filter out bridge exits that
     have already been claimed. Skipped when l1RpcUrl is not set in the config.

Use --step to run a single step (e.g. --step a). When running steps individually
the output files from previous steps must already exist in the output directory.`
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Usage:   "Path to parameters.json config file",
			Value:   "parameters.json",
		},
		&cli.StringFlag{
			Name:  "step",
			Usage: "Run a specific step: 0, a1, a2, b, c, d, e, f, sign, or all",
			Value: "all",
		},
		&cli.StringFlag{
			Name:  "signer-key-path",
			Usage: "Path to the keystore file used to sign the certificate (overrides signerKeyPath in config)",
		},
		&cli.StringFlag{
			Name:  "signer-key-password",
			Usage: "Password for the keystore file (overrides signerKeyPassword in config)",
		},
	}
	app.Action = exit_certificate.Run

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
