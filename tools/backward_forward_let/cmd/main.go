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
	app.Commands = []*cli.Command{
		{
			Name:  "send-cert",
			Usage: "Send a certificate to the agglayer and record it in the aggsender DB",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "cert-json",
					Usage: "Certificate JSON string (mutually exclusive with --cert-file)",
				},
				&cli.StringFlag{
					Name:    "cert-file",
					Aliases: []string{"f"},
					Usage:   "Path to a file containing the certificate JSON (mutually exclusive with --cert-json)",
				},
				&cli.StringFlag{
					Name:     "db-path",
					Usage:    "Path to the aggsender SQLite DB file (e.g. /path/to/aggsender.sqlite)",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "signer-key-path",
					Usage: "Path to the keystore file used to sign the certificate (optional)",
				},
				&cli.StringFlag{
					Name:  "signer-key-password",
					Usage: "Password for the keystore file (optional)",
				},
			},
			Action: backward_forward_let.RunSendCert,
		},
	}

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
