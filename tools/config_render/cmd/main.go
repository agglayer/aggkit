package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/agglayer/aggkit/config"
	configrender "github.com/agglayer/aggkit/tools/config_render"
	"github.com/urfave/cli/v2"
)

func main() {
	app := cli.NewApp()
	app.Name = "config-render"
	app.Usage = "Compose and resolve an aggkit config file (defaults + vars + config), reporting unresolved variables"
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{
			Name:     "cfg",
			Aliases:  []string{"c"},
			Usage:    "Config file to render (repeatable, merged in order)",
			Required: true,
		},
		&cli.StringFlag{
			Name:  "default-values",
			Usage: "File replacing aggkit's DefaultValues (default: the one embedded in aggkit/config/default.go)",
		},
		&cli.StringFlag{
			Name:  "default-vars",
			Usage: "File replacing aggkit's DefaultVars (default: the one embedded in aggkit/config/default.go)",
		},
		&cli.StringFlag{
			Name:  "mandatory-vars",
			Usage: "File with the DefaultMandatoryVars layer to use (implies using it)",
		},
		&cli.BoolFlag{
			Name:  "use-default-mandatory-vars",
			Usage: "Use aggkit's DefaultMandatoryVars from aggkit/config/default.go (ignored if --mandatory-vars is set)",
		},
		&cli.StringFlag{
			Name:  "deprecated-fields",
			Usage: "TOML file of \"pattern\" = \"reason\" entries; fail if the rendered config uses any of them",
		},
		&cli.BoolFlag{
			Name:  "use-default-deprecated-fields",
			Usage: "Check against aggkit's built-in deprecated fields (ignored if --deprecated-fields is set)",
		},
		&cli.StringFlag{
			Name:  "env-prefix",
			Usage: "Prefix of the env vars that override variables",
			Value: config.EnvVarPrefix,
		},
		&cli.StringFlag{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "Write the resolved config to this file (default: stdout)",
		},
	}
	app.Action = run

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(c *cli.Context) error {
	rendered, err := configrender.Render(configrender.Options{
		DefaultValuesFile:          c.String("default-values"),
		DefaultVarsFile:            c.String("default-vars"),
		MandatoryVarsFile:          c.String("mandatory-vars"),
		UseDefaultMandatoryVars:    c.Bool("use-default-mandatory-vars"),
		ConfigFiles:                c.StringSlice("cfg"),
		DeprecatedFieldsFile:       c.String("deprecated-fields"),
		UseDefaultDeprecatedFields: c.Bool("use-default-deprecated-fields"),
		EnvPrefix:                  c.String("env-prefix"),
	})
	if err != nil {
		switch {
		case errors.Is(err, config.ErrMissingVars):
			return fmt.Errorf("unresolved variables (define them in a vars file or via %s_<VAR>): %w",
				c.String("env-prefix"), err)
		case errors.Is(err, config.ErrCycleVars):
			return fmt.Errorf("variables reference each other in a cycle: %w", err)
		default:
			return err
		}
	}

	if out := c.String("output"); out != "" {
		return os.WriteFile(out, []byte(rendered), config.DefaultCreationFilePermissions)
	}
	_, err = fmt.Fprint(os.Stdout, rendered)
	return err
}
