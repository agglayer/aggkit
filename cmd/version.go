package main

import (
	"os"

	zkevm "github.com/agglayer/aggkit"
	"github.com/urfave/cli/v2"
)

func versionCmd(*cli.Context) error {
	zkevm.PrintVersion(os.Stdout)

	return nil
}
