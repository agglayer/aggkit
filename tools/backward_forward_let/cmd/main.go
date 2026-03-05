package main

import (
	"fmt"
	"os"

	aggkit "github.com/agglayer/aggkit"
	backward_forward_let "github.com/agglayer/aggkit/tools/backward_forward_let"
	"github.com/urfave/cli/v2"
)

func main() {
	app := cli.NewApp()
	app.Name = "backward-forward-let"
	app.Usage = "Diagnose and recover from backward/forward LET divergence between L1 settled state and L2 on-chain state"
	app.Version = aggkit.Version
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{
			Name:     "cfg",
			Aliases:  []string{"c"},
			Usage:    "Configuration file(s) (same format as aggkit-config.toml)",
			Required: true,
		},
		&cli.BoolFlag{
			Name:  "yes",
			Usage: "Skip interactive confirmation and execute the recovery plan immediately",
		},
		&cli.StringFlag{
			Name:    "cert-exits-file",
			Aliases: []string{"f"},
			Usage: "Path to a JSON override file containing pre-extracted bridge exits keyed by certificate height." +
				" Use when the aggsender DB is empty and the tool reports missing cert IDs.",
		},
	}
	app.Action = backward_forward_let.Run

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
