package exit_certificate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_FileNotFound(t *testing.T) {
	t.Parallel()
	_, err := LoadConfig("/nonexistent/path/parameters.json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read config file")
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{not valid json}"), 0o600))

	_, err := LoadConfig(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse config JSON")
}

func TestLoadConfig_MissingL2RPCURL(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.json")
	data := `{"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe", "targetBlock": "100"}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	_, err := LoadConfig(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "l2RpcUrl")
}

func TestLoadConfig_MissingL2BridgeAddress(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.json")
	data := `{"l2RpcUrl": "http://localhost:8545", "targetBlock": "100"}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	_, err := LoadConfig(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "l2BridgeAddress")
}

func TestLoadConfig_MinimalValid(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "minimal.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100"
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8545", cfg.L2RPCURL)
	require.Equal(t, common.HexToAddress("0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe"), cfg.L2BridgeAddress)
	require.Equal(t, *aggkittypes.NewBlockNumber(100), cfg.TargetBlock)
	require.Equal(t, uint32(1), cfg.L2NetworkID)
	require.Equal(t, cfg.L2BridgeAddress, cfg.L1BridgeAddress)
}

func TestLoadConfig_FullConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "full.json")
	data := `{
		"l2RpcUrl": "http://l2:8545",
		"l1RpcUrl": "http://l1:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"l1BridgeAddress": "0x1111111111111111111111111111111111111111",
		"l2NetworkId": 5,
		"targetBlock": "LatestBlock",
		"exitAddress": "0x0000000000000000000000000000000000000001",
		"destinationNetwork": 0,
		"options": {
			"blockRange": 10000,
			"concurrencyLimit": 200,
			"rpcBatchSize": 200,
			"rpcDelayMs": 10,
			"l1StartBlock": 1000
		}
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "http://l2:8545", cfg.L2RPCURL)
	require.Equal(t, "http://l1:8545", cfg.L1RPCURL)
	require.Equal(t, uint32(5), cfg.L2NetworkID)
	require.Equal(t, aggkittypes.LatestBlock, cfg.TargetBlock)
	require.Equal(t, common.HexToAddress("0x0000000000000000000000000000000000000001"), cfg.ExitAddress)
	require.Equal(t, common.HexToAddress("0x1111111111111111111111111111111111111111"), cfg.L1BridgeAddress)
	require.Equal(t, 10000, cfg.Options.BlockRange)
	require.Equal(t, 200, cfg.Options.ConcurrencyLimit)
	require.Equal(t, 200, cfg.Options.RPCBatchSize)
	require.Equal(t, 10, cfg.Options.RPCDelayMs)
	require.Equal(t, uint64(1000), cfg.Options.L1StartBlock)
}

func TestLoadConfig_DefaultOptions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "defaults.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100"
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, 5000, cfg.Options.BlockRange)
	require.Equal(t, 150000, cfg.Options.StepAWindowSize)
	require.Equal(t, 20, cfg.Options.ConcurrencyLimit)
	require.Equal(t, 200, cfg.Options.RPCBatchSize)
	require.Equal(t, 0, cfg.Options.RPCDelayMs)
	require.Equal(t, uint64(0), cfg.Options.L1StartBlock)
}

func TestLoadConfig_StepAWindowSize(t *testing.T) {
	t.Parallel()

	t.Run("explicit value is read from file", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "cfg.json")
		data := `{
			"l2RpcUrl": "http://localhost:8545",
			"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
			"targetBlock": "100",
			"options": {
				"stepAWindowSize": 2000
			}
		}`
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		require.Equal(t, 2000, cfg.Options.StepAWindowSize)
	})

	t.Run("defaults to 5000 when absent", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "cfg.json")
		data := `{
			"l2RpcUrl": "http://localhost:8545",
			"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
			"targetBlock": "100"
		}`
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		require.Equal(t, defaultStepAWindowSize, cfg.Options.StepAWindowSize)
	})
}

func TestLoadConfig_RelativeOutputDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "parameters.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100",
		"options": {
			"outputDir": "./output"
		}
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "output"), cfg.Options.OutputDir)
}

func TestLoadLBTWrappedTokens_EmptyPath(t *testing.T) {
	t.Parallel()
	tokens, err := LoadLBTWrappedTokens("")
	require.NoError(t, err)
	require.Nil(t, tokens)
}

func TestLoadLBTWrappedTokens_FileNotFound(t *testing.T) {
	t.Parallel()
	_, err := LoadLBTWrappedTokens("/nonexistent/file.json")
	require.Error(t, err)
}

func TestLoadLBTWrappedTokens_ValidFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "lbt.json")

	entries := []LBTEntry{
		{
			WrappedTokenAddress: common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
			OriginNetwork:       0,
			OriginTokenAddress:  common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"),
			Balance:             "1000000",
		},
		{
			WrappedTokenAddress: common.Address{},
			OriginNetwork:       0,
			OriginTokenAddress:  common.Address{},
			Balance:             "500000",
		},
		{
			WrappedTokenAddress: common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"),
			OriginNetwork:       1,
			OriginTokenAddress:  common.HexToAddress("0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"),
			Balance:             "2000000",
		},
	}

	data, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	tokens, err := LoadLBTWrappedTokens(path)
	require.NoError(t, err)
	require.Len(t, tokens, 2)
	require.Equal(t, common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), tokens[0].WrappedTokenAddress)
	require.Equal(t, common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"), tokens[1].WrappedTokenAddress)
}

func TestLoadConfig_AgglayerAdminToken(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cfg.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100",
		"options": {
			"agglayerAdminURL": "https://admin.example.com",
			"agglayerAdminToken": "test-jwt-token"
		}
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "https://admin.example.com", cfg.Options.AgglayerAdminURL)
	require.Equal(t, "test-jwt-token", cfg.Options.AgglayerAdminToken)
}

func TestParseSignerConfig_Valid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	raw := json.RawMessage(`{"Method": "local", "Path": "keystore.json", "Password": "secret"}`)

	cfg, err := parseSignerConfig(raw, dir)
	require.NoError(t, err)
	require.Equal(t, "local", string(cfg.Method))
	require.Equal(t, filepath.Join(dir, "keystore.json"), cfg.Config["path"])
	require.Equal(t, "secret", cfg.Config["password"])
}

func TestParseSignerConfig_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := parseSignerConfig(json.RawMessage(`{bad}`), "/tmp")
	require.Error(t, err)
}

func TestMergeOptions_BoolFlags(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cfg.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100",
		"options": {
			"ignoreGenesisBalance": true,
			"ignoreOnTraceError": true,
			"ignoreBalanceMismatch": true,
			"ignoreUnclaimed": true
		}
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.True(t, cfg.Options.IgnoreGenesisBalance)
	require.True(t, cfg.Options.IgnoreOnTraceError)
	require.True(t, cfg.Options.IgnoreBalanceMismatch)
	require.True(t, cfg.Options.IgnoreUnclaimed)
}

func TestLoadConfig_AgglayerClient(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cfg.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100",
		"options": {
			"agglayerClient": {
				"GRPC": {
					"URL": "agglayer.example.com:50051",
					"UseTLS": true
				}
			}
		}
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Options.AgglayerClient.GRPC)
	require.Equal(t, "agglayer.example.com:50051", cfg.Options.AgglayerClient.GRPC.URL)
	require.True(t, cfg.Options.AgglayerClient.GRPC.UseTLS)
}

func TestMergeOptions_BridgeService(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cfg.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100",
		"options": {
			"bridgeServiceURL": "http://bridge:8080",
			"bridgeServiceType": "zkevm"
		}
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "http://bridge:8080", cfg.Options.BridgeServiceURL)
	require.Equal(t, "zkevm", cfg.Options.BridgeServiceType)
}

func TestParseTargetBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		wantErr      bool
		wantBlock    string
		wantSpecific uint64
	}{
		{
			name:      "empty defaults to latest",
			input:     "",
			wantBlock: "LatestBlock",
		},
		{
			name:      "LatestBlock tag",
			input:     "LatestBlock",
			wantBlock: "LatestBlock",
		},
		{
			name:      "FinalizedBlock tag",
			input:     "FinalizedBlock",
			wantBlock: "FinalizedBlock",
		},
		{
			name:         "numeric block",
			input:        "12345",
			wantSpecific: 12345,
		},
		{
			name:    "typo FinalizedBock returns error",
			input:   "FinalizedBock",
			wantErr: true,
		},
		{
			name:    "hex garbage returns error",
			input:   "0xZZ",
			wantErr: true,
		},
		{
			name:    "random string returns error",
			input:   "notablock",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := parseTargetBlock(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.wantBlock != "" {
				require.Equal(t, tc.wantBlock, result.Block.String())
			}
			if tc.wantSpecific != 0 {
				require.Equal(t, tc.wantSpecific, result.Specific)
			}
		})
	}
}

func TestLoadConfig_InvalidTargetBlock(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cfg.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "FinalizedBock"
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	_, err := LoadConfig(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid targetBlock")
	require.Contains(t, err.Error(), "FinalizedBock")
}

func TestLoadLBTEntries_ValidFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "lbt.json")

	entries := []LBTEntry{
		{
			WrappedTokenAddress: common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
			OriginNetwork:       0,
			OriginTokenAddress:  common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"),
			Balance:             "1000000",
		},
	}

	data, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	result, err := LoadLBTEntries(path)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "1000000", result[0].Balance)
}
