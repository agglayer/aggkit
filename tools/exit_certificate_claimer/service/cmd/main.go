package main

import (
	"fmt"
	"os"

	aggkit "github.com/agglayer/aggkit"
	claimer "github.com/agglayer/aggkit/tools/exit_certificate_claimer/service"
	"github.com/urfave/cli/v2"
)

func main() {
	cli.VersionPrinter = func(*cli.Context) {
		aggkit.PrintVersion(os.Stdout)
	}

	app := cli.NewApp()
	app.Name = "exit-certificate-claimer"
	app.Usage = "Serve claimAsset / claimMessage parameters for the bridge exits of a settled exit certificate"
	app.Version = aggkit.Version
	app.Description = `Backend HTTP service for claiming the bridge exits produced by the exit_certificate tool.

Given an address it returns:
  - the available bridge exits for that address (GET /claimer/v2/bridges)
  - the full set of AgglayerBridge.claimAsset / claimMessage parameters per exit
    (GET /claimer/v2/claim-params)

API v1 (/claimer/v1) is still served: Transfer exits only, looked up by destination address.
API v2 also serves Message exits, looked up by destination address or by the beneficiary decoded
from their metadata through a configured message route (messageRoutes / --message-routes).

Data sources:
  - exit-certificate-signed.json       (the certificate bridge exits)
  - step-g-l2bridgesyncerlite.sqlite   (the L2 local exit tree, for local merkle proofs)
  - an l1infotreesync SQLite database   (mainnet/rollup exit roots + rollup proof), opened
                                         read-only or kept in sync from L1 when l1Sync.enabled.

The run data (certificate, local exit tree, WAIT result) can also be downloaded from the run's
archive in a GCS bucket: runSource in the config, or --run-source with --exit-certificate-config.`
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
			Name: "run-source",
			Usage: "With --exit-certificate-config: serve the run archived at this gs://<bucket>/<prefix> " +
				"(downloaded into the exit_certificate archive cache) instead of the files of its output dir",
			EnvVars: []string{"CLAIMER_RUN_SOURCE"},
		},
		&cli.StringFlag{
			Name: "message-routes",
			Usage: "Path to a JSON or TOML file with a messageRoutes list (how API v2 decodes the beneficiary " +
				"of Message exits). Replaces the messageRoutes of --config; the way to set them with " +
				"--exit-certificate-config",
			EnvVars: []string{"CLAIMER_MESSAGE_ROUTES"},
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
		&cli.BoolFlag{
			Name:  "log-json",
			Usage: "Emit logs in JSON format (default is human-readable console format)",
		},
	}
	app.Action = claimer.Run
	app.Commands = []*cli.Command{
		{
			Name:  "healthcheck",
			Usage: "Probe a running claimer's health endpoint and exit 0 (healthy) or 1 (unhealthy)",
			Description: "Meant to back a container HEALTHCHECK: the production image has no shell or curl, " +
				"so the claimer binary itself performs the HTTP probe.",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "address",
					Usage: "Host/IP where the claimer is listening",
					Value: "127.0.0.1",
				},
				&cli.IntFlag{
					Name:  "port",
					Usage: "Port where the claimer is listening",
					Value: claimer.HealthCheckDefaultPort,
				},
				&cli.DurationFlag{
					Name:  "timeout",
					Usage: "Probe timeout",
					Value: claimer.HealthCheckDefaultTimeout,
				},
			},
			Action: claimer.RunHealthCheck,
		},
	}

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
