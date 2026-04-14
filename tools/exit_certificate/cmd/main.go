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
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Usage:   "Path to parameters.json config file",
			Value:   "parameters.json",
		},
		&cli.StringFlag{
			Name:  "step",
			Usage: "Run a specific step: 0, a, b, c, d, e, or all (default: all)",
			Value: "all",
		},
	}
	app.Action = exit_certificate.Run

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
