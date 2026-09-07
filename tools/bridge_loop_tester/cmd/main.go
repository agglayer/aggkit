package main

import (
	"fmt"
	"os"

	aggkit "github.com/agglayer/aggkit"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/urfave/cli/v2"
)

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

	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{
			Name:    "cfg",
			Aliases: []string{"c"},
			Usage:   "Path to configuration file (TOML format, repeatable)",
		},
	}

	app.Commands = []*cli.Command{
		{
			Name:   "run",
			Usage:  "Run the continuous bridge loop test",
			Action: bridgelooptester.CmdRun,
			Flags: []cli.Flag{
				&cli.StringSliceFlag{
					Name:    "cfg",
					Aliases: []string{"c"},
					Usage:   "Path to configuration file (TOML format, repeatable)",
				},
			},
		},
		{
			Name:   "validate",
			Usage:  "Validate the configuration without running the loop",
			Action: bridgelooptester.CmdValidate,
			Flags: []cli.Flag{
				&cli.StringSliceFlag{
					Name:    "cfg",
					Aliases: []string{"c"},
					Usage:   "Path to configuration file (TOML format, repeatable)",
				},
			},
		},
		{
			Name:   "deploy-token",
			Usage:  "Deploy an ERC20 token on the specified network",
			Action: bridgelooptester.CmdDeployToken,
			Flags: []cli.Flag{
				&cli.StringSliceFlag{
					Name:    "cfg",
					Aliases: []string{"c"},
					Usage:   "Path to configuration file (TOML format, repeatable)",
				},
			},
		},
		{
			Name:   "claim",
			Usage:  "Manually claim pending bridge exits",
			Action: bridgelooptester.CmdClaim,
			Flags: []cli.Flag{
				&cli.StringSliceFlag{
					Name:    "cfg",
					Aliases: []string{"c"},
					Usage:   "Path to configuration file (TOML format, repeatable)",
				},
			},
		},
		{
			Name:   "status",
			Usage:  "Display the current status of the loop and pending claims",
			Action: bridgelooptester.CmdStatus,
			Flags: []cli.Flag{
				&cli.StringSliceFlag{
					Name:    "cfg",
					Aliases: []string{"c"},
					Usage:   "Path to configuration file (TOML format, repeatable)",
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
