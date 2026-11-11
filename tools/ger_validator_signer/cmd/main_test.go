package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func execute(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestRun_Usage(t *testing.T) {
	t.Run("no command", func(t *testing.T) {
		code, stdout, stderr := execute(t)
		require.Equal(t, exitUsage, code)
		require.Empty(t, stdout)
		require.Contains(t, stderr, "Usage:")
		require.Contains(t, stderr, "ger_validator_signer run --cfg")
	})

	t.Run("unknown command", func(t *testing.T) {
		code, _, stderr := execute(t, "serve")
		require.Equal(t, exitUsage, code)
		require.Contains(t, stderr, `unknown command "serve"`)
		require.Contains(t, stderr, "Usage:")
	})

	for _, arg := range []string{"help", "-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			code, stdout, stderr := execute(t, arg)
			require.Equal(t, exitOK, code)
			require.Contains(t, stdout, "Usage:")
			require.Empty(t, stderr)
		})
	}
}

func TestRun_Version(t *testing.T) {
	code, stdout, stderr := execute(t, "version")
	require.Equal(t, exitOK, code)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Version:      v0.0.0-dev", "the default version when it was not set at build time")
	require.Contains(t, stdout, "Git revision:")
}

func TestRun_RunCommandArguments(t *testing.T) {
	t.Run("the config is required", func(t *testing.T) {
		code, _, stderr := execute(t, "run")
		require.Equal(t, exitUsage, code)
		require.Contains(t, stderr, "the flag --cfg is required")
	})

	t.Run("unknown flag", func(t *testing.T) {
		code, _, stderr := execute(t, "run", "--bogus")
		require.Equal(t, exitUsage, code)
		require.Contains(t, stderr, "flag provided but not defined")
	})

	t.Run("the config file doesn't exist", func(t *testing.T) {
		code, _, stderr := execute(t, "run", "--cfg", filepath.Join(t.TempDir(), "missing.toml"))
		require.Equal(t, exitError, code)
		require.Contains(t, stderr, "failed to read the configuration file")
	})

	t.Run("the config is invalid", func(t *testing.T) {
		code, _, stderr := execute(t, "run", "--cfg", writeConfig(t, `L1RPCURL = "http://localhost:8545"`))
		require.Equal(t, exitError, code)
		require.Contains(t, stderr, "L1ChainID is required")
	})

	t.Run("an unknown key in the config", func(t *testing.T) {
		code, _, stderr := execute(t, "run", "--cfg", writeConfig(t, `Whatever = 1`))
		require.Equal(t, exitError, code)
		require.Contains(t, stderr, "Whatever")
	})
}

// A validator that can't start (here, the keystore is missing) exits with an error and says why. That the token of the
// L1 URL is not leaked when L1 fails is tested in the app package
func TestRun_StartupFailureExitsWithAnError(t *testing.T) {
	l1 := httptest.NewServer(http.NotFoundHandler())
	l1URL := l1.URL
	l1.Close() // L1 is down

	path := writeConfig(t, `
L1RPCURL = "`+l1URL+`/s3cr3t-t0k3n"
L1ChainID = 1337
AgglayerGERAddr = "0x1111111111111111111111111111111111111111"
L1RequestTimeout = "500ms"

[Signer]
Method = "local"
Path = "/does/not/exist.keystore"
Password = "x"

[REST]
InsecureHTTP = true
`)

	code, _, stderr := execute(t, "run", "--cfg", path)

	require.Equal(t, exitError, code)
	require.Contains(t, stderr, "the validator stopped with an error")
	require.NotContains(t, stderr, "s3cr3t-t0k3n")
}
