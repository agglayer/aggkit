package claimer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const sampleJSONConfig = `{
  "address": "127.0.0.1",
  "port": 9090,
  "signedCertificatePath": "exit-certificate-signed.json",
  "localExitTreeDBPath": "step-g-l2bridgesyncerlite.sqlite",
  "l1InfoTreeDBPath": "L1InfoTreeSync.sqlite",
  "stepWaitResultPath": "step-wait-result.json",
  "networkId": 2,
  "l1Sync": {
    "enabled": true,
    "rpcUrl": "http://localhost:8545"
  }
}`

func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadConfigJSON(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, "config.json", sampleJSONConfig)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	require.Equal(t, "127.0.0.1", cfg.Address)
	require.Equal(t, 9090, cfg.Port)
	require.Equal(t, uint32(2), cfg.NetworkID)
	require.True(t, cfg.L1Sync.Enabled)
	require.Equal(t, "http://localhost:8545", cfg.L1Sync.RPCURL)

	// Relative paths are resolved against the config's directory.
	baseDir := filepath.Dir(path)
	require.Equal(t, filepath.Join(baseDir, "exit-certificate-signed.json"), cfg.SignedCertificatePath)
	require.Equal(t, filepath.Join(baseDir, "step-wait-result.json"), cfg.StepWaitResultPath)

	// Defaults applied for the unspecified timeouts.
	require.Equal(t, defaultReadTimeoutSeconds, cfg.ReadTimeoutSeconds)
	require.Equal(t, defaultWriteTimeoutSeconds, cfg.WriteTimeoutSeconds)
}

func TestLoadConfigTOML(t *testing.T) {
	t.Parallel()

	const tomlConfig = `
port = 8081
signedCertificatePath = "/abs/exit-certificate-signed.json"
localExitTreeDBPath = "/abs/local.sqlite"
l1InfoTreeDBPath = "/abs/l1.sqlite"
stepWaitResultPath = "/abs/step-wait-result.json"

[l1Sync]
enabled = false
`
	path := writeConfig(t, "config.toml", tomlConfig)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	require.Equal(t, 8081, cfg.Port)
	require.Equal(t, defaultAddress, cfg.Address) // default applied
	require.False(t, cfg.L1Sync.Enabled)
	// Absolute paths are left untouched.
	require.Equal(t, "/abs/exit-certificate-signed.json", cfg.SignedCertificatePath)
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Parallel()

	const minimal = `{
  "signedCertificatePath": "/c.json",
  "localExitTreeDBPath": "/l.sqlite",
  "l1InfoTreeDBPath": "/i.sqlite",
  "stepWaitResultPath": "/w.json"
}`
	path := writeConfig(t, "config.json", minimal)
	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	require.Equal(t, defaultAddress, cfg.Address)
	require.Equal(t, defaultPort, cfg.Port)
	require.Equal(t, defaultReadTimeoutSeconds, cfg.ReadTimeoutSeconds)
	require.Equal(t, defaultWriteTimeoutSeconds, cfg.WriteTimeoutSeconds)
}

func TestLoadConfigErrors(t *testing.T) {
	t.Parallel()

	_, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)

	_, err = LoadConfig(writeConfig(t, "bad.json", `{not json`))
	require.ErrorContains(t, err, "parsing config")

	_, err = LoadConfig(writeConfig(t, "bad.toml", "= invalid toml"))
	require.ErrorContains(t, err, "parsing TOML config")

	// Parses fine but fails validation (required path missing).
	const incomplete = `{"localExitTreeDBPath": "/l.sqlite"}`
	_, err = LoadConfig(writeConfig(t, "incomplete.json", incomplete))
	require.ErrorContains(t, err, "signedCertificatePath is required")
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:    "missing signed certificate",
			mutate:  func(c *Config) { c.SignedCertificatePath = "" },
			wantErr: "signedCertificatePath is required",
		},
		{
			name:    "missing local exit tree",
			mutate:  func(c *Config) { c.LocalExitTreeDBPath = "" },
			wantErr: "localExitTreeDBPath is required",
		},
		{
			name:    "missing l1 info tree",
			mutate:  func(c *Config) { c.L1InfoTreeDBPath = "" },
			wantErr: "l1InfoTreeDBPath is required",
		},
		{
			name:    "missing wait result",
			mutate:  func(c *Config) { c.StepWaitResultPath = "" },
			wantErr: "stepWaitResultPath is required",
		},
		{
			name: "l1 sync enabled without rpc url",
			mutate: func(c *Config) {
				c.L1Sync.Enabled = true
				c.L1Sync.RPCURL = ""
			},
			wantErr: "l1Sync.rpcUrl is required",
		},
	}

	valid := func() *Config {
		return &Config{
			SignedCertificatePath: "/c.json",
			LocalExitTreeDBPath:   "/l.sqlite",
			L1InfoTreeDBPath:      "/i.sqlite",
			StepWaitResultPath:    "/w.json",
		}
	}

	require.NoError(t, valid().validate())

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid()
			tc.mutate(cfg)
			require.ErrorContains(t, cfg.validate(), tc.wantErr)
		})
	}
}

func TestListenAddress(t *testing.T) {
	t.Parallel()

	cfg := &Config{Address: "127.0.0.1", Port: 7080}
	require.Equal(t, "127.0.0.1:7080", cfg.ListenAddress())
}
