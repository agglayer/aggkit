// Package configrender composes and resolves an aggkit configuration file
// using the same pipeline as the aggkit binary (config.ConfigRender), but
// allowing each default layer to be overridden or skipped.
package configrender

import (
	"fmt"

	"github.com/agglayer/aggkit/config"
)

// Options describes the layers to compose. Layers are merged in this order
// (later ones override earlier ones): mandatory vars, vars, values, config files.
type Options struct {
	// DefaultValuesFile overrides config.DefaultValues when not empty.
	DefaultValuesFile string
	// DefaultVarsFile overrides config.DefaultVars when not empty.
	DefaultVarsFile string
	// MandatoryVarsFile overrides config.DefaultMandatoryVars when not empty.
	MandatoryVarsFile string
	// UseDefaultMandatoryVars uses config.DefaultMandatoryVars
	// (ignored if MandatoryVarsFile is set).
	UseDefaultMandatoryVars bool
	// ConfigFiles are the user config files, merged in order.
	ConfigFiles []string
	// EnvPrefix is the prefix for env var overrides (default config.EnvVarPrefix).
	EnvPrefix string
}

// Render composes all layers and resolves the variables. On failure the error
// wraps config.ErrMissingVars or config.ErrCycleVars when applicable.
func Render(opts Options) (string, error) {
	files, err := BuildFileData(opts)
	if err != nil {
		return "", err
	}
	prefix := opts.EnvPrefix
	if prefix == "" {
		prefix = config.EnvVarPrefix
	}
	return config.NewConfigRender(files, prefix).Render()
}

// BuildFileData reads every layer and returns them in merge order.
func BuildFileData(opts Options) ([]config.FileData, error) {
	files := make([]config.FileData, 0, 3+len(opts.ConfigFiles)) //nolint:mnd

	switch {
	case opts.MandatoryVarsFile != "":
		fd, err := readLayer("mandatory_vars", opts.MandatoryVarsFile)
		if err != nil {
			return nil, err
		}
		files = append(files, fd)
	case opts.UseDefaultMandatoryVars:
		files = append(files, config.FileData{Name: "default_mandatory_vars", Content: config.DefaultMandatoryVars})
	}

	vars, err := layerOrDefault("default_vars", opts.DefaultVarsFile, config.DefaultVars)
	if err != nil {
		return nil, err
	}
	values, err := layerOrDefault("default_values", opts.DefaultValuesFile, config.DefaultValues)
	if err != nil {
		return nil, err
	}
	files = append(files, vars, values)

	for _, path := range opts.ConfigFiles {
		fd, err := readLayer(path, path)
		if err != nil {
			return nil, err
		}
		files = append(files, fd)
	}
	return files, nil
}

func layerOrDefault(name, path, defaultContent string) (config.FileData, error) {
	if path == "" {
		return config.FileData{Name: name, Content: defaultContent}, nil
	}
	return readLayer(name, path)
}

func readLayer(name, path string) (config.FileData, error) {
	// Same loading as aggkit: non-TOML files (e.g. JSON) are converted to TOML.
	fds, err := config.ReadFiles([]string{path})
	if err != nil {
		return config.FileData{}, fmt.Errorf("reading %s (%s): %w", name, path, err)
	}
	return config.FileData{Name: name, Content: fds[0].Content}, nil
}
