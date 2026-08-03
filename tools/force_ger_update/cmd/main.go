package main

import (
	"fmt"
	"os"

	aggkit "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/tools/force_ger_update"
	"github.com/urfave/cli/v2"
)

func main() {
	app := cli.NewApp()
	app.Name = "force_ger_update"
	app.Usage = "Force periodic L1 Global Exit Root updates when none happen organically"
	app.Version = aggkit.Version
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{
			Name:     "cfg",
			Aliases:  []string{"c"},
			Usage:    "Configuration file(s) (same format as aggkit-config.toml)",
			Required: true,
		},
	}
	app.Action = force_ger_update.Run

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
