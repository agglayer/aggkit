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
		&cli.BoolFlag{
			Name:  "diagnose-only",
			Usage: "Print diagnosis and recovery plan, then stop without prompting or sending recovery transactions",
		},
		&cli.StringFlag{
			Name:    "cert-exits-file",
			Aliases: []string{"f"},
			Usage: "Path to a JSON fallback file containing either raw AggLayer certificates or pre-extracted bridge exits keyed by certificate height." +
				" Use when the aggsender DB is empty and the tool reports missing certificate exits.",
		},
	}
	app.Action = backward_forward_let.Run
	app.Commands = []*cli.Command{
		{
			Name:  "send-cert",
			Usage: "Send a certificate to the agglayer and optionally record it in the aggsender DB",
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
					Name:  "db-path",
					Usage: "Path to the aggsender SQLite DB file (e.g. /path/to/aggsender.sqlite)",
				},
				&cli.BoolFlag{
					Name:  "no-db",
					Usage: "Staging-only: send the certificate without recording it in the aggsender DB",
				},
				&cli.BoolFlag{
					Name:  "staging-only",
					Usage: "Required when using staging-only send modes such as --no-db",
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
		{
			Name:  "craft-cert",
			Usage: "Staging-only: craft a testing certificate for a backward/forward LET drill",
			Flags: []cli.Flag{
				&cli.BoolFlag{Name: "staging-only", Usage: "Required safety confirmation for testing certificate crafting"},
				&cli.UintFlag{Name: "num-fake-exits", Usage: "Number of fake bridge exits to include"},
				&cli.StringFlag{Name: "amount", Value: "0", Usage: "Fake bridge exit amount"},
				&cli.UintFlag{Name: "starting-exit-index", Usage: "Starting index for deterministic fake exit uniqueness"},
				&cli.StringFlag{Name: "nonce", Usage: "Optional nonce used to derive fake exit destination addresses"},
				&cli.Uint64Flag{
					Name:  "l1-info-tree-leaf-count",
					Usage: "Override L1 info tree leaf count when aggsender header data is unavailable",
				},
				&cli.Uint64Flag{Name: "signer-index", Usage: "Multisig signer index to write into the crafted certificate"},
				&cli.StringFlag{Name: "out", Usage: "Output path for the crafted certificate JSON", Required: true},
			},
			Action: backward_forward_let.RunCraftCert,
		},
		{
			Name:  "cert-status",
			Usage: "Print AggLayer certificate settlement and pending status",
			Flags: []cli.Flag{
				&cli.Uint64Flag{Name: "height", Usage: "Certificate height to check"},
				&cli.BoolFlag{Name: "wait-no-pending", Usage: "Wait until AggLayer has no open pending certificate"},
				&cli.BoolFlag{Name: "wait-settled", Usage: "Wait until --height is settled"},
				&cli.DurationFlag{
					Name:  "timeout",
					Value: backward_forward_let.DefaultCertStatusTimeout,
					Usage: "Maximum wait duration",
				},
			},
			Action: backward_forward_let.RunCertStatus,
		},
		{
			Name:  "export-cert-exits",
			Usage: "Export a certificate-exits override from an authoritative height-to-cert-ID map",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "agglayer-admin-url", Usage: "Read-only AggLayer admin JSON-RPC URL", Required: true},
				&cli.StringFlag{Name: "cert-ids-file", Usage: "JSON file mapping certificate heights to cert IDs", Required: true},
				&cli.StringFlag{Name: "out", Usage: "Output certificate exits override JSON path", Required: true},
				&cli.StringFlag{
					Name:  "manifest-out",
					Usage: "Output source manifest JSON path (default: <out>.manifest.json)",
				},
				&cli.Uint64Flag{
					Name:  "max-certs",
					Value: backward_forward_let.DefaultExportCertExitsMaxCerts,
					Usage: "Maximum certificates to export in one batch",
				},
				&cli.DurationFlag{
					Name:  "timeout",
					Value: backward_forward_let.DefaultExportCertExitsTimeout,
					Usage: "Maximum export duration",
				},
			},
			Action: backward_forward_let.RunExportCertExits,
		},
	}

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
