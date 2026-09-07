package bridgelooptester

import (
	"fmt"

	"github.com/agglayer/aggkit/log"
	"github.com/urfave/cli/v2"
)

// CmdRun executes the continuous bridge loop test.
func CmdRun(c *cli.Context) error {
	return fmt.Errorf("run command: not implemented yet")
}

// CmdValidate loads the configuration file(s) given via --cfg, runs (*Config).Validate, and reports
// every problem found. It does not perform any network preflight (e.g. confirming
// gasTokenAddress() live for every configured network, per DESIGN.md §6) - that is a later step's
// responsibility once the tool can actually dial out to the configured RPC/proxy endpoints.
func CmdValidate(c *cli.Context) error {
	cfgFiles := c.StringSlice("cfg")
	if len(cfgFiles) == 0 {
		return fmt.Errorf("validate command: no config file(s) given, use --cfg/-c")
	}

	cfg, err := LoadConfig(cfgFiles...)
	if err != nil {
		return fmt.Errorf("validate command: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate command: configuration is invalid:\n%w", err)
	}

	for _, warning := range cfg.Warnings() {
		log.Warnf("validate command: %s", warning)
	}

	log.Infof("validate command: configuration is valid (%d networks, %d loops); "+
		"network preflight (live gasTokenAddress()/eth_chainId checks) is not performed by this command",
		len(cfg.Networks), len(cfg.Loops))

	return nil
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
