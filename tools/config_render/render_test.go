package configrender

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/config"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func TestRender_CustomLayers(t *testing.T) {
	vars := write(t, "vars.toml", `Name = "bob"`)
	values := write(t, "values.toml", "Greeting = \"hi {{Name}}\"\nPort = {{Port}}")
	cfg := write(t, "cfg.toml", `Port = 8080`)

	out, err := Render(Options{DefaultVarsFile: vars, DefaultValuesFile: values, ConfigFiles: []string{cfg}})
	require.NoError(t, err)
	require.Contains(t, out, `hi bob`)
	require.Contains(t, out, `Port = 8080`)
}

func TestRender_MissingVars(t *testing.T) {
	vars := write(t, "vars.toml", `A = 1`)
	values := write(t, "values.toml", `B = {{Nope}}`)

	_, err := Render(Options{DefaultVarsFile: vars, DefaultValuesFile: values})
	require.ErrorIs(t, err, config.ErrMissingVars)
	require.ErrorContains(t, err, "Nope")
}

func TestRender_Cycle(t *testing.T) {
	vars := write(t, "vars.toml", "A = {{B}}\nB = {{A}}")
	values := write(t, "values.toml", `C = {{A}}`)

	_, err := Render(Options{DefaultVarsFile: vars, DefaultValuesFile: values})
	require.ErrorIs(t, err, config.ErrCycleVars)
}

func TestRender_MandatoryVarsOptional(t *testing.T) {
	values := write(t, "values.toml", `U = "{{L1URL}}"`)
	vars := write(t, "vars.toml", `X = 1`)

	_, err := Render(Options{DefaultVarsFile: vars, DefaultValuesFile: values})
	require.ErrorIs(t, err, config.ErrMissingVars)

	out, err := Render(Options{DefaultVarsFile: vars, DefaultValuesFile: values, UseDefaultMandatoryVars: true})
	require.NoError(t, err)
	require.Contains(t, out, "http://localhost:8545")

	mand := write(t, "mand.toml", `L1URL = "http://custom"`)
	out, err = Render(Options{DefaultVarsFile: vars, DefaultValuesFile: values, MandatoryVarsFile: mand})
	require.NoError(t, err)
	require.Contains(t, out, "http://custom")
}

func TestRender_AggkitDefaults(t *testing.T) {
	cfg := write(t, "cfg.toml", `[Log]
Level = "debug"`)
	out, err := Render(Options{UseDefaultMandatoryVars: true, ConfigFiles: []string{cfg}})
	require.NoError(t, err)
	require.Contains(t, out, `debug`)
}

func TestRender_MissingFile(t *testing.T) {
	_, err := Render(Options{ConfigFiles: []string{"/nonexistent.toml"}})
	require.Error(t, err)
}

func TestRender_JSONConfig(t *testing.T) {
	vars := write(t, "vars.toml", `A = 1`)
	values := write(t, "values.toml", `B = {{A}}`)
	cfg := write(t, "cfg.json", `{"Log": {"Level": "debug"}}`)

	out, err := Render(Options{DefaultVarsFile: vars, DefaultValuesFile: values, ConfigFiles: []string{cfg}})
	require.NoError(t, err)
	require.Contains(t, out, `debug`)
}
