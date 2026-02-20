package main

import (
	"fmt"
	"os"

	removeger "github.com/agglayer/aggkit/tools/remove_ger"
	"github.com/urfave/cli/v2"
)

func main() {
	app := cli.NewApp()
	app.Name = "remove-ger"
	app.Usage = "Diagnose and recover from invalid GER injection on L2"
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{
			Name:     "cfg",
			Aliases:  []string{"c"},
			Usage:    "Configuration file(s) (same format as aggkit-config.toml)",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "ger",
			Usage:    "The invalid GER hash to diagnose and remove (hex, 0x-prefixed)",
			Required: true,
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
	app.Action = removeger.Run

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
