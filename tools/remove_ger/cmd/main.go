package main

import (
	"fmt"
	"os"

	aggkit "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/tools/remove_ger"
	"github.com/urfave/cli/v2"
)

func main() {
	app := cli.NewApp()
	app.Name = "remove-ger"
	app.Usage = "Diagnose and recover from invalid GER injection on L2"
	app.Version = aggkit.Version
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{
			Name:     "cfg",
			Aliases:  []string{"c"},
			Usage:    "Configuration file(s) (same format as aggkit-config.toml)",
			Required: true,
		},
		&cli.StringFlag{
			Name:  "ger",
			Usage: "The invalid GER hash to diagnose and remove (hex, 0x-prefixed)",
		},
		&cli.BoolFlag{
			Name:  "yes",
			Usage: "Skip interactive confirmation and execute the recovery plan immediately",
		},
		&cli.BoolFlag{
			Name:  "force",
			Usage: "Continue even if GER exists on L1 (use when you still want to diagnose/remove)",
		},
	}
	app.Action = remove_ger.Run
	app.Commands = []*cli.Command{
		{
			Name:  "scan-invalid-claims",
			Usage: "Scan L2 claims from a starting block and report GERs that are invalid on L1",
			Flags: []cli.Flag{
				&cli.Uint64Flag{
					Name:     "from-block",
					Usage:    "Starting L2 block number to scan (inclusive)",
					Required: true,
				},
				&cli.Uint64Flag{
					Name:  "to-block",
					Usage: "Ending L2 block number to scan (inclusive, defaults to latest L2 block)",
				},
				&cli.Uint64Flag{
					Name:  "chunk-size",
					Usage: "Maximum L2 block range per eth_getLogs query",
					Value: 5000,
				},
			},
			Action: remove_ger.RunScanInvalidClaims,
		},
		{
			Name:  "generate",
			Usage: "Generate an invalid GER scenario with ready-to-run cast commands for testing",
			Flags: []cli.Flag{
				&cli.UintFlag{
					Name:     "network-id",
					Usage:    "Destination network ID (required, must be > 0)",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "dest-addr",
					Usage: "Destination address",
					Value: "0x0000000000000000000000000000000000000000",
				},
				&cli.UintFlag{
					Name:  "origin-network",
					Usage: "Origin network ID",
					Value: 0,
				},
				&cli.StringFlag{
					Name:  "origin-addr",
					Usage: "Origin token address",
					Value: "0x0000000000000000000000000000000000000000",
				},
				&cli.Uint64Flag{
					Name:  "amount",
					Usage: "Bridge amount in wei",
					Value: 1,
				},
				&cli.UintFlag{
					Name:  "deposit-count",
					Usage: "Deposit count for the fake bridge leaf",
					Value: uint(remove_ger.DefaultDepositCount),
				},
				&cli.UintFlag{
					Name:  "leaf-type",
					Usage: "Leaf type (0=asset, 1=message)",
					Value: 0,
				},
			},
			Action: remove_ger.RunGenerate,
		},
	}

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
