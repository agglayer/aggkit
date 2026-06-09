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
     for wrapped token mappings.

  A  Collect all unique sender/receiver addresses from bridge events up to the
     target block.

  B  Scan EOA native-token balances and wrapped-token balances for every address
     found in step A.

  C  Scan smart-contract locked values using the LBT from step 0.

  D  Aggregate step B and C results into a draft exit certificate.

  E  Cross-check the draft certificate against L1 to filter out bridge exits that
     have already been claimed. Skipped when l1RpcUrl is not set in the config.

	F  Verify agglayer token balances against the certificate exits.

	G  Calculate NewLocalExitRoot from the certificate bridge exits.

	H  Fetch PreviousLocalExitRoot from the agglayer via interop_getNetworkInfo.
	   Requires agglayerRpcUrl in options.

	I  Assemble the final certificate by writing NewLocalExitRoot (from G) and
	   PreviousLocalExitRoot (from H) into exit-certificate-final.json.

  SIGN   Sign the final certificate with the configured keystore.

  SUBMIT Send the signed certificate to the agglayer via gRPC.
	   Requires agglayerClient.grpc.url in options. Not part of the default pipeline.

  WAIT   Poll the agglayer every 5 seconds until the submitted certificate is
         settled or enters an error state. Reads step-submit-result.json for
         the certificate hash. Requires agglayerClient.grpc.url in options.

Use --step to run a single step (e.g. --step a). When running steps individually
the output files from previous steps must already exist in the output directory.`
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Usage:   "Path to the config file (JSON or TOML; format selected by .json/.toml extension)",
			Value:   "parameters.json",
		},
		&cli.StringFlag{
			Name:  "step",
			Usage: "Run a specific step: 0, a, b, c, d, e, f, g, sign, or all",
			Value: "all",
		},
		&cli.BoolFlag{
			Name:  "verbose",
			Usage: "Enable debug logging",
		},
	}
	app.Action = exit_certificate.Run

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
