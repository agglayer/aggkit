package main

import (
	"fmt"
	"os"

	aggkit "github.com/agglayer/aggkit"
	claimer "github.com/agglayer/aggkit/tools/exit_certificate_claimer/service"
	"github.com/urfave/cli/v2"
)

func main() {
	app := cli.NewApp()
	app.Name = "exit-certificate-claimer"
	app.Usage = "Serve claimAsset parameters for the bridge exits of a settled exit certificate"
	app.Version = aggkit.Version
	app.Description = `Backend HTTP service for claiming the bridge exits produced by the exit_certificate tool.

Given a destination address it returns:
  - the available bridge exits for that address (GET /claimer/v1/bridges)
  - the full set of AgglayerBridge.claimAsset parameters per exit (GET /claimer/v1/claim-params)

Data sources:
  - exit-certificate-signed.json       (the certificate bridge exits)
  - step-g-l2bridgesyncerlite.sqlite   (the L2 local exit tree, for local merkle proofs)
  - an l1infotreesync SQLite database   (mainnet/rollup exit roots + rollup proof), opened
                                         read-only or kept in sync from L1 when l1Sync.enabled.`
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Usage:   "Path to the claimer config file (JSON or TOML; format selected by .json/.toml extension)",
			Value:   "config.toml",
		},
		&cli.StringFlag{
			Name:    "exit-certificate-config",
			Aliases: []string{"e"},
			Usage: "Path to an exit_certificate config file to derive the claimer config from " +
				"(mutually exclusive with --config)",
		},
		&cli.StringFlag{
			Name:  "address",
			Usage: "HTTP server bind host/IP, without port (overrides the config)",
		},
		&cli.IntFlag{
			Name:  "port",
			Usage: "HTTP server bind port (overrides the config)",
		},
		&cli.BoolFlag{
			Name:  "verbose",
			Usage: "Enable debug logging",
		},
	}
	app.Action = claimer.Run

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
