package force_ger_update

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// newLoadConfigContext builds a urfave/cli context exposing the --cfg flag that LoadConfig reads.
func newLoadConfigContext(t *testing.T, cfgFiles ...string) *cli.Context {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Var(cli.NewStringSlice(), "cfg", "")
	args := make([]string, 0, len(cfgFiles)*2)
	for _, f := range cfgFiles {
		args = append(args, "--cfg", f)
	}
	require.NoError(t, fs.Parse(args))
	c := cli.NewContext(nil, fs, nil)
	c.Context = context.Background()
	return c
}

// writeTempConfig writes content to a temp file and returns its path.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func exampleConfigPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs("example-config.toml")
	require.NoError(t, err)
	_, err = os.Stat(path)
	require.NoError(t, err, "example-config.toml must exist")
	return path
}

func TestLoadConfig_ExampleConfig(t *testing.T) {
	c := newLoadConfigContext(t, exampleConfigPath(t))

	cfg, err := LoadConfig(c)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	fgu := cfg.ForceGERUpdate
	require.Equal(t, "http://localhost:8545", fgu.L1URL)
	require.Empty(t, fgu.L1WSURL)
	require.Equal(t, common.HexToAddress("0x1111111111111111111111111111111111111111"),
		fgu.GlobalExitRootManagerAddr)
	require.Equal(t, common.HexToAddress("0x2222222222222222222222222222222222222222"), fgu.BridgeAddr)

	// types.Duration fields must decode correctly from TOML duration strings.
	require.Equal(t, time.Hour, fgu.MaxTimeWithoutGERUpdate.Duration)
	require.Equal(t, 10*time.Second, fgu.CheckInterval.Duration)
	require.Equal(t, 15*time.Second, fgu.EventPollInterval.Duration)

	require.EqualValues(t, 50000, fgu.InitialLookbackBlocks)
	require.EqualValues(t, 10000, fgu.FilterLogsChunkSize)
	require.EqualValues(t, 1, fgu.DestinationNetwork)
	require.False(t, fgu.DryRun)

	// Nested EthTxManager section (same shape as AggOracle.EVMSender.EthTxManager).
	require.Equal(t, time.Second, fgu.EthTxManager.FrequencyToMonitorTxs.Duration)
	require.Equal(t, 2*time.Second, fgu.EthTxManager.WaitTxToBeMined.Duration)
	require.Equal(t, "/tmp/aggkit/ethtxmanager-force_ger_update.sqlite", fgu.EthTxManager.StoragePath)
	require.Equal(t, "http://localhost:8545", fgu.EthTxManager.Etherman.URL)
	require.EqualValues(t, 1337, fgu.EthTxManager.Etherman.L1ChainID)

	require.Len(t, fgu.EthTxManager.PrivateKeys, 1)
	require.Equal(t, signertypes.MethodLocal, fgu.EthTxManager.PrivateKeys[0].Method)

	// Config as loaded from the example must be valid on its own.
	require.NoError(t, fgu.Validate())
}

// TestLoadConfig_PrivateKeysMethods proves both the local-keystore and GCP-KMS SignerConfig shapes
// decode correctly from the [[ForceGERUpdate.EthTxManager.PrivateKeys]] section.
func TestLoadConfig_PrivateKeysMethods(t *testing.T) {
	const cfgTOML = `
[ForceGERUpdate]
L1URL = "http://localhost:8545"
GlobalExitRootManagerAddr = "0x1111111111111111111111111111111111111111"
BridgeAddr = "0x2222222222222222222222222222222222222222"
MaxTimeWithoutGERUpdate = "1h"
CheckInterval = "10s"
EventPollInterval = "15s"
InitialLookbackBlocks = 50000
FilterLogsChunkSize = 10000
DestinationNetwork = 1
DestinationAddress = "0x0000000000000000000000000000000000000000"
DryRun = false

	[ForceGERUpdate.EthTxManager]
	FrequencyToMonitorTxs = "1s"
	WaitTxToBeMined = "2s"
	GetReceiptMaxTime = "250ms"
	GetReceiptWaitInterval = "1s"
	PrivateKeys = [
		{Method = "local", Path = "/app/keystore/force_ger_update.keystore", Password = "testonly"},
		{Method = "GCP", KeyName = "projects/p/locations/l/keyRings/kr/cryptoKeys/k/cryptoKeyVersions/1"},
	]
	StoragePath = "/tmp/aggkit/ethtxmanager-force_ger_update.sqlite"

		[ForceGERUpdate.EthTxManager.Etherman]
		URL = "http://localhost:8545"
		L1ChainID = 1337
`

	path := writeTempConfig(t, cfgTOML)
	c := newLoadConfigContext(t, path)

	cfg, err := LoadConfig(c)
	require.NoError(t, err)

	keys := cfg.ForceGERUpdate.EthTxManager.PrivateKeys
	require.Len(t, keys, 2)

	local := keys[0]
	require.Equal(t, signertypes.MethodLocal, local.Method)
	path0, err := local.Get("Path")
	require.NoError(t, err)
	require.Equal(t, "/app/keystore/force_ger_update.keystore", path0)
	password0, err := local.Get("Password")
	require.NoError(t, err)
	require.Equal(t, "testonly", password0)

	gcp := keys[1]
	require.Equal(t, signertypes.MethodGCPKMS, gcp.Method)
	keyName, err := gcp.Get("KeyName")
	require.NoError(t, err)
	require.Equal(t, "projects/p/locations/l/keyRings/kr/cryptoKeys/k/cryptoKeyVersions/1", keyName)
}

func validBaseConfig() ForceGERUpdateConfig {
	return ForceGERUpdateConfig{
		L1URL:                     "http://localhost:8545",
		GlobalExitRootManagerAddr: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		BridgeAddr:                common.HexToAddress("0x2222222222222222222222222222222222222222"),
		DestinationNetwork:        1,
	}
}

func TestForceGERUpdateConfig_Validate(t *testing.T) {
	tests := map[string]struct {
		mutate  func(cfg *ForceGERUpdateConfig)
		wantErr string
	}{
		"valid config": {
			mutate:  func(cfg *ForceGERUpdateConfig) {},
			wantErr: "",
		},
		"missing L1URL": {
			mutate:  func(cfg *ForceGERUpdateConfig) { cfg.L1URL = "" },
			wantErr: "L1URL is required",
		},
		"zero DestinationNetwork": {
			mutate:  func(cfg *ForceGERUpdateConfig) { cfg.DestinationNetwork = 0 },
			wantErr: "DestinationNetwork must not be 0",
		},
		"zero BridgeAddr": {
			mutate:  func(cfg *ForceGERUpdateConfig) { cfg.BridgeAddr = common.Address{} },
			wantErr: "BridgeAddr is required",
		},
		"zero GlobalExitRootManagerAddr": {
			mutate:  func(cfg *ForceGERUpdateConfig) { cfg.GlobalExitRootManagerAddr = common.Address{} },
			wantErr: "GlobalExitRootManagerAddr is required",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := validBaseConfig()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}
