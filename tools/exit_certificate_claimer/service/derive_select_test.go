package claimer

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// newCLIContext builds a urfave/cli context with the flags loadOrDeriveConfig/selectConfig read,
// parsed from args (e.g. []string{"--config", path}). Flags present in args report IsSet()==true.
func newCLIContext(t *testing.T, args []string) *cli.Context {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("config", "", "")
	fs.String("exit-certificate-config", "", "")
	fs.String("address", "", "")
	fs.Int("port", 0, "")
	require.NoError(t, fs.Parse(args))
	return cli.NewContext(nil, fs, nil)
}

func writeNativeClaimerConfig(t *testing.T) string {
	t.Helper()
	const cfg = `{
  "signedCertificatePath": "/c.json",
  "localExitTreeDBPath": "/l.sqlite",
  "l1InfoTreeDBPath": "/i.sqlite",
  "stepWaitResultPath": "/w.json"
}`
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))
	return path
}

func writeExitCertificateConfig(t *testing.T) string {
	t.Helper()
	const cfg = `{
  "l2RpcUrl": "http://localhost:8545",
  "l2BridgeAddress": "0x1111111111111111111111111111111111111111",
  "exitAddress": "0x2222222222222222222222222222222222222222",
  "targetBlock": "100",
  "options": {
    "useAgglayerAdminToStepFCheck": false
  }
}`
	path := filepath.Join(t.TempDir(), "exit-certificate.json")
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))
	return path
}

func TestSelectConfigNative(t *testing.T) {
	t.Parallel()
	c := newCLIContext(t, []string{"--config", writeNativeClaimerConfig(t)})
	cfg, err := selectConfig(context.Background(), c, log.GetDefaultLogger())
	require.NoError(t, err)
	require.Equal(t, defaultAddress, cfg.Address)
}

func TestSelectConfigMutuallyExclusive(t *testing.T) {
	t.Parallel()
	c := newCLIContext(t, []string{
		"--config", writeNativeClaimerConfig(t),
		"--exit-certificate-config", writeExitCertificateConfig(t),
	})
	_, err := selectConfig(context.Background(), c, log.GetDefaultLogger())
	require.ErrorContains(t, err, "mutually exclusive")
}

func TestSelectConfigDeriveBadPath(t *testing.T) {
	t.Parallel()
	c := newCLIContext(t, []string{"--exit-certificate-config", filepath.Join(t.TempDir(), "missing.json")})
	_, err := selectConfig(context.Background(), c, log.GetDefaultLogger())
	require.ErrorContains(t, err, "loading exit_certificate config")
}

func TestSelectConfigDeriveResolveRollupManagerFails(t *testing.T) {
	t.Parallel()
	// A valid exit_certificate config with no l1RpcUrl: deriving fails resolving RollupManager.
	c := newCLIContext(t, []string{"--exit-certificate-config", writeExitCertificateConfig(t)})
	_, err := selectConfig(context.Background(), c, log.GetDefaultLogger())
	require.ErrorContains(t, err, "l1RpcUrl is not set")
}

func TestLoadOrDeriveConfigAppliesCLIOverrides(t *testing.T) {
	t.Parallel()
	c := newCLIContext(t, []string{
		"--config", writeNativeClaimerConfig(t),
		"--address", "0.0.0.0",
		"--port", "12345",
	})
	cfg, err := loadOrDeriveConfig(context.Background(), c, log.GetDefaultLogger())
	require.NoError(t, err)
	require.Equal(t, "0.0.0.0", cfg.Address)
	require.Equal(t, 12345, cfg.Port)
}

func TestLoadOrDeriveConfigPropagatesError(t *testing.T) {
	t.Parallel()
	// Neither flag set: LoadConfig("") fails and the error propagates.
	c := newCLIContext(t, nil)
	_, err := loadOrDeriveConfig(context.Background(), c, log.GetDefaultLogger())
	require.Error(t, err)
}

func TestRunConfigError(t *testing.T) {
	// Run with no config flags set fails fast at config loading (before any data source is opened).
	c := newCLIContext(t, nil)
	require.Error(t, Run(c))
}

// newRollupManagerRPCStub serves the single eth_call RollupManager() makes, returning rollupManager
// left-padded to a 32-byte ABI word. ethclient dials it lazily, so no chainId handshake is needed.
func newRollupManagerRPCStub(t *testing.T, rollupManager common.Address) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		padded := make([]byte, 32)
		copy(padded[12:], rollupManager.Bytes())
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result":  "0x" + common.Bytes2Hex(padded),
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDeriveFromExitCertificateSuccess(t *testing.T) {
	t.Parallel()
	rollupManager := common.HexToAddress("0x9999999999999999999999999999999999999999")
	srv := newRollupManagerRPCStub(t, rollupManager)

	ec := &exitcertificate.Config{
		L1RPCURL:                srv.URL,
		SovereignRollupAddr:     common.HexToAddress("0x3333333333333333333333333333333333333333"),
		L2NetworkID:             2,
		L1GlobalExitRootAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
	}
	ec.Options.OutputDir = t.TempDir()
	ec.Options.L1StartBlock = 10
	ec.Options.BlockRange = 5000

	cfg, err := DeriveFromExitCertificate(context.Background(), ec)
	require.NoError(t, err)
	require.True(t, cfg.L1Sync.Enabled)
	require.Equal(t, srv.URL, cfg.L1Sync.RPCURL)
	require.Equal(t, rollupManager.Hex(), cfg.L1Sync.RollupManagerAddr)
	require.Equal(t, uint32(2), cfg.NetworkID)
	require.Equal(t, fmt.Sprintf("%d", ec.Options.L1StartBlock), fmt.Sprintf("%d", cfg.L1Sync.InitialBlock))
}
