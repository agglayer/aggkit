// Package configrender composes and resolves an aggkit configuration file
// using the same pipeline as the aggkit binary (config.ConfigRender), but
// allowing each default layer to be overridden or skipped.
package configrender

import (
	"fmt"
	"os"
	"sort"

	"github.com/agglayer/aggkit/config"
	kotoml "github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
	"github.com/pelletier/go-toml/v2"
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
	// DeprecatedFieldsFile is a TOML file mapping a field pattern to its reason
	// (e.g. "AggSender.URLRPCL2" = "use L2URL"). Replaces config.DefaultDeprecatedFields.
	DeprecatedFieldsFile string
	// UseDefaultDeprecatedFields checks against aggkit's deprecated fields
	// (ignored if DeprecatedFieldsFile is set).
	UseDefaultDeprecatedFields bool
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
	rendered, err := config.NewConfigRender(files, prefix).Render()
	if err != nil {
		return rendered, err
	}
	rules, err := deprecatedRules(opts)
	if err != nil || len(rules) == 0 {
		return rendered, err
	}
	keys, err := renderedKeys(rendered)
	if err != nil {
		return rendered, err
	}
	return rendered, config.CheckDeprecatedFields(keys, rules)
}

func deprecatedRules(opts Options) ([]config.DeprecatedField, error) {
	switch {
	case opts.DeprecatedFieldsFile != "":
		return LoadDeprecatedFields(opts.DeprecatedFieldsFile)
	case opts.UseDefaultDeprecatedFields:
		return config.DefaultDeprecatedFields(), nil
	}
	return nil, nil
}

// LoadDeprecatedFields reads a TOML file of "pattern" = "reason" entries. A pattern ending
// with a dot, or naming a section, matches everything under it.
func LoadDeprecatedFields(path string) ([]config.DeprecatedField, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading deprecated fields file %s: %w", path, err)
	}
	entries := map[string]string{}
	if err := toml.Unmarshal(content, &entries); err != nil {
		return nil, fmt.Errorf("parsing deprecated fields file %s (expected \"pattern\" = \"reason\"): %w", path, err)
	}
	patterns := make([]string, 0, len(entries))
	for pattern := range entries {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	rules := make([]config.DeprecatedField, 0, len(patterns))
	for _, pattern := range patterns {
		rules = append(rules, config.DeprecatedField{FieldNamePattern: pattern, Reason: entries[pattern]})
	}
	return rules, nil
}

// renderedKeys returns the leaf keys (dot separated) of the rendered TOML config.
func renderedKeys(rendered string) ([]string, error) {
	k := koanf.New(".")
	if err := k.Load(rawbytes.Provider([]byte(rendered)), kotoml.Parser()); err != nil {
		return nil, fmt.Errorf("parsing rendered config: %w", err)
	}
	return k.Keys(), nil
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
