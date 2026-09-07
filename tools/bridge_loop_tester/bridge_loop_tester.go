package bridgelooptester

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

// CmdRun executes the continuous bridge loop test.
func CmdRun(c *cli.Context) error {
	return fmt.Errorf("run command: not implemented yet")
}

// CmdValidate validates the configuration without running the loop.
func CmdValidate(c *cli.Context) error {
	return fmt.Errorf("validate command: not implemented yet")
}

// CmdDeployToken deploys an ERC20 token on the specified network.
func CmdDeployToken(c *cli.Context) error {
	return fmt.Errorf("deploy-token command: not implemented yet")
}

// CmdClaim manually claims pending bridge exits.
func CmdClaim(c *cli.Context) error {
	return fmt.Errorf("claim command: not implemented yet")
}

// CmdStatus displays the current status of the loop and pending claims.
func CmdStatus(c *cli.Context) error {
	return fmt.Errorf("status command: not implemented yet")
}
