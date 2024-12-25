package main

import (
	"os"

	"github.com/agglayer/aggkit"
	"github.com/urfave/cli/v2"
)

func versionCmd(*cli.Context) error {
	aggkit.PrintVersion(os.Stdout)

	return nil
}
