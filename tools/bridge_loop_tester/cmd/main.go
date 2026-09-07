package main

import (
	"fmt"
	"os"

	aggkit "github.com/agglayer/aggkit"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/urfave/cli/v2"
)

// cfgFlag returns the repeatable --cfg/-c flag every command takes.
func cfgFlag() cli.Flag {
	return &cli.StringSliceFlag{
		Name:    "cfg",
		Aliases: []string{"c"},
		Usage:   "Path to configuration file (TOML format, repeatable; later files override earlier ones)",
	}
}

// jsonFlag returns the --json flag for the commands that can print a machine-readable report.
func jsonFlag() cli.Flag {
	return &cli.BoolFlag{
		Name:  "json",
		Usage: "Print the result as JSON instead of human-readable text",
	}
}

func main() {
	cli.VersionPrinter = func(*cli.Context) {
		aggkit.PrintVersion(os.Stdout)
	}

	app := cli.NewApp()
	app.Name = "bridge-loop-tester"
	app.Usage = "Continuously move value around circular bridge routes to soak-test all bridge directions"
	app.Version = aggkit.Version
	app.Description = `Bridge loop tester continuously moves value around circular routes covering
all three bridge directions (L1→L2, L2→L1, L2→L2) using ETH and a deployed ERC20.
Every observation goes through the aggkit proxy REST API and JSON-RPC endpoints.
Routes are circular, so value returns to its origin each cycle.`

	app.Flags = []cli.Flag{cfgFlag()}

	app.Commands = []*cli.Command{
		{
			Name:   "run",
			Usage:  "Run the continuous bridge loop test",
			Action: bridgelooptester.CmdRun,
			Flags: []cli.Flag{
				cfgFlag(),
				&cli.BoolFlag{
					Name: "dry-run",
					Usage: "Perform every read and fundability check but submit no transaction " +
						"(overrides Global.DryRun)",
				},
				&cli.Uint64Flag{
					Name:  "iterations",
					Usage: "Number of cycles to run per loop, 0 for forever (overrides Global.Iterations)",
				},
				&cli.BoolFlag{
					Name: "resume-halted",
					Usage: "Re-drive loops a previous run halted for a non-retryable reason " +
						"(claim-mode violation, ambiguous resume, insufficient balance)",
				},
			},
		},
		{
			Name: "validate",
			Usage: "Validate the configuration and run the full live preflight " +
				"(no transaction is sent)",
			Action: bridgelooptester.CmdValidate,
			Flags:  []cli.Flag{cfgFlag(), jsonFlag()},
		},
		{
			Name:   "deploy-token",
			Usage:  "Deploy (and optionally mint) the test ERC20 on one configured network",
			Action: bridgelooptester.CmdDeployToken,
			Flags: []cli.Flag{
				cfgFlag(),
				jsonFlag(),
				&cli.UintFlag{
					Name:  "network",
					Usage: "NetworkID to deploy the token on (required)",
				},
				&cli.StringFlag{
					Name:  "name",
					Usage: "ERC20 name (defaults to \"BridgeLoopTester <network name>\")",
				},
				&cli.StringFlag{
					Name:  "symbol",
					Usage: "ERC20 symbol (defaults to \"BLT\")",
				},
				&cli.StringFlag{
					Name: "mint",
					Usage: "Amount to mint to the network's signing account, as a decimal string in " +
						"base units (e.g. \"1000000000000000000000\")",
				},
				&cli.StringFlag{
					Name: "loop",
					Usage: "Record the deployed address as this loop's token in the state file, so a " +
						"later run reuses it",
				},
			},
		},
		{
			Name:   "claim",
			Usage:  "Submit one recovery claim for a deposit left unclaimed",
			Action: bridgelooptester.CmdClaim,
			Flags: []cli.Flag{
				cfgFlag(),
				jsonFlag(),
				&cli.UintFlag{
					Name:  "source",
					Usage: "NetworkID the bridge transaction was submitted on (required)",
				},
				&cli.UintFlag{
					Name:  "destination",
					Usage: "NetworkID the deposit is claimable on (required)",
				},
				&cli.UintFlag{
					Name:  "deposit-count",
					Usage: "The deposit's depositCount on the source network (required)",
				},
				&cli.Uint64Flag{
					Name:  "gas-limit",
					Usage: "Override the claim transaction's gas estimate",
				},
			},
		},
		{
			Name: "status",
			Usage: "Print where every loop stands according to the state file, including where its " +
				"value currently sits (reads no RPC or proxy)",
			Action: bridgelooptester.CmdStatus,
			Flags:  []cli.Flag{cfgFlag(), jsonFlag()},
		},
	}

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
