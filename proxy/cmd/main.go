package main

import (
	"os"

	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/log"
	proxyconfig "github.com/agglayer/aggkit/proxy/config"
	"github.com/urfave/cli/v2"
)

const appName = "aggkit-proxy"

var (
	configFileFlag = cli.StringSliceFlag{
		Name:     proxyconfig.FlagCfg,
		Aliases:  []string{"c"},
		Usage:    "Configuration file(s)",
		Required: false,
	}
	componentsFlag = cli.StringSliceFlag{
		Name:     proxyconfig.FlagComponents,
		Aliases:  []string{"co"},
		Usage:    "List of components to run",
		Required: false,
		Value:    cli.NewStringSlice(proxyconfig.PROXY, proxyconfig.TRACKER),
	}
)

func main() {
	app := cli.NewApp()
	app.Name = appName
	app.Version = aggkit.Version
	app.Commands = []*cli.Command{
		{
			Name:   "version",
			Usage:  "Application version and build",
			Action: versionCmd,
		},
		{
			Name:   "run",
			Usage:  "Run the aggkit proxy",
			Action: start,
			Flags:  []cli.Flag{&configFileFlag, &componentsFlag},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func versionCmd(*cli.Context) error {
	aggkit.PrintVersion(os.Stdout)

	return nil
}
